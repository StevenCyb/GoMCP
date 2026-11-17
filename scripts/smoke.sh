#!/usr/bin/env bash
# Smoke test: installs the tool, builds a fresh module outside the repository that uses the library
# and talks to the generated server over HTTP and stdio.
#
#   scripts/smoke.sh v0.1.0        # a release tag, fetched like a user would
#   GOPRIVATE=github.com/StevenCyb/GoMCP scripts/smoke.sh <commit sha>   # a pushed commit, as CI does after a merge
#   scripts/smoke.sh --local .     # a checkout: no network for the repository itself, for branches
set -euo pipefail

MODULE=github.com/StevenCyb/GoMCP
if [ "${1:-}" = "--local" ]; then
  REPO="$(cd "${2:?usage: smoke.sh --local <repository path>}" && pwd)"
  command -v cygpath >/dev/null 2>&1 && REPO="$(cygpath -m "$REPO")" # Go on Windows needs C:/... paths
  VERSION=local
else
  VERSION="${1:?usage: smoke.sh <tag or commit sha> | --local <repository path>}"
fi

# retry <command...>: the Go module cache sometimes fails on a just pushed commit (shallow file has changed)
retry() {
  for attempt in 1 2 3; do
    "$@" && return 0
    echo "retry $attempt: $*" >&2
    sleep $((attempt * 3))
  done
  "$@"
}
WORK="$(mktemp -d)"
PORT="${PORT:-18765}"
cleanup() {
  kill "${PID:-0}" 2>/dev/null || true
  wait "${PID:-0}" 2>/dev/null || true
  cd / # Windows cannot delete the working directory of a running shell
  rm -rf "$WORK" 2>/dev/null || true # a leftover temp directory must not fail the test
}
trap cleanup EXIT

echo "== install gomcp $VERSION"
if [ "$VERSION" = local ]; then
  (cd "$REPO" && GOBIN="$WORK/bin" go install ./cmd/gomcp)
else
  retry env GOBIN="$WORK/bin" go install "$MODULE/cmd/gomcp@$VERSION"
fi
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
if [ "$VERSION" = local ]; then
  go mod edit -require "$MODULE@v0.0.0" -replace "$MODULE=$REPO"
  retry go mod tidy # fetches the dependencies of the library
else
  retry go get "$MODULE@$VERSION"
fi

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
echo "== stdio (gomcp stdio)"
LIST='{"jsonrpc":"2.0","id":1,"method":"tools/list","params":{'"$META"'}}'
OUT="$(printf '%s\n' "$LIST" | "$WORK/bin/gomcp" stdio . 2>"$WORK/stdio.log")" || { echo "FAIL: gomcp stdio"; cat "$WORK/stdio.log"; exit 1; }
echo "$OUT" | grep -q '"name":"hello"' || { echo "FAIL: stdio tools/list: $OUT"; cat "$WORK/stdio.log"; exit 1; }
[ "$(echo "$OUT" | wc -l | tr -d ' ')" = "1" ] || { echo "FAIL: stdout carries more than the response: $OUT"; exit 1; }

echo "== stdio (built binary)"
BIN="$WORK/smoke-server$(go env GOEXE)" # not app: that is the module directory
"$WORK/bin/gomcp" build . "$BIN"

# stdio_check <label> <command...>: sends one request on stdin, expects exactly the tools/list response on stdout
stdio_check() {
  label="$1"; shift
  code=0
  OUT="$(printf '%s\n' "$LIST" | "$@" 2>"$WORK/stdio.err")" || code=$?
  if [ "$code" != 0 ] || ! echo "$OUT" | grep -q '"name":"hello"'; then
    echo "FAIL: $label (exit $code)"; echo "stdout: $OUT"; echo "stderr:"; cat "$WORK/stdio.err"; exit 1
  fi
}
stdio_check "built binary with --stdio" "$BIN" --stdio
stdio_check "built binary with GOMCP_TRANSPORT=stdio" env GOMCP_TRANSPORT=stdio "$BIN"

echo "OK: $VERSION works from a fresh module over HTTP and stdio"
