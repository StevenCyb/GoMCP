package mcp

import "encoding/json"

// clientCaps are the capabilities a client declares with every request.
type clientCaps struct {
	Roots    json.RawMessage `json:"roots"`
	Sampling *struct {
		Context json.RawMessage `json:"context"`
		Tools   json.RawMessage `json:"tools"`
	} `json:"sampling"`
	Elicitation *struct {
		Form json.RawMessage `json:"form"`
		URL  json.RawMessage `json:"url"`
	} `json:"elicitation"`
	Extensions map[string]json.RawMessage `json:"extensions"`
}

// Capability names used by ToolDef.Requires.
const (
	CapRoots          = "roots"
	CapSampling       = "sampling"
	CapSamplingTools  = "sampling:tools"
	CapElicitation    = "elicitation"
	CapElicitationURL = "elicitation:url"
)

// tasksExtension is the identifier of the tasks extension.
const tasksExtension = "io.modelcontextprotocol/tasks"

func (c clientCaps) supports(name string) bool {
	switch name {
	case CapRoots:
		return c.Roots != nil
	case CapSampling:
		return c.Sampling != nil
	case CapSamplingTools:
		return c.Sampling != nil && c.Sampling.Tools != nil
	case CapElicitation:
		// an empty elicitation object means form mode only
		return c.Elicitation != nil && (c.Elicitation.Form != nil || c.Elicitation.URL == nil)
	case CapElicitationURL:
		return c.Elicitation != nil && c.Elicitation.URL != nil
	default:
		return false
	}
}

func (c clientCaps) hasExtension(id string) bool {
	_, ok := c.Extensions[id]
	return ok
}

// capabilityObject is the ClientCapabilities object that contains the named capabilities.
func capabilityObject(names ...string) map[string]any {
	out := map[string]any{}
	for _, name := range names {
		switch name {
		case CapRoots:
			out["roots"] = map[string]any{}
		case CapSampling:
			out["sampling"] = map[string]any{}
		case CapSamplingTools:
			out["sampling"] = map[string]any{"tools": map[string]any{}}
		case CapElicitation:
			out["elicitation"] = map[string]any{}
		case CapElicitationURL:
			out["elicitation"] = map[string]any{"url": map[string]any{}}
		}
	}
	return out
}

// extensionObject is the ClientCapabilities object that contains the extension.
func extensionObject(id string) map[string]any {
	return map[string]any{"extensions": map[string]any{id: map[string]any{}}}
}
