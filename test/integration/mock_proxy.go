package integration

import (
	"io"
	"math/rand"
	"net/http"
	"net/http/httptest"
	"sync"
	"sync/atomic"
	"time"
)

// ProxyConfig configures mock proxy behavior
type ProxyConfig struct {
	Name            string        // e.g., "proxy-1"
	Latency         time.Duration // Simulated latency (e.g., 50ms)
	FailureRate     float64       // 0.0 to 1.0 (e.g., 0.1 = 10% failures)
	SlowRequestRate float64       // 0.0 to 1.0 (slow requests percentage)
	SlowLatency     time.Duration // Latency for slow requests
	TimeoutRate     float64       // 0.0 to 1.0 (timeout percentage)
}

// DefaultProxyConfig returns default configuration for a mock proxy
func DefaultProxyConfig() ProxyConfig {
	return ProxyConfig{
		Name:            "default-proxy",
		Latency:         10 * time.Millisecond,
		FailureRate:     0.0,
		SlowRequestRate: 0.0,
		SlowLatency:     0,
		TimeoutRate:     0.0,
	}
}

// ProxyStats contains statistics for a mock proxy
type ProxyStats struct {
	RequestCount  int64
	SuccessCount  int64
	FailureCount  int64
	TimeoutCount  int64
	AverageLatency time.Duration
}

// MockProxy represents a single mock HTTP proxy server
type MockProxy struct {
	Config ProxyConfig
	Server *httptest.Server
	URL    string

	// Statistics (atomic for thread-safety)
	requestCount atomic.Int64
	successCount atomic.Int64
	failureCount atomic.Int64
	timeoutCount atomic.Int64

	mu     sync.Mutex
	closed bool
	rnd    *rand.Rand
}

// ProxyHandler handles HTTP requests with configured behavior
type ProxyHandler struct {
	proxy *MockProxy
}

// NewMockProxy creates a new mock proxy with the given configuration
func NewMockProxy(config ProxyConfig) *MockProxy {
	return &MockProxy{
		Config: config,
		rnd:    rand.New(rand.NewSource(time.Now().UnixNano())),
	}
}

// Start starts the mock proxy server
func (mp *MockProxy) Start() {
	mp.mu.Lock()
	defer mp.mu.Unlock()

	if mp.Server != nil {
		return // Already started
	}

	handler := &ProxyHandler{proxy: mp}
	mp.Server = httptest.NewServer(handler)
	mp.URL = mp.Server.URL
}

// Stop stops the mock proxy server
func (mp *MockProxy) Stop() {
	mp.mu.Lock()
	defer mp.mu.Unlock()

	if mp.closed {
		return
	}

	if mp.Server != nil {
		mp.Server.Close()
		mp.closed = true
	}
}

// Stats returns current statistics for the proxy
func (mp *MockProxy) Stats() ProxyStats {
	return ProxyStats{
		RequestCount: mp.requestCount.Load(),
		SuccessCount: mp.successCount.Load(),
		FailureCount: mp.failureCount.Load(),
		TimeoutCount: mp.timeoutCount.Load(),
	}
}

// Reset resets all statistics counters
func (mp *MockProxy) Reset() {
	mp.requestCount.Store(0)
	mp.successCount.Store(0)
	mp.failureCount.Store(0)
	mp.timeoutCount.Store(0)
}

// ServeHTTP handles incoming HTTP requests with configured behavior
// Acts as an HTTP proxy - forwards requests to target URL and returns real responses
func (h *ProxyHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	h.proxy.requestCount.Add(1)

	// Simulate base latency
	if h.proxy.Config.Latency > 0 {
		time.Sleep(h.proxy.Config.Latency)
	}

	// Simulate timeout (don't write response, just return)
	if h.randFloat() < h.proxy.Config.TimeoutRate {
		h.proxy.timeoutCount.Add(1)
		// Don't write response - simulate connection timeout
		return
	}

	// Simulate slow request
	if h.randFloat() < h.proxy.Config.SlowRequestRate {
		time.Sleep(h.proxy.Config.SlowLatency)
	}

	// Simulate failure
	if h.randFloat() < h.proxy.Config.FailureRate {
		h.proxy.failureCount.Add(1)
		w.WriteHeader(http.StatusBadGateway)
		_, _ = w.Write([]byte(`{"error": "mock proxy error", "proxy": "` + h.proxy.Config.Name + `"}`))
		return
	}

	// Forward request to target URL (act as HTTP proxy)
	// Create new request to target
	targetURL := r.URL.String()
	if !r.URL.IsAbs() {
		// If URL is not absolute, construct it from Host header
		scheme := "http"
		if r.TLS != nil {
			scheme = "https"
		}
		targetURL = scheme + "://" + r.Host + r.URL.String()
	}

	// Create HTTP client for forwarding (without proxy to avoid recursion)
	client := &http.Client{
		Timeout: 30 * time.Second,
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			return http.ErrUseLastResponse // Don't follow redirects
		},
	}

	// Create forwarded request
	proxyReq, err := http.NewRequest(r.Method, targetURL, r.Body)
	if err != nil {
		h.proxy.failureCount.Add(1)
		w.WriteHeader(http.StatusBadGateway)
		_, _ = w.Write([]byte(`{"error": "failed to create proxy request"}`))
		return
	}

	// Copy headers from original request
	for key, values := range r.Header {
		for _, value := range values {
			proxyReq.Header.Add(key, value)
		}
	}

	// Add proxy identification header
	proxyReq.Header.Set("X-Proxy-Name", h.proxy.Config.Name)

	// Execute forwarded request
	resp, err := client.Do(proxyReq)
	if err != nil {
		h.proxy.failureCount.Add(1)
		w.WriteHeader(http.StatusBadGateway)
		_, _ = w.Write([]byte(`{"error": "failed to forward request", "details": "` + err.Error() + `"}`))
		return
	}
	defer func() { _ = resp.Body.Close() }()

	// Copy response headers
	for key, values := range resp.Header {
		for _, value := range values {
			w.Header().Add(key, value)
		}
	}

	// Add proxy identification header to response
	w.Header().Set("X-Proxied-By", h.proxy.Config.Name)

	// Write status code
	w.WriteHeader(resp.StatusCode)

	// Copy response body
	_, err = io.Copy(w, resp.Body)
	if err != nil {
		// Body copy failed, but we already wrote headers, can't change status
		h.proxy.failureCount.Add(1)
		return
	}

	// Success
	h.proxy.successCount.Add(1)
}

// randFloat returns a random float64 in [0.0, 1.0)
func (h *ProxyHandler) randFloat() float64 {
	h.proxy.mu.Lock()
	defer h.proxy.mu.Unlock()
	return h.proxy.rnd.Float64()
}
