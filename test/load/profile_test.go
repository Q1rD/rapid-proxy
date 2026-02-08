package load

import (
	"net/http"
	"os"
	"runtime"
	"runtime/pprof"
	"sync"
	"testing"
	"time"

	"github.com/Q1rD/rapid-proxy/rapidproxy"
	"github.com/Q1rD/rapid-proxy/test/integration"
)

// TestProfile_CPU generates CPU profile for bottleneck analysis
func TestProfile_CPU(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping profiling in short mode")
	}

	// Create CPU profile file
	f, err := os.Create("cpu.prof")
	if err != nil {
		t.Fatalf("Failed to create CPU profile: %v", err)
	}
	defer func() { _ = f.Close() }()

	// Start CPU profiling
	if err := pprof.StartCPUProfile(f); err != nil {
		t.Fatalf("Failed to start CPU profile: %v", err)
	}
	defer pprof.StopCPUProfile()

	// Create mock target server
	target := integration.NewMockTarget()
	defer target.Stop()

	// Create farm
	farm := integration.NewMockProxyFarm(integration.FarmConfig{
		NumProxies:  100,
		BaseLatency: 5 * time.Millisecond,
		FailureRate: 0.0,
	})

	err = farm.Start()
	if err != nil {
		t.Fatalf("Failed to start farm: %v", err)
	}
	defer farm.Stop()

	// Create pool
	config := rapidproxy.DefaultConfig()
	config.ProxyURLs = farm.URLs()
	config.NumWorkers = 500
	config.RateLimitPerProxy = 50

	pool, err := rapidproxy.New(config)
	if err != nil {
		t.Fatalf("Failed to create pool: %v", err)
	}
	defer func() { _ = pool.Close() }()

	// Run load test for profiling
	const numRequests = 5000
	var wg sync.WaitGroup

	start := time.Now()

	for i := 0; i < numRequests; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()

			req, _ := http.NewRequest("GET", target.URL, nil)
			_, _ = pool.Do(req)
		}()
	}

	wg.Wait()
	elapsed := time.Since(start)

	t.Logf("Completed %d requests in %v", numRequests, elapsed)
	t.Logf("CPU profile written to cpu.prof")
	t.Logf("")
	t.Logf("Analyze with: go tool pprof cpu.prof")
	t.Logf("  > top10        - Show top 10 functions by CPU time")
	t.Logf("  > list <func>  - Show source code for function")
	t.Logf("  > web          - Open interactive graph in browser")
}

// TestProfile_Memory generates memory profile for leak detection
func TestProfile_Memory(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping profiling in short mode")
	}

	// Run GC before starting
	runtime.GC()
	time.Sleep(100 * time.Millisecond)

	// Create mock target server
	target := integration.NewMockTarget()
	defer target.Stop()

	// Create farm and pool
	farm := integration.NewMockProxyFarm(integration.FarmConfig{
		NumProxies:  100,
		BaseLatency: 5 * time.Millisecond,
		FailureRate: 0.0,
	})

	if err := farm.Start(); err != nil {
		t.Fatalf("Failed to start farm: %v", err)
	}
	defer farm.Stop()

	config := rapidproxy.DefaultConfig()
	config.ProxyURLs = farm.URLs()
	config.NumWorkers = 500
	config.RateLimitPerProxy = 50

	pool, _ := rapidproxy.New(config)
	defer func() { _ = pool.Close() }()

	// Get initial memory stats
	var m1 runtime.MemStats
	runtime.ReadMemStats(&m1)

	// Run load test
	const numRequests = 5000
	var wg sync.WaitGroup

	for i := 0; i < numRequests; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			req, _ := http.NewRequest("GET", target.URL, nil)
			_, _ = pool.Do(req)
		}()
	}

	wg.Wait()

	// Force GC before taking profile
	runtime.GC()
	time.Sleep(100 * time.Millisecond)

	// Get final memory stats
	var m2 runtime.MemStats
	runtime.ReadMemStats(&m2)

	// Write heap profile
	f, err := os.Create("mem.prof")
	if err != nil {
		t.Fatalf("Failed to create memory profile: %v", err)
	}
	defer func() { _ = f.Close() }()

	if err := pprof.WriteHeapProfile(f); err != nil {
		t.Fatalf("Failed to write memory profile: %v", err)
	}

	memUsedMB := float64(m2.Alloc-m1.Alloc) / 1024 / 1024
	totalAllocMB := float64(m2.TotalAlloc-m1.TotalAlloc) / 1024 / 1024

	t.Logf("Memory stats:")
	t.Logf("  Allocated: %.2f MB", memUsedMB)
	t.Logf("  Total allocated: %.2f MB", totalAllocMB)
	t.Logf("  Heap objects: %d", m2.HeapObjects)
	t.Logf("")
	t.Logf("Memory profile written to mem.prof")
	t.Logf("Analyze with: go tool pprof mem.prof")
	t.Logf("  > top10               - Show top 10 by memory")
	t.Logf("  > list <func>         - Show allocations in function")
	t.Logf("  > inuse_space         - Show allocated memory")
	t.Logf("  > alloc_objects       - Show allocation count")
}

// TestProfile_GoroutineLeaks checks for goroutine leaks
func TestProfile_GoroutineLeaks(t *testing.T) {
	// Record initial goroutine count
	runtime.GC()
	time.Sleep(100 * time.Millisecond)
	startGoroutines := runtime.NumGoroutine()

	// Create mock target server
	target := integration.NewMockTarget()
	defer target.Stop()

	// Create and use pool
	farm := integration.NewMockProxyFarm(integration.FarmConfig{
		NumProxies:  10,
		BaseLatency: 5 * time.Millisecond,
	})

	if err := farm.Start(); err != nil {
		t.Fatalf("Failed to start farm: %v", err)
	}

	config := rapidproxy.DefaultConfig()
	config.ProxyURLs = farm.URLs()
	config.NumWorkers = 20

	pool, _ := rapidproxy.New(config)

	// Do some work
	var wg sync.WaitGroup
	for i := 0; i < 100; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			req, _ := http.NewRequest("GET", target.URL, nil)
			_, _ = pool.Do(req)
		}()
	}
	wg.Wait()

	// Clean shutdown
	if err := pool.Close(); err != nil {
		t.Errorf("Error closing pool: %v", err)
	}
	farm.Stop()

	// Check goroutine count after cleanup
	runtime.GC()
	time.Sleep(200 * time.Millisecond)
	endGoroutines := runtime.NumGoroutine()

	leaked := endGoroutines - startGoroutines

	t.Logf("Goroutines: start=%d, end=%d, leaked=%d",
		startGoroutines, endGoroutines, leaked)

	// Allow small variance (test framework goroutines)
	if leaked > 5 {
		t.Errorf("Goroutine leak detected: %d leaked (threshold: 5)", leaked)
	} else if leaked > 0 {
		t.Logf("Warning: %d goroutines may have leaked (within tolerance)", leaked)
	}
}

// TestProfile_GoroutineSnapshot creates goroutine profile snapshot
func TestProfile_GoroutineSnapshot(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping profiling in short mode")
	}

	// Create mock target server
	target := integration.NewMockTarget()
	defer target.Stop()

	// Create farm and pool
	farm := integration.NewMockProxyFarm(integration.FarmConfig{
		NumProxies:  50,
		BaseLatency: 5 * time.Millisecond,
	})

	if err := farm.Start(); err != nil {
		t.Fatalf("Failed to start farm: %v", err)
	}
	defer farm.Stop()

	config := rapidproxy.DefaultConfig()
	config.ProxyURLs = farm.URLs()
	config.NumWorkers = 200

	pool, _ := rapidproxy.New(config)
	defer func() { _ = pool.Close() }()

	// Start some long-running operations
	var wg sync.WaitGroup
	const concurrentOps = 100

	for i := 0; i < concurrentOps; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < 10; j++ {
				req, _ := http.NewRequest("GET", target.URL, nil)
				_, _ = pool.Do(req)
				time.Sleep(10 * time.Millisecond)
			}
		}()
	}

	// Take snapshot while operations are running
	time.Sleep(50 * time.Millisecond)

	f, err := os.Create("goroutine.prof")
	if err != nil {
		t.Fatalf("Failed to create goroutine profile: %v", err)
	}
	defer func() { _ = f.Close() }()

	if err := pprof.Lookup("goroutine").WriteTo(f, 1); err != nil {
		t.Fatalf("Failed to write goroutine profile: %v", err)
	}

	numGoroutines := runtime.NumGoroutine()
	t.Logf("Goroutines running: %d", numGoroutines)
	t.Logf("Goroutine profile written to goroutine.prof")
	t.Logf("Analyze with: go tool pprof goroutine.prof")

	// Wait for operations to complete
	wg.Wait()
}
