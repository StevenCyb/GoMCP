package mcp

import (
	"crypto/rand"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/StevenCyb/GoMCP/internal/rpc"
)

const (
	defaultMaxTokens = 1024
	// stateResponses is the key of the requestState data that carries the answers of earlier rounds.
	stateResponses = "_responses"
)

// ErrDeclined is returned when the user declined or cancelled an elicitation.
var ErrDeclined = errors.New("mcp: the user declined or cancelled the request")

func (c *callContext) need(feature, capability string) error {
	if !c.caps.supports(capability) {
		return fmt.Errorf("%s: %w", feature, ErrCapability)
	}
	return nil
}

func (c *callContext) nextKey(kind string) string {
	c.counters[kind]++
	return kind + "-" + strconv.Itoa(c.counters[kind])
}

// ask puts a question to the client. In a request it answers from the inputResponses of the
// retry or records the question and returns ErrInputRequired; in a task it waits for tasks/update.
func (c *callContext) ask(kind, method string, params any) (json.RawMessage, error) {
	key := c.nextKey(kind)
	if c.task != nil {
		return c.task.ask(c, key, method, params)
	}
	if raw, ok := c.responses[key]; ok {
		return raw, nil
	}
	c.asked = append(c.asked, inputRequest{key: key, method: method, params: params})
	return nil, ErrInputRequired
}

// inputRequired builds the InputRequiredResult for the questions asked so far.
func (c *callContext) inputRequired() map[string]any {
	requests := make(map[string]any, len(c.asked))
	for _, q := range c.asked {
		entry := map[string]any{"method": q.method}
		if q.params != nil {
			entry["params"] = q.params
		}
		requests[q.key] = entry
	}
	res := map[string]any{"resultType": "input_required", "inputRequests": requests}
	if len(c.stateOut) > 0 || len(c.responses) > 0 {
		data := map[string]string{}
		maps.Copy(data, c.state)
		maps.Copy(data, c.stateOut)
		if len(c.responses) > 0 {
			data[stateResponses] = string(rpc.MustJSON(c.responses))
		}
		res["requestState"] = c.srv.sealState(c.principal.Subject, c.digest, data)
	}
	return res
}

func (c *callContext) Sample(req SampleReq) (SampleRes, error) {
	if err := c.need("sampling", CapSampling); err != nil {
		return SampleRes{}, err
	}
	if len(req.Tools) > 0 {
		if err := c.need("sampling with tools", CapSamplingTools); err != nil {
			return SampleRes{}, err
		}
	}
	maxTokens := req.MaxTokens
	if maxTokens <= 0 {
		maxTokens = defaultMaxTokens
	}
	params := map[string]any{"messages": req.Messages, "maxTokens": maxTokens}
	if req.System != "" {
		params["systemPrompt"] = req.System
	}
	if req.Temperature != nil {
		params["temperature"] = *req.Temperature
	}
	if len(req.StopSequences) > 0 {
		params["stopSequences"] = req.StopSequences
	}
	if prefs := req.Prefs.params(); len(prefs) > 0 {
		params["modelPreferences"] = prefs
	}
	if len(req.Tools) > 0 {
		params["tools"] = req.Tools
	}
	if req.ToolChoice != "" {
		params["toolChoice"] = map[string]string{"mode": req.ToolChoice}
	}

	raw, err := c.ask("sample", "sampling/createMessage", params)
	if err != nil {
		return SampleRes{}, err
	}
	var r struct {
		Role       Role            `json:"role"`
		Content    json.RawMessage `json:"content"`
		Model      string          `json:"model"`
		StopReason string          `json:"stopReason"`
	}
	if err := json.Unmarshal(raw, &r); err != nil {
		return SampleRes{}, fmt.Errorf("decode sampling result: %w", err)
	}
	content, err := firstContent(r.Content)
	if err != nil {
		return SampleRes{}, err
	}
	return SampleRes{Role: r.Role, Content: content, Text: content.Text, Model: r.Model, StopReason: r.StopReason}, nil
}

// firstContent decodes a sampling result content, which is one block or a list of blocks.
func firstContent(raw json.RawMessage) (Content, error) {
	var c Content
	if strings.HasPrefix(strings.TrimSpace(string(raw)), "[") {
		var list []Content
		if err := json.Unmarshal(raw, &list); err != nil {
			return c, fmt.Errorf("decode sampling content: %w", err)
		}
		if len(list) > 0 {
			c = list[0]
		}
		return c, nil
	}
	if err := json.Unmarshal(raw, &c); err != nil {
		return c, fmt.Errorf("decode sampling content: %w", err)
	}
	return c, nil
}

func (p ModelPrefs) params() map[string]any {
	out := map[string]any{}
	if len(p.Hints) > 0 {
		hints := make([]map[string]string, 0, len(p.Hints))
		for _, h := range p.Hints {
			hints = append(hints, map[string]string{"name": h})
		}
		out["hints"] = hints
	}
	if p.Cost > 0 {
		out["costPriority"] = p.Cost
	}
	if p.Speed > 0 {
		out["speedPriority"] = p.Speed
	}
	if p.Intelligence > 0 {
		out["intelligencePriority"] = p.Intelligence
	}
	return out
}

func (c *callContext) Roots() ([]Root, error) {
	if err := c.need("roots", CapRoots); err != nil {
		return nil, err
	}
	raw, err := c.ask("roots", "roots/list", nil)
	if err != nil {
		return nil, err
	}
	var r struct {
		Roots []Root `json:"roots"`
	}
	if err := json.Unmarshal(raw, &r); err != nil {
		return nil, fmt.Errorf("decode roots: %w", err)
	}
	return r.Roots, nil
}

// elicitationAnswer is the client's answer to an elicitation.
type elicitationAnswer struct {
	Action  Action          `json:"action"`
	Content json.RawMessage `json:"content"`
}

func (c *callContext) elicitForm(message string, requested map[string]any) (Action, json.RawMessage, error) {
	if err := c.need("form elicitation", CapElicitation); err != nil {
		return "", nil, err
	}
	raw, err := c.ask("elicit", "elicitation/create", map[string]any{"mode": "form", "message": message, "requestedSchema": requested})
	if err != nil {
		return "", nil, err
	}
	var a elicitationAnswer
	if err := json.Unmarshal(raw, &a); err != nil {
		return "", nil, fmt.Errorf("decode elicitation answer: %w", err)
	}
	return a.Action, a.Content, nil
}

// elicitation is a URL elicitation that waits for the page the user was sent to.
type elicitation struct {
	owner   string
	expires time.Time
	done    chan struct{}
	once    sync.Once
}

var (
	elicitations   sync.Map                                                                     //nolint:gochecknoglobals // id -> *elicitation; the web page only knows the id
	closedChan     = func() chan struct{} { ch := make(chan struct{}); close(ch); return ch }() //nolint:gochecknoglobals // shared closed channel
	elicitationTTL = 30 * time.Minute                                                           //nolint:gochecknoglobals // lifetime of an open URL elicitation
)

func newElicitation(owner string) string {
	now := time.Now()
	elicitations.Range(func(k, v any) bool {
		if e, ok := v.(*elicitation); ok && now.After(e.expires) {
			elicitations.Delete(k)
		}
		return true
	})
	id := rand.Text()
	elicitations.Store(id, &elicitation{owner: owner, expires: now.Add(elicitationTTL), done: make(chan struct{})})
	return id
}

func lookupElicitation(id, owner string) (*elicitation, bool) {
	v, ok := elicitations.Load(id)
	if !ok {
		return nil, false
	}
	e, ok := v.(*elicitation)
	return e, ok && e.owner == owner && time.Now().Before(e.expires)
}

// ElicitURL sends the user to a web page. The first call asks the client; once the client says
// the user accepted, the handler runs again, this call returns the id and the handler waits with ElicitDone.
func (c *callContext) ElicitURL(message, url string) (string, error) {
	if err := c.need("URL elicitation", CapElicitationURL); err != nil {
		return "", err
	}
	key := c.nextKey("url")
	if c.task != nil {
		id := newElicitation(c.principal.Subject)
		raw, err := c.task.ask(c, key, "elicitation/create", urlParams(message, url, id))
		if err != nil {
			return "", err
		}
		return id, urlAnswer(raw)
	}
	if raw, ok := c.responses[key]; ok {
		if err := urlAnswer(raw); err != nil {
			return "", err
		}
		if id := c.state[key]; id != "" {
			if _, known := lookupElicitation(id, c.principal.Subject); known {
				return id, nil
			}
		}
		// the elicitation is gone (expired or the server restarted): ask again
	}
	id := newElicitation(c.principal.Subject)
	c.stateOut[key] = id
	c.asked = append(c.asked, inputRequest{key: key, method: "elicitation/create", params: urlParams(message, url, id)})
	return "", ErrInputRequired
}

func urlParams(message, url, id string) map[string]any {
	return map[string]any{"mode": "url", "message": message, "url": strings.ReplaceAll(url, "{id}", id)}
}

// urlAnswer turns the answer to a URL elicitation into ErrDeclined unless the user accepted.
func urlAnswer(raw json.RawMessage) error {
	var a elicitationAnswer
	if err := json.Unmarshal(raw, &a); err != nil {
		return fmt.Errorf("decode elicitation answer: %w", err)
	}
	if a.Action != Accept {
		return fmt.Errorf("%w (%s)", ErrDeclined, a.Action)
	}
	return nil
}

func (c *callContext) ElicitDone(id string) <-chan struct{} {
	if e, ok := lookupElicitation(id, c.principal.Subject); ok {
		return e.done
	}
	return closedChan
}

// CompleteElicitation ends a URL elicitation: it unblocks ElicitDone. The backend of the page the
// user was sent to calls it with the id from the URL; it should first check that the user of the
// page is the user who started the elicitation. It reports whether the elicitation was known.
// The elicitation stays known until it expires: the user may finish the page before the client
// has answered and retried, and the retry must still find it.
func CompleteElicitation(id string) bool {
	v, ok := elicitations.Load(id)
	if !ok {
		return false
	}
	e, ok := v.(*elicitation)
	if !ok {
		return false
	}
	e.once.Do(func() { close(e.done) })
	return true
}
