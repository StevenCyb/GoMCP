package mcp

import (
	"bytes"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"strings"
	"time"
)

const stateTTL = 10 * time.Minute

var errBadState = errors.New("invalid requestState")

// statePayload is what requestState protects. It is bound to the caller, the request and a deadline.
type statePayload struct {
	Subject string            `json:"s"`
	Expires int64             `json:"e"`
	Request string            `json:"r"`
	Data    map[string]string `json:"d,omitempty"`
}

// newStateKey returns the configured key or a random one.
func newStateKey(configured []byte) []byte {
	if len(configured) > 0 {
		return configured
	}
	key := make([]byte, 32)
	if _, err := rand.Read(key); err != nil {
		panic("mcp: no randomness: " + err.Error())
	}
	return key
}

func (s *Server) mac(payload []byte) []byte {
	m := hmac.New(sha256.New, s.stateKey)
	m.Write(payload)
	return m.Sum(nil)
}

// sealState makes the opaque requestState: the payload and its HMAC.
func (s *Server) sealState(subject, digest string, data map[string]string) string {
	payload, err := json.Marshal(statePayload{Subject: subject, Expires: time.Now().Add(stateTTL).Unix(), Request: digest, Data: data})
	if err != nil {
		panic("mcp: encode state: " + err.Error()) // a map of strings cannot fail
	}
	enc := base64.RawURLEncoding
	return enc.EncodeToString(payload) + "." + enc.EncodeToString(s.mac(payload))
}

// openState verifies a requestState and returns its data.
func (s *Server) openState(token, subject, digest string) (map[string]string, error) {
	enc := base64.RawURLEncoding
	body, sig, ok := strings.Cut(token, ".")
	if !ok {
		return nil, errBadState
	}
	payload, err := enc.DecodeString(body)
	if err != nil {
		return nil, errBadState
	}
	want, err := enc.DecodeString(sig)
	if err != nil || !hmac.Equal(want, s.mac(payload)) {
		return nil, errBadState
	}
	var p statePayload
	if err := json.Unmarshal(payload, &p); err != nil {
		return nil, errBadState
	}
	if p.Subject != subject || p.Request != digest || time.Now().Unix() > p.Expires {
		return nil, errBadState
	}
	return p.Data, nil
}

// requestDigest identifies a request independent of the multi round-trip fields and _meta,
// so a requestState only works for the request it was issued for.
func requestDigest(method string, params json.RawMessage) string {
	var p map[string]json.RawMessage
	_ = json.Unmarshal(params, &p)
	delete(p, "_meta")
	delete(p, "inputResponses")
	delete(p, "requestState")
	b, _ := json.Marshal(p) //nolint:errchkjson // a map of raw JSON that was just decoded
	sum := sha256.Sum256(append(append([]byte(method), 0), bytes.TrimSpace(b)...))
	return hex.EncodeToString(sum[:])
}
