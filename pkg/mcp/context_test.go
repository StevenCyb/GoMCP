package mcp_test

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/StevenCyb/GoMCP/internal/rpc"
	"github.com/StevenCyb/GoMCP/pkg/mcp"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// toolServer registers one tool and connects a client with the given capabilities.
func toolServer[In, Out any](t *testing.T, caps map[string]any, h func(mcp.Context, In) (Out, error), opts ...mcp.Option) *testClient {
	t.Helper()
	s := newBasicServer(opts...)
	require.NoError(t, mcp.AddTool(s, mcp.ToolDef{Name: "t"}, h))
	return connect(t, newServer(t, s), caps)
}

func firstText(t *testing.T, res rpc.Message) string {
	t.Helper()
	out := result[toolResult](t, res)
	require.NotEmpty(t, out.Content)
	text, ok := out.Content[0][keyText].(string)
	require.True(t, ok)
	return text
}

func callT(t *testing.T, c *testClient) rpc.Message {
	t.Helper()
	return callTool(t, c, "t", nil)
}

// ---- logging

func TestLoggingNeedsALevelInTheRequest(t *testing.T) {
	c := toolServer(t, nil, func(ctx mcp.Context, _ mcp.Empty) (string, error) {
		ctx.Log().Error("boom")
		return "ok", nil
	})
	_ = callT(t, c)
	assert.Empty(t, c.notifications, "no log level in the request, no log notifications")
}

func TestLoggingFollowsTheRequestLevel(t *testing.T) {
	c := toolServer(t, nil, func(ctx mcp.Context, _ mcp.Empty) (string, error) {
		ctx.Log().Debug("d")
		ctx.Log().Info("i")
		ctx.Log().Warn("w", "key", "value")
		ctx.Log().Error("e")
		return "ok", nil
	})
	c.logLevel = "warning"
	_ = callT(t, c)
	require.Len(t, c.notifications, 2)
	assert.Equal(t, "notifications/message", c.notifications[0].Method)
	assert.JSONEq(t, `{"level":"warning","data":{"message":"w","key":"value"}}`, string(c.notifications[0].Params))
	assert.JSONEq(t, `{"level":"error","data":"e"}`, string(c.notifications[1].Params))
}

func TestUnknownLogLevelIsRejected(t *testing.T) {
	c := toolServer(t, nil, func(mcp.Context, mcp.Empty) (string, error) { return "", nil })
	c.logLevel = "loud"
	res := callT(t, c)
	require.NotNil(t, res.Error)
	assert.Equal(t, rpc.CodeInvalidParams, res.Error.Code)
}

// ---- progress

func TestProgress(t *testing.T) {
	c := toolServer(t, nil, func(ctx mcp.Context, _ mcp.Empty) (string, error) {
		ctx.Progress(1, 4, "one")
		ctx.Progress(2, 0, "")
		return "ok", nil
	})
	c.progressToken = "tok"
	require.Nil(t, callT(t, c).Error)
	require.Len(t, c.notifications, 2)
	assert.Equal(t, "notifications/progress", c.notifications[0].Method)
	assert.JSONEq(t, `{"progressToken":"tok","progress":1,"total":4,"message":"one"}`, string(c.notifications[0].Params))
	assert.JSONEq(t, `{"progressToken":"tok","progress":2}`, string(c.notifications[1].Params))
}

func TestProgressWithoutTokenIsDropped(t *testing.T) {
	c := toolServer(t, nil, func(ctx mcp.Context, _ mcp.Empty) (string, error) {
		ctx.Progress(1, 4, "one")
		return "ok", nil
	})
	_ = callT(t, c)
	assert.Empty(t, c.notifications)
}

// ---- multi round-trip: sampling, roots, elicitation

func TestSampling(t *testing.T) {
	var got map[string]any
	c := toolServer(t, map[string]any{mcp.CapSampling: map[string]any{}}, func(ctx mcp.Context, _ mcp.Empty) (string, error) {
		res, err := ctx.Sample(mcp.SampleReq{
			System:      "Be brief.",
			Messages:    []mcp.SampleMsg{mcp.SampleText(mcp.RoleUser, "hello")},
			MaxTokens:   50,
			Prefs:       mcp.ModelPrefs{Hints: []string{"claude"}, Speed: 0.8, Intelligence: 0.5},
			Temperature: new(0.2),
		})
		if err != nil {
			return "", err
		}
		return res.Text + "|" + res.Model + "|" + res.StopReason, nil
	})
	c.onRequest = func(req rpc.Message) (any, *rpc.Error) {
		assert.Equal(t, "sampling/createMessage", req.Method)
		require.NoError(t, json.Unmarshal(req.Params, &got))
		return map[string]any{
			"role": roleAssistant, "content": map[string]any{keyType: "text", keyText: "hi there"}, "model": "m1", "stopReason": "endTurn",
		}, nil
	}

	assert.Equal(t, "hi there|m1|endTurn", firstText(t, callT(t, c)))
	assert.Equal(t, 1, c.rounds, "one question, one retry")
	assert.Equal(t, "Be brief.", got["systemPrompt"])
	assert.InDelta(t, 50, got["maxTokens"], 0)
	assert.InDelta(t, 0.2, got["temperature"], 0.0001)
	assert.JSONEq(t, `{"hints":[{"name":"claude"}],"speedPriority":0.8,"intelligencePriority":0.5}`, string(rpc.MustJSON(got["modelPreferences"])))
}

func TestSamplingWithToolsAndArrayContent(t *testing.T) {
	var got map[string]any
	c := toolServer(t, map[string]any{mcp.CapSampling: map[string]any{"tools": map[string]any{}}}, func(ctx mcp.Context, _ mcp.Empty) (string, error) {
		res, err := ctx.Sample(mcp.SampleReq{
			Messages: []mcp.SampleMsg{mcp.SampleText(mcp.RoleUser, "weather?")}, MaxTokens: 10,
			Tools:      []mcp.SampleTool{{Name: "weather", InputSchema: map[string]any{keyType: "object"}}},
			ToolChoice: "required",
		})
		if err != nil {
			return "", err
		}
		return res.Content.Type + "|" + res.StopReason, nil
	})
	c.onRequest = func(req rpc.Message) (any, *rpc.Error) {
		require.NoError(t, json.Unmarshal(req.Params, &got))
		return map[string]any{
			"role": roleAssistant, "model": "m", "stopReason": "toolUse",
			"content": []any{map[string]any{keyType: "tool_use", "id": "1", keyName: "weather", "input": map[string]any{}}},
		}, nil
	}
	assert.Equal(t, "tool_use|toolUse", firstText(t, callT(t, c)))
	assert.JSONEq(t, `{"mode":"required"}`, string(rpc.MustJSON(got["toolChoice"])))
}

func TestSamplingNeedsCapability(t *testing.T) {
	c := toolServer(t, nil, func(ctx mcp.Context, _ mcp.Empty) (string, error) {
		_, err := ctx.Sample(mcp.SampleReq{MaxTokens: 1})
		return "", err
	})
	res := result[toolResult](t, callT(t, c))
	assert.True(t, res.IsError)
	assert.Equal(t, 0, c.rounds, "the client is not asked for something it cannot do")

	c = toolServer(t, map[string]any{mcp.CapSampling: map[string]any{}}, func(ctx mcp.Context, _ mcp.Empty) (string, error) {
		_, err := ctx.Sample(mcp.SampleReq{MaxTokens: 1, Tools: []mcp.SampleTool{{Name: "x"}}})
		if errors.Is(err, mcp.ErrCapability) {
			return "no tools", nil
		}
		return "", err
	})
	assert.Equal(t, "no tools", firstText(t, callT(t, c)), "tools need the sampling.tools capability")
}

func TestRoots(t *testing.T) {
	c := toolServer(t, map[string]any{mcp.CapRoots: map[string]any{}}, func(ctx mcp.Context, _ mcp.Empty) ([]string, error) {
		roots, err := ctx.Roots()
		out := make([]string, 0, len(roots))
		for _, r := range roots {
			out = append(out, r.URI+"="+r.Name)
		}
		return out, err
	})
	c.onRequest = func(req rpc.Message) (any, *rpc.Error) {
		assert.Equal(t, "roots/list", req.Method)
		return map[string]any{mcp.CapRoots: []any{map[string]any{keyURI: "file:///a", keyName: "A"}}}, nil
	}
	res := result[toolResult](t, callT(t, c))
	assert.Equal(t, []any{"file:///a=A"}, res.StructuredContent, "a list is structured content")
}

func TestRootsNeedCapability(t *testing.T) {
	c := toolServer(t, nil, func(ctx mcp.Context, _ mcp.Empty) (string, error) {
		_, err := ctx.Roots()
		return "", err
	})
	assert.True(t, result[toolResult](t, callT(t, c)).IsError)
}

type confirm struct {
	Confirm bool   `desc:"Really?"          json:"confirm"`
	Reason  string `json:"reason,omitempty"`
}

func TestElicitForm(t *testing.T) {
	var got map[string]any
	c := toolServer(t, map[string]any{mcp.CapElicitation: map[string]any{}}, func(ctx mcp.Context, _ mcp.Empty) (string, error) {
		ans, action, err := mcp.Elicit[confirm](ctx, "Delete it?")
		if err != nil {
			return "", err
		}
		return string(action) + "|" + ans.Reason, nil
	})
	c.onRequest = func(req rpc.Message) (any, *rpc.Error) {
		assert.Equal(t, "elicitation/create", req.Method)
		require.NoError(t, json.Unmarshal(req.Params, &got))
		return map[string]any{keyAction: valAccept, "content": map[string]any{"confirm": true, "reason": "stale"}}, nil
	}

	assert.Equal(t, "accept|stale", firstText(t, callT(t, c)))
	assert.Equal(t, "form", got["mode"])
	assert.Equal(t, "Delete it?", got["message"])
	assert.JSONEq(t, `{"type":"object","properties":{"confirm":{"type":"boolean","description":"Really?"},"reason":{"type":"string"}},"required":["confirm"]}`,
		string(rpc.MustJSON(got["requestedSchema"])))
}

func TestElicitFormDeclined(t *testing.T) {
	c := toolServer(t, map[string]any{mcp.CapElicitation: map[string]any{"form": map[string]any{}}}, func(ctx mcp.Context, _ mcp.Empty) (string, error) {
		ans, action, err := mcp.Elicit[confirm](ctx, "Delete it?")
		return string(action) + "|" + ans.Reason, err
	})
	c.onRequest = func(rpc.Message) (any, *rpc.Error) { return map[string]any{keyAction: "decline"}, nil }
	assert.Equal(t, "decline|", firstText(t, callT(t, c)))
}

func TestElicitFormErrors(t *testing.T) {
	c := toolServer(t, nil, func(ctx mcp.Context, _ mcp.Empty) (string, error) {
		_, _, err := mcp.Elicit[confirm](ctx, "x")
		return "", err
	})
	assert.True(t, result[toolResult](t, callT(t, c)).IsError, "no elicitation capability")

	type nested struct {
		Inner confirm `json:"inner"`
	}
	c = toolServer(t, map[string]any{mcp.CapElicitation: map[string]any{}}, func(ctx mcp.Context, _ mcp.Empty) (string, error) {
		_, _, err := mcp.Elicit[nested](ctx, "x")
		return "", err
	})
	assert.True(t, result[toolResult](t, callT(t, c)).IsError, "forms must be flat")

	c = toolServer(t, map[string]any{mcp.CapElicitation: map[string]any{keyURL: map[string]any{}}}, func(ctx mcp.Context, _ mcp.Empty) (string, error) {
		_, _, err := mcp.Elicit[confirm](ctx, "x")
		return "", err
	})
	assert.True(t, result[toolResult](t, callT(t, c)).IsError, "url only clients cannot show forms")
}

func TestSeveralQuestionsTakeSeveralRounds(t *testing.T) {
	c := toolServer(t, map[string]any{mcp.CapRoots: map[string]any{}, mcp.CapElicitation: map[string]any{}}, func(ctx mcp.Context, _ mcp.Empty) (string, error) {
		roots, err := ctx.Roots()
		if err != nil {
			return "", err
		}
		ans, _, err := mcp.Elicit[confirm](ctx, "ok?")
		if err != nil {
			return "", err
		}
		return roots[0].URI + "|" + ans.Reason, nil
	})
	c.onRequest = func(req rpc.Message) (any, *rpc.Error) {
		if req.Method == "roots/list" {
			return map[string]any{mcp.CapRoots: []any{map[string]any{keyURI: "file:///w"}}}, nil
		}
		return map[string]any{keyAction: valAccept, "content": map[string]any{"confirm": true, "reason": "yes"}}, nil
	}
	assert.Equal(t, "file:///w|yes", firstText(t, callT(t, c)))
	assert.Equal(t, 2, c.rounds)
}

func TestHandlerThatIgnoresTheInputErrorStillAsks(t *testing.T) {
	c := toolServer(t, map[string]any{mcp.CapRoots: map[string]any{}}, func(ctx mcp.Context, _ mcp.Empty) (string, error) {
		_, _ = ctx.Roots() // the error is dropped on purpose
		return "finished", nil
	})
	res := c.once("tools/call", map[string]any{keyName: "t"}, nil)
	out := result[map[string]any](t, res)
	assert.Equal(t, "input_required", out["resultType"], "an unanswered question always wins over the handler result")
	assert.Contains(t, out, "inputRequests")
}

func TestUrlElicitation(t *testing.T) {
	var asked map[string]any
	var elicitationID string
	c := toolServer(t, map[string]any{mcp.CapElicitation: map[string]any{keyURL: map[string]any{}}}, func(ctx mcp.Context, _ mcp.Empty) (string, error) {
		id, err := ctx.ElicitURL("Sign in", "https://example.com/connect?e={id}")
		if err != nil {
			return "", err
		}
		elicitationID = id
		go func() { _ = mcp.CompleteElicitation(id) }() // the sign-in page's backend reports that the user is done
		<-ctx.ElicitDone(id)
		return "connected", nil
	})
	c.onRequest = func(req rpc.Message) (any, *rpc.Error) {
		assert.Equal(t, "elicitation/create", req.Method)
		require.NoError(t, json.Unmarshal(req.Params, &asked))
		return map[string]any{keyAction: valAccept}, nil
	}

	assert.Equal(t, "connected", firstText(t, callT(t, c)))
	assert.Equal(t, 1, c.rounds)
	assert.Equal(t, "url", asked["mode"])
	assert.Equal(t, "Sign in", asked["message"])
	assert.NotContains(t, asked, "elicitationId", "the id is not part of the request any more")
	require.NotEmpty(t, elicitationID)
	assert.Equal(t, "https://example.com/connect?e="+elicitationID, asked["url"])
}

func TestUrlElicitationDeclinedAndUnsupported(t *testing.T) {
	c := toolServer(t, map[string]any{mcp.CapElicitation: map[string]any{keyURL: map[string]any{}}}, func(ctx mcp.Context, _ mcp.Empty) (string, error) {
		_, err := ctx.ElicitURL("Sign in", "https://example.com")
		if errors.Is(err, mcp.ErrDeclined) {
			return "declined", nil
		}
		return "", err
	})
	c.onRequest = func(rpc.Message) (any, *rpc.Error) { return map[string]any{keyAction: "cancel"}, nil }
	assert.Equal(t, "declined", firstText(t, callT(t, c)))

	c = toolServer(t, map[string]any{mcp.CapElicitation: map[string]any{}}, func(ctx mcp.Context, _ mcp.Empty) (string, error) {
		_, err := ctx.ElicitURL("Sign in", "https://example.com")
		return "", err
	})
	assert.True(t, result[toolResult](t, callT(t, c)).IsError, "form only clients cannot do URL mode")
}

func TestElicitDoneForUnknownIDStaysBlocked(t *testing.T) {
	c := toolServer(t, nil, func(ctx mcp.Context, _ mcp.Empty) (string, error) {
		select {
		case <-ctx.ElicitDone("nope"):
			return "closed", nil
		case <-time.After(50 * time.Millisecond):
			return "blocked", nil
		}
	})
	assert.Equal(t, "blocked", firstText(t, callT(t, c)), "an unknown id must not look like a completed page")
	assert.False(t, mcp.CompleteElicitation("nope"))
}

// limitScheme accepts every token and makes it the subject, so each test owns its callers.
func limitScheme() mcp.AuthScheme {
	s := oauthScheme()
	s.Verify = func(_ context.Context, token string) (mcp.Principal, error) {
		return mcp.Principal{Subject: token, Scopes: []string{scopeRead}}, nil
	}
	return s
}

func TestElicitURLIsLimitedPerCaller(t *testing.T) {
	s := newBasicServer(mcp.WithAuth(limitScheme()), mcp.WithLimits(mcp.Limits{ElicitationsPerCaller: 2}))
	require.NoError(t, mcp.AddTool(s, mcp.ToolDef{Name: "t"}, func(ctx mcp.Context, _ mcp.Empty) (string, error) {
		_, err := ctx.ElicitURL("Sign in", "https://example.com/?e={id}")
		return "", err
	}))
	srv := newServer(t, s)
	caps := map[string]any{mcp.CapElicitation: map[string]any{keyURL: map[string]any{}}}
	flooder := connectAs(t, srv, "elicit-flooder", caps)
	for range 2 {
		assert.Equal(t, "input_required", result[map[string]any](t, flooder.once("tools/call", map[string]any{keyName: "t"}, nil))["resultType"])
	}
	assert.True(t, result[toolResult](t, flooder.once("tools/call", map[string]any{keyName: "t"}, nil)).IsError, "the third open elicitation is refused")
	other := connectAs(t, srv, "elicit-other", caps)
	assert.Equal(t, "input_required", result[map[string]any](t, other.once("tools/call", map[string]any{keyName: "t"}, nil))["resultType"], "other callers are not affected")
}

// firstRound returns the input_required result of the first request of a URL elicitation.
func firstRound(t *testing.T, c *testClient) (state string, key string) {
	t.Helper()
	out := result[struct {
		ResultType    string                    `json:"resultType"`
		InputRequests map[string]map[string]any `json:"inputRequests"`
		RequestState  string                    `json:"requestState"`
	}](t, c.once("tools/call", map[string]any{keyName: "t"}, nil))
	require.Equal(t, "input_required", out.ResultType)
	require.NotEmpty(t, out.RequestState, "a URL elicitation needs requestState to find the elicitation again")
	for k := range out.InputRequests {
		key = k
	}
	return out.RequestState, key
}

func TestRequestStateIsProtected(t *testing.T) {
	c := toolServer(t, map[string]any{mcp.CapElicitation: map[string]any{keyURL: map[string]any{}}}, func(ctx mcp.Context, _ mcp.Empty) (string, error) {
		id, err := ctx.ElicitURL("Sign in", "https://example.com/?e={id}")
		if err != nil {
			return "", err
		}
		return id, nil
	})
	state, key := firstRound(t, c)
	answers := map[string]any{key: map[string]any{keyAction: valAccept}}

	good := c.once("tools/call", map[string]any{keyName: "t", "inputResponses": answers, keyState: state}, nil)
	require.Nil(t, good.Error)

	for name, token := range map[string]string{
		"tampered":  state[:len(state)-3] + "AAA",
		"garbage":   "not-a-state",
		"no mac":    state[:len(state)/2],
		"empty mac": state[:len(state)-len(state[len(state)-43:])],
	} {
		res := c.once("tools/call", map[string]any{keyName: "t", "inputResponses": answers, keyState: token}, nil)
		require.NotNil(t, res.Error, name)
		assert.Equal(t, rpc.CodeInvalidParams, res.Error.Code, name)
	}
}

func TestRequestStateOnlyWorksForItsOwnRequest(t *testing.T) {
	s := newBasicServer()
	handler := func(ctx mcp.Context, _ mcp.Empty) (string, error) {
		_, err := ctx.ElicitURL("Sign in", "https://example.com/?e={id}")
		return "", err
	}
	require.NoError(t, mcp.AddTool(s, mcp.ToolDef{Name: "a"}, handler))
	require.NoError(t, mcp.AddTool(s, mcp.ToolDef{Name: "b"}, handler))
	c := connect(t, newServer(t, s), map[string]any{mcp.CapElicitation: map[string]any{keyURL: map[string]any{}}})

	out := result[map[string]any](t, c.once("tools/call", map[string]any{keyName: "a"}, nil))
	state, _ := out[keyState].(string)
	require.NotEmpty(t, state)

	res := c.once("tools/call", map[string]any{keyName: "b", keyState: state}, nil)
	require.NotNil(t, res.Error, "a state of tool a cannot be used for tool b")
	assert.Equal(t, rpc.CodeInvalidParams, res.Error.Code)
}

func TestRequestStateBelongsToTheCaller(t *testing.T) {
	s := authedServer(t, oauthScheme())
	require.NoError(t, mcp.AddTool(s, mcp.ToolDef{Name: "ask"}, func(ctx mcp.Context, _ mcp.Empty) (string, error) {
		_, err := ctx.ElicitURL("Sign in", "https://example.com/?e={id}")
		return "", err
	}))
	srv := newServer(t, s)
	caps := map[string]any{mcp.CapElicitation: map[string]any{keyURL: map[string]any{}}}
	alice, bob := connectAs(t, srv, "reader", caps), connectAs(t, srv, "writer", caps)

	out := result[map[string]any](t, alice.once("tools/call", map[string]any{keyName: "ask"}, nil))
	state, _ := out[keyState].(string)
	require.NotEmpty(t, state)

	res := bob.once("tools/call", map[string]any{keyName: "ask", keyState: state}, nil)
	require.NotNil(t, res.Error, "another caller cannot use the state")
}

func TestElicitURLCompletedBeforeTheRetry(t *testing.T) {
	var id string
	c := toolServer(t, map[string]any{mcp.CapElicitation: map[string]any{keyURL: map[string]any{}}}, func(ctx mcp.Context, _ mcp.Empty) (string, error) {
		got, err := ctx.ElicitURL("Sign in", "https://example.com/?e={id}")
		if err != nil {
			return "", err
		}
		<-ctx.ElicitDone(got)
		return "signed in", nil
	})
	c.onRequest = func(m rpc.Message) (any, *rpc.Error) {
		var p struct {
			URL string `json:"url"`
		}
		require.NoError(t, json.Unmarshal(m.Params, &p))
		id = strings.TrimPrefix(p.URL, "https://example.com/?e=")
		// the user approves on the web page before the client sends the retry
		require.True(t, mcp.CompleteElicitation(id))
		return map[string]any{keyAction: valAccept}, nil
	}
	res := callT(t, c)
	assert.Equal(t, "signed in", firstText(t, res))
	assert.Equal(t, 1, c.rounds, "the approval is not lost: the user is asked once")
}
