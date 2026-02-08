# rapid-proxy

High-performance Go library for executing HTTP requests through a pool of proxy servers.

## Installation

```bash
go get github.com/Q1rD/rapid-proxy
```

## Features

- High throughput: 29k+ RPS with 1000+ proxies
- Per-proxy rate limiting with token bucket algorithm
- Per-domain rate limiting for target API protection
- Circuit breaker for automatic proxy health monitoring
- Worker pool with configurable concurrency
- Connection pooling (singleton HTTP client per proxy)
- Real-time statistics and monitoring
- Context support for timeouts and cancellation

## Quick Start

```go
package main

import (
    "log"
    "net/http"
    "github.com/Q1rD/rapid-proxy/rapidproxy"
)

func main() {
    config := rapidproxy.DefaultConfig()
    config.ProxyURLs = []string{
        "http://proxy1.example.com:8080",
        "http://proxy2.example.com:8080",
    }

    pool, err := rapidproxy.New(config)
    if err != nil {
        log.Fatal(err)
    }
    defer pool.Close()

    req, _ := http.NewRequest("GET", "http://api.example.com", nil)
    result, err := pool.Do(req)
    if err != nil {
        log.Fatal(err)
    }

    log.Printf("Status: %d, Proxy: %s", result.Response.StatusCode, result.ProxyUsed)
}
```

## Configuration

```go
config := rapidproxy.DefaultConfig()

// Worker pool
config.NumWorkers = 1000
config.QueueSize = 10000

// Rate limiting
config.RateLimitPerProxy = 10  // RPS per proxy

// Per-domain rate limiting (optional)
config.DomainRateLimits = map[string]int64{
    "api.example.com": 100,  // Max 100 RPS to this domain
}

// Timeouts
config.RequestTimeout = 30 * time.Second
config.DialTimeout = 5 * time.Second

// Circuit breaker
config.FailureThreshold = 0.5
config.RecoveryTimeout = 60 * time.Second
```

## API

### Single Request

```go
result, err := pool.Do(req)
if err != nil {
    // Pool-level error (queue full, all proxies busy, etc.)
    return err
}
if result.Error != nil {
    // Request-level error (timeout, connection refused, etc.)
    return result.Error
}
// Use result.Response and result.Body
```

### Batch Requests

```go
requests := make([]*http.Request, 100)
for i := range requests {
    requests[i], _ = http.NewRequest("GET", fmt.Sprintf("http://api.example.com/item/%d", i), nil)
}

results, err := pool.DoBatch(requests)
if err != nil {
    return err
}

for _, result := range results {
    // Process each result
}
```

### Context Support

```go
ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
defer cancel()

result, err := pool.DoWithContext(ctx, req)
```

### Monitoring

```go
// Global statistics
stats := pool.Stats()
log.Printf("Healthy: %d/%d, Queue: %d/%d",
    stats.HealthyProxies, stats.TotalProxies,
    stats.QueueSize, stats.QueueCapacity)

// Per-proxy statistics
proxyStats := pool.ProxyStats()
for _, ps := range proxyStats {
    log.Printf("%s [%s]: %d requests, %.1f%% errors",
        ps.ProxyURL, ps.State, ps.TotalRequests, ps.ErrorRate*100)
}
```

## Performance

Benchmarked on 4-core CPU, 16 GB RAM:

| Metric | Result |
|--------|--------|
| Throughput | 29,413 RPS |
| Memory (10k concurrent) | 53 MB |
| Latency p99 | 225 ms |
| Race Conditions | 0 |
| Goroutine Leaks | 0 |

## Architecture

```
Your App
  |
  +-> Pool.Do() / DoBatch()
       |
       v
  Worker Pool (N goroutines)
       |
       v
  Proxy Selector (round-robin, health-aware)
       |
       +-> Proxy 1 -> Rate Limiter -> Circuit Breaker -> HTTP Client
       +-> Proxy 2 -> Rate Limiter -> Circuit Breaker -> HTTP Client
       +-> Proxy N -> Rate Limiter -> Circuit Breaker -> HTTP Client
```

Components:
- **Worker Pool**: Concurrent job processing with backpressure
- **Selector**: Health-aware round-robin proxy selection
- **Rate Limiter**: Per-proxy token bucket (prevents blocking)
- **Circuit Breaker**: 3-state health monitoring (Healthy/Degraded/Dead)
- **HTTP Clients**: Singleton per proxy for connection pooling

## Documentation

- [docs/PERFORMANCE.md](docs/PERFORMANCE.md) - Performance tuning guide
- [docs/TROUBLESHOOTING.md](docs/TROUBLESHOOTING.md) - Common issues and solutions
- [examples/advanced/main.go](examples/advanced/main.go) - Comprehensive examples

## License

MIT License - See [LICENSE](LICENSE) file for details.
