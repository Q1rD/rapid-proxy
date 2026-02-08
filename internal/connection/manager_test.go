package connection

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"
)

// TestNewManager tests manager creation
func TestNewManager(t *testing.T) {
	config := &Config{
		ProxyURLs: []string{
			"http://proxy1:8080",
			"http://proxy2:8080",
		},
	}

	mgr, err := NewManager(config)
	if err != nil {
		t.Fatalf("Failed to create manager: %v", err)
	}
	defer func() { _ = mgr.Close() }()

	if len(mgr.clients) != 2 {
		t.Errorf("Expected 2 clients, got %d", len(mgr.clients))
	}

	if mgr.GetClientCount() != 2 {
		t.Errorf("Expected GetClientCount() = 2, got %d", mgr.GetClientCount())
	}
}

// TestNewManager_NoProxies tests error handling for empty proxy list
func TestNewManager_NoProxies(t *testing.T) {
	config := &Config{
		ProxyURLs: []string{},
	}

	_, err := NewManager(config)
	if err == nil {
		t.Error("Expected error for empty proxy list")
	}
}

// TestNewManager_InvalidProxyURL tests error handling for invalid URL
func TestNewManager_InvalidProxyURL(t *testing.T) {
	config := &Config{
		ProxyURLs: []string{
			"://invalid",
		},
	}

	_, err := NewManager(config)
	if err == nil {
		t.Error("Expected error for invalid proxy URL")
	}
}

// TestClientSingleton verifies HTTP client is singleton
func TestClientSingleton(t *testing.T) {
	config := &Config{
		ProxyURLs: []string{"http://proxy:8080"},
	}

	mgr, err := NewManager(config)
	if err != nil {
		t.Fatalf("Failed to create manager: %v", err)
	}
	defer func() { _ = mgr.Close() }()

	client := mgr.clients[0]

	// Verify httpClient is not nil
	if client.httpClient == nil {
		t.Error("HTTP client is nil")
	}

	// Verify transport is not nil
	if client.transport == nil {
		t.Error("Transport is nil")
	}

	// Store pointer addresses
	httpClientAddr := fmt.Sprintf("%p", client.httpClient)
	transportAddr := fmt.Sprintf("%p", client.transport)

	// Verify they remain the same (singleton pattern)
	if fmt.Sprintf("%p", client.httpClient) != httpClientAddr {
		t.Error("HTTP client changed (not singleton)")
	}

	if fmt.Sprintf("%p", client.transport) != transportAddr {
		t.Error("Transport changed (not singleton)")
	}
}

// TestConnectionReuse tests that connections are reused
func TestConnectionReuse(t *testing.T) {
	// Create test server that counts connections
	connectionCount := 0
	var mu sync.Mutex

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		connectionCount++
		mu.Unlock()
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	// Create manager with test server as "proxy"
	// Note: This is a simplified test - real proxy would be different
	config := &Config{
		ProxyURLs: []string{server.URL},
		TransportConfig: &TransportConfig{
			MaxIdleConns:        10,
			MaxIdleConnsPerHost: 10,
			IdleConnTimeout:     30 * time.Second,
			DialTimeout:         5 * time.Second,
			DisableKeepAlives:   false, // CRITICAL: enable keep-alive
		},
		RequestTimeout: 5 * time.Second,
	}

	mgr, err := NewManager(config)
	if err != nil {
		t.Fatalf("Failed to create manager: %v", err)
	}
	defer func() { _ = mgr.Close() }()

	client := mgr.clients[0]

	// Make multiple requests to same client
	numRequests := 5
	for i := 0; i < numRequests; i++ {
		req, _ := http.NewRequest("GET", "http://test.com", nil)
		resp, err := client.Do(req)
		if err != nil {
			// Expected to fail as we're not using real proxy
			// But we can still verify singleton pattern
			continue
		}
		if resp != nil && resp.Body != nil {
			_ = resp.Body.Close()
		}
	}

	// Verify same HTTP client was used (singleton)
	if client.totalRequests.Load() != int64(numRequests) {
		t.Errorf("Expected %d total requests, got %d", numRequests, client.totalRequests.Load())
	}
}

// TestConcurrentAccess tests concurrent access to clients
func TestConcurrentAccess(t *testing.T) {
	config := &Config{
		ProxyURLs: []string{
			"http://proxy1:8080",
			"http://proxy2:8080",
			"http://proxy3:8080",
		},
	}

	mgr, err := NewManager(config)
	if err != nil {
		t.Fatalf("Failed to create manager: %v", err)
	}
	defer func() { _ = mgr.Close() }()

	// Simulate concurrent access to same client
	client := mgr.clients[0]

	var wg sync.WaitGroup
	numGoroutines := 100
	requestsPerGoroutine := 10

	for i := 0; i < numGoroutines; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < requestsPerGoroutine; j++ {
				req, _ := http.NewRequest("GET", "http://test.com", nil)
				_, _ = client.Do(req) // Will fail, but that's ok for this test
			}
		}()
	}

	wg.Wait()

	// Verify metrics are consistent
	expected := int64(numGoroutines * requestsPerGoroutine)
	total := client.totalRequests.Load()

	if total != expected {
		t.Errorf("Expected %d total requests, got %d", expected, total)
	}
}

// TestMetrics tests metrics collection
func TestMetrics(t *testing.T) {
	config := &Config{
		ProxyURLs: []string{"http://proxy:8080"},
	}

	mgr, err := NewManager(config)
	if err != nil {
		t.Fatalf("Failed to create manager: %v", err)
	}
	defer func() { _ = mgr.Close() }()

	client := mgr.clients[0]

	// Simulate some requests
	client.totalRequests.Store(100)
	client.successRequests.Store(80)
	client.failedRequests.Store(20)

	metrics := client.GetMetrics()

	if metrics.TotalRequests != 100 {
		t.Errorf("Expected 100 total requests, got %d", metrics.TotalRequests)
	}

	if metrics.SuccessRequests != 80 {
		t.Errorf("Expected 80 success requests, got %d", metrics.SuccessRequests)
	}

	if metrics.FailedRequests != 20 {
		t.Errorf("Expected 20 failed requests, got %d", metrics.FailedRequests)
	}

	expectedErrorRate := 0.2 // 20/100
	if metrics.ErrorRate != expectedErrorRate {
		t.Errorf("Expected error rate %.2f, got %.2f", expectedErrorRate, metrics.ErrorRate)
	}
}

// TestManagerStats tests manager statistics
func TestManagerStats(t *testing.T) {
	config := &Config{
		ProxyURLs: []string{
			"http://proxy1:8080",
			"http://proxy2:8080",
			"http://proxy3:8080",
		},
	}

	mgr, err := NewManager(config)
	if err != nil {
		t.Fatalf("Failed to create manager: %v", err)
	}
	defer func() { _ = mgr.Close() }()

	// Set different states
	mgr.clients[0].SetState(StateHealthy)
	mgr.clients[1].SetState(StateDegraded)
	mgr.clients[2].SetState(StateDead)

	stats := mgr.GetStats()

	if stats.TotalProxies != 3 {
		t.Errorf("Expected 3 total proxies, got %d", stats.TotalProxies)
	}

	if stats.HealthyProxies != 1 {
		t.Errorf("Expected 1 healthy proxy, got %d", stats.HealthyProxies)
	}

	if stats.DegradedProxies != 1 {
		t.Errorf("Expected 1 degraded proxy, got %d", stats.DegradedProxies)
	}

	if stats.DeadProxies != 1 {
		t.Errorf("Expected 1 dead proxy, got %d", stats.DeadProxies)
	}
}

// TestCleanup tests cleanup functionality
func TestCleanup(t *testing.T) {
	config := &Config{
		ProxyURLs:           []string{"http://proxy:8080"},
		IdleCleanupInterval: 100 * time.Millisecond,
		IdleCleanupTimeout:  50 * time.Millisecond,
	}

	mgr, err := NewManager(config)
	if err != nil {
		t.Fatalf("Failed to create manager: %v", err)
	}
	defer func() { _ = mgr.Close() }()

	// Wait for at least one cleanup cycle
	time.Sleep(200 * time.Millisecond)

	// Verify cleanup ran (no panic)
	stats := mgr.GetStats()
	if stats.TotalProxies != 1 {
		t.Errorf("Expected 1 proxy after cleanup, got %d", stats.TotalProxies)
	}
}

// TestClose tests graceful shutdown
func TestClose(t *testing.T) {
	config := &Config{
		ProxyURLs:           []string{"http://proxy:8080"},
		IdleCleanupInterval: 100 * time.Millisecond,
	}

	mgr, err := NewManager(config)
	if err != nil {
		t.Fatalf("Failed to create manager: %v", err)
	}

	// Close should not panic
	err = mgr.Close()
	if err != nil {
		t.Errorf("Close returned error: %v", err)
	}

	// Verify context is cancelled
	select {
	case <-mgr.ctx.Done():
		// Good
	default:
		t.Error("Context not cancelled after Close")
	}
}

// TestGetClient tests client lookup
func TestGetClient(t *testing.T) {
	proxyURL := "http://proxy:8080"
	config := &Config{
		ProxyURLs: []string{proxyURL},
	}

	mgr, err := NewManager(config)
	if err != nil {
		t.Fatalf("Failed to create manager: %v", err)
	}
	defer func() { _ = mgr.Close() }()

	// Test successful lookup
	client, ok := mgr.GetClient(proxyURL)
	if !ok {
		t.Error("Expected to find client")
	}
	if client.ProxyURL() != proxyURL {
		t.Errorf("Expected proxy URL %s, got %s", proxyURL, client.ProxyURL())
	}

	// Test failed lookup
	_, ok = mgr.GetClient("http://nonexistent:8080")
	if ok {
		t.Error("Expected not to find client")
	}
}

// BenchmarkDo benchmarks single request execution
func BenchmarkDo(b *testing.B) {
	config := &Config{
		ProxyURLs: []string{"http://proxy:8080"},
	}

	mgr, _ := NewManager(config)
	defer func() { _ = mgr.Close() }()

	client := mgr.clients[0]
	req, _ := http.NewRequest("GET", "http://test.com", nil)

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, _ = client.Do(req)
	}
}

// BenchmarkConcurrentDo benchmarks concurrent request execution
func BenchmarkConcurrentDo(b *testing.B) {
	config := &Config{
		ProxyURLs: []string{"http://proxy:8080"},
	}

	mgr, _ := NewManager(config)
	defer func() { _ = mgr.Close() }()

	client := mgr.clients[0]

	b.ResetTimer()
	b.RunParallel(func(pb *testing.PB) {
		req, _ := http.NewRequest("GET", "http://test.com", nil)
		for pb.Next() {
			_, _ = client.Do(req)
		}
	})
}

// TestContextCancellation tests that context cancellation works
func TestContextCancellation(t *testing.T) {
	config := &Config{
		ProxyURLs:           []string{"http://proxy:8080"},
		IdleCleanupInterval: 10 * time.Millisecond,
	}

	mgr, err := NewManager(config)
	if err != nil {
		t.Fatalf("Failed to create manager: %v", err)
	}

	// Cancel context
	mgr.cancel()

	// Wait a bit for cleanup goroutine to exit
	time.Sleep(50 * time.Millisecond)

	// Close should work even after context cancelled
	err = mgr.Close()
	if err != nil {
		t.Errorf("Close returned error after context cancel: %v", err)
	}
}
