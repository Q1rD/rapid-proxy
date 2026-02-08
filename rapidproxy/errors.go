package rapidproxy

import "errors"

// Pool-level errors returned by Do/DoBatch methods

// ErrNoProxies indicates that the configuration contains no proxy URLs.
// This error is returned at pool creation time (New) if config.ProxyURLs is empty.
//
// Fix: Add at least one proxy URL to config.ProxyURLs.
var ErrNoProxies = errors.New("no proxy URLs provided")

// ErrAllProxiesBusy indicates that all proxies are either rate-limited or unhealthy.
// This can happen when:
//   - All proxies have hit their rate limit (RateLimitPerProxy)
//   - All proxies have circuit breakers in "dead" state due to high error rates
//   - All proxies are marked as degraded
//
// Fix: Wait and retry, increase RateLimitPerProxy, add more proxies, or check proxy health with ProxyStats().
var ErrAllProxiesBusy = errors.New("all proxies are busy or unhealthy")

// ErrQueueFull indicates that the worker pool queue is full.
// This happens when requests are submitted faster than workers can process them.
// The queue size is limited by config.QueueSize.
//
// Fix: Increase QueueSize, increase NumWorkers, or implement backpressure in your application.
var ErrQueueFull = errors.New("worker pool queue is full")

// ErrPoolClosed indicates that the pool has been closed via Close().
// After Close() is called, no new requests can be submitted.
// Any in-flight requests are cancelled with context.Canceled.
//
// This error is returned for all new Do/DoBatch calls after shutdown.
var ErrPoolClosed = errors.New("pool is closed")

// ErrTimeout indicates that a request exceeded its timeout.
// The timeout is controlled by config.RequestTimeout or the context deadline
// (whichever is shorter when using DoWithContext).
//
// Note: In practice, you'll usually see context.DeadlineExceeded in result.Error
// rather than this error directly.
var ErrTimeout = errors.New("request timeout")

// ErrContextCancelled indicates that the request was cancelled via context cancellation.
// This happens when:
//   - Context passed to DoWithContext is cancelled
//   - Context deadline exceeded
//   - Pool is closing and in-flight requests are cancelled
//
// Note: In practice, you'll usually see context.Canceled in result.Error
// rather than this error directly.
var ErrContextCancelled = errors.New("context cancelled")
