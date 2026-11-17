// Example: fetch a web page as text, with limits. Only hosts in FETCH_ALLOW (comma
// separated) can be reached; an empty list denies everything. The tool needs no
// client features (no Requires), so it works over HTTP and stdio alike.

// @MCP.Server("fetch", version="1.0.0")
// @MCP.Instructions("fetch_url downloads a page with GET. Only hosts the operator allowed can be fetched.")
package api

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"

	"github.com/StevenCyb/GoMCP/pkg/mcp"
)

const (
	maxBody = 256 << 10 // bytes read from a response
	timeout = 10 * time.Second
)

// check validates the URL against the allowlist and returns it parsed.
func check(raw string, allow []string) (*url.URL, error) {
	u, err := url.Parse(raw)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Hostname() == "" {
		return nil, errors.New("only absolute http and https URLs are allowed")
	}
	if len(allow) == 0 {
		return nil, errors.New("no host is allowed: set FETCH_ALLOW to a comma separated list of hosts")
	}
	for _, h := range allow {
		if strings.EqualFold(h, u.Hostname()) {
			return u, nil
		}
	}
	return nil, fmt.Errorf("host %q is not in FETCH_ALLOW", u.Hostname())
}

func allowlist() []string {
	var out []string
	for h := range strings.SplitSeq(os.Getenv("FETCH_ALLOW"), ",") {
		if h = strings.TrimSpace(h); h != "" {
			out = append(out, h)
		}
	}
	return out
}

// get does the GET and returns at most max bytes of the body; the rest is cut and truncated is set.
func get(ctx context.Context, client *http.Client, u string, max int64) (body string, truncated bool, err error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return "", false, err
	}
	resp, err := client.Do(req)
	if err != nil {
		return "", false, err
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode >= 400 {
		return "", false, fmt.Errorf("server answered %s", resp.Status)
	}
	data, err := io.ReadAll(io.LimitReader(resp.Body, max+1))
	if err != nil {
		return "", false, err
	}
	if int64(len(data)) > max {
		return string(data[:max]), true, nil
	}
	return string(data), false, nil
}

type FetchIn struct {
	URL string `json:"url" desc:"http or https URL on an allowed host" min:"1"`
}

type Page struct {
	Body      string `json:"body"`
	Truncated bool   `json:"truncated"`
}

// Example: failures are tool errors, so the model sees the reason and can react.
//
// @MCP.Tool("fetch_url", description="Fetches a URL with GET and returns the body as text (at most 256 KiB)")
// @MCP.Hint(readOnly, openWorld)
func FetchURL(ctx mcp.Context, in FetchIn) (Page, error) {
	u, err := check(in.URL, allowlist())
	if err != nil {
		return Page{}, mcp.ToolError(err.Error())
	}
	// a redirect could leave the allowlist, so every hop is checked again
	client := &http.Client{Timeout: timeout, CheckRedirect: func(r *http.Request, _ []*http.Request) error {
		_, err := check(r.URL.String(), allowlist())
		return err
	}}
	body, truncated, err := get(ctx, client, u.String(), maxBody)
	if err != nil {
		return Page{}, mcp.ToolError("fetch failed: " + err.Error())
	}
	return Page{Body: body, Truncated: truncated}, nil
}
