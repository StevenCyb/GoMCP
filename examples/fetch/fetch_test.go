package api

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestCheck(t *testing.T) {
	cases := map[string]struct {
		url   string
		allow []string
		want  string // part of the error, empty = allowed
	}{
		"allowed":        {"https://Example.com/x", []string{"example.com"}, ""},
		"empty denies":   {"https://example.com", nil, "FETCH_ALLOW"},
		"other host":     {"https://evil.test", []string{"example.com"}, "not in FETCH_ALLOW"},
		"subdomain":      {"https://a.example.com", []string{"example.com"}, "not in FETCH_ALLOW"},
		"scheme":         {"file:///etc/passwd", []string{"example.com"}, "http and https"},
		"relative":       {"/etc/passwd", []string{"example.com"}, "http and https"},
		"port is fine":   {"http://example.com:8080", []string{"example.com"}, ""},
		"userinfo trick": {"http://example.com@evil.test/", []string{"example.com"}, "not in FETCH_ALLOW"},
	}
	for name, c := range cases {
		_, err := check(c.url, c.allow)
		if c.want == "" && err != nil || c.want != "" && (err == nil || !strings.Contains(err.Error(), c.want)) {
			t.Errorf("%s: got %v, want %q", name, err, c.want)
		}
	}
}

func TestGetLimitsBody(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/missing" {
			http.NotFound(w, r)
			return
		}
		_, _ = w.Write([]byte("0123456789"))
	}))
	defer srv.Close()

	body, cut, err := get(t.Context(), srv.Client(), srv.URL, 4)
	if err != nil || body != "0123" || !cut {
		t.Errorf("got %q, %v, %v", body, cut, err)
	}
	body, cut, err = get(t.Context(), srv.Client(), srv.URL, 10)
	if err != nil || body != "0123456789" || cut {
		t.Errorf("exact size must not be cut: got %q, %v, %v", body, cut, err)
	}
	if _, _, err = get(t.Context(), srv.Client(), srv.URL+"/missing", 10); err == nil {
		t.Error("an error status must fail")
	}
}
