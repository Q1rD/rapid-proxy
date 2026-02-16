package connection

import (
	"context"
	"net/http"
	"sync/atomic"
	"time"

	"github.com/Q1rD/rapid-proxy/internal/ratelimit"
)

// ProxyClient represents a single proxy with singleton HTTP client
type ProxyClient struct {
	// Proxy configuration
	proxyURL string

	// HTTP client (singleton - created once, reused forever)
	httpClient *http.Client
	transport  *http.Transport

	// Metrics (atomic for thread-safety)
	totalRequests   atomic.Int64
	successRequests atomic.Int64
	failedRequests  atomic.Int64

	// Health state
	state         atomic.Int32 // 0=healthy, 1=degraded, 2=dead
	lastError     atomic.Value // stores *errorWithTimestamp
	lastSuccess   atomic.Int64 // unix nano
	lastFailure   atomic.Int64 // unix nano

	// Rate limiter (will be set by manager)
	rateLimiter RateLimiter

	// Circuit breaker (will be set by manager)
	circuitBreaker CircuitBreaker

	// Concurrency semaphore (nil = unlimited)
	concurrencySem chan struct{}

	// Per-proxy domain rate limiter (nil = no limits)
	domainLimiter *ratelimit.DomainLimiter
}

// State constants
const (
	StateHealthy  int32 = 0
	StateDegraded int32 = 1
	StateDead     int32 = 2
)

// errorWithTimestamp stores error with timestamp
type errorWithTimestamp struct {
	Error     error
	Timestamp time.Time
}

// NewProxyClient creates a new proxy client with singleton HTTP client
func NewProxyClient(proxyURL string, transport *http.Transport, timeout time.Duration) *ProxyClient {
	client := &ProxyClient{
		proxyURL:  proxyURL,
		transport: transport,
		httpClient: &http.Client{
			Transport: transport,
			Timeout:   timeout,
			CheckRedirect: func(req *http.Request, via []*http.Request) error {
				return http.ErrUseLastResponse // Don't follow redirects
			},
		},
	}

	// Initialize state
	client.state.Store(StateHealthy)
	client.lastSuccess.Store(time.Now().UnixNano())

	return client
}

// Do executes HTTP request using singleton client
func (c *ProxyClient) Do(req *http.Request) (*http.Response, error) {
	// Increment total requests
	c.totalRequests.Add(1)

	// Execute request (connection reuse happens here!)
	resp, err := c.httpClient.Do(req)

	// Update metrics based on result
	if err != nil {
		c.recordFailure(err)
		return nil, err
	}

	// Consider 4xx/5xx as failures for circuit breaker
	if resp.StatusCode >= 400 {
		c.recordFailure(nil) // nil = HTTP error, not network error
	} else {
		c.recordSuccess()
	}

	return resp, nil
}

// recordSuccess updates success metrics
func (c *ProxyClient) recordSuccess() {
	c.successRequests.Add(1)
	c.lastSuccess.Store(time.Now().UnixNano())

	// Update circuit breaker
	if c.circuitBreaker != nil {
		c.circuitBreaker.RecordSuccess()
	}
}

// recordFailure updates failure metrics
func (c *ProxyClient) recordFailure(err error) {
	c.failedRequests.Add(1)
	c.lastFailure.Store(time.Now().UnixNano())

	// Store error
	if err != nil {
		c.lastError.Store(&errorWithTimestamp{
			Error:     err,
			Timestamp: time.Now(),
		})
	}

	// Update circuit breaker
	if c.circuitBreaker != nil {
		c.circuitBreaker.RecordFailure(err)
	}
}

// ProxyURL returns the proxy URL
func (c *ProxyClient) ProxyURL() string {
	return c.proxyURL
}

// IsHealthy checks if proxy is in healthy state
func (c *ProxyClient) IsHealthy() bool {
	state := c.state.Load()
	return state == StateHealthy || state == StateDegraded
}

// GetState returns current state
func (c *ProxyClient) GetState() int32 {
	return c.state.Load()
}

// GetStateName returns human-readable state name
func (c *ProxyClient) GetStateName() string {
	switch c.GetState() {
	case StateHealthy:
		return "healthy"
	case StateDegraded:
		return "degraded"
	case StateDead:
		return "dead"
	default:
		return "unknown"
	}
}

// SetState updates state (used by circuit breaker)
func (c *ProxyClient) SetState(state int32) {
	c.state.Store(state)
}

// SetRateLimiter sets the rate limiter for this proxy
func (c *ProxyClient) SetRateLimiter(limiter RateLimiter) {
	c.rateLimiter = limiter
}

// GetRateLimiter returns the rate limiter
func (c *ProxyClient) GetRateLimiter() RateLimiter {
	return c.rateLimiter
}

// SetCircuitBreaker sets the circuit breaker for this proxy
func (c *ProxyClient) SetCircuitBreaker(breaker CircuitBreaker) {
	c.circuitBreaker = breaker
}

// GetCircuitBreaker returns the circuit breaker
func (c *ProxyClient) GetCircuitBreaker() CircuitBreaker {
	return c.circuitBreaker
}

// SetConcurrencySemaphore sets the per-proxy concurrency limiter
func (c *ProxyClient) SetConcurrencySemaphore(capacity int) {
	if capacity > 0 {
		c.concurrencySem = make(chan struct{}, capacity)
	}
}

// TryAcquireConcurrency attempts to acquire a concurrency slot (non-blocking).
// Returns true if acquired or no semaphore configured.
func (c *ProxyClient) TryAcquireConcurrency() bool {
	if c.concurrencySem == nil {
		return true
	}
	select {
	case c.concurrencySem <- struct{}{}:
		return true
	default:
		return false
	}
}

// ReleaseConcurrency releases a concurrency slot. No-op if no semaphore configured.
func (c *ProxyClient) ReleaseConcurrency() {
	if c.concurrencySem == nil {
		return
	}
	<-c.concurrencySem
}

// SetDomainLimiter sets the per-proxy domain rate limiter
func (c *ProxyClient) SetDomainLimiter(limiter *ratelimit.DomainLimiter) {
	c.domainLimiter = limiter
}

// GetDomainLimiter returns the per-proxy domain rate limiter
func (c *ProxyClient) GetDomainLimiter() *ratelimit.DomainLimiter {
	return c.domainLimiter
}

// GetMetrics returns current metrics snapshot
func (c *ProxyClient) GetMetrics() ProxyMetrics {
	total := c.totalRequests.Load()
	success := c.successRequests.Load()
	failed := c.failedRequests.Load()

	var errorRate float64
	if total > 0 {
		errorRate = float64(failed) / float64(total)
	}

	var lastErr error
	if errVal := c.lastError.Load(); errVal != nil {
		if ewt, ok := errVal.(*errorWithTimestamp); ok {
			lastErr = ewt.Error
		}
	}

	return ProxyMetrics{
		ProxyURL:        c.proxyURL,
		State:           c.GetStateName(),
		TotalRequests:   total,
		SuccessRequests: success,
		FailedRequests:  failed,
		ErrorRate:       errorRate,
		LastSuccess:     time.Unix(0, c.lastSuccess.Load()),
		LastFailure:     time.Unix(0, c.lastFailure.Load()),
		LastError:       lastErr,
	}
}

// ProxyMetrics contains metrics snapshot
type ProxyMetrics struct {
	ProxyURL        string
	State           string
	TotalRequests   int64
	SuccessRequests int64
	FailedRequests  int64
	ErrorRate       float64
	LastSuccess     time.Time
	LastFailure     time.Time
	LastError       error
}

// Cleanup closes idle connections (call on shutdown)
func (c *ProxyClient) Cleanup() {
	if c.transport != nil {
		c.transport.CloseIdleConnections()
	}
}

// RateLimiter interface (will be implemented in internal/ratelimit)
type RateLimiter interface {
	TryAcquire() bool
	Acquire(ctx context.Context) error
}

// CircuitBreaker interface (will be implemented in internal/health)
type CircuitBreaker interface {
	RecordSuccess()
	RecordFailure(err error)
	IsHealthy() bool
	GetState() int32
}
