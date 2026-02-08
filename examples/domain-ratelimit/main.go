package main

import (
	"fmt"
	"log"
	"net/http"
	"time"

	"github.com/Q1rD/rapid-proxy/rapidproxy"
)

func main() {
	// Configure pool with per-domain rate limiting
	config := rapidproxy.DefaultConfig()

	// Setup proxies (replace with your actual proxies)
	config.ProxyURLs = []string{
		"http://127.0.0.1:10000",
		"http://127.0.0.1:10001",
		"http://127.0.0.1:10002",
	}

	// Basic pool settings
	config.NumWorkers = 50
	config.QueueSize = 500
	config.RateLimitPerProxy = 20 // 20 RPS per proxy

	// Configure per-domain rate limits
	// This is useful when target APIs have their own rate limits
	config.DomainRateLimits = map[string]int64{
		"api.github.com":       60,  // GitHub API: 60 requests per hour for unauthenticated
		"api.example.com":      100, // Your API: 100 RPS limit
		"slow-api.example.com": 5,   // Slow API: only 5 RPS allowed
	}

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

	log.Println("Pool created with per-domain rate limiting")
	log.Println("Domain limits:")
	for domain, limit := range config.DomainRateLimits {
		log.Printf("  %s: %d RPS", domain, limit)
	}
	log.Println()

	// Example 1: Fast API (high domain limit)
	log.Println("=== Example 1: Fast API (100 RPS limit) ===")
	testDomain(pool, "http://api.example.com/data", 10)

	// Example 2: Slow API (low domain limit)
	log.Println("\n=== Example 2: Slow API (5 RPS limit) ===")
	testDomain(pool, "http://slow-api.example.com/data", 10)

	// Example 3: No domain limit (unlimited, only proxy limit applies)
	log.Println("\n=== Example 3: Unlimited domain (no domain limit) ===")
	testDomain(pool, "http://unlimited.example.com/data", 10)

	log.Println("\n=== All examples complete ===")
	log.Println("Note: Replace proxy URLs with your actual proxies for real usage")
}

func testDomain(pool *rapidproxy.Pool, url string, numRequests int) {
	start := time.Now()
	successCount := 0
	errorCount := 0

	for i := 0; i < numRequests; i++ {
		req, _ := http.NewRequest("GET", url, nil)
		result, err := pool.Do(req)

		if err != nil {
			log.Printf("  Request %d: Pool error: %v", i+1, err)
			errorCount++
			continue
		}

		if result.Error != nil {
			log.Printf("  Request %d: Request error: %v", i+1, result.Error)
			errorCount++
			continue
		}

		log.Printf("  Request %d: Success (status: %d, latency: %v, proxy: %s)",
			i+1, result.Response.StatusCode, result.Latency, result.ProxyUsed)
		successCount++
	}

	elapsed := time.Since(start)
	actualRPS := float64(successCount) / elapsed.Seconds()

	fmt.Printf("\nResults for %s:\n", url)
	fmt.Printf("  Total: %d requests in %v\n", numRequests, elapsed)
	fmt.Printf("  Success: %d, Errors: %d\n", successCount, errorCount)
	fmt.Printf("  Actual RPS: %.1f\n", actualRPS)
}
