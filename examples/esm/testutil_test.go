package api

import (
	"bufio"
	"bytes"
	"context"
	"crypto/rand"
	"crypto/rsa"
	"encoding/json"
	"io"
	"log"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/StevenCyb/GoMCP/pkg/mcp"
	"github.com/coreos/go-oidc/v3/oidc"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
)

const (
	toolCreate  = "CreateTicket"
	toolList    = "ListOwnTickets"
	toolMine    = "UpdateMyTicket"
	toolUpdate  = "UpdateTicket"
	toolPending = "ListPendingReviews"
	toolReview  = "ReviewTicket"
	roleReview  = "esm:reviewer"
	reviewerOid = "reviewer-oid"
	opsOid      = "ops-oid"
	roleOps     = "esm:ops"
	userOid     = "user-oid"
	elicitation = "elicitation/create"
	methodCall  = "tools/call"
	codeParams  = -32602
	codeNoCap   = -32021
)

// mockRepository is a TicketRepository whose calls the tests set up and check.
type mockRepository struct{ mock.Mock }

func (m *mockRepository) Create(ctx context.Context, t Ticket) (Ticket, error) {
	args := m.Called(ctx, t)
	return args.Get(0).(Ticket), args.Error(1)
}

func (m *mockRepository) ListByStatus(ctx context.Context, status TicketStatus, exceptOwner string) ([]Ticket, error) {
	args := m.Called(ctx, status, exceptOwner)
	tickets, _ := args.Get(0).([]Ticket)
	return tickets, args.Error(1)
}

func (m *mockRepository) Get(ctx context.Context, id string) (Ticket, error) {
	args := m.Called(ctx, id)
	return args.Get(0).(Ticket), args.Error(1)
}

func (m *mockRepository) AppendJournal(ctx context.Context, id string, expected TicketStatus, j Journal) (Ticket, error) {
	args := m.Called(ctx, id, expected, j)
	return args.Get(0).(Ticket), args.Error(1)
}

func (m *mockRepository) ListByOwner(ctx context.Context, owner string, f TicketFilter) ([]Ticket, error) {
	args := m.Called(ctx, owner, f)
	tickets, _ := args.Get(0).([]Ticket)
	return tickets, args.Error(1)
}

type rpcMessage struct {
	Method string          `json:"method"`
	Result json.RawMessage `json:"result"`
	Error  *struct {
		Code    int    `json:"code"`
		Message string `json:"message"`
	} `json:"error"`
}

type toolResult struct {
	Content           []map[string]any `json:"content"`
	StructuredContent json.RawMessage  `json:"structuredContent"`
	IsError           bool             `json:"isError"`
}

func (r toolResult) text() string {
	if len(r.Content) == 0 {
		return ""
	}
	s, _ := r.Content[0]["text"].(string)
	return s
}

// entra is one fake Entra for the whole run: generating an RSA key per test is what made them slow.
var entra struct {
	key      *rsa.PrivateKey
	srv      *httptest.Server
	verifier *oidc.IDTokenVerifier
}

func TestMain(m *testing.M) {
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		log.Fatal(err)
	}
	entra.key, entra.srv = key, startFakeEntra(key)
	if entra.verifier, err = newVerifier(context.Background(), entra.srv.URL, testAudience); err != nil {
		log.Fatal(err)
	}
	code := m.Run()
	entra.srv.Close()
	os.Exit(code)
}

// What the user does with the confirmation form.
var (
	confirmed = map[string]any{"action": "accept", "content": map[string]any{"confirm": true}}
	unchecked = map[string]any{"action": "accept", "content": map[string]any{"confirm": false}}
	declined  = map[string]any{"action": "decline"}
	cancelled = map[string]any{"action": "cancel"}
)

// client plays an MCP client over HTTP: it sends tools/call and answers the confirmation form.
type client struct {
	t     *testing.T
	url   string
	token string
	caps  map[string]any
	reply map[string]any
	key   *rsa.PrivateKey
	iss   string
	// asked collects the messages of the confirmation forms the user saw.
	asked []string
}

// setup starts the tool behind a real server that verifies tokens against a fake Entra.
func setup(t *testing.T, s TicketRepository) *client {
	t.Helper()
	issuer := entra.srv.URL
	idVerifier, ticketService = entra.verifier, NewTicketService(s)
	t.Cleanup(func() { idVerifier, ticketService = nil, nil })

	srv := mcp.NewServer(mcp.Info{Name: "esm", Version: "test"},
		mcp.WithAuth(mcp.AuthScheme{Type: mcp.AuthOAuth, Verify: VerifyToken, Resource: "https://esm.test/mcp", AuthorizationServers: []string{issuer}}))
	require.NoError(t, mcp.AddTool(srv, mcp.ToolDef{
		Name: toolCreate, Requires: []string{mcp.CapElicitation}, Scopes: [][]string{{"esm:user"}},
	}, CreateTicket))
	require.NoError(t, mcp.AddTool(srv, mcp.ToolDef{
		Name: toolList, Scopes: [][]string{{"esm:user"}}, Hint: mcp.Hint{ReadOnly: true},
	}, ListOwnTickets))
	require.NoError(t, mcp.AddTool(srv, mcp.ToolDef{
		Name: toolMine, Requires: []string{mcp.CapElicitation}, Scopes: [][]string{{"esm:user"}},
	}, UpdateMyTicket))
	require.NoError(t, mcp.AddTool(srv, mcp.ToolDef{
		Name: toolUpdate, Requires: []string{mcp.CapElicitation}, Scopes: [][]string{{roleOps}},
	}, UpdateTicket))
	require.NoError(t, mcp.AddTool(srv, mcp.ToolDef{
		Name: toolPending, Scopes: [][]string{{roleReview}}, Hint: mcp.Hint{ReadOnly: true},
	}, ListPendingReviews))
	require.NoError(t, mcp.AddTool(srv, mcp.ToolDef{
		Name: toolReview, Requires: []string{mcp.CapElicitation}, Scopes: [][]string{{roleReview}},
	}, ReviewTicket))
	ts := httptest.NewServer(srv)
	t.Cleanup(func() { srv.Close(); ts.Close() })

	c := &client{t: t, url: ts.URL, key: entra.key, iss: issuer, caps: map[string]any{mcp.CapElicitation: map[string]any{}}}
	c.as(userOid)
	return c
}

// as makes the client call with a token of this user, holding these roles.
func (c *client) as(oid string, roles ...string) {
	claims := map[string]any{
		"iss": c.iss, "aud": testAudience, "sub": "sub", "oid": oid,
		"exp": time.Now().Add(time.Hour).Unix(),
	}
	if len(roles) > 0 {
		claims["roles"] = roles
	}
	c.token = sign(c.t, c.key, claims)
}

// call sends the call of a tool and answers input requests with c.reply, like a client does.
// It returns the HTTP status and the final message.
func (c *client) call(tool string, args map[string]any) (int, rpcMessage) {
	c.t.Helper()
	params := map[string]any{"name": tool, "arguments": args}
	for range 4 {
		status, msg := c.post(tool, params)
		if status != http.StatusOK || msg.Error != nil {
			return status, msg
		}
		var head struct {
			ResultType    string                    `json:"resultType"`
			InputRequests map[string]map[string]any `json:"inputRequests"`
			RequestState  string                    `json:"requestState"`
		}
		require.NoError(c.t, json.Unmarshal(msg.Result, &head))
		if head.ResultType != "input_required" {
			return status, msg
		}
		responses := map[string]any{}
		for key, req := range head.InputRequests {
			require.Equal(c.t, elicitation, req["method"])
			require.NotNil(c.t, c.reply, "the user was asked but no answer is set")
			p, _ := req["params"].(map[string]any)
			c.asked = append(c.asked, p["message"].(string))
			responses[key] = c.reply
		}
		params = map[string]any{"name": tool, "arguments": args, "inputResponses": responses}
		if head.RequestState != "" {
			params["requestState"] = head.RequestState
		}
	}
	require.FailNow(c.t, "too many round trips")
	return 0, rpcMessage{}
}

func (c *client) post(tool string, params map[string]any) (int, rpcMessage) {
	c.t.Helper()
	params["_meta"] = map[string]any{
		"io.modelcontextprotocol/protocolVersion":    "2026-07-28",
		"io.modelcontextprotocol/clientCapabilities": c.caps,
	}
	body, err := json.Marshal(map[string]any{"jsonrpc": "2.0", "id": 1, "method": methodCall, "params": params})
	require.NoError(c.t, err)
	req, err := http.NewRequestWithContext(c.t.Context(), http.MethodPost, c.url, bytes.NewReader(body))
	require.NoError(c.t, err)
	for k, v := range map[string]string{
		"Content-Type": "application/json", "Accept": "application/json, text/event-stream",
		"Mcp-Protocol-Version": "2026-07-28", "Mcp-Method": methodCall, "Mcp-Name": tool,
	} {
		req.Header.Set(k, v)
	}
	if c.token != "" {
		req.Header.Set("Authorization", "Bearer "+c.token)
	}
	resp, err := http.DefaultClient.Do(req)
	require.NoError(c.t, err)
	defer func() { _ = resp.Body.Close() }()

	var msg rpcMessage
	if strings.HasPrefix(resp.Header.Get("Content-Type"), "text/event-stream") {
		sc := bufio.NewScanner(resp.Body)
		sc.Buffer(make([]byte, 0, 64*1024), 1<<20)
		for sc.Scan() {
			data, ok := strings.CutPrefix(sc.Text(), "data: ")
			if !ok || data == "" {
				continue
			}
			var m rpcMessage
			require.NoError(c.t, json.Unmarshal([]byte(data), &m))
			if m.Method == "" { // not a notification
				msg = m
			}
		}
	} else if b, _ := io.ReadAll(resp.Body); len(b) > 0 {
		_ = json.Unmarshal(b, &msg)
	}
	return resp.StatusCode, msg
}

func (c *client) result(msg rpcMessage) toolResult {
	c.t.Helper()
	require.Nil(c.t, msg.Error)
	var r toolResult
	require.NoError(c.t, json.Unmarshal(msg.Result, &r))
	return r
}

// isRejected reports whether the call failed, as a tool error or as invalid params.
func (c *client) isRejected(msg rpcMessage) (bool, string) {
	if msg.Error != nil {
		return msg.Error.Code == codeParams, msg.Error.Message
	}
	res := c.result(msg)
	return res.IsError, res.text()
}
