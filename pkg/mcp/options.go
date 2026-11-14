package mcp

import "time"

// Info describes the server in the discover result.
type Info struct {
	Name        string
	Title       string
	Version     string
	Description string
	WebsiteURL  string
	Icons       []Icon
}

// CacheScope says who may reuse a cacheable response.
type CacheScope string

// Cache scopes.
const (
	// CachePublic responses may be shared between callers.
	CachePublic CacheScope = "public"
	// CachePrivate responses may only be reused for the same authorization context.
	CachePrivate CacheScope = "private"
)

type options struct {
	instructions string
	origins      []string
	keepAlive    time.Duration
	pageSize     int
	auth         []AuthScheme
	baseURL      string
	cacheTTL     time.Duration
	cacheScope   CacheScope
	stateKey     []byte
}

// Option configures a Server.
type Option func(*options)

// WithInstructions sets the instructions returned by server/discover.
func WithInstructions(text string) Option { return func(o *options) { o.instructions = text } }

// WithAllowedOrigins sets the accepted Origin header values; without it only localhost origins pass.
func WithAllowedOrigins(origins ...string) Option {
	return func(o *options) { o.origins = append(o.origins, origins...) }
}

// WithKeepAlive sets the interval of SSE keep-alive comments.
func WithKeepAlive(d time.Duration) Option { return func(o *options) { o.keepAlive = d } }

// WithPageSize sets the page size of list operations; zero returns everything at once.
func WithPageSize(n int) Option { return func(o *options) { o.pageSize = n } }

// WithBaseURL sets the public URL of the server. Relative icon sources are resolved against it;
// without it the scheme and host of the request are used.
func WithBaseURL(url string) Option { return func(o *options) { o.baseURL = url } }

// WithCache sets the caching hints (ttlMs and cacheScope) of discover, list and read results.
// The default is no caching (ttl 0) and the private scope.
func WithCache(ttl time.Duration, scope CacheScope) Option {
	return func(o *options) { o.cacheTTL, o.cacheScope = ttl, scope }
}

// WithStateKey sets the secret that protects requestState. Servers that run several instances
// behind one endpoint must share it; without it a random key is made per process.
func WithStateKey(key []byte) Option { return func(o *options) { o.stateKey = key } }
