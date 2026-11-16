package mcp

import (
	"encoding/json"
	"maps"
	"slices"
	"strconv"
	"sync"

	"github.com/StevenCyb/GoMCP/internal/rpc"
)

// subscriptionFilter is what a subscriptions/listen request asks for.
type subscriptionFilter struct {
	ToolsListChanged      bool     `json:"toolsListChanged,omitempty"`
	PromptsListChanged    bool     `json:"promptsListChanged,omitempty"`
	ResourcesListChanged  bool     `json:"resourcesListChanged,omitempty"`
	ResourceSubscriptions []string `json:"resourceSubscriptions,omitempty"`
	TaskIDs               []string `json:"taskIds,omitempty"`
}

// outgoing is a notification waiting to be written to a listen stream.
type outgoing struct {
	method string
	params map[string]any
}

// subscriber is one open subscriptions/listen stream.
type subscriber struct {
	owner  string
	filter subscriptionFilter
	out    chan outgoing
}

type subscribers struct {
	mu   sync.Mutex
	list map[*subscriber]struct{}
}

// add registers the stream unless the server or its owner has too many open.
func (s *subscribers) add(sub *subscriber, lim Limits) *rpc.Error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if len(s.list) >= lim.Streams {
		return rpc.InvalidRequest("too many open streams on the server, try again later")
	}
	own := 0
	for o := range s.list {
		if o.owner == sub.owner {
			own++
		}
	}
	if own >= lim.StreamsPerCaller {
		return rpc.InvalidRequest("too many open streams of this caller, close one or try again later")
	}
	if s.list == nil {
		s.list = map[*subscriber]struct{}{}
	}
	s.list[sub] = struct{}{}
	return nil
}

func (s *subscribers) remove(sub *subscriber) {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.list, sub)
}

// publish queues a notification for every subscriber that match accepts. A subscriber that
// cannot keep up loses the notification.
func (s *subscribers) publish(method string, params map[string]any, match func(subscriptionFilter) bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for sub := range s.list {
		if !match(sub.filter) {
			continue
		}
		select {
		case sub.out <- outgoing{method: method, params: params}:
		default:
		}
	}
}

// ResourcesChanged tells the listeners that the list of resources changed.
func (s *Server) ResourcesChanged() {
	s.subs.publish("notifications/resources/list_changed", map[string]any{}, func(f subscriptionFilter) bool { return f.ResourcesListChanged })
}

// ToolsChanged tells the listeners that the list of tools changed.
func (s *Server) ToolsChanged() {
	s.subs.publish("notifications/tools/list_changed", map[string]any{}, func(f subscriptionFilter) bool { return f.ToolsListChanged })
}

// PromptsChanged tells the listeners that the list of prompts changed.
func (s *Server) PromptsChanged() {
	s.subs.publish("notifications/prompts/list_changed", map[string]any{}, func(f subscriptionFilter) bool { return f.PromptsListChanged })
}

// ResourceUpdated tells the listeners subscribed to uri that its content changed.
func (s *Server) ResourceUpdated(uri string) {
	s.subs.publish("notifications/resources/updated", map[string]any{"uri": uri}, func(f subscriptionFilter) bool {
		return slices.Contains(f.ResourceSubscriptions, uri)
	})
}

// listen serves subscriptions/listen: it keeps the response stream open and writes the notifications
// the client asked for, tagged with the id of the request, until the client closes the stream.
func (s *Server) listen(ctx *callContext, params json.RawMessage) (any, *rpc.Error) {
	var p struct {
		Notifications subscriptionFilter `json:"notifications"`
	}
	if err := json.Unmarshal(params, &p); err != nil {
		return nil, rpc.InvalidParams("invalid subscriptions/listen params: " + err.Error())
	}
	if len(p.Notifications.TaskIDs) > 0 && !ctx.caps.hasExtension(tasksExtension) {
		return nil, rpc.MissingCapability(extensionObject(tasksExtension))
	}
	if !ctx.call.CanStream() {
		return nil, rpc.InvalidRequest("subscriptions/listen needs a client that accepts text/event-stream")
	}

	if n := p.Notifications; len(n.ResourceSubscriptions) > s.limits.ListenEntries || len(n.TaskIDs) > s.limits.ListenEntries {
		return nil, rpc.InvalidParams("too many resourceSubscriptions or taskIds in one request, at most " + strconv.Itoa(s.limits.ListenEntries))
	}

	agreed := s.agree(ctx, p.Notifications)
	sub := &subscriber{owner: ctx.principal.Subject, filter: agreed, out: make(chan outgoing, 256)}
	if rpcErr := s.subs.add(sub, s.limits); rpcErr != nil {
		return nil, rpcErr
	}
	defer s.subs.remove(sub)

	tag := map[string]any{"io.modelcontextprotocol/subscriptionId": ctx.call.ID()}
	ctx.notify("notifications/subscriptions/acknowledged", map[string]any{"_meta": tag, "notifications": agreed})
	for {
		select {
		case n := <-sub.out:
			params := map[string]any{}
			maps.Copy(params, n.params)
			params["_meta"] = tag
			ctx.notify(n.method, params)
		case <-ctx.Done():
			return nil, nil //nolint:nilnil // the client closed the stream; nobody reads a response
		case <-ctx.call.Closing():
			return map[string]any{"resultType": "complete", "_meta": tag}, nil
		}
	}
}

// agree returns the part of the filter the server honors: subscribable resources and the
// caller's own tasks.
func (s *Server) agree(ctx *callContext, f subscriptionFilter) subscriptionFilter {
	agreed := subscriptionFilter{
		ToolsListChanged: f.ToolsListChanged, PromptsListChanged: f.PromptsListChanged, ResourcesListChanged: f.ResourcesListChanged,
	}
	for _, uri := range f.ResourceSubscriptions {
		if r, _ := s.findResource(uri); r != nil && r.def.Subscribable && (ctx.trusted || hasScopes(ctx.principal, r.def.Scopes)) {
			agreed.ResourceSubscriptions = append(agreed.ResourceSubscriptions, uri)
		}
	}
	for _, id := range f.TaskIDs {
		if t := s.tasks.get(id); t != nil && t.owner == ctx.principal.Subject {
			agreed.TaskIDs = append(agreed.TaskIDs, id)
		}
	}
	return agreed
}
