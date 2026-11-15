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
	limits       Limits
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

// Limits bound what a caller can make the server hold. A zero field selects its default; the
// per-caller limits count by principal subject, so without authentication all callers share one.
type Limits struct {
	// Tasks and TasksPerCaller cap the tasks the server keeps, running and finished (default 1000 / 100).
	Tasks, TasksPerCaller int
	// TaskTTL is how long a finished task is kept when its tool sets no ttl (default 1h).
	TaskTTL time.Duration
	// TaskInputTimeout is how long a task waits for the client to answer one question (default 15m).
	TaskInputTimeout time.Duration
	// Streams and StreamsPerCaller cap the open subscriptions/listen streams (default 1000 / 20).
	Streams, StreamsPerCaller int
	// ListenEntries caps the resourceSubscriptions and the taskIds of one listen request (default 100 each).
	ListenEntries int
	// Elicitations and ElicitationsPerCaller cap the open URL elicitations of the process (default 10000 / 100).
	Elicitations, ElicitationsPerCaller int
}

func (l Limits) withDefaults() Limits {
	def := func(v *int, d int) {
		if *v <= 0 {
			*v = d
		}
	}
	def(&l.Tasks, 1000)
	def(&l.TasksPerCaller, 100)
	def(&l.Streams, 1000)
	def(&l.StreamsPerCaller, 20)
	def(&l.ListenEntries, 100)
	def(&l.Elicitations, 10000)
	def(&l.ElicitationsPerCaller, 100)
	if l.TaskTTL <= 0 {
		l.TaskTTL = time.Hour
	}
	if l.TaskInputTimeout <= 0 {
		l.TaskInputTimeout = 15 * time.Minute
	}
	return l
}

// WithLimits sets the resource limits; zero fields keep their defaults. Elicitations are tracked per
// process (CompleteElicitation has no server), so the limits of the server that starts one apply.
func WithLimits(l Limits) Option { return func(o *options) { o.limits = l } }
