package rapidproxy

import (
	"fmt"
	"time"
)

// Config contains pool configuration for rapid-proxy.
// Use DefaultConfig() to get sensible defaults, then override specific fields as needed.
type Config struct {
	// ProxyURLs is the list of proxy server URLs to use (REQUIRED).
	// Format: "http://host:port" or "http://username:password@host:port"
	//
	// The pool will distribute requests across all proxies using round-robin selection.
	// At least one proxy URL must be provided.
	//
	// Example:
	//   config.ProxyURLs = []string{
	//       "http://proxy1.example.com:8080",
	//       "http://user:pass@proxy2.example.com:3128",
	//   }
	ProxyURLs []string

	// NumWorkers is the number of concurrent worker goroutines.
	// Each worker can process one request at a time.
	//
	// Default: 1000 (good for 8-10k concurrent requests)
	// Recommended: 2x your target concurrency level
	// Trade-off: More workers = higher throughput but more memory and goroutine overhead
	//
	// Example scenarios:
	//   - 500 concurrent requests: NumWorkers = 1000
	//   - 5k concurrent requests: NumWorkers = 10000
	NumWorkers int

	// QueueSize is the maximum number of requests that can be queued
	// waiting for an available worker.
	//
	// Default: 10000 (10x workers)
	// Recommended: 10x NumWorkers for smooth burst handling
	// Trade-off: Larger queue = more burst tolerance but higher memory usage
	//
	// If queue is full, new requests will fail with ErrQueueFull.
	// This provides backpressure to prevent resource exhaustion.
	QueueSize int

	// MaxIdleConns is the maximum number of idle (keep-alive) connections
	// across all proxies.
	//
	// Default: 1000
	// Trade-off: More idle connections = faster reuse but more memory/FDs
	MaxIdleConns int

	// MaxIdleConnsPerHost is the maximum number of idle (keep-alive) connections
	// to keep per proxy.
	//
	// Default: 10
	// This limits connection pooling per proxy to prevent resource exhaustion.
	MaxIdleConnsPerHost int

	// IdleConnTimeout is how long an idle connection remains in the pool
	// before being closed.
	//
	// Default: 90 seconds
	// Trade-off: Longer timeout = better reuse, Shorter = faster cleanup
	IdleConnTimeout time.Duration

	// DialTimeout is the maximum time to wait for a TCP connection to be established.
	//
	// Default: 5 seconds
	// Increase if proxies are slow to connect or geographically distant.
	DialTimeout time.Duration

	// TLSHandshakeTimeout is the maximum time to wait for a TLS handshake.
	//
	// Default: 5 seconds
	// Increase if TLS handshakes are slow (common with distant proxies).
	TLSHandshakeTimeout time.Duration

	// KeepAlive specifies the interval between keep-alive probes
	// for an active network connection.
	//
	// Default: 30 seconds
	// This helps detect dead connections faster.
	KeepAlive time.Duration

	// ResponseHeaderTimeout is the maximum time to wait for server's
	// response headers after fully writing the request.
	//
	// Default: 10 seconds
	// This doesn't limit time to read the response body.
	ResponseHeaderTimeout time.Duration

	// RequestTimeout is the maximum duration for a complete request.
	// Includes queue time, connection establishment, request transmission,
	// and response reading.
	//
	// Default: 30 seconds
	// Recommended: Your SLA + buffer (e.g., 10s SLA → 15-20s timeout)
	//
	// Note: Can be overridden per-request using DoWithContext with a shorter deadline.
	RequestTimeout time.Duration

	// RateLimitPerProxy is the maximum requests per second for each proxy.
	// Uses token bucket algorithm with burst capacity = 2x rate.
	//
	// Default: 10 RPS per proxy
	// CRITICAL: Set conservatively to avoid proxy blocking/throttling!
	// Total throughput = RateLimitPerProxy * len(ProxyURLs)
	//
	// Example: 100 proxies * 10 RPS = 1000 RPS total capacity
	//
	// Warning: Setting too high will cause proxies to block you (HTTP 429).
	// Start conservative and increase gradually based on monitoring.
	RateLimitPerProxy int64

	// DomainRateLimits is optional per-domain rate limiting (map: domain -> RPS).
	// Limits total requests per second to each target domain across ALL proxies.
	//
	// Default: nil (no per-domain limits)
	// Optional: Set to enforce rate limits on specific domains
	//
	// Example:
	//   config.DomainRateLimits = map[string]int64{
	//       "api.example.com":     100,  // Max 100 RPS to this domain
	//       "slow-api.example.com": 10,  // Max 10 RPS to this domain
	//   }
	//
	// Note: Both proxy and domain limits are enforced. The effective rate is
	// limited by whichever is more restrictive.
	//
	// Domain matching:
	//   - Exact match only (no wildcards)
	//   - Port is ignored (api.example.com:443 matches api.example.com)
	//   - Use hostname from request URL
	DomainRateLimits map[string]int64

	// FailureThreshold is the error rate that triggers circuit breaker to "dead" state.
	// If error_rate >= FailureThreshold, proxy is marked as "dead" and not used.
	//
	// Default: 0.5 (50% errors)
	// Range: 0.0 to 1.0
	// Trade-off: Lower = faster failure detection, Higher = more error tolerance
	//
	// Example: 0.3 means circuit breaker trips when 30% of requests fail
	FailureThreshold float64

	// DegradedThreshold is the error rate that marks proxy as "degraded".
	// Degraded proxies are still used but with lower priority.
	//
	// Default: 0.2 (20% errors)
	// Range: 0.0 to FailureThreshold
	// Must be less than FailureThreshold.
	DegradedThreshold float64

	// MinRequestsForTrip is the minimum number of requests before circuit breaker can trip.
	// Prevents premature circuit breaker trips due to small sample sizes.
	//
	// Default: 10 requests
	// Recommended: 10-20 (lower = faster response to failures, higher = more stable)
	//
	// Example: With MinRequestsForTrip=10, need at least 10 requests before
	// circuit breaker evaluates error rate.
	MinRequestsForTrip int64

	// RecoveryTimeout is how long to wait before retrying a "dead" proxy.
	// After this timeout, circuit breaker transitions from "dead" to "healthy"
	// and the proxy gets another chance.
	//
	// Default: 60 seconds
	// Recommended: 30-120s depending on proxy reliability
	//
	// Note: Circuit breaker state is reset after recovery, giving proxy a fresh start.
	RecoveryTimeout time.Duration

	// IdleCleanupInterval is how often to run idle connection cleanup.
	//
	// Default: 2 minutes
	// This helps free resources from unused connections.
	IdleCleanupInterval time.Duration

	// IdleCleanupTimeout is the maximum time for cleanup operations.
	//
	// Default: 90 seconds
	IdleCleanupTimeout time.Duration

	// InsecureSkipVerify controls whether TLS certificate verification is skipped.
	//
	// Default: false (certificates are verified)
	// WARNING: Setting to true makes connections susceptible to man-in-the-middle attacks.
	// Only use for testing with self-signed certificates.
	InsecureSkipVerify bool
}

// DefaultConfig returns default configuration
func DefaultConfig() *Config {
	return &Config{
		// Worker pool defaults
		NumWorkers: 1000,
		QueueSize:  10000,

		// Connection pool defaults
		MaxIdleConns:        1000,
		MaxIdleConnsPerHost: 10,
		IdleConnTimeout:     90 * time.Second,

		// Timeout defaults
		DialTimeout:           5 * time.Second,
		TLSHandshakeTimeout:   5 * time.Second,
		KeepAlive:             30 * time.Second,
		ResponseHeaderTimeout: 10 * time.Second,
		RequestTimeout:        30 * time.Second,

		// Rate limiting default
		RateLimitPerProxy: 10,

		// Circuit breaker defaults
		FailureThreshold:   0.5,  // 50%
		DegradedThreshold:  0.2,  // 20%
		MinRequestsForTrip: 10,
		RecoveryTimeout:    60 * time.Second,

		// Cleanup defaults
		IdleCleanupInterval: 2 * time.Minute,
		IdleCleanupTimeout:  90 * time.Second,

		// Advanced defaults
		InsecureSkipVerify: false,
	}
}

// Validate validates the configuration
func (c *Config) Validate() error {
	if len(c.ProxyURLs) == 0 {
		return ErrNoProxies
	}

	if c.NumWorkers <= 0 {
		return fmt.Errorf("NumWorkers must be positive, got %d", c.NumWorkers)
	}

	if c.QueueSize <= 0 {
		return fmt.Errorf("QueueSize must be positive, got %d", c.QueueSize)
	}

	if c.RateLimitPerProxy <= 0 {
		return fmt.Errorf("RateLimitPerProxy must be positive, got %d", c.RateLimitPerProxy)
	}

	if c.FailureThreshold <= 0 || c.FailureThreshold > 1 {
		return fmt.Errorf("FailureThreshold must be between 0 and 1, got %f", c.FailureThreshold)
	}

	if c.DegradedThreshold <= 0 || c.DegradedThreshold > 1 {
		return fmt.Errorf("DegradedThreshold must be between 0 and 1, got %f", c.DegradedThreshold)
	}

	if c.DegradedThreshold >= c.FailureThreshold {
		return fmt.Errorf("DegradedThreshold (%f) must be less than FailureThreshold (%f)",
			c.DegradedThreshold, c.FailureThreshold)
	}

	return nil
}

// ApplyDefaults applies default values for unset fields
func (c *Config) ApplyDefaults() {
	defaults := DefaultConfig()

	if c.NumWorkers == 0 {
		c.NumWorkers = defaults.NumWorkers
	}
	if c.QueueSize == 0 {
		c.QueueSize = defaults.QueueSize
	}
	if c.MaxIdleConns == 0 {
		c.MaxIdleConns = defaults.MaxIdleConns
	}
	if c.MaxIdleConnsPerHost == 0 {
		c.MaxIdleConnsPerHost = defaults.MaxIdleConnsPerHost
	}
	if c.IdleConnTimeout == 0 {
		c.IdleConnTimeout = defaults.IdleConnTimeout
	}
	if c.DialTimeout == 0 {
		c.DialTimeout = defaults.DialTimeout
	}
	if c.TLSHandshakeTimeout == 0 {
		c.TLSHandshakeTimeout = defaults.TLSHandshakeTimeout
	}
	if c.KeepAlive == 0 {
		c.KeepAlive = defaults.KeepAlive
	}
	if c.ResponseHeaderTimeout == 0 {
		c.ResponseHeaderTimeout = defaults.ResponseHeaderTimeout
	}
	if c.RequestTimeout == 0 {
		c.RequestTimeout = defaults.RequestTimeout
	}
	if c.RateLimitPerProxy == 0 {
		c.RateLimitPerProxy = defaults.RateLimitPerProxy
	}
	if c.FailureThreshold == 0 {
		c.FailureThreshold = defaults.FailureThreshold
	}
	if c.DegradedThreshold == 0 {
		c.DegradedThreshold = defaults.DegradedThreshold
	}
	if c.MinRequestsForTrip == 0 {
		c.MinRequestsForTrip = defaults.MinRequestsForTrip
	}
	if c.RecoveryTimeout == 0 {
		c.RecoveryTimeout = defaults.RecoveryTimeout
	}
	if c.IdleCleanupInterval == 0 {
		c.IdleCleanupInterval = defaults.IdleCleanupInterval
	}
	if c.IdleCleanupTimeout == 0 {
		c.IdleCleanupTimeout = defaults.IdleCleanupTimeout
	}
}
