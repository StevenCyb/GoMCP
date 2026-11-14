#!/usr/bin/env bash
# Smoke test of a published version: installs the tool, builds a fresh module that depends on the
# published library (no replace directive) and talks to the generated server.
#
#   scripts/smoke.sh v0.1.0
set -euo pipefail

VERSION="${1:?usage: smoke.sh <version, e.g. v0.1.0>}"
MODULE=github.com/StevenCyb/GoMCP
WORK="$(mktemp -d)"
PORT="${PORT:-18765}"
trap 'kill "${PID:-0}" 2>/dev/null || true; rm -rf "$WORK"' EXIT

echo "== install gomcp $VERSION"
GOBIN="$WORK/bin" go install "$MODULE/cmd/gomcp@$VERSION"
echo "version: $("$WORK/bin/gomcp" version)"

echo "== fresh module"
mkdir "$WORK/app" && cd "$WORK/app"
go mod init example.com/smoke >/dev/null
cat > api.go <<'GO'
// @MCP.Server("smoke", version="0.0.1")
package api

import "github.com/StevenCyb/GoMCP/pkg/mcp"

type HelloIn struct {
	Name string `json:"name" desc:"Who to greet"`
}

// @MCP.Tool("hello", description="Greets someone")
func Hello(ctx mcp.Context, in HelloIn) (string, error) { return "Hello " + in.Name, nil }
GO
go get "$MODULE@$VERSION"

echo "== serve"
GOMCP_ADDR="127.0.0.1:$PORT" "$WORK/bin/gomcp" serve . >"$WORK/serve.log" 2>&1 &
PID=$!
for _ in $(seq 1 90); do
  curl -s -o /dev/null "http://127.0.0.1:$PORT/mcp" && break
  sleep 1
done

META='"_meta":{"io.modelcontextprotocol/protocolVersion":"2026-07-28","io.modelcontextprotocol/clientCapabilities":{}}'
call() { # method, params, name header
  curl -s -X POST "http://127.0.0.1:$PORT/mcp" -H 'Content-Type: application/json' -H 'Accept: application/json' \
    -H 'Mcp-Protocol-Version: 2026-07-28' -H "Mcp-Method: $1" -H "Mcp-Name: ${3:-}" \
    -d "{\"jsonrpc\":\"2.0\",\"id\":1,\"method\":\"$1\",\"params\":{$2$META}}"
}

echo "== check"
call tools/list "" | grep -q '"name":"hello"' || { echo "FAIL: tools/list"; cat "$WORK/serve.log"; exit 1; }
call tools/call '"name":"hello","arguments":{"name":"world"},' hello | grep -q 'Hello world' || { echo "FAIL: tools/call"; exit 1; }
echo "OK: $VERSION works from a fresh module"
