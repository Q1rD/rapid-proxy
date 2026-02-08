package main

import (
	"context"
	"fmt"
	"log"
	"net/http"
	"time"

	"github.com/Q1rD/rapid-proxy/rapidproxy"
)

func main() {
	// Example proxy list (replace with your actual proxies)
	// For production, load from file or environment variable
	proxyURLs := []string{
		"http://127.0.0.1:10000",
		"http://127.0.0.1:10001",
		"http://127.0.0.1:10002",
		// Add more proxies...
	}

	// Create pool with configuration optimized for batch processing
	config := rapidproxy.DefaultConfig()
	config.ProxyURLs = proxyURLs
	config.NumWorkers = 500       // Adjust based on proxy count
	config.QueueSize = 5000       // Larger queue for batch
	config.RateLimitPerProxy = 10 // 10 RPS per proxy
	config.RequestTimeout = 30 * time.Second
	config.FailureThreshold = 0.5 // 50% error rate trips circuit breaker
	config.RecoveryTimeout = 60 * time.Second

	pool, err := rapidproxy.New(config)
	if err != nil {
		log.Fatalf("Failed to create pool: %v", err)
	}
	defer func() {
		if err := pool.Close(); err != nil {
			log.Printf("Error closing pool: %v", err)
		}
	}()

	fmt.Printf("Starting batch request test...\n\n")

	// Create batch of requests
	// Example: fetching data from multiple API endpoints
	numRequests := 100 // Start with smaller batch for testing
	requests := make([]*http.Request, numRequests)

	// Example URLs - replace with your actual API endpoints
	urls := []string{
		"https://httpbin.org/ip",
		"https://httpbin.org/user-agent",
		"https://httpbin.org/headers",
		"https://httpbin.org/get",
	}

	for i := 0; i < numRequests; i++ {
		url := urls[i%len(urls)]
		requests[i], _ = http.NewRequest("GET", url, nil)
	}

	// Execute batch with timeout
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()

	fmt.Printf("Executing %d requests in parallel...\n", numRequests)
	start := time.Now()

	results, err := pool.DoBatchWithContext(ctx, requests)
	if err != nil {
		log.Fatalf("Batch failed: %v", err)
	}

	elapsed := time.Since(start)
	fmt.Printf("Batch completed in %v\n\n", elapsed)

	// Analyze results
	var (
		successful   int
		failed       int
		totalLatency time.Duration
		minLatency   = time.Hour
		maxLatency   time.Duration
	)

	for _, result := range results {
		if result.Error != nil {
			failed++
			continue
		}
		successful++
		totalLatency += result.Latency

		if result.Latency < minLatency {
			minLatency = result.Latency
		}
		if result.Latency > maxLatency {
			maxLatency = result.Latency
		}
	}

	// Print statistics
	fmt.Printf("Results:\n")
	fmt.Printf("  Total: %d\n", numRequests)
	fmt.Printf("  Successful: %d (%.2f%%)\n", successful, float64(successful)/float64(numRequests)*100)
	fmt.Printf("  Failed: %d (%.2f%%)\n", failed, float64(failed)/float64(numRequests)*100)

	if successful > 0 {
		fmt.Printf("  Average latency: %v\n", totalLatency/time.Duration(successful))
		fmt.Printf("  Min latency: %v\n", minLatency)
		fmt.Printf("  Max latency: %v\n", maxLatency)
	}

	fmt.Printf("  Throughput: %.2f req/s\n", float64(numRequests)/elapsed.Seconds())

	// Print pool stats
	stats := pool.Stats()
	fmt.Printf("\nPool stats:\n")
	fmt.Printf("  Healthy proxies: %d/%d\n", stats.HealthyProxies, stats.TotalProxies)
	fmt.Printf("  Degraded proxies: %d\n", stats.DegradedProxies)
	fmt.Printf("  Dead proxies: %d\n", stats.DeadProxies)

	// Print detailed proxy stats for unhealthy proxies
	proxyStats := pool.ProxyStats()
	unhealthyCount := 0
	for _, ps := range proxyStats {
		if ps.State != "healthy" {
			if unhealthyCount == 0 {
				fmt.Printf("\nUnhealthy proxies:\n")
			}
			unhealthyCount++
			fmt.Printf("  %s [%s]: error_rate=%.2f%%, total=%d, failed=%d\n",
				ps.ProxyURL, ps.State, ps.ErrorRate*100, ps.TotalRequests, ps.FailedRequests)
			if unhealthyCount >= 10 {
				fmt.Printf("  ... and %d more\n", len(proxyStats)-unhealthyCount)
				break
			}
		}
	}
}
