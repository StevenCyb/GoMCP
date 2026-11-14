package e2e_test

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/StevenCyb/GoMCP/internal/runner"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const (
	bearer     = "any-token" // the example's OAuth verifier is a stub that accepts every non-empty token
	capElicit  = "elicitation"
	capRoots   = "roots"
	capSamples = "sampling"
	capTasks   = "extensions"
	extTasks   = "io.modelcontextprotocol/tasks"
	noteURI    = "note:///"
)

// startExample builds and runs the annotated example and returns its MCP endpoint.
func startExample(t *testing.T) string {
	t.Helper()
	if testing.Short() {
		t.Skip("builds and runs the example")
	}
	addr := freeAddr(t)
	t.Setenv("GOMCP_ADDR", addr)

	ctx, cancel := context.WithCancel(t.Context())
	done := make(chan error, 1)
	go func() {
		done <- runner.Serve(ctx, runner.Options{Dir: "../example", Stdout: io.Discard, Stderr: io.Discard})
	}()
	t.Cleanup(func() {
		cancel()
		select {
		case err := <-done:
			assert.NoError(t, err)
		case <-time.After(20 * time.Second):
			t.Error("the example did not stop")
		}
	})

	url := "http://" + addr + "/mcp"
	require.Eventually(t, func() bool {
		req, err := http.NewRequestWithContext(t.Context(), http.MethodPost, url, strings.NewReader("{}"))
		if err != nil {
			return false
		}
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			return false
		}
		_ = resp.Body.Close()
		return true
	}, 60*time.Second, 200*time.Millisecond, "the example did not start")
	return url
}

// TestExample runs every feature of the example through a client of the protocol. It is the
// regression test of the whole chain: annotations, generator, runtime and transport.
func TestExample(t *testing.T) {
	url := startExample(t)
	base := strings.TrimSuffix(url, "/mcp")
	used := newUsage()
	anon := &e2eClient{t: t, url: url, caps: obj{}, used: used}
	c := anon.with(bearer, obj{})

	t.Run("protocol", func(t *testing.T) {
		c := c.with(bearer, obj{})
		c.t = t
		res, rpcErr := c.once("server/discover", obj{})
		require.Nil(t, rpcErr)
		assert.Equal(t, "complete", res[keyResultTyp])
		assert.Equal(t, []any{specVersion}, res["supportedVersions"])
		assert.Contains(t, res["instructions"], "notes tools")
		assert.InDelta(t, 300000, res["ttlMs"], 0, "@MCP.Cache(5m)")
		assert.Equal(t, "private", res["cacheScope"])
		caps, _ := res["capabilities"].(obj)
		for _, name := range []string{"tools", "resources", "prompts", "completions"} {
			assert.Contains(t, caps, name)
		}
		info, _ := res["_meta"].(obj)["io.modelcontextprotocol/serverInfo"].(obj)
		assert.Equal(t, "notes", info[keyName])
		assert.Equal(t, "Notes MCP Example", info["title"])
		assert.Equal(t, "1.0.0", info["version"])

		// the protocol headers must match the body
		resp := c.post("tools/list", c.meta(obj{}), map[string]string{"Mcp-Method": "tools/call"})
		_ = resp.Body.Close()
		assert.Equal(t, http.StatusBadRequest, resp.StatusCode)
		_, bad := c.raw("tools/call", c.meta(obj{keyName: "get_note"}), map[string]string{"Mcp-Name": "other"})
		require.NotNil(t, bad)
		assert.InDelta(t, -32020, bad["code"], 0, "header mismatch")

		// an unknown protocol version is refused with the supported ones
		params := obj{"_meta": obj{metaVersion: "1999-01-01", metaCaps: obj{}}}
		_, unsupported := c.raw("tools/list", params, map[string]string{"Mcp-Protocol-Version": "1999-01-01"})
		require.NotNil(t, unsupported)
		assert.InDelta(t, -32022, unsupported["code"], 0)

		// only POST, only trusted origins
		get, err := http.NewRequestWithContext(t.Context(), http.MethodGet, url, nil)
		require.NoError(t, err)
		getResp, err := http.DefaultClient.Do(get)
		require.NoError(t, err)
		_ = getResp.Body.Close()
		assert.Equal(t, http.StatusMethodNotAllowed, getResp.StatusCode)

		resp = c.post("tools/list", c.meta(obj{}), map[string]string{"Origin": "https://evil.example"})
		_ = resp.Body.Close()
		assert.Equal(t, http.StatusForbidden, resp.StatusCode)
		resp = c.post("tools/list", c.meta(obj{}), map[string]string{"Origin": "https://claude.ai"})
		_ = resp.Body.Close()
		assert.Equal(t, http.StatusOK, resp.StatusCode)
	})

	t.Run("auth", func(t *testing.T) {
		resp := anon.post("tools/list", anon.meta(obj{}), nil)
		_ = resp.Body.Close()
		assert.Equal(t, http.StatusUnauthorized, resp.StatusCode)
		challenge := resp.Header.Get("WWW-Authenticate")
		assert.Contains(t, challenge, "Bearer")
		assert.Contains(t, challenge, "resource_metadata=")

		meta, err := http.NewRequestWithContext(t.Context(), http.MethodGet, base+"/.well-known/oauth-protected-resource/mcp", nil)
		require.NoError(t, err)
		metaResp, err := http.DefaultClient.Do(meta)
		require.NoError(t, err)
		defer func() { _ = metaResp.Body.Close() }()
		require.Equal(t, http.StatusOK, metaResp.StatusCode)
		var doc obj
		require.NoError(t, json.NewDecoder(metaResp.Body).Decode(&doc))
		assert.Equal(t, "https://notes.example.com/mcp", doc["resource"])
		assert.Equal(t, []any{"https://auth.example.com"}, doc["authorization_servers"])
		assert.ElementsMatch(t, []any{"notes:read", "notes:write"}, doc["scopes_supported"])
	})

	t.Run("tools", func(t *testing.T) {
		c := c.with(bearer, obj{})
		c.t = t
		list, rpcErr := c.once("tools/list", obj{})
		require.Nil(t, rpcErr)
		assert.Equal(t, "complete", list[keyResultTyp])
		assert.InDelta(t, 300000, list["ttlMs"], 0)
		byName := map[string]obj{}
		for _, item := range list["tools"].([]any) {
			tool, _ := item.(obj)
			byName[tool[keyName].(string)] = tool
		}
		for _, name := range []string{"add_note", "get_note", "delete_note", "detail_note", "connect_account", "summarize_all", toolImport} {
			assert.Contains(t, byName, name)
		}
		assert.Equal(t, obj{"destructiveHint": true}, byName["delete_note"]["annotations"])
		assert.Equal(t, obj{"readOnlyHint": true, "idempotentHint": true}, byName["get_note"]["annotations"])
		assert.Equal(t, obj{"team": "docs"}, byName["add_note"]["_meta"])
		input, _ := byName["add_note"]["inputSchema"].(obj)
		props, _ := input["properties"].(obj)
		assert.Equal(t, []any{keyText}, input["required"])
		assert.Equal(t, []any{"work", "private", "idea"}, props["tag"].(obj)["enum"])
		assert.Equal(t, "idea", props["tag"].(obj)["default"])
		assert.InDelta(t, 500, props[keyText].(obj)["maxLength"], 0, "max on a string is its length")
		assert.NotNil(t, byName["add_note"]["outputSchema"], "a struct result has an output schema")

		icons, _ := byName["add_note"]["icons"].([]any)
		require.Len(t, icons, 1)
		icon, _ := icons[0].(obj)
		assert.Equal(t, base+"/assets/add.svg", icon["src"], "relative icon paths resolve to the served URL")
		assert.Equal(t, "image/svg+xml", icon["mimeType"])
		resp, err := http.DefaultClient.Do(mustGet(t, icon["src"].(string)))
		require.NoError(t, err)
		_ = resp.Body.Close()
		assert.Equal(t, http.StatusOK, resp.StatusCode, "the server serves its assets")

		// add and get
		added := c.tool("add_note", obj{keyText: "buy milk", "tag": "work"})
		assert.False(t, isError(added))
		note := structured(t, added)
		id, _ := note["id"].(string)
		assert.NotEmpty(t, id)
		assert.Equal(t, "buy milk", note[keyText])
		assert.Equal(t, "work", note["tag"])

		got := c.tool("get_note", obj{"id": id})
		assert.Equal(t, "buy milk", structured(t, got)[keyText])
		content, _ := got[keyContent].([]any)
		require.Len(t, content, 2)
		link, _ := content[1].(obj)
		assert.Equal(t, "resource_link", link["type"])
		assert.Equal(t, noteURI+id, link[keyURI])

		// execution errors are results the model can read, invalid arguments too
		assert.True(t, isError(c.tool("get_note", obj{"id": "nope"})))
		assert.True(t, isError(c.tool("add_note", obj{keyText: ""})), "text needs at least one character")
		assert.True(t, isError(c.tool("add_note", obj{keyText: "x", "tag": "bogus"})), "tag is an enum")
		assert.True(t, isError(c.tool("add_note", obj{})), "text is required")

		// the middleware and the log level: logs only travel when the request asks for them
		quiet := c.tool("add_note", obj{keyText: "quiet"})
		assert.False(t, isError(quiet))
		assert.Empty(t, notesOf(c.notes, "notifications/message"))
		res, err2 := c.call("tools/call", obj{keyName: "add_note", keyArguments: obj{keyText: "loud"}, "_meta": obj{"io.modelcontextprotocol/logLevel": "info"}})
		require.Nil(t, err2)
		assert.False(t, isError(res))
		assert.NotEmpty(t, notesOf(c.notes, "notifications/message"), "the tool logs when the request has a log level")
	})

	t.Run("resources", func(t *testing.T) {
		c := c.with(bearer, obj{})
		c.t = t
		id := structured(t, c.tool("add_note", obj{keyText: "resource body"}))["id"].(string)

		templates, rpcErr := c.once("resources/templates/list", obj{})
		require.Nil(t, rpcErr)
		list, _ := templates["resourceTemplates"].([]any)
		require.Len(t, list, 1)
		first, _ := list[0].(obj)
		assert.Equal(t, noteURI+"{id}", first["uriTemplate"])
		assert.Equal(t, "text/plain", first["mimeType"])
		assert.Equal(t, obj{"audience": []any{"user", "assistant"}, "priority": 0.8}, first["annotations"])
		assert.InDelta(t, 300000, templates["ttlMs"], 0)

		listed, rpcErr := c.once("resources/list", obj{})
		require.Nil(t, rpcErr)
		resources, _ := listed["resources"].([]any)
		uris := make([]string, 0, len(resources))
		for _, item := range resources {
			uris = append(uris, item.(obj)[keyURI].(string))
		}
		assert.Contains(t, uris, noteURI+id)

		read, rpcErr := c.read(noteURI + id)
		require.Nil(t, rpcErr)
		contents, _ := read["contents"].([]any)
		require.Len(t, contents, 1)
		item, _ := contents[0].(obj)
		assert.Equal(t, "resource body", item[keyText])
		assert.Equal(t, "text/plain", item["mimeType"])
		assert.InDelta(t, 300000, read["ttlMs"], 0, "reads are cacheable")

		// the static resource wins over the template
		index, rpcErr := c.read(noteURI + "index")
		require.Nil(t, rpcErr)
		indexItem, _ := index["contents"].([]any)[0].(obj)
		assert.Equal(t, "application/json", indexItem["mimeType"])
		assert.Contains(t, indexItem[keyText], id)

		_, missing := c.read(noteURI + "missing")
		require.NotNil(t, missing)
		assert.InDelta(t, -32602, missing["code"], 0)
		assert.Equal(t, obj{keyURI: noteURI + "missing"}, missing["data"])
	})

	t.Run("prompts and completion", func(t *testing.T) {
		c := c.with(bearer, obj{})
		c.t = t
		id := structured(t, c.tool("add_note", obj{keyText: "prompt body"}))["id"].(string)

		list, rpcErr := c.once("prompts/list", obj{})
		require.Nil(t, rpcErr)
		prompts, _ := list["prompts"].([]any)
		require.Len(t, prompts, 1)
		prompt, _ := prompts[0].(obj)
		assert.Equal(t, "summarize_notes", prompt[keyName])
		args, _ := prompt["arguments"].([]any)
		require.Len(t, args, 2)

		got, rpcErr := c.prompt("summarize_notes", obj{"id": id, "style": "short"})
		require.Nil(t, rpcErr)
		messages, _ := got["messages"].([]any)
		require.Len(t, messages, 2)
		first, _ := messages[0].(obj)["content"].(obj)
		assert.Contains(t, first[keyText], "(short)")
		embedded, _ := messages[1].(obj)["content"].(obj)
		assert.Equal(t, "resource", embedded["type"])
		assert.Equal(t, noteURI+id, embedded["resource"].(obj)[keyURI])
		assert.Equal(t, "prompt body", embedded["resource"].(obj)[keyText])

		_, noID := c.prompt("summarize_notes", obj{})
		assert.NotNil(t, noID, "id is required")

		complete := func(ref obj, arg, value string) []any {
			res, rpcErr := c.once("completion/complete", obj{"ref": ref, "argument": obj{keyName: arg, "value": value}})
			require.Nil(t, rpcErr)
			values, _ := res["completion"].(obj)["values"].([]any)
			return values
		}
		promptRef := obj{"type": "ref/prompt", keyName: "summarize_notes"}
		assert.Equal(t, []any{"short"}, complete(promptRef, "style", "sh"), "static values")
		assert.Equal(t, []any{"short", "bullets"}, complete(promptRef, "style", ""))
		assert.Contains(t, complete(promptRef, "id", "note-"), id, "dynamic values of a prompt argument")
		assert.Contains(t, complete(obj{"type": "ref/resource", keyURI: noteURI + "{id}"}, "id", "note-"), id, "dynamic values of a resource variable")
		assert.Empty(t, complete(promptRef, "id", "zzz"))
	})

	t.Run("elicitation", func(t *testing.T) {
		c := c.with(bearer, obj{capElicit: obj{"form": obj{}, "url": obj{}}})
		c.t = t
		deleteAfter := func(answerWith obj) (obj, string) {
			id := structured(t, c.tool("add_note", obj{keyText: "to delete"}))["id"].(string)
			c.answer = func(method string, _ obj) obj {
				assert.Equal(t, "elicitation/create", method)
				return answerWith
			}
			return c.tool("delete_note", obj{"id": id}), id
		}

		// the user confirms
		res, id := deleteAfter(obj{keyAction: valAccept, keyContent: obj{"confirm": true}})
		assert.False(t, isError(res), "%v", res)
		got := c.tool("get_note", obj{"id": id})
		assert.True(t, isError(got), "the note is gone")

		// the user says no, declines or cancels: the note stays
		for name, answer := range map[string]obj{
			"confirm false": {keyAction: valAccept, keyContent: obj{"confirm": false}},
			"declined":      {keyAction: "decline"},
			"cancelled":     {keyAction: "cancel"},
		} {
			res, id := deleteAfter(answer)
			assert.True(t, isError(res), name)
			assert.Equal(t, "not confirmed", text(t, res), name)
			assert.False(t, isError(c.tool("get_note", obj{"id": id})), name)
		}

		// a form with several fields
		noteID := structured(t, c.tool("add_note", obj{keyText: "details"}))["id"].(string)
		var form obj
		c.answer = func(method string, params obj) obj {
			form, _ = params["requestedSchema"].(obj)
			return obj{keyAction: valAccept, keyContent: obj{"tag": "work", "priority": 2, "notify": false}}
		}
		detailed := c.tool("detail_note", obj{"id": noteID})
		assert.Equal(t, "work", structured(t, detailed)["tag"])
		formProps, _ := form["properties"].(obj)
		assert.Equal(t, []any{"work", "private", "idea"}, formProps["tag"].(obj)["enum"])
		assert.InDelta(t, 5, formProps["priority"].(obj)["maximum"], 0)

		// URL elicitation: declined means no wait; the page backend of the example is not part of this process
		c.answer = func(method string, params obj) obj {
			assert.Equal(t, "url", params["mode"])
			assert.True(t, strings.HasPrefix(params[keyURL].(string), "https://example.com/connect?e="))
			return obj{keyAction: "decline"}
		}
		assert.True(t, isError(c.tool("connect_account", obj{})))

		// without the capability the request fails before the tool runs
		plain := c.with(bearer, obj{})
		plain.t = t
		for _, tool := range []string{"delete_note", "detail_note", "connect_account", "summarize_all", toolImport} {
			rpcErr := plain.toolErr(tool, obj{"id": noteID})
			assert.InDelta(t, -32021, rpcErr["code"], 0, tool)
			assert.NotNil(t, rpcErr["data"], tool)
		}
	})

	t.Run("sampling", func(t *testing.T) {
		c := c.with(bearer, obj{capSamples: obj{}})
		c.t = t
		var asked obj
		c.answer = func(method string, params obj) obj {
			assert.Equal(t, "sampling/createMessage", method)
			asked = params
			return obj{"role": "assistant", keyContent: obj{"type": keyText, keyText: "two notes"}, "model": "test-model", "stopReason": "endTurn"}
		}
		res := c.tool("summarize_all", obj{})
		assert.False(t, isError(res), "%v", res)
		assert.Contains(t, text(t, res), "two notes")
		assert.NotEmpty(t, asked["messages"])
		assert.NotZero(t, asked["maxTokens"])
	})

	t.Run("roots and progress", func(t *testing.T) {
		c := c.with(bearer, obj{capRoots: obj{}})
		c.t = t
		asked := 0
		c.answer = func(method string, params obj) obj {
			asked++
			assert.Equal(t, "roots/list", method)
			return obj{"roots": []any{obj{keyURI: "file:///work/a"}, obj{keyURI: "file:///work/b"}}}
		}
		res, rpcErr := c.call("tools/call", obj{keyName: toolImport, keyArguments: obj{}, "_meta": obj{"progressToken": "tok"}})
		require.Nil(t, rpcErr)
		c.used.tools[toolImport] = true
		assert.False(t, isError(res), "%v", res)
		assert.Equal(t, 1, asked, "one round trip for the roots")
		assert.Len(t, notesOf(c.notes, "notifications/progress"), 2, "one progress per root")
	})

	t.Run("tasks", func(t *testing.T) {
		c := c.with(bearer, obj{capRoots: obj{}, capTasks: obj{extTasks: obj{}}})
		c.t = t
		c.used.tools[toolImport] = true
		created, rpcErr := c.call("tools/call", obj{keyName: toolImport, keyArguments: obj{}})
		require.Nil(t, rpcErr)
		require.Equal(t, "task", created[keyResultTyp], "the server turns the call into a task")
		id, _ := created[keyTaskID].(string)
		require.NotEmpty(t, id)
		assert.InDelta(t, 600000, created["ttlMs"], 0, "@MCP.Task(ttl=10m)")
		assert.InDelta(t, 2000, created["pollIntervalMs"], 0, "@MCP.Task(poll=2s)")

		// the import needs the roots, so the task waits for the client
		var waiting obj
		require.Eventually(t, func() bool {
			got, err := c.call("tasks/get", obj{keyTaskID: id})
			waiting = got
			return err == nil && got[keyStatus] == resInputReq
		}, 5*time.Second, 50*time.Millisecond)
		requests, _ := waiting["inputRequests"].(obj)
		require.Len(t, requests, 1)
		var key string
		for k, item := range requests {
			key = k
			assert.Equal(t, "roots/list", item.(obj)[keyMethod])
		}
		_, err := c.call("tasks/update", obj{keyTaskID: id, "inputResponses": obj{key: obj{"roots": []any{obj{keyURI: "file:///work/a"}}}}})
		require.Nil(t, err)

		var done obj
		require.Eventually(t, func() bool {
			done, _ = c.call("tasks/get", obj{keyTaskID: id})
			return done[keyStatus] == "completed"
		}, 10*time.Second, 50*time.Millisecond)
		assert.NotNil(t, done["result"])

		// a task can be cancelled and unknown ids are rejected
		second, _ := c.call("tools/call", obj{keyName: toolImport, keyArguments: obj{}})
		_, err = c.call("tasks/cancel", obj{keyTaskID: second[keyTaskID]})
		require.Nil(t, err)
		cancelled, _ := c.call("tasks/get", obj{keyTaskID: second[keyTaskID]})
		assert.Equal(t, "cancelled", cancelled[keyStatus])
		_, unknown := c.call("tasks/get", obj{keyTaskID: "nope"})
		assert.NotNil(t, unknown)

		// a client without the extension gets no task methods
		noExt := c.with(bearer, obj{})
		noExt.t = t
		_, missing := noExt.once("tasks/get", obj{keyTaskID: id})
		require.NotNil(t, missing)
		assert.InDelta(t, -32021, missing["code"], 0)
	})

	t.Run("listening", func(t *testing.T) {
		c := c.with(bearer, obj{capElicit: obj{"form": obj{}}})
		c.t = t
		id := structured(t, c.tool("add_note", obj{keyText: "watched"}))["id"].(string)

		events := c.listen(obj{"resourcesListChanged": true, "resourceSubscriptions": []any{noteURI + id, "unknown://x"}})
		ack := events.waitFor("notifications/subscriptions/acknowledged")
		agreed, _ := ack["params"].(obj)["notifications"].(obj)
		assert.Equal(t, []any{noteURI + id}, agreed["resourceSubscriptions"], "only known resources are acknowledged")
		assert.Equal(t, true, agreed["resourcesListChanged"])

		c.tool("add_note", obj{keyText: "another"})
		events.waitFor("notifications/resources/list_changed")

		c.answer = func(string, obj) obj { return obj{keyAction: valAccept, keyContent: obj{"confirm": true}} }
		c.tool("delete_note", obj{"id": id})
		updated := events.waitFor("notifications/resources/updated")
		assert.Equal(t, noteURI+id, updated["params"].(obj)[keyURI])
	})

	t.Run("every feature is covered", func(t *testing.T) {
		c := c.with(bearer, obj{})
		c.t = t
		listed := func(method, key, field string) []string {
			res, rpcErr := c.once(method, obj{})
			require.Nil(t, rpcErr)
			items, _ := res[key].([]any)
			out := make([]string, 0, len(items))
			for _, item := range items {
				out = append(out, item.(obj)[field].(string))
			}
			return out
		}
		for _, name := range listed("tools/list", "tools", keyName) {
			assert.True(t, used.tools[name], "the e2e test never calls tool %s", name)
		}
		for _, name := range listed("prompts/list", "prompts", keyName) {
			assert.True(t, used.prompts[name], "the e2e test never gets prompt %s", name)
		}
		assert.True(t, used.resources[noteURI+"index"], "the static resource is never read")
	})
}

func notesOf(notes []obj, method string) []obj {
	var out []obj
	for _, n := range notes {
		if n[keyMethod] == method {
			out = append(out, n)
		}
	}
	return out
}

func mustGet(t *testing.T, target string) *http.Request {
	t.Helper()
	req, err := http.NewRequestWithContext(t.Context(), http.MethodGet, target, nil)
	require.NoError(t, err)
	return req
}
