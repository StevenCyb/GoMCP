// Example: OAuth (type="oauth") and plain API key (type="bearer") verifiers.
// Omit @MCP.Auth for no auth.

package api

import (
	"context"
	"crypto/subtle"
	"errors"
	"os"

	"github.com/StevenCyb/GoMCP/pkg/mcp"
)

// Example: OAuth token validation with scopes. This verifier is a STUB that accepts every non-empty
// token: never use it as it is, validate the signature, expiry and audience of a real token.
//
// @MCP.Auth(type="oauth", resource="https://notes.example.com/mcp")
// @MCP.AuthServer("https://auth.example.com")
// @MCP.AuthScope("notes:read", description="Read notes", default)
// @MCP.AuthScope("notes:write", description="Create and delete notes")
func VerifyToken(ctx context.Context, token string) (mcp.Principal, error) {
	if token == "" {
		return mcp.Principal{}, errors.New("empty token")
	}
	// validate JWT signature, exp and aud == resource ...
	return mcp.Principal{Subject: "user-1", Scopes: []string{"notes:read", "notes:write"}}, nil
}

// Example: plain API key validation.
//
// @MCP.Auth(type="bearer")
func VerifyAPIKey(ctx context.Context, token string) (mcp.Principal, error) {
	if subtle.ConstantTimeCompare([]byte(token), []byte(os.Getenv("NOTES_API_KEY"))) != 1 {
		return mcp.Principal{}, errors.New("invalid api key")
	}
	return mcp.Principal{Subject: "service", Scopes: []string{"notes:read", "notes:write"}}, nil
}
