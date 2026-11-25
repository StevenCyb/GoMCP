// Stdio test: build and run examples/features/ over stdin and stdout, like a client that starts the server itself.
package e2e_test

import (
	"bufio"
	"encoding/json"
	"io"
	"maps"
	"testing"
	"time"

	"github.com/StevenCyb/GoMCP/internal/runner"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// stdioClient writes requests to the stdin of the server and reads its stdout line by line.
type stdioClient struct {
	t     *testing.T
	in    *io.PipeWriter
	lines chan obj
	id    int
	bad   chan string // stdout lines that are not JSON: the protocol must be the only thing on stdout
}

func startStdioExample(t *testing.T) *stdioClient {
	t.Helper()
	return startStdio(t, "../examples/features")
}

// startStdio builds and runs the server in dir over pipes.
func startStdio(t *testing.T, dir string) *stdioClient {
	t.Helper()
	if testing.Short() {
		t.Skip("builds and runs the example")
	}
	inR, inW := io.Pipe()
	outR, outW := io.Pipe()
	c := &stdioClient{t: t, in: inW, lines: make(chan obj, 64), bad: make(chan string, 8)}

	done := make(chan error, 1)
	go func() {
		done <- runner.Serve(t.Context(), runner.Options{Dir: dir, Stdio: true, Stdin: inR, Stdout: outW, Stderr: io.Discard})
		_ = outW.Close()
	}()
	go func() {
		sc := bufio.NewScanner(outR)
		sc.Buffer(make([]byte, 0, 64*1024), 4<<20)
		for sc.Scan() {
			var m obj
			if json.Unmarshal(sc.Bytes(), &m) != nil {
				c.bad <- sc.Text()
				continue
			}
			c.lines <- m
		}
		close(c.lines)
	}()
	t.Cleanup(func() {
		_ = inW.Close() // end of input stops the server
		select {
		case err := <-done:
			assert.NoError(t, err)
		case <-time.After(20 * time.Second):
			t.Error("the stdio server did not stop at end of input")
		}
		select {
		case line := <-c.bad:
			t.Errorf("stdout carried something that is not a protocol message: %q", line)
		default:
		}
	})
	return c
}

func (c *stdioClient) send(method string, params obj) int {
	c.t.Helper()
	c.id++
	meta := obj{metaVersion: specVersion, metaCaps: obj{capRoots: obj{}, capElicit: obj{"form": obj{}}}}
	p := obj{keyMeta: meta}
	for k, v := range params {
		if k == keyMeta {
			given, _ := v.(obj)
			maps.Copy(meta, given)
			continue
		}
		p[k] = v
	}
	raw, err := json.Marshal(obj{keyJSONRPC: jsonRPC, "id": c.id, keyMethod: method, keyParams: p})
	require.NoError(c.t, err)
	_, err = c.in.Write(append(raw, '\n'))
	require.NoError(c.t, err)
	return c.id
}

func (c *stdioClient) next() obj {
	c.t.Helper()
	select {
	case m, ok := <-c.lines:
		require.True(c.t, ok, "the server closed stdout")
		return m
	case <-time.After(60 * time.Second): // the first message waits for the build
		require.FailNow(c.t, "timed out waiting for a message")
		return nil
	}
}

// call sends a request and returns its result, answering input requests with answer and collecting notifications.
func (c *stdioClient) call(method string, params obj, answer func(method string, params obj) obj) (obj, obj, []obj) {
	c.t.Helper()
	var notes []obj
	for range 6 {
		id := c.send(method, params)
		for {
			m := c.next()
			if m[keyMethod] != nil {
				notes = append(notes, m)
				continue
			}
			require.InDelta(c.t, id, m["id"], 0)
			result, rpcErr := split(m)
			if rpcErr != nil || result[keyResultTyp] != resInputReq {
				return result, rpcErr, notes
			}
			require.NotNil(c.t, answer, "unexpected input request")
			answers := obj{}
			requests, _ := result["inputRequests"].(obj)
			for key, item := range requests {
				req, _ := item.(obj)
				p, _ := req[keyParams].(obj)
				answers[key] = answer(req[keyMethod].(string), p)
			}
			next := obj{}
			maps.Copy(next, params)
			next["inputResponses"] = answers
			if state, ok := result["requestState"].(string); ok {
				next["requestState"] = state
			}
			params = next
			break
		}
	}
	require.FailNow(c.t, "too many rounds")
	return nil, nil, nil
}

func TestStdio(t *testing.T) {
	c := startStdioExample(t)

	t.Run("discover and tools without any authentication", func(t *testing.T) {
		res, rpcErr, _ := c.call("server/discover", obj{}, nil)
		require.Nil(t, rpcErr)
		assert.Equal(t, []any{specVersion}, res["supportedVersions"])

		// add_note needs the scope notes:write over HTTP; stdio callers are trusted
		added, rpcErr, _ := c.call("tools/call", obj{keyName: "add_note", keyArguments: obj{keyText: "over stdio"}}, nil)
		require.Nil(t, rpcErr)
		assert.False(t, isError(added), "%v", added)
		id := structured(t, added)["id"].(string)

		got, rpcErr, _ := c.call("tools/call", obj{keyName: "get_note", keyArguments: obj{"id": id}}, nil)
		require.Nil(t, rpcErr)
		assert.Equal(t, "over stdio", structured(t, got)[keyText])

		read, rpcErr, _ := c.call("resources/read", obj{keyURI: noteURI + id}, nil)
		require.Nil(t, rpcErr)
		assert.Equal(t, "over stdio", read["contents"].([]any)[0].(obj)[keyText])
	})

	t.Run("the server asks the client for input", func(t *testing.T) {
		asked := 0
		res, rpcErr, notes := c.call("tools/call", obj{
			keyName: toolImport, keyArguments: obj{}, keyMeta: obj{"progressToken": "tok"},
		}, func(method string, _ obj) obj {
			asked++
			assert.Equal(t, "roots/list", method)
			return obj{"roots": []any{obj{keyURI: rootA}}}
		})
		require.Nil(t, rpcErr)
		assert.False(t, isError(res), "%v", res)
		assert.Equal(t, 1, asked)
		assert.Len(t, notesOf(notes, "notifications/progress"), 1)
	})

	t.Run("protocol errors have no http status but the same codes", func(t *testing.T) {
		_, rpcErr, _ := c.call("resources/read", obj{keyURI: noteURI + "missing"}, nil)
		require.NotNil(t, rpcErr)
		assert.InDelta(t, -32602, rpcErr["code"], 0)
	})

	t.Run("listening delivers notifications on the same stream", func(t *testing.T) {
		id := structured(t, mustTool(t, c, "add_note", obj{keyText: "watched"}))["id"].(string)
		listenID := c.send("subscriptions/listen", obj{"notifications": obj{"resourcesListChanged": true, "resourceSubscriptions": []any{noteURI + id}}})
		ack := c.next()
		assert.Equal(t, "notifications/subscriptions/acknowledged", ack[keyMethod])

		// a tool call while the listen request is open: its notifications and response arrive on the same stdout
		added, rpcErr, notes := c.call("tools/call", obj{keyName: "add_note", keyArguments: obj{keyText: "another"}}, nil)
		require.Nil(t, rpcErr)
		assert.False(t, isError(added))
		listChanged := notesOf(notes, "notifications/resources/list_changed")
		if len(listChanged) == 0 {
			assert.Equal(t, "notifications/resources/list_changed", c.next()[keyMethod])
		}

		// cancelling ends the listen request without a response
		raw, err := json.Marshal(obj{keyJSONRPC: jsonRPC, keyMethod: "notifications/cancelled", keyParams: obj{"requestId": listenID}})
		require.NoError(t, err)
		_, err = c.in.Write(append(raw, '\n'))
		require.NoError(t, err)
		_, rpcErr, _ = c.call("tools/list", obj{}, nil)
		assert.Nil(t, rpcErr, "the server keeps serving after the listen was cancelled")
	})

	t.Run("bad input does not stop the server", func(t *testing.T) {
		_, err := c.in.Write([]byte("not json\n"))
		require.NoError(t, err)
		m := c.next()
		_, rpcErr := split(m)
		require.NotNil(t, rpcErr)
		assert.InDelta(t, -32700, rpcErr["code"], 0)
		_, rpcErr, _ = c.call("tools/list", obj{}, nil)
		assert.Nil(t, rpcErr)
	})
}

func mustTool(t *testing.T, c *stdioClient, name string, args obj) obj {
	t.Helper()
	res, rpcErr, _ := c.call("tools/call", obj{keyName: name, keyArguments: args}, nil)
	require.Nil(t, rpcErr)
	return res
}
