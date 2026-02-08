package worker

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Q1rD/rapid-proxy/internal/connection"
	"github.com/Q1rD/rapid-proxy/internal/selector"
)

// Mock selector for testing
type mockSelector struct {
	client        *connection.ProxyClient
	shouldFail    bool
	selectCount   atomic.Int64
	failUntilCall int // Fail until this call number
}

func (m *mockSelector) Select(ctx context.Context) (*connection.ProxyClient, error) {
	callNum := int(m.selectCount.Add(1))

	if m.shouldFail || (m.failUntilCall > 0 && callNum <= m.failUntilCall) {
		return nil, selector.ErrAllProxiesBusy
	}

	return m.client, nil
}

func (m *mockSelector) GetStats() selector.SelectorStats {
	return selector.SelectorStats{}
}

// createTestServer creates a simple HTTP test server
func createTestServer() *httptest.Server {
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("test response"))
	}))
}

// createMockSelector creates a mock selector (no server for failed selectors)
func createMockSelector(shouldFail bool) *mockSelector {
	// If shouldFail, don't bother creating client (won't be used)
	if shouldFail {
		return &mockSelector{
			client:     nil,
			shouldFail: true,
		}
	}

	server := createTestServer()

	// Parse server URL as proxy URL
	proxyURL, _ := url.Parse(server.URL)

	// Create transport with short timeout
	transport := connection.NewTransport(proxyURL, &connection.TransportConfig{
		MaxIdleConns:        10,
		MaxIdleConnsPerHost: 2,
		IdleConnTimeout:     5 * time.Second,
		DialTimeout:         1 * time.Second,
		TLSHandshakeTimeout: 1 * time.Second,
	})

	client := connection.NewProxyClient(server.URL, transport, 2*time.Second)

	// Note: server will leak in tests, but that's OK for unit tests
	return &mockSelector{
		client:     client,
		shouldFail: false,
	}
}

// TestNewPool tests pool creation
func TestNewPool(t *testing.T) {
	sel := createMockSelector(false)

	pool := NewPool(sel, nil)

	if pool == nil {
		t.Fatal("Expected non-nil pool")
	}

	if pool.numWorkers != 100 {
		t.Errorf("Expected 100 workers, got %d", pool.numWorkers)
	}

	if cap(pool.jobQueue) != 1000 {
		t.Errorf("Expected queue capacity 1000, got %d", cap(pool.jobQueue))
	}
}

// TestNewPool_CustomConfig tests pool creation with custom config
func TestNewPool_CustomConfig(t *testing.T) {
	sel := createMockSelector(false)

	config := &Config{
		NumWorkers: 50,
		QueueSize:  500,
	}

	pool := NewPool(sel, config)

	if pool.numWorkers != 50 {
		t.Errorf("Expected 50 workers, got %d", pool.numWorkers)
	}

	if cap(pool.jobQueue) != 500 {
		t.Errorf("Expected queue capacity 500, got %d", cap(pool.jobQueue))
	}
}

// TestStart tests starting workers
func TestStart(t *testing.T) {
	sel := createMockSelector(false)

	pool := NewPool(sel, &Config{
		NumWorkers: 10,
		QueueSize:  10,
	})

	// Start workers
	pool.Start()

	// Give workers time to start
	time.Sleep(50 * time.Millisecond)

	stats := pool.GetStats()
	if stats.ActiveWorkers != 10 {
		t.Errorf("Expected 10 active workers, got %d", stats.ActiveWorkers)
	}

	// Cleanup
	pool.Stop()
}

// TestSubmit tests non-blocking job submission
func TestSubmit(t *testing.T) {
	sel := createMockSelector(false)

	pool := NewPool(sel, &Config{
		NumWorkers: 2,
		QueueSize:  5,
	})
	pool.Start()
	defer pool.Stop()

	req, _ := http.NewRequest("GET", "https://example.com", nil)
	job := NewJob(req)

	err := pool.Submit(job)
	if err != nil {
		t.Fatalf("Submit failed: %v", err)
	}

	// Job should be in queue
	stats := pool.GetStats()
	if stats.QueueSize != 1 {
		t.Errorf("Expected queue size 1, got %d", stats.QueueSize)
	}
}

// TestSubmit_QueueFull tests queue full error
func TestSubmit_QueueFull(t *testing.T) {
	sel := createMockSelector(true) // Will fail immediately

	pool := NewPool(sel, &Config{
		NumWorkers: 0, // No workers - jobs will stay in queue
		QueueSize:  2, // Small queue
	})
	// Don't start pool - no workers to drain queue

	req, _ := http.NewRequest("GET", "https://example.com", nil)

	// Fill the queue completely
	err1 := pool.Submit(NewJob(req))
	err2 := pool.Submit(NewJob(req))

	if err1 != nil || err2 != nil {
		t.Fatalf("Failed to fill queue: %v, %v", err1, err2)
	}

	// Next submit should fail (queue full)
	err := pool.Submit(NewJob(req))
	if err != ErrQueueFull {
		t.Errorf("Expected ErrQueueFull, got %v", err)
	}
}

// TestSubmitBlocking tests blocking job submission
func TestSubmitBlocking(t *testing.T) {
	sel := createMockSelector(false)

	pool := NewPool(sel, &Config{
		NumWorkers: 2,
		QueueSize:  5,
	})
	pool.Start()
	defer pool.Stop()

	req, _ := http.NewRequest("GET", "https://example.com", nil)
	job := NewJob(req)

	ctx := context.Background()
	err := pool.SubmitBlocking(ctx, job)
	if err != nil {
		t.Fatalf("SubmitBlocking failed: %v", err)
	}
}

// TestSubmitBlocking_ContextCancellation tests context cancellation
func TestSubmitBlocking_ContextCancellation(t *testing.T) {
	sel := createMockSelector(true) // Fail selector to block queue

	pool := NewPool(sel, &Config{
		NumWorkers: 0, // No workers - queue will fill up
		QueueSize:  2, // Small queue
	})
	// Don't start pool - no workers

	req, _ := http.NewRequest("GET", "https://example.com", nil)

	// Fill queue completely
	_ = pool.Submit(NewJob(req))
	_ = pool.Submit(NewJob(req))

	// Create cancelled context
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	// SubmitBlocking should return immediately with context error
	job := NewJob(req)
	err := pool.SubmitBlocking(ctx, job)
	if err != context.Canceled {
		t.Errorf("Expected context.Canceled, got %v", err)
	}
}

// TestExecuteJob_Success tests job execution flow
func TestExecuteJob_Success(t *testing.T) {
	// Note: Without actual HTTP server, this will result in connection error
	// But we test that the job executes and returns a result
	sel := createMockSelector(false)

	pool := NewPool(sel, &Config{
		NumWorkers: 1,
		QueueSize:  10,
	})
	pool.Start()
	defer pool.Stop()

	req, _ := http.NewRequest("GET", "https://example.com", nil)
	job := NewJob(req)

	err := pool.Submit(job)
	if err != nil {
		t.Fatalf("Submit failed: %v", err)
	}

	// Wait for result
	select {
	case result := <-job.ResultCh:
		// We expect a connection error (no real HTTP server)
		// But ProxyURL should be set
		if result.ProxyURL == "" {
			t.Error("Expected ProxyURL to be set")
		}

		// Check that duration is recorded
		if result.Duration <= 0 {
			t.Error("Duration should be positive")
		}

		// With no real server, we expect an error
		if result.Error == nil {
			t.Error("Expected connection error with no real server")
		}

	case <-time.After(2 * time.Second):
		t.Fatal("Timeout waiting for result")
	}
}

// TestExecuteJob_SelectorFailure tests selector failure handling
func TestExecuteJob_SelectorFailure(t *testing.T) {
	sel := createMockSelector(true) // Selector will fail

	pool := NewPool(sel, &Config{
		NumWorkers: 1,
		QueueSize:  10,
	})
	pool.Start()
	defer pool.Stop()

	req, _ := http.NewRequest("GET", "https://example.com", nil)
	job := NewJob(req)

	err := pool.Submit(job)
	if err != nil {
		t.Fatalf("Submit failed: %v", err)
	}

	// Wait for result
	select {
	case result := <-job.ResultCh:
		// Should have error from selector
		if result.Error != selector.ErrAllProxiesBusy {
			t.Errorf("Expected ErrAllProxiesBusy, got %v", result.Error)
		}

		// ProxyURL should be empty (no proxy selected)
		if result.ProxyURL != "" {
			t.Errorf("Expected empty ProxyURL, got %s", result.ProxyURL)
		}

	case <-time.After(1 * time.Second):
		t.Fatal("Timeout waiting for result")
	}

	// Check stats
	stats := pool.GetStats()
	if stats.FailedJobs != 1 {
		t.Errorf("Expected 1 failed job, got %d", stats.FailedJobs)
	}
}

// TestStop_GracefulShutdown tests graceful shutdown
func TestStop_GracefulShutdown(t *testing.T) {
	sel := createMockSelector(false)

	pool := NewPool(sel, &Config{
		NumWorkers: 5,
		QueueSize:  10,
	})
	pool.Start()

	// Submit some jobs
	for i := 0; i < 5; i++ {
		req, _ := http.NewRequest("GET", "https://example.com", nil)
		_ = pool.Submit(NewJob(req))
	}

	// Stop pool (should wait for workers to finish)
	start := time.Now()
	pool.Stop()
	duration := time.Since(start)

	// Stop should complete relatively quickly
	if duration > 2*time.Second {
		t.Errorf("Stop took too long: %v", duration)
	}

	// Workers should be stopped
	stats := pool.GetStats()
	if stats.ActiveWorkers != 0 {
		t.Errorf("Expected 0 active workers after stop, got %d", stats.ActiveWorkers)
	}
}

// TestConcurrentSubmit tests concurrent job submission
func TestConcurrentSubmit(t *testing.T) {
	sel := createMockSelector(false)

	pool := NewPool(sel, &Config{
		NumWorkers: 10,
		QueueSize:  100,
	})
	pool.Start()
	defer pool.Stop()

	// Submit jobs concurrently
	var wg sync.WaitGroup
	numGoroutines := 20
	jobsPerGoroutine := 5

	for i := 0; i < numGoroutines; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()

			for j := 0; j < jobsPerGoroutine; j++ {
				req, _ := http.NewRequest("GET", "https://example.com", nil)
				job := NewJob(req)
				_ = pool.Submit(job)
			}
		}()
	}

	wg.Wait()

	// Give workers time to process
	time.Sleep(100 * time.Millisecond)

	// Check stats
	stats := pool.GetStats()
	expectedTotal := int64(numGoroutines * jobsPerGoroutine)

	if stats.TotalJobs != expectedTotal {
		t.Errorf("Expected %d total jobs, got %d", expectedTotal, stats.TotalJobs)
	}
}

// TestGetStats tests statistics tracking
func TestGetStats(t *testing.T) {
	sel := createMockSelector(false)

	pool := NewPool(sel, &Config{
		NumWorkers: 5,
		QueueSize:  10,
	})
	pool.Start()
	defer pool.Stop()

	// Initial stats
	stats := pool.GetStats()
	if stats.NumWorkers != 5 {
		t.Errorf("Expected 5 workers, got %d", stats.NumWorkers)
	}

	if stats.QueueCapacity != 10 {
		t.Errorf("Expected queue capacity 10, got %d", stats.QueueCapacity)
	}

	if stats.TotalJobs != 0 {
		t.Errorf("Expected 0 total jobs initially, got %d", stats.TotalJobs)
	}

	// Submit a job
	req, _ := http.NewRequest("GET", "https://example.com", nil)
	job := NewJob(req)
	_ = pool.Submit(job)

	// Wait for processing
	<-job.ResultCh

	// Check stats again
	stats = pool.GetStats()
	if stats.TotalJobs != 1 {
		t.Errorf("Expected 1 total job, got %d", stats.TotalJobs)
	}
}

// BenchmarkSubmit benchmarks job submission
func BenchmarkSubmit(b *testing.B) {
	sel := createMockSelector(false)

	pool := NewPool(sel, &Config{
		NumWorkers: 10,
		QueueSize:  10000,
	})
	pool.Start()
	defer pool.Stop()

	req, _ := http.NewRequest("GET", "https://example.com", nil)

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		job := NewJob(req)
		_ = pool.Submit(job)
	}
}

// BenchmarkExecuteJob benchmarks job execution
func BenchmarkExecuteJob(b *testing.B) {
	sel := createMockSelector(false)

	pool := NewPool(sel, &Config{
		NumWorkers: 10,
		QueueSize:  10000,
	})
	pool.Start()
	defer pool.Stop()

	req, _ := http.NewRequest("GET", "https://example.com", nil)

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		job := NewJob(req)
		_ = pool.Submit(job)

		// Wait for result
		<-job.ResultCh
	}
}
