package mcp_test

import (
	"errors"
	"testing"

	"github.com/StevenCyb/GoMCP/internal/rpc"
	"github.com/StevenCyb/GoMCP/pkg/mcp"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type summarizeArgs struct {
	ID    string `desc:"Note to summarize" json:"id"`
	Style string `json:"style,omitempty"`
}

type promptList struct {
	Prompts []struct {
		Name        string `json:"name"`
		Title       string `json:"title"`
		Description string `json:"description"`
		Arguments   []struct {
			Name        string `json:"name"`
			Description string `json:"description"`
			Required    bool   `json:"required"`
		} `json:"arguments"`
		Icons []mcp.Icon     `json:"icons"`
		Meta  map[string]any `json:"_meta"` //nolint:tagliatelle // field name defined by the MCP specification
	} `json:"prompts"`
	NextCursor string `json:"nextCursor"`
}

type promptResult struct {
	Description string `json:"description"`
	Messages    []struct {
		Role    string         `json:"role"`
		Content map[string]any `json:"content"`
	} `json:"messages"`
}

func promptsServer(t *testing.T, opts ...mcp.Option) (*mcp.Server, *testClient) {
	t.Helper()
	s := newBasicServer(opts...)
	require.NoError(t, mcp.AddStaticResource(s, mcp.ResourceDef{URI: indexURI, Name: "index", MIMEType: "text/plain"},
		func(mcp.Context) (mcp.Contents, error) { return mcp.Contents{mcp.TextContent("the index")}, nil }))
	require.NoError(t, mcp.AddPrompt(s, mcp.PromptDef{
		Name: promptSummarize, Title: "Summarize", Description: "Summarizes a note",
		Icons: []mcp.Icon{{Src: "https://example.com/s.svg"}}, Meta: map[string]any{keyTeam: valDocs},
	}, func(_ mcp.Context, a summarizeArgs) (mcp.Messages, error) {
		style := a.Style
		if style == "" {
			style = "short"
		}
		return mcp.Messages{
			mcp.UserText("Summarize " + a.ID + " (" + style + ")"),
			mcp.Message{Role: roleAssistant, Content: mcp.Text("Sure.")},
			mcp.User(mcp.EmbedResource(indexURI)),
		}, nil
	}))
	require.NoError(t, mcp.AddPrompt(s, mcp.PromptDef{Name: "broken"},
		func(mcp.Context, mcp.Empty) (mcp.Messages, error) { return nil, errors.New("oops") }))
	require.NoError(t, mcp.AddPrompt(s, mcp.PromptDef{Name: "bad_embed"},
		func(mcp.Context, mcp.Empty) (mcp.Messages, error) {
			return mcp.Messages{mcp.User(mcp.EmbedResource("note:///nowhere"))}, nil
		}))
	return s, connect(t, newServer(t, s), nil)
}

func getPrompt(t *testing.T, c *testClient, name string, args map[string]any) rpc.Message {
	t.Helper()
	return c.call("prompts/get", map[string]any{keyName: name, "arguments": args})
}

func TestPromptsList(t *testing.T) {
	_, c := promptsServer(t)
	list := result[promptList](t, c.call("prompts/list", nil))
	require.Len(t, list.Prompts, 3)

	p := list.Prompts[0]
	assert.Equal(t, promptSummarize, p.Name)
	assert.Equal(t, "Summarize", p.Title)
	assert.Equal(t, "Summarizes a note", p.Description)
	require.Len(t, p.Arguments, 2)
	assert.Equal(t, "id", p.Arguments[0].Name)
	assert.Equal(t, "Note to summarize", p.Arguments[0].Description)
	assert.True(t, p.Arguments[0].Required)
	assert.Equal(t, "style", p.Arguments[1].Name)
	assert.False(t, p.Arguments[1].Required)
	assert.Len(t, p.Icons, 1)
	assert.Equal(t, map[string]any{keyTeam: valDocs}, p.Meta)
}

func TestPromptsListPagination(t *testing.T) {
	_, c := promptsServer(t, mcp.WithPageSize(2))
	first := result[promptList](t, c.call("prompts/list", nil))
	require.Len(t, first.Prompts, 2)
	require.NotEmpty(t, first.NextCursor)
	second := result[promptList](t, c.call("prompts/list", map[string]any{"cursor": first.NextCursor}))
	assert.Len(t, second.Prompts, 1)
}

func TestPromptGet(t *testing.T) {
	_, c := promptsServer(t)
	res := result[promptResult](t, getPrompt(t, c, promptSummarize, map[string]any{"id": idNote1}))
	assert.Equal(t, "Summarizes a note", res.Description)
	require.Len(t, res.Messages, 3)

	assert.Equal(t, "user", res.Messages[0].Role)
	assert.Equal(t, "Summarize note-1 (short)", res.Messages[0].Content[keyText])
	assert.Equal(t, roleAssistant, res.Messages[1].Role)

	embedded := res.Messages[2].Content
	assert.Equal(t, "resource", embedded["type"])
	assert.JSONEq(t, `{"uri":"note:///index","mimeType":"text/plain","text":"the index"}`, string(rpc.MustJSON(embedded["resource"])))
}

func TestPromptGetErrors(t *testing.T) {
	_, c := promptsServer(t)

	missing := getPrompt(t, c, promptSummarize, map[string]any{"style": "long"})
	require.NotNil(t, missing.Error, "id is required")
	assert.Equal(t, rpc.CodeInvalidParams, missing.Error.Code)

	unknown := getPrompt(t, c, "nope", nil)
	require.NotNil(t, unknown.Error)
	assert.Equal(t, rpc.CodeInvalidParams, unknown.Error.Code)

	broken := getPrompt(t, c, "broken", nil)
	require.NotNil(t, broken.Error)
	assert.Equal(t, rpc.CodeInternal, broken.Error.Code)

	badEmbed := getPrompt(t, c, "bad_embed", nil)
	require.NotNil(t, badEmbed.Error)
	assert.Equal(t, rpc.CodeInvalidParams, badEmbed.Error.Code)

	bad := c.call("prompts/get", map[string]any{})
	require.NotNil(t, bad.Error)
	assert.Equal(t, rpc.CodeInvalidParams, bad.Error.Code)
}

func TestPromptMiddleware(t *testing.T) {
	s, _ := promptsServer(t)
	var seen []mcp.Request
	s.Use(func(ctx mcp.Context, req mcp.Request, next mcp.Handler) (any, error) {
		seen = append(seen, req)
		return next(ctx, req)
	})
	c := connect(t, newServer(t, s), nil)
	_ = getPrompt(t, c, promptSummarize, map[string]any{"id": "x"})
	require.NotEmpty(t, seen)
	assert.Equal(t, mcp.KindPrompt, seen[0].Kind)
	assert.Equal(t, promptSummarize, seen[0].Name)
}

func TestPromptRegistrationErrors(t *testing.T) {
	s := newBasicServer()
	handler := func(mcp.Context, mcp.Empty) (mcp.Messages, error) { return nil, nil }
	require.Error(t, mcp.AddPrompt(s, mcp.PromptDef{}, handler))
	require.NoError(t, mcp.AddPrompt(s, mcp.PromptDef{Name: "p"}, handler))
	require.Error(t, mcp.AddPrompt(s, mcp.PromptDef{Name: "p"}, handler))
	require.Error(t, mcp.AddPrompt(s, mcp.PromptDef{Name: "str"}, func(mcp.Context, string) (mcp.Messages, error) { return nil, nil }))
}

// ---- completion

type completeResult struct {
	Completion struct {
		Values  []string `json:"values"`
		Total   int      `json:"total"`
		HasMore bool     `json:"hasMore"`
	} `json:"completion"`
}

func complete(t *testing.T, c *testClient, ref map[string]any, arg, value string, resolved map[string]string) rpc.Message {
	t.Helper()
	return c.call("completion/complete", map[string]any{
		"ref":      ref,
		"argument": map[string]any{keyName: arg, "value": value},
		"context":  map[string]any{"arguments": resolved},
	})
}

func completionServer(t *testing.T) *testClient {
	t.Helper()
	s := newBasicServer()
	require.NoError(t, mcp.AddPrompt(s, mcp.PromptDef{Name: promptSummarize},
		func(mcp.Context, summarizeArgs) (mcp.Messages, error) { return nil, nil }))
	require.NoError(t, mcp.AddResource(s, mcp.ResourceDef{URI: noteTemplate, Name: "note"},
		func(mcp.Context, noteURI) (mcp.Contents, error) { return nil, nil }))
	require.NoError(t, s.AddPromptCompletion(promptSummarize, "style", mcp.Values("short", "bullets", "sparse")))
	require.NoError(t, s.AddResourceCompletion(noteTemplate, "id", func(_ mcp.Context, partial string, resolved map[string]string) (mcp.Completion, error) {
		if partial == "many" {
			values := make([]string, 150)
			for i := range values {
				values[i] = "note-" + string(rune('a'+i%26))
			}
			return mcp.Completion{Values: values}, nil
		}
		if partial == "fail" {
			return mcp.Completion{}, errors.New("index down")
		}
		return mcp.Completion{Values: []string{"note-1:" + resolved["owner"]}, Total: 5, HasMore: true}, nil
	}))
	return connect(t, newServer(t, s), nil)
}

func TestCompletionForPromptArgument(t *testing.T) {
	c := completionServer(t)
	promptRef := map[string]any{keyType: "ref/prompt", keyName: promptSummarize}

	res := result[completeResult](t, complete(t, c, promptRef, "style", "s", nil))
	assert.Equal(t, []string{"short", "sparse"}, res.Completion.Values)

	none := result[completeResult](t, complete(t, c, promptRef, "style", "zzz", nil))
	assert.Empty(t, none.Completion.Values)
	assert.NotNil(t, none.Completion.Values)
}

func TestCompletionForResourceTemplate(t *testing.T) {
	c := completionServer(t)
	ref := map[string]any{keyType: "ref/resource", keyURI: noteTemplate}

	res := result[completeResult](t, complete(t, c, ref, "id", "n", map[string]string{"owner": "bob"}))
	assert.Equal(t, []string{"note-1:bob"}, res.Completion.Values)
	assert.Equal(t, 5, res.Completion.Total)
	assert.True(t, res.Completion.HasMore)
}

func TestCompletionLimitsToHundredValues(t *testing.T) {
	c := completionServer(t)
	res := result[completeResult](t, complete(t, c, map[string]any{keyType: "ref/resource", keyURI: noteTemplate}, "id", "many", nil))
	assert.Len(t, res.Completion.Values, 100)
	assert.Equal(t, 150, res.Completion.Total)
	assert.True(t, res.Completion.HasMore)
}

func TestCompletionWithoutCompleterIsEmpty(t *testing.T) {
	c := completionServer(t)
	res := result[completeResult](t, complete(t, c, map[string]any{keyType: "ref/prompt", keyName: promptSummarize}, "id", "x", nil))
	assert.Empty(t, res.Completion.Values)

	res = result[completeResult](t, complete(t, c, map[string]any{keyType: "ref/prompt", keyName: "unknown"}, "x", "x", nil))
	assert.Empty(t, res.Completion.Values)
}

func TestCompletionErrors(t *testing.T) {
	c := completionServer(t)

	failing := complete(t, c, map[string]any{keyType: "ref/resource", keyURI: noteTemplate}, "id", "fail", nil)
	require.NotNil(t, failing.Error)
	assert.Equal(t, rpc.CodeInternal, failing.Error.Code)

	badRef := complete(t, c, map[string]any{keyType: "ref/other"}, "id", "x", nil)
	require.NotNil(t, badRef.Error)
	assert.Equal(t, rpc.CodeInvalidParams, badRef.Error.Code)

	badParams := c.call("completion/complete", map[string]any{})
	require.NotNil(t, badParams.Error)
	assert.Equal(t, rpc.CodeInvalidParams, badParams.Error.Code)
}

func TestCompletionRegistrationErrors(t *testing.T) {
	s := newBasicServer()
	require.Error(t, s.AddPromptCompletion("nope", "a", mcp.Values("x")))
	require.Error(t, s.AddResourceCompletion("note:///{id}", "id", mcp.Values("x")))
}

func TestValuesFiltersByPrefix(t *testing.T) {
	got, err := mcp.Values("alpha", "beta", "alpine")(nil, "al", nil)
	require.NoError(t, err)
	assert.Equal(t, []string{"alpha", "alpine"}, got.Values)
}

func TestCompletionMiddleware(t *testing.T) {
	s := newBasicServer()
	require.NoError(t, mcp.AddPrompt(s, mcp.PromptDef{Name: promptSummarize},
		func(mcp.Context, summarizeArgs) (mcp.Messages, error) { return nil, nil }))
	require.NoError(t, s.AddPromptCompletion(promptSummarize, "style", mcp.Values("short")))
	var seen []mcp.Kind
	s.Use(func(ctx mcp.Context, req mcp.Request, next mcp.Handler) (any, error) {
		seen = append(seen, req.Kind)
		return next(ctx, req)
	})
	c := connect(t, newServer(t, s), nil)
	_ = complete(t, c, map[string]any{keyType: "ref/prompt", keyName: promptSummarize}, "style", "s", nil)
	assert.Equal(t, []mcp.Kind{mcp.KindCompletion}, seen)
}
