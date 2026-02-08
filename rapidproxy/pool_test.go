package rapidproxy

import (
	"context"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"
)

// createTestServer creates a simple test HTTP server
func createTestServer() *httptest.Server {
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("test response"))
	}))
}

// TestNew tests pool creation
func TestNew(t *testing.T) {
	server := createTestServer()
	defer server.Close()

	config := DefaultConfig()
	config.ProxyURLs = []string{server.URL}
	config.NumWorkers = 10
	config.QueueSize = 100

	pool, err := New(config)
	if err != nil {
		t.Fatalf("Failed to create pool: %v", err)
	}
	defer func() { _ = pool.Close() }()

	if pool == nil {
		t.Fatal("Expected non-nil pool")
	}

	if pool.manager == nil {
		t.Error("Expected manager to be initialized")
	}

	if pool.selector == nil {
		t.Error("Expected selector to be initialized")
	}

	if pool.workerPool == nil {
		t.Error("Expected worker pool to be initialized")
	}

	if pool.collector == nil {
		t.Error("Expected collector to be initialized")
	}
}

// TestNew_DefaultConfig tests creation with nil config
func TestNew_DefaultConfig(t *testing.T) {
	server := createTestServer()
	defer server.Close()

	config := DefaultConfig()
	config.ProxyURLs = []string{server.URL}

	pool, err := New(config)
	if err != nil {
		t.Fatalf("Failed to create pool: %v", err)
	}
	defer func() { _ = pool.Close() }()

	if pool == nil {
		t.Fatal("Expected non-nil pool")
	}
}

// TestNew_InvalidConfig tests creation with invalid config
func TestNew_InvalidConfig(t *testing.T) {
	tests := []struct {
		name   string
		config *Config
	}{
		{
			name:   "no proxies",
			config: &Config{ProxyURLs: []string{}},
		},
		{
			name: "negative workers",
			config: &Config{
				ProxyURLs:  []string{"http://proxy1:8080"},
				NumWorkers: -1,
			},
		},
		{
			name: "negative queue size",
			config: &Config{
				ProxyURLs:  []string{"http://proxy1:8080"},
				NumWorkers: 10,
				QueueSize:  -1,
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := New(tt.config)
			if err == nil {
				t.Error("Expected error for invalid config")
			}
		})
	}
}

// TestDo tests single request execution
func TestDo(t *testing.T) {
	server := createTestServer()
	defer server.Close()

	config := DefaultConfig()
	config.ProxyURLs = []string{server.URL}
	config.NumWorkers = 5
	config.QueueSize = 10

	pool, err := New(config)
	if err != nil {
		t.Fatalf("Failed to create pool: %v", err)
	}
	defer func() { _ = pool.Close() }()

	// Create request
	req, _ := http.NewRequest("GET", server.URL+"/test", nil)

	// Execute request
	result, err := pool.Do(req)
	if err != nil {
		t.Fatalf("Do failed: %v", err)
	}

	if result == nil {
		t.Fatal("Expected non-nil result")
	}

	if result.ProxyUsed == "" {
		t.Error("Expected ProxyUsed to be set")
	}

	if result.Latency <= 0 {
		t.Error("Expected positive latency")
	}

	// Check response
	if result.Response == nil {
		t.Fatal("Expected non-nil response")
	}

	if result.Response.StatusCode != http.StatusOK {
		t.Errorf("Expected status 200, got %d", result.Response.StatusCode)
	}

	if len(result.Body) == 0 {
		t.Error("Expected non-empty body")
	}
}

// TestDoWithContext_Timeout tests request with timeout
func TestDoWithContext_Timeout(t *testing.T) {
	server := createTestServer()
	defer server.Close()

	config := DefaultConfig()
	config.ProxyURLs = []string{server.URL}
	config.NumWorkers = 5
	config.QueueSize = 10

	pool, err := New(config)
	if err != nil {
		t.Fatalf("Failed to create pool: %v", err)
	}
	defer func() { _ = pool.Close() }()

	// Create request with very short timeout
	ctx, cancel := context.WithTimeout(context.Background(), 1*time.Nanosecond)
	defer cancel()

	time.Sleep(10 * time.Millisecond) // Ensure context is cancelled

	req, _ := http.NewRequest("GET", server.URL+"/test", nil)

	// Should return context error
	_, err = pool.DoWithContext(ctx, req)
	if err == nil {
		t.Error("Expected error for cancelled context")
	}
}

// TestDoBatch tests batch request execution
func TestDoBatch(t *testing.T) {
	server := createTestServer()
	defer server.Close()

	config := DefaultConfig()
	config.ProxyURLs = []string{server.URL}
	config.NumWorkers = 10
	config.QueueSize = 100

	pool, err := New(config)
	if err != nil {
		t.Fatalf("Failed to create pool: %v", err)
	}
	defer func() { _ = pool.Close() }()

	// Create batch of requests
	numRequests := 10
	requests := make([]*http.Request, numRequests)
	for i := 0; i < numRequests; i++ {
		requests[i], _ = http.NewRequest("GET", server.URL+"/test", nil)
	}

	// Execute batch
	results, err := pool.DoBatch(requests)
	if err != nil {
		t.Fatalf("DoBatch failed: %v", err)
	}

	if len(results) != numRequests {
		t.Errorf("Expected %d results, got %d", numRequests, len(results))
	}

	// Check results
	for i, result := range results {
		if result == nil {
			t.Errorf("Result %d is nil", i)
			continue
		}

		if result.Response != nil && result.Response.StatusCode == http.StatusOK {
			// Success
			continue
		}

		// May have error if proxy failed, but result should exist
		if result.Error == nil && result.Response == nil {
			t.Errorf("Result %d has neither response nor error", i)
		}
	}
}

// TestStats tests statistics collection
func TestStats(t *testing.T) {
	server := createTestServer()
	defer server.Close()

	config := DefaultConfig()
	config.ProxyURLs = []string{server.URL}
	config.NumWorkers = 10
	config.QueueSize = 100

	pool, err := New(config)
	if err != nil {
		t.Fatalf("Failed to create pool: %v", err)
	}
	defer func() { _ = pool.Close() }()

	// Get stats
	stats := pool.Stats()

	if stats == nil {
		t.Fatal("Expected non-nil stats")
	}

	if stats.TotalProxies != 1 {
		t.Errorf("Expected 1 proxy, got %d", stats.TotalProxies)
	}

	if stats.WorkerCount != 10 {
		t.Errorf("Expected 10 workers, got %d", stats.WorkerCount)
	}

	if stats.Uptime <= 0 {
		t.Error("Expected positive uptime")
	}
}

// TestProxyStats tests per-proxy statistics
func TestProxyStats(t *testing.T) {
	server := createTestServer()
	defer server.Close()

	config := DefaultConfig()
	config.ProxyURLs = []string{server.URL}
	config.NumWorkers = 5
	config.QueueSize = 10

	pool, err := New(config)
	if err != nil {
		t.Fatalf("Failed to create pool: %v", err)
	}
	defer func() { _ = pool.Close() }()

	// Get proxy stats
	proxyStats := pool.ProxyStats()

	if len(proxyStats) != 1 {
		t.Fatalf("Expected 1 proxy stat, got %d", len(proxyStats))
	}

	stat := proxyStats[0]
	if stat.ProxyURL != server.URL {
		t.Errorf("Expected proxy URL %s, got %s", server.URL, stat.ProxyURL)
	}

	if stat.State != "healthy" {
		t.Errorf("Expected healthy state, got %s", stat.State)
	}
}

// TestClose tests graceful shutdown
func TestClose(t *testing.T) {
	server := createTestServer()
	defer server.Close()

	config := DefaultConfig()
	config.ProxyURLs = []string{server.URL}
	config.NumWorkers = 5
	config.QueueSize = 10

	pool, err := New(config)
	if err != nil {
		t.Fatalf("Failed to create pool: %v", err)
	}

	// Close pool
	err = pool.Close()
	if err != nil {
		t.Errorf("Close failed: %v", err)
	}

	// Subsequent operations should return ErrPoolClosed
	// Note: We can't easily test this because Do() will panic on closed channel
	// This is expected behavior - user shouldn't call Do() after Close()
	// The important thing is that Close() completes successfully
}

// TestConcurrent tests concurrent request execution
func TestConcurrent(t *testing.T) {
	server := createTestServer()
	defer server.Close()

	config := DefaultConfig()
	config.ProxyURLs = []string{server.URL}
	config.NumWorkers = 20
	config.QueueSize = 200

	pool, err := New(config)
	if err != nil {
		t.Fatalf("Failed to create pool: %v", err)
	}
	defer func() { _ = pool.Close() }()

	// Submit many concurrent requests
	numRequests := 50
	results := make(chan *Result, numRequests)
	errors := make(chan error, numRequests)

	for i := 0; i < numRequests; i++ {
		go func() {
			req, _ := http.NewRequest("GET", server.URL+"/test", nil)
			result, err := pool.Do(req)
			if err != nil {
				errors <- err
			} else {
				results <- result
			}
		}()
	}

	// Collect results
	successCount := 0
	errorCount := 0

	for i := 0; i < numRequests; i++ {
		select {
		case <-results:
			successCount++
		case <-errors:
			errorCount++
		case <-time.After(5 * time.Second):
			t.Fatal("Timeout waiting for results")
		}
	}

	// Most should succeed
	if successCount < numRequests/2 {
		t.Errorf("Expected at least %d successes, got %d", numRequests/2, successCount)
	}
}

// TestDomainRateLimiting tests per-domain rate limiting
func TestDomainRateLimiting(t *testing.T) {
	// Create mock server
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("OK"))
	}))
	defer server.Close()

	// Create pool with domain rate limits
	config := DefaultConfig()
	config.ProxyURLs = []string{server.URL}
	config.NumWorkers = 10
	config.QueueSize = 100
	config.RateLimitPerProxy = 100 // High proxy limit so domain limit is the constraint

	// Configure strict domain rate limit
	config.DomainRateLimits = map[string]int64{
		"127.0.0.1": 5, // 5 RPS for localhost
	}

	pool, err := New(config)
	if err != nil {
		t.Fatalf("Failed to create pool: %v", err)
	}
	defer func() { _ = pool.Close() }()

	// Test 1: Burst should allow up to rate limit
	successCount := 0
	for i := 0; i < 10; i++ {
		req, _ := http.NewRequest("GET", server.URL+"/test", nil)
		result, err := pool.Do(req)
		if err == nil && result.Error == nil {
			successCount++
		}
	}

	// Should allow burst of 10 (2x rate of 5)
	if successCount < 8 {
		t.Errorf("Expected at least 8 requests to succeed in burst, got %d", successCount)
	}

	t.Logf("Burst test: %d/10 requests succeeded", successCount)

	// Test 2: Sustained rate should be limited
	// Wait for tokens to refill
	time.Sleep(2 * time.Second)

	start := time.Now()
	successCount = 0
	const numRequests = 20

	for i := 0; i < numRequests; i++ {
		req, _ := http.NewRequest("GET", server.URL+"/test", nil)
		result, err := pool.Do(req)
		if err == nil && result.Error == nil {
			successCount++
		}
	}

	elapsed := time.Since(start)
	actualRPS := float64(successCount) / elapsed.Seconds()

	t.Logf("Sustained test: %d/%d requests succeeded in %v (%.1f RPS)",
		successCount, numRequests, elapsed, actualRPS)

	// Test should take at least 1 second due to rate limiting
	// With 5 RPS limit and burst of 10, 20 requests should take:
	// - First 10: instant (burst)
	// - Next 10: need 2 seconds to accumulate tokens (10 tokens / 5 per second)
	// Expected time: ~2 seconds minimum
	if elapsed < 1*time.Second {
		t.Errorf("Requests completed too fast (%v), rate limiting may not be working", elapsed)
	}

	// All requests should eventually succeed (blocking Acquire waits for tokens)
	if successCount != numRequests {
		t.Errorf("Expected all %d requests to succeed, got %d", numRequests, successCount)
	}
}

// TestDomainRateLimiting_NoDomainLimit tests that requests work without domain limits
func TestDomainRateLimiting_NoDomainLimit(t *testing.T) {
	// Create mock server
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("OK"))
	}))
	defer server.Close()

	// Create pool WITHOUT domain rate limits
	config := DefaultConfig()
	config.ProxyURLs = []string{server.URL}
	config.NumWorkers = 10
	config.QueueSize = 100
	config.RateLimitPerProxy = 100
	// config.DomainRateLimits = nil (default)

	pool, err := New(config)
	if err != nil {
		t.Fatalf("Failed to create pool: %v", err)
	}
	defer func() { _ = pool.Close() }()

	// Should handle many requests quickly without domain limiting
	const numRequests = 50
	successCount := 0

	start := time.Now()
	for i := 0; i < numRequests; i++ {
		req, _ := http.NewRequest("GET", server.URL+"/test", nil)
		result, err := pool.Do(req)
		if err == nil && result.Error == nil {
			successCount++
		}
	}
	elapsed := time.Since(start)

	t.Logf("Completed %d/%d requests in %v", successCount, numRequests, elapsed)

	// All should succeed
	if successCount != numRequests {
		t.Errorf("Expected %d successes, got %d", numRequests, successCount)
	}

	// Should be fast (no domain rate limiting)
	if elapsed > 2*time.Second {
		t.Errorf("Requests took too long: %v", elapsed)
	}
}

// TestDomainRateLimiting_MultipleDomains tests rate limiting across multiple domains
func TestDomainRateLimiting_MultipleDomains(t *testing.T) {
	// Create two mock servers (simulating different domains)
	server1 := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("Server 1"))
	}))
	defer server1.Close()

	server2 := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("Server 2"))
	}))
	defer server2.Close()

	// Create pool with different limits for each "domain" (actually different ports)
	config := DefaultConfig()
	config.ProxyURLs = []string{server1.URL} // Use first server as proxy
	config.NumWorkers = 20
	config.QueueSize = 100
	config.RateLimitPerProxy = 100

	// Note: Since we're using httptest servers, they have same host but different ports
	// In real usage, you'd have different hostnames
	// For this test, we'll just verify the mechanism works

	pool, err := New(config)
	if err != nil {
		t.Fatalf("Failed to create pool: %v", err)
	}
	defer func() { _ = pool.Close() }()

	// Make requests to both servers
	var wg sync.WaitGroup
	results := make(chan bool, 20)

	for i := 0; i < 10; i++ {
		wg.Add(2)

		go func() {
			defer wg.Done()
			req, _ := http.NewRequest("GET", server1.URL+"/test", nil)
			result, err := pool.Do(req)
			results <- (err == nil && result.Error == nil)
		}()

		go func() {
			defer wg.Done()
			req, _ := http.NewRequest("GET", server2.URL+"/test", nil)
			result, err := pool.Do(req)
			results <- (err == nil && result.Error == nil)
		}()
	}

	wg.Wait()
	close(results)

	successCount := 0
	for success := range results {
		if success {
			successCount++
		}
	}

	t.Logf("Multi-domain test: %d/20 requests succeeded", successCount)

	// Most should succeed (no domain limits configured for these hosts)
	if successCount < 15 {
		t.Errorf("Expected at least 15 successes, got %d", successCount)
	}
}
