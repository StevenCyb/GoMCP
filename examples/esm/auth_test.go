package api

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/go-jose/go-jose/v4"
	"github.com/go-jose/go-jose/v4/jwt"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const testAudience = "api-client-id"

// startFakeEntra serves OIDC discovery and the signing keys, like login.microsoftonline.com.
func startFakeEntra(key *rsa.PrivateKey) *httptest.Server {
	mux := http.NewServeMux()
	srv := httptest.NewServer(mux)
	mux.HandleFunc("/.well-known/openid-configuration", func(w http.ResponseWriter, _ *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{
			"issuer": srv.URL, "jwks_uri": srv.URL + "/keys",
			"id_token_signing_alg_values_supported": []string{"RS256"},
		})
	})
	mux.HandleFunc("/keys", func(w http.ResponseWriter, _ *http.Request) {
		_ = json.NewEncoder(w).Encode(jose.JSONWebKeySet{Keys: []jose.JSONWebKey{
			{Key: &key.PublicKey, KeyID: "k1", Algorithm: "RS256", Use: "sig"},
		}})
	})
	return srv
}

func sign(t *testing.T, key *rsa.PrivateKey, claims map[string]any) string {
	t.Helper()
	signer, err := jose.NewSigner(jose.SigningKey{Algorithm: jose.RS256, Key: key},
		(&jose.SignerOptions{}).WithType("JWT").WithHeader("kid", "k1"))
	require.NoError(t, err)
	token, err := jwt.Signed(signer).Claims(claims).Serialize()
	require.NoError(t, err)
	return token
}

func newKey(t *testing.T) *rsa.PrivateKey {
	t.Helper()
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	require.NoError(t, err)
	return key
}

func TestVerifyToken(t *testing.T) {
	key, issuer := entra.key, entra.srv.URL
	idVerifier = entra.verifier
	t.Cleanup(func() { idVerifier = nil })

	claims := func(override map[string]any) map[string]any {
		c := map[string]any{
			"iss": issuer, "aud": testAudience, "sub": "pairwise-sub", "oid": "user-oid",
			"exp": time.Now().Add(time.Hour).Unix(), "iat": time.Now().Unix(),
			"roles": []string{"esm:ops"},
		}
		for k, val := range override {
			if val == nil {
				delete(c, k)
			} else {
				c[k] = val
			}
		}
		return c
	}

	t.Run("valid token", func(t *testing.T) {
		p, err := VerifyToken(context.Background(), sign(t, key, claims(nil)))
		require.NoError(t, err)
		assert.Equal(t, "user-oid", p.Subject)
		assert.Equal(t, []string{"esm:user", "esm:ops"}, p.Scopes)
	})
	t.Run("everyone is a user, roles are optional", func(t *testing.T) {
		p, err := VerifyToken(context.Background(), sign(t, key, claims(map[string]any{"roles": nil})))
		require.NoError(t, err)
		assert.Equal(t, []string{"esm:user"}, p.Scopes)
	})
	t.Run("several roles", func(t *testing.T) {
		p, err := VerifyToken(context.Background(), sign(t, key, claims(map[string]any{"roles": []string{"esm:ops", "esm:reviewer"}})))
		require.NoError(t, err)
		assert.Equal(t, []string{"esm:user", "esm:ops", "esm:reviewer"}, p.Scopes)
	})
	t.Run("subject falls back to sub", func(t *testing.T) {
		p, err := VerifyToken(context.Background(), sign(t, key, claims(map[string]any{"oid": nil})))
		require.NoError(t, err)
		assert.Equal(t, "pairwise-sub", p.Subject)
	})

	rejected := map[string]string{
		"wrong audience": sign(t, key, claims(map[string]any{"aud": "other-api"})),
		"wrong issuer":   sign(t, key, claims(map[string]any{"iss": "https://evil.example.com"})),
		"expired":        sign(t, key, claims(map[string]any{"exp": time.Now().Add(-time.Hour).Unix()})),
		"foreign key":    sign(t, newKey(t), claims(nil)),
		"not a jwt":      "garbage",
	}
	for name, token := range rejected {
		t.Run("rejects "+name, func(t *testing.T) {
			_, err := VerifyToken(context.Background(), token)
			assert.Error(t, err)
		})
	}
}

func TestVerifyTokenRejectsEverythingWithoutConfig(t *testing.T) {
	idVerifier = nil
	_, err := VerifyToken(context.Background(), "anything")
	assert.Error(t, err)
}
