package mcp

import (
	"encoding/json"
	"errors"
	"slices"

	"github.com/StevenCyb/GoMCP/internal/rpc"
	"github.com/StevenCyb/GoMCP/internal/transport"
)

// dispatch is the transport's entry point into the server.
func (s *Server) dispatch(c *transport.Call, req transport.Request) (any, *rpc.Error) {
	ctx := s.newContext(c)
	if level := ctx.meta.LogLevel; level != "" && !slices.Contains(logLevels, level) {
		return nil, invalidLogLevel(level)
	}
	res, rpcErr := s.route(ctx, req)
	if rpcErr != nil {
		return nil, rpcErr
	}
	return s.decorate(ctx, req, res), nil
}

func (s *Server) route(ctx *callContext, req transport.Request) (any, *rpc.Error) {
	switch req.Method {
	case "server/discover":
		return s.discover(), nil
	case "tools/list":
		return s.listTools(ctx, req.Params)
	case "tools/call":
		return s.callTool(ctx, req.Params)
	case "resources/list":
		return s.listResources(ctx, req.Params)
	case "resources/templates/list":
		return s.listTemplates(ctx, req.Params)
	case "resources/read":
		return s.readResourceCall(ctx, req.Params)
	case "prompts/list":
		return s.listPrompts(ctx, req.Params)
	case "prompts/get":
		return s.getPrompt(ctx, req.Params)
	case "completion/complete":
		return s.complete(ctx, req.Params)
	case "subscriptions/listen":
		return s.listen(ctx, req.Params)
	case "tasks/get":
		return s.getTask(ctx, req.Params)
	case "tasks/update":
		return s.updateTask(ctx, req.Params)
	case "tasks/cancel":
		return s.cancelTask(ctx, req.Params)
	default:
		return nil, rpc.MethodNotFound(req.Method)
	}
}

// loadInput reads the multi round-trip fields of a retry: the answers and the verified requestState.
func (c *callContext) loadInput(method string, params json.RawMessage) *rpc.Error {
	var p struct {
		InputResponses map[string]json.RawMessage `json:"inputResponses"`
		RequestState   string                     `json:"requestState"`
	}
	if err := json.Unmarshal(params, &p); err != nil {
		return rpc.InvalidParams("invalid params: " + err.Error())
	}
	c.responses = p.InputResponses
	if c.responses == nil {
		c.responses = map[string]json.RawMessage{}
	}
	c.digest = requestDigest(method, params)
	if p.RequestState != "" {
		data, err := c.srv.openState(p.RequestState, c.principal.Subject, c.digest)
		if err != nil {
			return rpc.InvalidParams(err.Error())
		}
		c.state = data
		// answers of earlier rounds travel in the state, the client only sends the latest ones
		var earlier map[string]json.RawMessage
		if json.Unmarshal([]byte(data[stateResponses]), &earlier) == nil {
			for key, raw := range earlier {
				if _, newer := c.responses[key]; !newer {
					c.responses[key] = raw
				}
			}
		}
	}
	return nil
}

// cacheable are the methods whose complete results carry ttlMs and cacheScope.
var cacheable = []string{ //nolint:gochecknoglobals // constant lookup table
	"server/discover", "tools/list", "prompts/list", "resources/list", "resources/templates/list", "resources/read",
}

// decorate adds what every result carries: resultType, the server identity and, for cacheable
// results, the caching hints.
func (s *Server) decorate(ctx *callContext, req transport.Request, res any) any {
	raw, err := json.Marshal(res)
	if err != nil {
		return res
	}
	m := map[string]json.RawMessage{}
	if json.Unmarshal(raw, &m) != nil || m == nil {
		m = map[string]json.RawMessage{}
	}
	if _, ok := m["resultType"]; !ok {
		m["resultType"] = json.RawMessage(`"complete"`)
	}
	meta := map[string]json.RawMessage{}
	_ = json.Unmarshal(m["_meta"], &meta)
	meta["io.modelcontextprotocol/serverInfo"] = rpc.MustJSON(s.serverInfo(ctx))
	m["_meta"] = rpc.MustJSON(meta)

	var retry struct {
		InputResponses map[string]json.RawMessage `json:"inputResponses"`
		RequestState   string                     `json:"requestState"`
	}
	_ = json.Unmarshal(req.Params, &retry)
	isRetry := len(retry.InputResponses) > 0 || retry.RequestState != ""
	if string(m["resultType"]) == `"complete"` && slices.Contains(cacheable, req.Method) && !isRetry {
		m["ttlMs"] = rpc.MustJSON(s.opts.cacheTTL.Milliseconds())
		m["cacheScope"] = rpc.MustJSON(string(s.opts.cacheScope))
	}
	return m
}

// serverInfo is the Implementation that identifies the server.
func (s *Server) serverInfo(ctx *callContext) map[string]any {
	info := map[string]any{"name": s.info.Name, "version": s.info.Version}
	setIfSet(info, "title", s.info.Title)
	setIfSet(info, "description", s.info.Description)
	setIfSet(info, "websiteUrl", s.info.WebsiteURL)
	if icons := s.resolveIcons(s.info.Icons, ctx.call.HTTPRequest()); len(icons) > 0 {
		info["icons"] = icons
	}
	return info
}

func setIfSet(m map[string]any, key, value string) {
	if value != "" {
		m[key] = value
	}
}

// discover is the answer to server/discover.
func (s *Server) discover() map[string]any {
	res := map[string]any{"supportedVersions": transport.SupportedVersions(), "capabilities": s.capabilities()}
	setIfSet(res, "instructions", s.opts.instructions)
	return res
}

func (s *Server) capabilities() map[string]any {
	s.mu.RLock()
	defer s.mu.RUnlock()
	caps := map[string]any{"logging": map[string]any{}}
	if len(s.tools) > 0 {
		caps["tools"] = map[string]any{"listChanged": true}
	}
	if len(s.statics)+len(s.templates) > 0 {
		res := map[string]any{"listChanged": true}
		for _, r := range append(append([]*resource(nil), s.statics...), s.templates...) {
			if r.def.Subscribable {
				res["subscribe"] = true
			}
		}
		caps["resources"] = res
	}
	if len(s.prompts) > 0 {
		caps["prompts"] = map[string]any{"listChanged": true}
	}
	if len(s.completions) > 0 {
		caps["completions"] = map[string]any{}
	}
	if s.tasksEnabledLocked() {
		caps["extensions"] = map[string]any{tasksExtension: map[string]any{}}
	}
	return caps
}

// pageRequest reads the cursor of a list request and applies the server page size.
func (s *Server) pageRequest(params json.RawMessage) (PageReq, *rpc.Error) {
	var p struct {
		Cursor string `json:"cursor"`
	}
	if len(params) > 0 {
		if err := json.Unmarshal(params, &p); err != nil {
			return PageReq{}, rpc.InvalidParams("invalid list params: " + err.Error())
		}
	}
	return PageReq{Cursor: p.Cursor, Size: s.opts.pageSize}, nil
}

// listResult builds a list response such as {"tools": [...], "nextCursor": "..."}.
func listResult(key string, items any, next string) map[string]any {
	out := map[string]any{key: items}
	if next != "" {
		out["nextCursor"] = next
	}
	return out
}

// toRPCError turns a handler error into a protocol error.
func toRPCError(err error) *rpc.Error {
	var rpcErr *rpc.Error
	if errors.As(err, &rpcErr) {
		return rpcErr
	}
	return rpc.Internal(err.Error())
}
