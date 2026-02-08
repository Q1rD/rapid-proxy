package main

import (
	"fmt"
	"log"
	"net/http"
	"time"

	"github.com/Q1rD/rapid-proxy/rapidproxy"
)

func main() {
	// Example proxy list (replace with your actual proxies)
	// For testing, you can use mock proxies or public proxy services
	proxyURLs := []string{
		"http://127.0.0.1:10000",
		"http://127.0.0.1:10001",
		"http://127.0.0.1:10002",
		// Add more proxies...
	}

	// Create pool configuration
	// Using DefaultConfig() and overriding specific values
	config := rapidproxy.DefaultConfig()
	config.ProxyURLs = proxyURLs
	config.NumWorkers = 100 // Adjust based on your needs
	config.QueueSize = 1000
	config.RateLimitPerProxy = 10 // 10 RPS per proxy
	config.RequestTimeout = 30 * time.Second
	config.FailureThreshold = 0.5 // 50% error rate trips circuit breaker
	config.RecoveryTimeout = 60 * time.Second

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

	// Print initial stats
	stats := pool.Stats()
	fmt.Printf("Pool initialized:\n")
	fmt.Printf("  Total proxies: %d\n", stats.TotalProxies)
	fmt.Printf("  Workers: %d\n", stats.WorkerCount)
	fmt.Printf("  Queue capacity: %d\n\n", stats.QueueCapacity)

	// Make a single request
	fmt.Println("Making single request...")
	req, err := http.NewRequest("GET", "https://httpbin.org/ip", nil)
	if err != nil {
		log.Fatalf("Failed to create request: %v", err)
	}

	result, err := pool.Do(req)
	if err != nil {
		log.Fatalf("Request failed: %v", err)
	}

	if result.Error != nil {
		log.Fatalf("Request returned error: %v", result.Error)
	}

	fmt.Printf("Success!\n")
	fmt.Printf("  Status: %d\n", result.Response.StatusCode)
	fmt.Printf("  Proxy used: %s\n", result.ProxyUsed)
	fmt.Printf("  Latency: %v\n", result.Latency)
	fmt.Printf("  Body preview: %s\n\n", string(result.Body[:min(len(result.Body), 100)]))

	// Print final stats
	stats = pool.Stats()
	fmt.Printf("Final stats:\n")
	fmt.Printf("  Healthy proxies: %d/%d\n", stats.HealthyProxies, stats.TotalProxies)
	fmt.Printf("  Queue size: %d/%d\n", stats.QueueSize, stats.QueueCapacity)
	fmt.Printf("  Uptime: %v\n", stats.Uptime)

	// Print per-proxy stats
	fmt.Println("\nPer-proxy stats:")
	proxyStats := pool.ProxyStats()
	for i, pstat := range proxyStats {
		if i >= 5 {
			fmt.Printf("  ... and %d more proxies\n", len(proxyStats)-5)
			break
		}
		fmt.Printf("  %s: state=%s, requests=%d, errors=%.1f%%\n",
			pstat.ProxyURL, pstat.State, pstat.TotalRequests, pstat.ErrorRate*100)
	}
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}
