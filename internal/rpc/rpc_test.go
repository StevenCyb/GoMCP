package rpc_test

import (
	"encoding/json"
	"testing"

	"github.com/StevenCyb/GoMCP/internal/rpc"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestMessageClassification(t *testing.T) {
	tests := []struct {
		name         string
		raw          string
		request      bool
		notification bool
		response     bool
	}{
		{"request", `{"jsonrpc":"2.0","id":1,"method":"ping"}`, true, false, false},
		{"string id request", `{"jsonrpc":"2.0","id":"a","method":"ping"}`, true, false, false},
		{"notification", `{"jsonrpc":"2.0","method":"notifications/initialized"}`, false, true, false},
		{"null id is notification", `{"jsonrpc":"2.0","id":null,"method":"x"}`, false, true, false},
		{"result response", `{"jsonrpc":"2.0","id":1,"result":{}}`, false, false, true},
		{"error response", `{"jsonrpc":"2.0","id":1,"error":{"code":-1,"message":"no"}}`, false, false, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var m rpc.Message
			require.NoError(t, json.Unmarshal([]byte(tt.raw), &m))
			assert.Equal(t, tt.request, m.IsRequest())
			assert.Equal(t, tt.notification, m.IsNotification())
		})
	}
}

func TestNewResultAndError(t *testing.T) {
	res := rpc.NewResult(json.RawMessage(`7`), map[string]string{"a": "b"})
	b, err := json.Marshal(res)
	require.NoError(t, err)
	assert.JSONEq(t, `{"jsonrpc":"2.0","id":7,"result":{"a":"b"}}`, string(b))

	failure := rpc.NewError(json.RawMessage(`"x"`), rpc.MethodNotFound("foo"))
	b, err = json.Marshal(failure)
	require.NoError(t, err)
	assert.JSONEq(t, `{"jsonrpc":"2.0","id":"x","error":{"code":-32601,"message":"method not found: foo"}}`, string(b))
}

func TestNewNotification(t *testing.T) {
	n := rpc.NewNotification("notifications/progress", map[string]int{"progress": 1})
	assert.True(t, n.IsNotification())
	assert.JSONEq(t, `{"progress":1}`, string(n.Params))
}

func TestErrorConstructors(t *testing.T) {
	assert.Equal(t, rpc.CodeInvalidParams, rpc.InvalidParams("bad").Code)
	assert.Contains(t, rpc.InvalidParams("bad").Error(), "bad")
	assert.Equal(t, rpc.CodeInternal, rpc.Internal("oops").Code)
	assert.Equal(t, rpc.CodeInvalidRequest, rpc.InvalidRequest("nope").Code)
	assert.Equal(t, rpc.CodeParse, rpc.ParseError("bad json").Code)
	assert.Equal(t, rpc.CodeHeaderMismatch, rpc.HeaderMismatch("x").Code)

	missing := rpc.MissingCapability(map[string]any{"roots": map[string]any{}})
	assert.Equal(t, rpc.CodeMissingCapability, missing.Code)
	assert.JSONEq(t, `{"requiredCapabilities":{"roots":{}}}`, string(rpc.MustJSON(missing.Data)))

	version := rpc.UnsupportedVersion([]string{"2026-07-28"}, "1900-01-01")
	assert.Equal(t, rpc.CodeUnsupportedProtocol, version.Code)
	assert.JSONEq(t, `{"supported":["2026-07-28"],"requested":"1900-01-01"}`, string(rpc.MustJSON(version.Data)))
}

func TestMustJSON(t *testing.T) {
	assert.Nil(t, rpc.MustJSON(nil))
	assert.JSONEq(t, `{"a":1}`, string(rpc.MustJSON(map[string]int{"a": 1})))
	assert.Panics(t, func() { rpc.MustJSON(make(chan int)) })
}
