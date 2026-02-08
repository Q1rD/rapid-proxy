package selector

import (
	"context"
	"net/url"
	"sync"
	"testing"
	"time"

	"github.com/Q1rD/rapid-proxy/internal/connection"
)

// Mock RateLimiter for testing
type mockRateLimiter struct {
	allowAcquire bool
}

func (m *mockRateLimiter) TryAcquire() bool {
	return m.allowAcquire
}

func (m *mockRateLimiter) Acquire(ctx context.Context) error {
	if m.allowAcquire {
		return nil
	}
	return nil
}

// Mock CircuitBreaker for testing
type mockCircuitBreaker struct {
	healthy bool
	state   int32
}

func (m *mockCircuitBreaker) RecordSuccess()          {}
func (m *mockCircuitBreaker) RecordFailure(err error) {}
func (m *mockCircuitBreaker) IsHealthy() bool         { return m.healthy }
func (m *mockCircuitBreaker) GetState() int32         { return m.state }

// createMockClient creates a mock ProxyClient for testing
func createMockClient(proxyURL string, healthy bool, allowAcquire bool) *connection.ProxyClient {
	// Parse URL
	parsedURL, _ := url.Parse(proxyURL)

	// Create transport and client
	transport := connection.NewTransport(parsedURL, &connection.TransportConfig{
		MaxIdleConns:        10,
		MaxIdleConnsPerHost: 2,
		IdleConnTimeout:     30 * time.Second,
		DialTimeout:         5 * time.Second,
		TLSHandshakeTimeout: 5 * time.Second,
		KeepAlive:           30 * time.Second,
	})

	client := connection.NewProxyClient(proxyURL, transport, 30*time.Second)

	// Set mock rate limiter
	client.SetRateLimiter(&mockRateLimiter{allowAcquire: allowAcquire})

	// Set mock circuit breaker
	client.SetCircuitBreaker(&mockCircuitBreaker{healthy: healthy, state: 0})

	return client
}

// TestNewRoundRobinSelector tests selector creation
func TestNewRoundRobinSelector(t *testing.T) {
	clients := []*connection.ProxyClient{
		createMockClient("http://proxy1:8080", true, true),
		createMockClient("http://proxy2:8080", true, true),
	}

	selector, err := NewRoundRobinSelector(clients, nil)
	if err != nil {
		t.Fatalf("Failed to create selector: %v", err)
	}

	if selector == nil {
		t.Fatal("Expected non-nil selector")
	}

	if len(selector.clients) != 2 {
		t.Errorf("Expected 2 clients, got %d", len(selector.clients))
	}

	if selector.maxRetries != 2 {
		t.Errorf("Expected maxRetries=2, got %d", selector.maxRetries)
	}
}

// TestNewRoundRobinSelector_NoProxies tests creation with no proxies
func TestNewRoundRobinSelector_NoProxies(t *testing.T) {
	_, err := NewRoundRobinSelector([]*connection.ProxyClient{}, nil)
	if err != ErrNoProxies {
		t.Errorf("Expected ErrNoProxies, got %v", err)
	}
}

// TestSelect_RoundRobin tests round-robin order
func TestSelect_RoundRobin(t *testing.T) {
	clients := []*connection.ProxyClient{
		createMockClient("http://proxy1:8080", true, true),
		createMockClient("http://proxy2:8080", true, true),
		createMockClient("http://proxy3:8080", true, true),
	}

	selector, err := NewRoundRobinSelector(clients, nil)
	if err != nil {
		t.Fatalf("Failed to create selector: %v", err)
	}

	ctx := context.Background()

	// Select 6 times and verify round-robin order
	// currentIndex starts at 0, so first Add(1) gives 1
	// Order: 1,2,0,1,2,0 (indices in clients array)
	expectedOrder := []int{1, 2, 0, 1, 2, 0}

	for i, expectedIdx := range expectedOrder {
		client, err := selector.Select(ctx)
		if err != nil {
			t.Fatalf("Selection %d failed: %v", i, err)
		}

		expectedURL := clients[expectedIdx].ProxyURL()
		if client.ProxyURL() != expectedURL {
			t.Errorf("Selection %d: expected %s, got %s", i, expectedURL, client.ProxyURL())
		}
	}
}

// TestSelect_SkipsUnhealthy tests skipping unhealthy proxies
func TestSelect_SkipsUnhealthy(t *testing.T) {
	clients := []*connection.ProxyClient{
		createMockClient("http://proxy1:8080", true, true),  // healthy
		createMockClient("http://proxy2:8080", false, true), // unhealthy
		createMockClient("http://proxy3:8080", true, true),  // healthy
	}

	selector, err := NewRoundRobinSelector(clients, nil)
	if err != nil {
		t.Fatalf("Failed to create selector: %v", err)
	}

	ctx := context.Background()

	// Should skip proxy2 (unhealthy) and select proxy1, proxy3, proxy1, proxy3...
	for i := 0; i < 4; i++ {
		client, err := selector.Select(ctx)
		if err != nil {
			t.Fatalf("Selection %d failed: %v", i, err)
		}

		url := client.ProxyURL()
		if url == "http://proxy2:8080" {
			t.Errorf("Selection %d: should have skipped unhealthy proxy2", i)
		}
	}
}

// TestSelect_RespectsRateLimit tests rate limit checking
func TestSelect_RespectsRateLimit(t *testing.T) {
	clients := []*connection.ProxyClient{
		createMockClient("http://proxy1:8080", true, true),  // rate OK
		createMockClient("http://proxy2:8080", true, false), // rate limited
		createMockClient("http://proxy3:8080", true, true),  // rate OK
	}

	selector, err := NewRoundRobinSelector(clients, nil)
	if err != nil {
		t.Fatalf("Failed to create selector: %v", err)
	}

	ctx := context.Background()

	// Should skip proxy2 (rate limited)
	for i := 0; i < 4; i++ {
		client, err := selector.Select(ctx)
		if err != nil {
			t.Fatalf("Selection %d failed: %v", i, err)
		}

		url := client.ProxyURL()
		if url == "http://proxy2:8080" {
			t.Errorf("Selection %d: should have skipped rate-limited proxy2", i)
		}
	}
}

// TestSelect_AllProxiesBusy tests all proxies busy scenario
func TestSelect_AllProxiesBusy(t *testing.T) {
	clients := []*connection.ProxyClient{
		createMockClient("http://proxy1:8080", false, true), // unhealthy
		createMockClient("http://proxy2:8080", false, true), // unhealthy
		createMockClient("http://proxy3:8080", false, true), // unhealthy
	}

	selector, err := NewRoundRobinSelector(clients, nil)
	if err != nil {
		t.Fatalf("Failed to create selector: %v", err)
	}

	ctx := context.Background()

	client, err := selector.Select(ctx)
	if err != ErrAllProxiesBusy {
		t.Errorf("Expected ErrAllProxiesBusy, got %v", err)
	}

	if client != nil {
		t.Error("Expected nil client when all proxies busy")
	}
}

// TestSelect_ContextCancellation tests context cancellation
func TestSelect_ContextCancellation(t *testing.T) {
	clients := []*connection.ProxyClient{
		createMockClient("http://proxy1:8080", true, true),
	}

	selector, err := NewRoundRobinSelector(clients, nil)
	if err != nil {
		t.Fatalf("Failed to create selector: %v", err)
	}

	// Create cancelled context
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	client, err := selector.Select(ctx)
	if err != context.Canceled {
		t.Errorf("Expected context.Canceled, got %v", err)
	}

	if client != nil {
		t.Error("Expected nil client on context cancellation")
	}
}

// TestSelect_Concurrent tests concurrent safety
func TestSelect_Concurrent(t *testing.T) {
	clients := []*connection.ProxyClient{
		createMockClient("http://proxy1:8080", true, true),
		createMockClient("http://proxy2:8080", true, true),
		createMockClient("http://proxy3:8080", true, true),
	}

	selector, err := NewRoundRobinSelector(clients, nil)
	if err != nil {
		t.Fatalf("Failed to create selector: %v", err)
	}

	ctx := context.Background()

	// Run 100 goroutines selecting concurrently
	var wg sync.WaitGroup
	numGoroutines := 100
	errors := make(chan error, numGoroutines)

	for i := 0; i < numGoroutines; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()

			for j := 0; j < 10; j++ {
				_, err := selector.Select(ctx)
				if err != nil {
					errors <- err
					return
				}
			}
		}()
	}

	wg.Wait()
	close(errors)

	// Check for errors
	for err := range errors {
		t.Errorf("Concurrent select failed: %v", err)
	}
}

// TestGetStats tests statistics tracking
func TestGetStats(t *testing.T) {
	clients := []*connection.ProxyClient{
		createMockClient("http://proxy1:8080", true, true),
		createMockClient("http://proxy2:8080", false, true), // unhealthy
	}

	selector, err := NewRoundRobinSelector(clients, nil)
	if err != nil {
		t.Fatalf("Failed to create selector: %v", err)
	}

	ctx := context.Background()

	// Perform selections
	_, _ = selector.Select(ctx) // success (proxy1)
	_, _ = selector.Select(ctx) // success (proxy1, skips proxy2)

	// All proxies unhealthy
	clients[0].SetCircuitBreaker(&mockCircuitBreaker{healthy: false})
	_, err = selector.Select(ctx) // failure (all busy)
	if err != ErrAllProxiesBusy {
		t.Errorf("Expected ErrAllProxiesBusy, got %v", err)
	}

	stats := selector.GetStats()

	if stats.TotalSelections != 3 {
		t.Errorf("Expected 3 total selections, got %d", stats.TotalSelections)
	}

	if stats.SuccessSelections != 2 {
		t.Errorf("Expected 2 success selections, got %d", stats.SuccessSelections)
	}

	if stats.FailedSelections != 1 {
		t.Errorf("Expected 1 failed selection, got %d", stats.FailedSelections)
	}

	expectedRate := 2.0 / 3.0
	if stats.SuccessRate < expectedRate-0.01 || stats.SuccessRate > expectedRate+0.01 {
		t.Errorf("Expected success rate ~%.2f, got %.2f", expectedRate, stats.SuccessRate)
	}
}

// BenchmarkSelect benchmarks selection performance
func BenchmarkSelect(b *testing.B) {
	clients := []*connection.ProxyClient{
		createMockClient("http://proxy1:8080", true, true),
		createMockClient("http://proxy2:8080", true, true),
		createMockClient("http://proxy3:8080", true, true),
	}

	selector, _ := NewRoundRobinSelector(clients, nil)
	ctx := context.Background()

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, _ = selector.Select(ctx)
	}
}

// BenchmarkSelect_Concurrent benchmarks concurrent selection
func BenchmarkSelect_Concurrent(b *testing.B) {
	clients := []*connection.ProxyClient{
		createMockClient("http://proxy1:8080", true, true),
		createMockClient("http://proxy2:8080", true, true),
		createMockClient("http://proxy3:8080", true, true),
	}

	selector, _ := NewRoundRobinSelector(clients, nil)
	ctx := context.Background()

	b.ResetTimer()
	b.RunParallel(func(pb *testing.PB) {
		for pb.Next() {
			_, _ = selector.Select(ctx)
		}
	})
}
