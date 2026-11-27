package api

import (
	"context"
	"errors"
	"fmt"
	"log"
	"os"

	"github.com/StevenCyb/GoMCP/pkg/mcp"
	"github.com/coreos/go-oidc/v3/oidc"
)

// Microsoft Entra ID. Configure through the environment or .env (see gomcp serve --dotenv):
//
//	GOMCP_AUTH_RESOURCE=https://esm.example.com/mcp                     public URL of this server
//	GOMCP_AUTH_SERVERS=https://login.microsoftonline.com/<tenant>/v2.0  issuer shown to clients
//	ESM_ENTRA_TENANT=<tenant id>
//	ESM_ENTRA_AUDIENCE=<application (client) id of the API app registration>
//
// App registration: set "accessTokenAcceptedVersion": 2 in its manifest, and create the app roles
// "esm:ops" and "esm:reviewer" (allowed member types: users). Entra puts the assigned roles into
// the token's "roles" claim. Every valid token of the tenant for this API is an esm:user.
//
// @MCP.Auth(type="oauth")
// @MCP.AuthScope("esm:user", default)
// @MCP.AuthScope("esm:ops")
// @MCP.AuthScope("esm:reviewer")
func VerifyToken(ctx context.Context, token string) (mcp.Principal, error) {
	if idVerifier == nil {
		return mcp.Principal{}, errors.New("entra is not configured")
	}
	idToken, err := idVerifier.Verify(ctx, token) // signature, issuer, audience, expiry
	if err != nil {
		return mcp.Principal{}, err
	}
	var claims struct {
		OID   string   `json:"oid"`
		Roles []string `json:"roles"`
	}
	if err := idToken.Claims(&claims); err != nil {
		return mcp.Principal{}, err
	}
	subject := claims.OID
	if subject == "" {
		subject = idToken.Subject
	}
	return mcp.Principal{Subject: subject, Scopes: append([]string{"esm:user"}, claims.Roles...)}, nil
}

var idVerifier *oidc.IDTokenVerifier

// SetupAuth loads the Entra signing keys. Without ESM_ENTRA_TENANT every token is rejected;
// stdio does not use authentication, so a local run needs no configuration.
//
// @MCP.Setup
func SetupAuth() error {
	tenant, audience := os.Getenv("ESM_ENTRA_TENANT"), os.Getenv("ESM_ENTRA_AUDIENCE")
	if tenant == "" || audience == "" {
		log.Println("auth: ESM_ENTRA_TENANT or ESM_ENTRA_AUDIENCE not set, all tokens are rejected")
		return nil
	}
	v, err := newVerifier(context.Background(), "https://login.microsoftonline.com/"+tenant+"/v2.0", audience)
	if err != nil {
		return err
	}
	idVerifier = v
	return nil
}

func newVerifier(ctx context.Context, issuer, audience string) (*oidc.IDTokenVerifier, error) {
	provider, err := oidc.NewProvider(ctx, issuer)
	if err != nil {
		return nil, fmt.Errorf("entra discovery: %w", err)
	}
	return provider.Verifier(&oidc.Config{ClientID: audience}), nil
}
