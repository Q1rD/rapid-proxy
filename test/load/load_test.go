package load

import (
	"net/http"
	"runtime"
	"sort"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Q1rD/rapid-proxy/rapidproxy"
	"github.com/Q1rD/rapid-proxy/test/integration"
)

// TestLoad_1k_Concurrent tests moderate load with 1k concurrent requests
func TestLoad_1k_Concurrent(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping load test in short mode")
	}

	// Create mock target server
	target := integration.NewMockTarget()
	defer target.Stop()

	// Create medium farm
	farm := integration.NewMockProxyFarm(integration.FarmConfig{
		NumProxies:  50,
		BaseLatency: 5 * time.Millisecond,
		FailureRate: 0.01, // 1% failures
	})

	err := farm.Start()
	if err != nil {
		t.Fatalf("Failed to start farm: %v", err)
	}
	defer farm.Stop()

	// Create pool
	config := rapidproxy.DefaultConfig()
	config.ProxyURLs = farm.URLs()
	config.NumWorkers = 200
	config.QueueSize = 2000
	config.RateLimitPerProxy = 50

	pool, err := rapidproxy.New(config)
	if err != nil {
		t.Fatalf("Failed to create pool: %v", err)
	}
	defer func() { _ = pool.Close() }()

	// Prepare latency tracking
	latencies := make([]time.Duration, 0, 1000)
	var latenciesMu sync.Mutex

	var successCount, failCount atomic.Int64

	const numRequests = 1000
	var wg sync.WaitGroup

	start := time.Now()

	// Launch goroutines
	for i := 0; i < numRequests; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()

			reqStart := time.Now()

			req, _ := http.NewRequest("GET", target.URL+"/data", nil)
			result, err := pool.Do(req)

			latency := time.Since(reqStart)

			// Record latency
			latenciesMu.Lock()
			latencies = append(latencies, latency)
			latenciesMu.Unlock()

			if err != nil || result.Error != nil {
				failCount.Add(1)
				return
			}

			t.Log(string(result.Body))

			successCount.Add(1)
		}()
	}

	wg.Wait()
	elapsed := time.Since(start)

	// Calculate metrics
	success := successCount.Load()
	fail := failCount.Load()
	rps := float64(numRequests) / elapsed.Seconds()

	// Calculate latency percentiles
	sort.Slice(latencies, func(i, j int) bool {
		return latencies[i] < latencies[j]
	})

	p50 := latencies[len(latencies)/2]
	p95 := latencies[int(float64(len(latencies))*0.95)]
	p99 := latencies[int(float64(len(latencies))*0.99)]

	// Log results
	t.Logf("=== Load Test Results (1k concurrent) ===")
	t.Logf("Total requests: %d", numRequests)
	t.Logf("Success: %d (%.2f%%)", success, float64(success)/float64(numRequests)*100)
	t.Logf("Failures: %d (%.2f%%)", fail, float64(fail)/float64(numRequests)*100)
	t.Logf("Elapsed: %v", elapsed)
	t.Logf("Throughput: %.0f RPS", rps)
	t.Logf("Latency p50: %v", p50)
	t.Logf("Latency p95: %v", p95)
	t.Logf("Latency p99: %v", p99)

	// Basic assertions
	successRate := float64(success) / float64(numRequests)
	if successRate < 0.95 {
		t.Errorf("Success rate too low: %.2f%%, expected >= 95%%", successRate*100)
	}
}

// TestLoad_10k_Concurrent tests high load with 10k concurrent requests
func TestLoad_10k_Concurrent(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping load test in short mode")
	}

	// Create mock target server
	target := integration.NewMockTarget()
	defer target.Stop()

	// Create large farm
	farm := integration.NewMockProxyFarm(integration.FarmConfig{
		NumProxies:  200,
		BaseLatency: 5 * time.Millisecond,
		FailureRate: 0.01, // 1% failures
	})

	err := farm.Start()
	if err != nil {
		t.Fatalf("Failed to start farm: %v", err)
	}
	defer farm.Stop()

	// Create pool with high concurrency
	config := rapidproxy.DefaultConfig()
	config.ProxyURLs = farm.URLs()
	config.NumWorkers = 1000
	config.QueueSize = 10000
	config.RateLimitPerProxy = 50 // High rate limit

	pool, err := rapidproxy.New(config)
	if err != nil {
		t.Fatalf("Failed to create pool: %v", err)
	}
	defer func() { _ = pool.Close() }()

	// Prepare latency tracking
	latencies := make([]time.Duration, 0, 10000)
	var latenciesMu sync.Mutex

	var successCount, failCount atomic.Int64

	// Get initial memory stats
	var m1 runtime.MemStats
	runtime.ReadMemStats(&m1)

	const numRequests = 10000
	var wg sync.WaitGroup

	start := time.Now()

	// Launch goroutines
	for i := 0; i < numRequests; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()

			reqStart := time.Now()

			req, _ := http.NewRequest("GET", target.URL+"/data", nil)
			result, err := pool.Do(req)

			latency := time.Since(reqStart)

			// Record latency
			latenciesMu.Lock()
			latencies = append(latencies, latency)
			latenciesMu.Unlock()

			if err != nil || result.Error != nil {
				failCount.Add(1)
				return
			}

			successCount.Add(1)
		}()
	}

	wg.Wait()
	elapsed := time.Since(start)

	// Get final memory stats
	var m2 runtime.MemStats
	runtime.ReadMemStats(&m2)

	// Calculate metrics
	success := successCount.Load()
	fail := failCount.Load()
	rps := float64(numRequests) / elapsed.Seconds()

	// Calculate latency percentiles
	sort.Slice(latencies, func(i, j int) bool {
		return latencies[i] < latencies[j]
	})

	p50 := latencies[len(latencies)/2]
	p95 := latencies[int(float64(len(latencies))*0.95)]
	p99 := latencies[int(float64(len(latencies))*0.99)]

	// Memory usage
	memUsedMB := float64(m2.Alloc-m1.Alloc) / 1024 / 1024

	// Log results
	t.Logf("=== Load Test Results (10k concurrent) ===")
	t.Logf("Total requests: %d", numRequests)
	t.Logf("Success: %d (%.2f%%)", success, float64(success)/float64(numRequests)*100)
	t.Logf("Failures: %d (%.2f%%)", fail, float64(fail)/float64(numRequests)*100)
	t.Logf("Elapsed: %v", elapsed)
	t.Logf("Throughput: %.0f RPS", rps)
	t.Logf("Latency p50: %v", p50)
	t.Logf("Latency p95: %v", p95)
	t.Logf("Latency p99: %v", p99)
	t.Logf("Memory used: %.2f MB", memUsedMB)

	// Assert performance targets
	if rps < 8000 {
		t.Logf("WARNING: RPS below target: %.0f, expected >= 8000", rps)
		// Note: Not failing test as performance depends on system
	}

	if p99 > 500*time.Millisecond {
		t.Logf("WARNING: p99 latency high: %v, expected < 500ms", p99)
	}

	if memUsedMB > 500 {
		t.Logf("WARNING: Memory usage high: %.2f MB, expected < 500MB", memUsedMB)
	}

	// Must have good success rate
	successRate := float64(success) / float64(numRequests)
	if successRate < 0.95 {
		t.Errorf("Success rate too low: %.2f%%, expected >= 95%%", successRate*100)
	}
}

// BenchmarkLoad_Throughput benchmarks throughput with native Go benchmarking
func BenchmarkLoad_Throughput(b *testing.B) {
	// Create mock target server
	target := integration.NewMockTarget()
	defer target.Stop()

	// Create farm
	farm := integration.NewMockProxyFarm(integration.FarmConfig{
		NumProxies:  100,
		BaseLatency: 5 * time.Millisecond,
		FailureRate: 0.0,
	})

	if err := farm.Start(); err != nil {
		b.Fatalf("Failed to start farm: %v", err)
	}
	defer farm.Stop()

	// Create pool
	config := rapidproxy.DefaultConfig()
	config.ProxyURLs = farm.URLs()
	config.NumWorkers = 500
	config.RateLimitPerProxy = 100

	pool, _ := rapidproxy.New(config)
	defer func() { _ = pool.Close() }()

	b.ResetTimer()
	b.RunParallel(func(pb *testing.PB) {
		for pb.Next() {
			req, _ := http.NewRequest("GET", target.URL, nil)
			_, _ = pool.Do(req)
		}
	})

	// Log RPS
	rps := float64(b.N) / b.Elapsed().Seconds()
	b.Logf("Throughput: %.0f RPS", rps)
}
