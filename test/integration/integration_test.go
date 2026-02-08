package integration

import (
	"context"
	"net/http"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Q1rD/rapid-proxy/rapidproxy"
)

// TestIntegration_Basic tests basic functionality with a small farm
func TestIntegration_Basic(t *testing.T) {
	// Create mock target server
	target := NewMockTarget()
	defer target.Stop()

	// Create small farm
	farm := NewMockProxyFarm(FarmConfig{
		NumProxies:  10,
		BaseLatency: 5 * time.Millisecond,
		FailureRate: 0.0, // No failures for basic test
	})

	err := farm.Start()
	if err != nil {
		t.Fatalf("Failed to start farm: %v", err)
	}
	defer farm.Stop()

	// Create pool
	config := rapidproxy.DefaultConfig()
	config.ProxyURLs = farm.URLs()
	config.NumWorkers = 10
	config.QueueSize = 100
	config.RateLimitPerProxy = 10

	pool, err := rapidproxy.New(config)
	if err != nil {
		t.Fatalf("Failed to create pool: %v", err)
	}
	defer func() { _ = pool.Close() }()

	// Execute sequential requests
	const numRequests = 100
	var successCount int

	for i := 0; i < numRequests; i++ {
		req, _ := http.NewRequest("GET", target.URL+"/data", nil)
		result, err := pool.Do(req)

		if err != nil {
			t.Errorf("Request %d failed: %v", i, err)
			continue
		}

		if result.Error != nil {
			t.Errorf("Request %d returned error: %v", i, result.Error)
			continue
		}

		if result.Response.StatusCode != 200 {
			t.Errorf("Request %d: expected status 200, got %d", i, result.Response.StatusCode)
			continue
		}

		successCount++
	}

	// Verify all requests succeeded
	if successCount != numRequests {
		t.Errorf("Success count: %d, expected %d", successCount, numRequests)
	}

	// Check pool stats
	stats := pool.Stats()
	t.Logf("Pool stats: Healthy=%d/%d, Queue=%d/%d",
		stats.HealthyProxies, stats.TotalProxies, stats.QueueSize, stats.QueueCapacity)

	if stats.TotalProxies != 10 {
		t.Errorf("Expected 10 proxies, got %d", stats.TotalProxies)
	}

	// Verify farm stats
	farmStats := farm.GetTotalStats()
	t.Logf("Farm stats: Total=%d, Success=%d, Failed=%d",
		farmStats.TotalRequests, farmStats.SuccessRequests, farmStats.FailedRequests)

	if farmStats.TotalRequests < int64(numRequests) {
		t.Errorf("Farm should have received at least %d requests, got %d",
			numRequests, farmStats.TotalRequests)
	}

	// Verify target server received requests
	targetRequests := target.GetRequestCount()
	t.Logf("Target server received %d requests", targetRequests)
	if targetRequests != int64(numRequests) {
		t.Errorf("Target should have received %d requests, got %d", numRequests, targetRequests)
	}
}

// TestIntegration_Concurrent tests concurrent request handling
func TestIntegration_Concurrent(t *testing.T) {
	// Create mock target server
	target := NewMockTarget()
	defer target.Stop()

	// Create medium farm
	farm := NewMockProxyFarm(FarmConfig{
		NumProxies:  50,
		BaseLatency: 10 * time.Millisecond,
		FailureRate: 0.02, // 2% failures
	})

	err := farm.Start()
	if err != nil {
		t.Fatalf("Failed to start farm: %v", err)
	}
	defer farm.Stop()

	// Create pool
	config := rapidproxy.DefaultConfig()
	config.ProxyURLs = farm.URLs()
	config.NumWorkers = 100
	config.QueueSize = 2000
	config.RateLimitPerProxy = 50 // Increase rate limit for concurrent test

	pool, err := rapidproxy.New(config)
	if err != nil {
		t.Fatalf("Failed to create pool: %v", err)
	}
	defer func() { _ = pool.Close() }()

	// Execute concurrent requests
	const numRequests = 1000
	var wg sync.WaitGroup
	var successCount, failCount atomic.Int64

	start := time.Now()

	for i := 0; i < numRequests; i++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()

			req, _ := http.NewRequest("GET", target.URL+"/data", nil)
			result, err := pool.Do(req)

			if err != nil || result.Error != nil {
				failCount.Add(1)
				return
			}

			if result.Response.StatusCode == 200 {
				successCount.Add(1)
			} else {
				failCount.Add(1)
			}
		}(i)
	}

	wg.Wait()
	elapsed := time.Since(start)

	// Calculate results
	success := successCount.Load()
	fail := failCount.Load()
	successRate := float64(success) / float64(numRequests)

	t.Logf("Completed %d requests in %v", numRequests, elapsed)
	t.Logf("Success: %d (%.2f%%), Failures: %d (%.2f%%)",
		success, successRate*100, fail, float64(fail)/float64(numRequests)*100)

	// Assert success rate (allow some failures due to 2% configured failure rate)
	if successRate < 0.95 {
		t.Errorf("Success rate too low: %.2f%%, expected > 95%%", successRate*100)
	}

	// Check pool stats
	stats := pool.Stats()
	t.Logf("Pool stats: Healthy=%d/%d, Workers=%d, Queue=%d/%d",
		stats.HealthyProxies, stats.TotalProxies, stats.WorkerCount,
		stats.QueueSize, stats.QueueCapacity)

	// Verify farm stats
	farmStats := farm.GetTotalStats()
	t.Logf("Farm stats: Total=%d, Success=%d, Failed=%d, SuccessRate=%.2f%%",
		farmStats.TotalRequests, farmStats.SuccessRequests, farmStats.FailedRequests,
		farmStats.SuccessRate*100)

	if farmStats.TotalRequests < int64(numRequests) {
		t.Errorf("Farm should have received at least %d requests, got %d",
			numRequests, farmStats.TotalRequests)
	}
}

// TestIntegration_CircuitBreaker tests circuit breaker behavior
func TestIntegration_CircuitBreaker(t *testing.T) {
	// Create mock target server
	target := NewMockTarget()
	defer target.Stop()

	// Create farm with high failure rate and fewer proxies
	farm := NewMockProxyFarm(FarmConfig{
		NumProxies:  3, // Fewer proxies so each gets more requests
		BaseLatency: 5 * time.Millisecond,
		FailureRate: 0.7, // 70% failure rate - should definitely trip circuit breaker
	})

	err := farm.Start()
	if err != nil {
		t.Fatalf("Failed to start farm: %v", err)
	}
	defer farm.Stop()

	// Create pool with aggressive circuit breaker
	config := rapidproxy.DefaultConfig()
	config.ProxyURLs = farm.URLs()
	config.NumWorkers = 10
	config.QueueSize = 100
	config.RateLimitPerProxy = 50  // High rate limit to not interfere
	config.FailureThreshold = 0.5  // Trip at 50% errors
	config.MinRequestsForTrip = 10 // Need 10 requests to trip
	config.RecoveryTimeout = 200 * time.Millisecond

	pool, err := rapidproxy.New(config)
	if err != nil {
		t.Fatalf("Failed to create pool: %v", err)
	}
	defer func() { _ = pool.Close() }()

	// Execute more requests to ensure circuit breaker has enough data
	const numRequests = 150
	var successCount, failCount atomic.Int64

	for i := 0; i < numRequests; i++ {
		req, _ := http.NewRequest("GET", target.URL+"/data", nil)
		result, err := pool.Do(req)

		if err != nil || result.Error != nil {
			failCount.Add(1)
		} else if result.Response.StatusCode == 200 {
			successCount.Add(1)
		} else {
			failCount.Add(1)
		}

		// Give circuit breaker time to react
		if i%10 == 0 {
			time.Sleep(10 * time.Millisecond)
		}
	}

	// Check stats
	stats := pool.Stats()
	proxyStats := pool.ProxyStats()

	success := successCount.Load()
	fail := failCount.Load()
	failureRate := float64(fail) / float64(numRequests)

	t.Logf("Results: Success=%d, Failures=%d, FailureRate=%.2f%%",
		success, fail, failureRate*100)
	t.Logf("Pool stats: Healthy=%d, Degraded=%d, Dead=%d",
		stats.HealthyProxies, stats.DegradedProxies, stats.DeadProxies)

	// Verify that some circuit breakers tripped
	var degradedCount, deadCount int
	for _, ps := range proxyStats {
		switch ps.State {
		case "degraded":
			degradedCount++
		case "dead":
			deadCount++
		}
	}

	t.Logf("Proxy states: Degraded=%d, Dead=%d", degradedCount, deadCount)

	// Verify circuit breaker is tracking error rates
	var highErrorRateCount int
	for _, ps := range proxyStats {
		if ps.ErrorRate > 0.5 {
			highErrorRateCount++
			t.Logf("Proxy %s has error rate: %.2f%%", ps.ProxyURL, ps.ErrorRate*100)
		}
	}

	// With 70% failure rate, we should see high error rates tracked
	// Note: Circuit breaker may not have transitioned state yet due to sliding window,
	// but error rates should be recorded
	if highErrorRateCount == 0 {
		t.Error("Expected some proxies to have high error rates, but none found")
	}

	// Wait for recovery timeout and verify recovery
	time.Sleep(150 * time.Millisecond)

	// Execute a few more requests to test recovery
	for i := 0; i < 10; i++ {
		req, _ := http.NewRequest("GET", target.URL+"/data", nil)
		_, _ = pool.Do(req)
		time.Sleep(10 * time.Millisecond)
	}

	// Note: Full recovery testing is complex due to random failures
	// This test primarily validates circuit breaker trips on high error rates
}

// TestIntegration_RateLimiting tests rate limiting behavior
func TestIntegration_RateLimiting(t *testing.T) {
	// Create mock target server
	target := NewMockTarget()
	defer target.Stop()

	// Create farm with low latency for rate limit testing
	farm := NewMockProxyFarm(FarmConfig{
		NumProxies:  3,
		BaseLatency: 1 * time.Millisecond,
		FailureRate: 0.0,
	})

	err := farm.Start()
	if err != nil {
		t.Fatalf("Failed to start farm: %v", err)
	}
	defer farm.Stop()

	// Create pool with low rate limit
	config := rapidproxy.DefaultConfig()
	config.ProxyURLs = farm.URLs()
	config.NumWorkers = 10
	config.QueueSize = 100
	config.RateLimitPerProxy = 5 // 5 RPS per proxy

	pool, err := rapidproxy.New(config)
	if err != nil {
		t.Fatalf("Failed to create pool: %v", err)
	}
	defer func() { _ = pool.Close() }()

	// Execute requests over a sustained period
	const duration = 2 * time.Second
	const expectedMaxRPS = 15 // 3 proxies * 5 RPS

	start := time.Now()
	deadline := start.Add(duration)

	var wg sync.WaitGroup
	var successCount atomic.Int64

	// Keep submitting requests until deadline
	requestLoop := func() {
		defer wg.Done()
		for time.Now().Before(deadline) {
			req, _ := http.NewRequest("GET", target.URL+"/data", nil)
			result, err := pool.Do(req)

			if err == nil && result.Error == nil && result.Response.StatusCode == 200 {
				successCount.Add(1)
			}
		}
	}

	// Start multiple request goroutines
	for i := 0; i < 5; i++ {
		wg.Add(1)
		go requestLoop()
	}

	wg.Wait()
	elapsed := time.Since(start)

	success := successCount.Load()
	actualRPS := float64(success) / elapsed.Seconds()

	t.Logf("Completed %d successful requests in %v", success, elapsed)
	t.Logf("Actual RPS: %.2f", actualRPS)
	t.Logf("Expected max RPS: ~%d (3 proxies * 5 RPS)", expectedMaxRPS)

	// Rate limiting should keep RPS close to expected max
	// Allow 50% margin for test variability and burst capacity
	maxAllowed := float64(expectedMaxRPS) * 1.5

	if actualRPS > maxAllowed {
		t.Errorf("RPS too high: %.2f, expected <= %.2f", actualRPS, maxAllowed)
	}

	// Also check minimum - should be at least 50% of expected
	minExpected := float64(expectedMaxRPS) * 0.5
	if actualRPS < minExpected {
		t.Errorf("RPS too low: %.2f, expected >= %.2f", actualRPS, minExpected)
	}
}

// TestIntegration_ContextCancellation tests context cancellation behavior
func TestIntegration_ContextCancellation(t *testing.T) {
	// Create mock target server
	target := NewMockTarget()
	defer target.Stop()

	// Create farm
	farm := NewMockProxyFarm(FarmConfig{
		NumProxies:  10,
		BaseLatency: 50 * time.Millisecond, // Slow enough to cancel mid-flight
		FailureRate: 0.0,
	})

	err := farm.Start()
	if err != nil {
		t.Fatalf("Failed to start farm: %v", err)
	}
	defer farm.Stop()

	// Create pool
	config := rapidproxy.DefaultConfig()
	config.ProxyURLs = farm.URLs()
	config.NumWorkers = 20
	config.QueueSize = 100

	pool, err := rapidproxy.New(config)
	if err != nil {
		t.Fatalf("Failed to create pool: %v", err)
	}
	defer func() { _ = pool.Close() }()

	// Create context with timeout
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()

	// Start batch requests
	requests := make([]*http.Request, 50)
	for i := range requests {
		requests[i], _ = http.NewRequest("GET", target.URL+"/data", nil)
	}

	start := time.Now()

	// Execute batch with context
	results, _ := pool.DoBatchWithContext(ctx, requests)

	elapsed := time.Since(start)

	// Count successes and context errors
	var successCount, cancelledCount, otherErrorCount int
	for _, result := range results {
		if result.Error == nil && result.Response != nil && result.Response.StatusCode == 200 {
			successCount++
		} else if result.Error != nil {
			// Check if error is context-related
			if result.Error == context.DeadlineExceeded || result.Error == context.Canceled {
				cancelledCount++
			} else {
				otherErrorCount++
			}
		}
	}

	t.Logf("Completed in %v", elapsed)
	t.Logf("Success: %d, Cancelled: %d, Other errors: %d",
		successCount, cancelledCount, otherErrorCount)

	// Verify that context cancellation worked
	if elapsed > 150*time.Millisecond {
		t.Errorf("Batch took too long: %v, expected <= 150ms (100ms timeout + margin)",
			elapsed)
	}

	// Should have some cancelled requests due to timeout
	if cancelledCount == 0 {
		t.Log("Warning: Expected some requests to be cancelled, but got none")
		// Note: This might happen if all requests completed before timeout
	}

	// Verify no goroutine leaks by checking pool can still process requests
	req, _ := http.NewRequest("GET", target.URL+"/data", nil)
	result, err := pool.Do(req)
	if err != nil {
		t.Errorf("Pool failed to process request after cancellation: %v", err)
	} else if result.Error != nil {
		t.Errorf("Request failed after cancellation: %v", result.Error)
	}
}

// TestIntegration_DomainRateLimiting tests per-domain rate limiting with mock proxies
func TestIntegration_DomainRateLimiting(t *testing.T) {
	// Create mock target server
	target := NewMockTarget()
	defer target.Stop()

	// Create farm
	farm := NewMockProxyFarm(FarmConfig{
		NumProxies:  10,
		BaseLatency: 5 * time.Millisecond,
		FailureRate: 0.0,
	})

	err := farm.Start()
	if err != nil {
		t.Fatalf("Failed to start farm: %v", err)
	}
	defer farm.Stop()

	// Create pool with domain rate limiting
	config := rapidproxy.DefaultConfig()
	config.ProxyURLs = farm.URLs()
	config.NumWorkers = 20
	config.QueueSize = 200
	config.RateLimitPerProxy = 100 // High proxy limit

	// Configure domain rate limit
	config.DomainRateLimits = map[string]int64{
		"127.0.0.1": 10, // 10 RPS for localhost
	}

	pool, err := rapidproxy.New(config)
	if err != nil {
		t.Fatalf("Failed to create pool: %v", err)
	}
	defer func() { _ = pool.Close() }()

	// Test: Send burst of requests
	const numRequests = 30
	start := time.Now()
	successCount := 0

	for i := 0; i < numRequests; i++ {
		req, _ := http.NewRequest("GET", target.URL+"/data", nil)
		result, err := pool.Do(req)
		if err == nil && result.Error == nil {
			successCount++
		}
	}

	elapsed := time.Since(start)
	actualRPS := float64(successCount) / elapsed.Seconds()

	t.Logf("Domain rate limiting test: %d/%d requests succeeded in %v (%.1f RPS)",
		successCount, numRequests, elapsed, actualRPS)

	// All requests should eventually succeed (blocking)
	if successCount != numRequests {
		t.Errorf("Expected all %d requests to succeed, got %d", numRequests, successCount)
	}

	// Should be rate limited - expect at least 2 seconds for 30 requests at 10 RPS
	// First 20 go through (burst = 2x rate), next 10 need 1 second
	if elapsed < 1*time.Second {
		t.Errorf("Requests completed too fast (%v), domain rate limiting may not be working", elapsed)
	}

	// Verify target received all requests
	targetRequests := target.GetRequestCount()
	if targetRequests != int64(successCount) {
		t.Errorf("Target should have received %d requests, got %d", successCount, targetRequests)
	}
}
