package main

import (
	"context"
	"errors"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/Q1rD/rapid-proxy/rapidproxy"
)

// createOptimizedConfig demonstrates how to configure the pool for production use
func createOptimizedConfig() *rapidproxy.Config {
	config := rapidproxy.DefaultConfig()

	// Proxy setup - replace with your actual proxies
	// For testing, you can use mock proxies or public proxy services
	config.ProxyURLs = []string{
		"http://127.0.0.1:10000",
		"http://127.0.0.1:10001",
		"http://127.0.0.1:10002",
		// With authentication:
		// "http://user:pass@proxy4.example.com:8080",
	}

	// Worker pool tuning
	// Rule of thumb: NumWorkers = 2x your target concurrency
	config.NumWorkers = 500 // For moderate concurrency (250-500 concurrent requests)
	config.QueueSize = 5000 // 10x worker count for smooth burst handling

	// Rate limiting - CRITICAL: Set conservatively to avoid proxy blocking
	// Total throughput = RateLimitPerProxy * len(ProxyURLs)
	// Example: 3 proxies * 10 RPS = 30 RPS total capacity
	config.RateLimitPerProxy = 10 // Conservative limit (adjust based on your proxies)

	// Per-domain rate limiting (optional)
	// Limits total RPS to specific target domains across ALL proxies
	config.DomainRateLimits = map[string]int64{
		"api.example.com":      100, // Max 100 RPS to this domain
		"slow-api.example.com": 10,  // Max 10 RPS to slow APIs
		// Useful for respecting per-domain rate limits imposed by target APIs
	}

	// Timeouts
	config.RequestTimeout = 30 * time.Second        // End-to-end request timeout
	config.DialTimeout = 5 * time.Second            // Connection establishment timeout
	config.TLSHandshakeTimeout = 5 * time.Second    // TLS handshake timeout
	config.ResponseHeaderTimeout = 10 * time.Second // Time to receive response headers
	config.IdleConnTimeout = 90 * time.Second       // Keep-alive timeout

	// Circuit breaker - auto-detect and skip unhealthy proxies
	config.FailureThreshold = 0.5             // Trip at 50% error rate
	config.DegradedThreshold = 0.2            // Mark degraded at 20% error rate
	config.MinRequestsForTrip = 10            // Need 10 requests before tripping
	config.RecoveryTimeout = 60 * time.Second // Wait 60s before retrying dead proxies

	// Connection pooling
	config.MaxIdleConns = 1000      // Total idle connections across all proxies
	config.MaxIdleConnsPerHost = 10 // Idle connections per proxy

	return config
}

// exampleSingleRequest demonstrates making a single HTTP request with full error handling
func exampleSingleRequest(pool *rapidproxy.Pool) {
	log.Println("=== Single Request Example ===")

	// Create HTTP request
	req, err := http.NewRequest("GET", "http://api.example.com/data", nil)
	if err != nil {
		log.Printf("Failed to create request: %v", err)
		return
	}

	// Set custom headers
	req.Header.Set("User-Agent", "MyApp/1.0")
	req.Header.Set("Accept", "application/json")

	// Execute request through proxy pool
	result, err := pool.Do(req)

	// Handle pool-level errors
	if err != nil {
		// Pool-level error: all proxies busy, queue full, pool closed, etc.
		log.Printf("Pool error: %v", err)
		return
	}

	// Handle request-level errors
	if result.Error != nil {
		// Request-level error: timeout, connection refused, circuit breaker tripped, etc.
		log.Printf("Request error: %v (proxy: %s)", result.Error, result.ProxyUsed)
		return
	}

	// Handle HTTP errors
	if result.Response.StatusCode >= 400 {
		log.Printf("HTTP error: %d (proxy: %s)", result.Response.StatusCode, result.ProxyUsed)
		return
	}

	// Success!
	log.Printf("Success: %d bytes in %v (proxy: %s)",
		len(result.Body), result.Latency, result.ProxyUsed)
	log.Printf("  Status: %d", result.Response.StatusCode)
	log.Printf("  Content-Type: %s", result.Response.Header.Get("Content-Type"))
}

// exampleBatchRequests demonstrates batch request processing with result analysis
func exampleBatchRequests(pool *rapidproxy.Pool) {
	log.Println("\n=== Batch Request Example ===")

	// Create 100 requests
	const numRequests = 100
	requests := make([]*http.Request, numRequests)
	for i := range requests {
		url := fmt.Sprintf("http://api.example.com/item/%d", i)
		requests[i], _ = http.NewRequest("GET", url, nil)
	}

	// Execute batch
	start := time.Now()
	results, err := pool.DoBatch(requests)
	if err != nil {
		log.Printf("Batch failed: %v", err)
		return
	}

	// Analyze results
	var success, clientErrors, serverErrors, networkErrors int
	for _, result := range results {
		if result.Error != nil {
			networkErrors++
			continue
		}

		switch {
		case result.Response.StatusCode < 400:
			success++
		case result.Response.StatusCode < 500:
			clientErrors++
		default:
			serverErrors++
		}
	}

	elapsed := time.Since(start)
	rps := float64(len(requests)) / elapsed.Seconds()

	// Print summary
	log.Printf("Batch complete in %v (%.0f RPS)", elapsed, rps)
	log.Printf("  Success: %d/%d (%.1f%%)", success, len(requests),
		float64(success)/float64(len(requests))*100)
	log.Printf("  Client errors (4xx): %d", clientErrors)
	log.Printf("  Server errors (5xx): %d", serverErrors)
	log.Printf("  Network errors: %d", networkErrors)
}

// exampleWithContext demonstrates context usage for timeouts and cancellation
func exampleWithContext(pool *rapidproxy.Pool) {
	log.Println("\n=== Context Example ===")

	// Set timeout
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	req, _ := http.NewRequest("GET", "http://api.example.com/slow", nil)

	start := time.Now()
	result, err := pool.DoWithContext(ctx, req)
	elapsed := time.Since(start)

	if err == context.DeadlineExceeded {
		log.Printf("Request timed out after %v", elapsed)
		return
	}

	if err != nil {
		log.Printf("Error: %v", err)
		return
	}

	if result.Error != nil {
		if errors.Is(result.Error, context.DeadlineExceeded) {
			log.Printf("Request timed out after %v", elapsed)
		} else {
			log.Printf("Request failed: %v", result.Error)
		}
		return
	}

	log.Printf("Completed in %v (proxy: %s)", result.Latency, result.ProxyUsed)
}

// exampleMonitoring demonstrates monitoring pool and proxy statistics
func exampleMonitoring(pool *rapidproxy.Pool) {
	log.Println("\n=== Monitoring Example ===")

	// Get global pool stats
	stats := pool.Stats()
	log.Printf("Pool Statistics:")
	log.Printf("  Total proxies: %d", stats.TotalProxies)
	log.Printf("  Healthy: %d, Degraded: %d, Dead: %d",
		stats.HealthyProxies, stats.DegradedProxies, stats.DeadProxies)
	log.Printf("  Workers: %d", stats.WorkerCount)
	log.Printf("  Queue: %d/%d (%.1f%% full)",
		stats.QueueSize, stats.QueueCapacity,
		float64(stats.QueueSize)/float64(stats.QueueCapacity)*100)
	log.Printf("  Uptime: %v", stats.Uptime)

	// Get per-proxy stats
	proxyStats := pool.ProxyStats()
	log.Printf("\nProxy Details:")
	for _, ps := range proxyStats {
		if ps.TotalRequests > 0 {
			log.Printf("  %s [%s]: %d requests, %.1f%% errors",
				ps.ProxyURL, ps.State, ps.TotalRequests, ps.ErrorRate*100)
		}
	}

	// Check for unhealthy proxies
	var unhealthy int
	for _, ps := range proxyStats {
		if ps.State != "healthy" {
			unhealthy++
			log.Printf("  WARNING: %s is %s (error rate: %.1f%%)",
				ps.ProxyURL, ps.State, ps.ErrorRate*100)
		}
	}

	if unhealthy > stats.TotalProxies/2 {
		log.Printf("ALERT: More than 50%% of proxies are unhealthy!")
	}

	// Monitor queue utilization
	utilization := float64(stats.QueueSize) / float64(stats.QueueCapacity)
	if utilization > 0.8 {
		log.Printf("WARNING: Queue is %.1f%% full, consider increasing QueueSize or NumWorkers",
			utilization*100)
	}
}

// exampleErrorHandling demonstrates comprehensive error handling patterns
func exampleErrorHandling(pool *rapidproxy.Pool) {
	log.Println("\n=== Error Handling Example ===")

	req, _ := http.NewRequest("GET", "http://api.example.com", nil)

	result, err := pool.Do(req)

	// Pattern 1: Pool-level errors
	switch err {
	case rapidproxy.ErrNoProxies:
		log.Fatal("No proxies configured - check config.ProxyURLs")

	case rapidproxy.ErrAllProxiesBusy:
		log.Printf("All proxies busy or rate-limited, will retry")
		time.Sleep(100 * time.Millisecond)
		// Implement retry logic here
		// Consider exponential backoff for production use

	case rapidproxy.ErrQueueFull:
		log.Printf("Queue full, applying backpressure")
		time.Sleep(50 * time.Millisecond)
		// Retry with backoff or reject request
		// Consider increasing QueueSize or NumWorkers

	case rapidproxy.ErrPoolClosed:
		log.Fatal("Pool has been closed, cannot submit requests")

	case nil:
		// No pool error, check result

	default:
		log.Printf("Unexpected pool error: %v", err)
		return
	}

	// Pattern 2: Request-level errors
	if result.Error != nil {
		// Circuit breaker tripped, timeout, network error
		if errors.Is(result.Error, context.DeadlineExceeded) {
			log.Printf("Request timed out (proxy: %s)", result.ProxyUsed)
			// Consider retry with different timeout or proxy
		} else {
			log.Printf("Request failed: %v (proxy: %s)", result.Error, result.ProxyUsed)
			// Consider retry or fallback
		}
		return
	}

	// Pattern 3: HTTP errors
	if result.Response.StatusCode >= 500 {
		log.Printf("Server error: %d (proxy: %s), might be temporary",
			result.Response.StatusCode, result.ProxyUsed)
		// Consider retry with exponential backoff

	} else if result.Response.StatusCode >= 400 {
		log.Printf("Client error: %d (proxy: %s), don't retry",
			result.Response.StatusCode, result.ProxyUsed)
		// Don't retry 4xx errors - they indicate client mistakes

	} else {
		// Success
		log.Printf("Success: %d (proxy: %s)", result.Response.StatusCode, result.ProxyUsed)
	}
}

// exampleGracefulShutdown demonstrates graceful shutdown handling
func exampleGracefulShutdown(pool *rapidproxy.Pool) {
	log.Println("\n=== Graceful Shutdown Example ===")

	// Setup signal handling
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, os.Interrupt, syscall.SIGTERM)

	log.Println("Press Ctrl+C to trigger graceful shutdown...")

	// Wait for signal in background
	go func() {
		<-sigCh
		log.Println("\nShutdown signal received, starting graceful shutdown...")

		// 1. Stop accepting new requests (application-level)
		// In a real application, you would:
		// - Stop accepting new HTTP requests
		// - Drain existing connections
		// - Wait for in-flight requests to complete

		// 2. Close pool (waits for in-flight requests with timeout)
		log.Println("Closing pool...")
		if err := pool.Close(); err != nil {
			log.Printf("Error during shutdown: %v", err)
		}

		log.Println("Shutdown complete")
		os.Exit(0)
	}()

	// Simulate application running
	log.Println("Application running... (in real app, this would be your main loop)")
	log.Println("Note: This example won't actually wait for Ctrl+C")
}

// exampleRealTimeMonitoring demonstrates real-time stats monitoring
func exampleRealTimeMonitoring(pool *rapidproxy.Pool, duration time.Duration) {
	log.Println("\n=== Real-Time Monitoring Example ===")

	ticker := time.NewTicker(2 * time.Second)
	defer ticker.Stop()

	deadline := time.Now().Add(duration)

	log.Printf("Monitoring for %v...", duration)

	for time.Now().Before(deadline) {
		select {
		case <-ticker.C:
			stats := pool.Stats()
			log.Printf("[STATS] Queue: %d/%d, Proxies: %d healthy, %d degraded, %d dead",
				stats.QueueSize, stats.QueueCapacity,
				stats.HealthyProxies, stats.DegradedProxies, stats.DeadProxies)
		default:
			time.Sleep(100 * time.Millisecond)
		}
	}

	log.Println("Monitoring complete")
}

func main() {
	// Create optimized pool configuration
	config := createOptimizedConfig()

	// Create pool
	pool, err := rapidproxy.New(config)
	if err != nil {
		log.Fatalf("Failed to create pool: %v", err)
	}
	defer func() {
		if err := pool.Close(); err != nil {
			log.Printf("Error closing pool: %v", err)
		}
	}()

	log.Println("Proxy pool initialized successfully")
	log.Printf("  Proxies: %d", len(config.ProxyURLs))
	log.Printf("  Workers: %d", config.NumWorkers)
	log.Printf("  Queue size: %d", config.QueueSize)
	log.Printf("  Rate limit: %d RPS per proxy\n", config.RateLimitPerProxy)

	// Note: These examples use fake URLs. In production:
	// 1. Replace proxy URLs with your actual proxies
	// 2. Replace target URLs with your actual API endpoints
	// 3. Handle errors appropriately for your use case
	// 4. Implement retry logic where appropriate
	// 5. Add logging and monitoring

	// Run examples
	exampleSingleRequest(pool)
	exampleBatchRequests(pool)
	exampleWithContext(pool)
	exampleMonitoring(pool)
	exampleErrorHandling(pool)
	exampleGracefulShutdown(pool)
	exampleRealTimeMonitoring(pool, 10*time.Second)

	log.Println("\n=== All Examples Complete ===")
	log.Println("For production use:")
	log.Println("  1. Configure real proxy URLs")
	log.Println("  2. Tune NumWorkers and QueueSize for your workload")
	log.Println("  3. Set RateLimitPerProxy conservatively")
	log.Println("  4. Monitor pool stats and proxy health")
	log.Println("  5. Implement proper error handling and retries")
	log.Println("\nSee docs/PERFORMANCE.md and docs/TROUBLESHOOTING.md for more details")
}
