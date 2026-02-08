# Performance Tuning Guide

## Overview

rapid-proxy is designed for high throughput (8-10k+ RPS with 1000+ proxies). This guide helps you tune configuration for your specific workload and achieve optimal performance.

## Quick Start: Performance Checklist

Before diving into details, check these essentials:

- Set `NumWorkers` = 1000 (or 2x your target concurrency)
- Set `QueueSize` = 10x `NumWorkers`
- Set `RateLimitPerProxy` conservatively (10-20 RPS)
- Configure `DomainRateLimits` if needed for target APIs
- Monitor circuit breaker transitions with `ProxyStats()`
- Adjust timeouts based on your proxy latency
- Ensure OS file descriptor limit is sufficient (`ulimit -n 100000`)

## Configuration Parameters

### 1. Worker Pool (NumWorkers, QueueSize)

#### NumWorkers: How many concurrent requests?

**Purpose**: Controls maximum concurrent request processing.

**Default**: 1000 (good for 8-10k RPS)

**Trade-offs**:
- **Too low**: Throughput bottleneck, requests queue up
- **Too high**: Memory overhead, goroutine context switch cost

**Rule of thumb**: Set to 2x your target concurrent requests.

**Example scenarios**:
```go
// 500 concurrent requests
config.NumWorkers = 1000

// 2k concurrent requests
config.NumWorkers = 4000

// 10k concurrent requests
config.NumWorkers = 20000
```

**When to increase**:
- Queue is consistently full (`Stats().QueueSize` near `QueueCapacity`)
- Low CPU usage but requests are slow
- High latency due to queuing delay

**When to decrease**:
- High memory usage with low throughput
- Many goroutines but low utilization

#### QueueSize: How much backpressure buffering?

**Purpose**: Buffer for requests when all workers are busy.

**Default**: 10000 (10x workers)

**Trade-offs**:
- **Too low**: `ErrQueueFull` errors under burst load
- **Too high**: Memory overhead, delayed backpressure signal

**Rule of thumb**: 10x `NumWorkers` for smooth burst handling.

```go
config.NumWorkers = 1000
config.QueueSize = 10000  // 10x workers
```

**When to increase**:
- Frequent `ErrQueueFull` errors
- Burst traffic patterns
- Need tolerance for temporary spikes

**When to decrease**:
- Memory constraints
- Want faster backpressure feedback
- Steady (non-bursty) traffic

---

### 2. Rate Limiting (RateLimitPerProxy)

**Purpose**: Prevent proxy blocking due to excessive request rate.

**Default**: 10 RPS per proxy

**CRITICAL**: This is the most important setting for avoiding proxy blocks!

**Examples**:
```go
config.RateLimitPerProxy = 20
```

**Warning**: Setting too high will cause proxy blocking (HTTP 429).

---

### 2.5. Per-Domain Rate Limiting (DomainRateLimits)

**Purpose**: Limit total requests per second to specific target domains.

**Default**: nil (no per-domain limits)

**Optional**: Set to respect API rate limits on target domains.

**How it works**:
- Limits total RPS to each domain **across ALL proxies**
- Independent from proxy rate limiting (both limits enforced)
- Exact hostname matching (no wildcards, port ignored)

**Examples**:
```go
// Limit specific domains
config.DomainRateLimits = map[string]int64{
    "api.example.com":      100,  // Max 100 RPS to this domain
    "slow-api.example.com": 10,   // Max 10 RPS to slow API
    "free-tier.api.com":    5,    // Respect free tier limits
}
```

**When to use**:
- Target API has documented rate limits per domain
- Prevent overwhelming specific backend services
- Respect free tier limitations on target APIs
- Testing - simulate production rate limits

**Important notes**:
- Domain matching: `api.example.com` matches both `http://api.example.com` and `https://api.example.com:443`
- Both proxy and domain limits apply: effective rate = min(proxy_limit, domain_limit)
- If domain not in map, no domain-specific limit applied
- Invalid rates (≤0) are ignored

**Example calculation**:
```go
config.RateLimitPerProxy = 10    // 10 RPS per proxy
config.ProxyURLs = []string{...} // 100 proxies
config.DomainRateLimits = map[string]int64{
    "api.example.com": 50,  // 50 RPS domain limit
}

// Effective rates:
// - api.example.com: min(100*10, 50) = 50 RPS (domain limit wins)
// - other-api.com:   100*10 = 1000 RPS (no domain limit)
```

---

### 3. Timeouts

#### RequestTimeout (end-to-end)

**Purpose**: Maximum time for complete request lifecycle.

**Default**: 30 seconds

**Includes**: Queue time + connection + request + response reading

**Recommendation**: Your SLA + buffer

```go
// If SLA is 10s, set to 15-20s for buffer
config.RequestTimeout = 15 * time.Second

// For fast APIs
config.RequestTimeout = 5 * time.Second

// For slow/unpredictable APIs
config.RequestTimeout = 60 * time.Second
```

**Note**: Can be overridden per-request using `DoWithContext`.

#### DialTimeout, TLSHandshakeTimeout

**Purpose**: Time to establish connection.

**Defaults**: 5 seconds each

**Increase if**:
- Proxies are geographically distant
- Connection establishment is slow
- Seeing "dial timeout" errors

```go
// For distant/slow proxies
config.DialTimeout = 10 * time.Second
config.TLSHandshakeTimeout = 10 * time.Second
```

#### ResponseHeaderTimeout

**Purpose**: Time to receive response headers after sending request.

**Default**: 10 seconds

**Does not** limit response body reading time.

```go
// For slow backend servers
config.ResponseHeaderTimeout = 20 * time.Second
```

---

### 4. Circuit Breaker

#### FailureThreshold (when to trip)

**Purpose**: Error rate that marks proxy as "dead".

**Default**: 0.5 (50% errors)

**Range**: 0.0 to 1.0

**Trade-offs**:
- **Lower** (e.g., 0.3): Faster failure detection, less tolerance
- **Higher** (e.g., 0.7): More error tolerance, slower response

```go
// Aggressive: trip at 30% errors
config.FailureThreshold = 0.3

// Tolerant: trip at 70% errors
config.FailureThreshold = 0.7
```

**When to decrease** (be more aggressive):
- High-quality proxies with rare failures
- Want fast failure detection
- Cost of using bad proxy is high

**When to increase** (be more tolerant):
- Lower-quality proxies with expected errors
- Temporary network issues are common
- Need maximum availability

#### MinRequestsForTrip

**Purpose**: Minimum requests before circuit breaker can trip.

**Default**: 10 requests

**Purpose**: Prevents premature trips due to small sample sizes.

```go
// Fast response to failures (but less stable)
config.MinRequestsForTrip = 5

// More stable (but slower to detect)
config.MinRequestsForTrip = 20
```

#### RecoveryTimeout

**Purpose**: How long to wait before retrying a "dead" proxy.

**Default**: 60 seconds

```go
// Fast recovery
config.RecoveryTimeout = 30 * time.Second

// Conservative (persistent errors)
config.RecoveryTimeout = 2 * time.Minute
```

---

## Monitoring Performance

### Using Stats() for Pool Health

```go
stats := pool.Stats()

// Check queue utilization
utilization := float64(stats.QueueSize) / float64(stats.QueueCapacity)
if utilization > 0.8 {
    log.Println("WARNING: Queue is 80% full!")
    // Consider: Increase QueueSize or NumWorkers
}

// Check proxy health
if stats.DeadProxies > stats.TotalProxies/2 {
    log.Println("WARNING: More than 50% of proxies are dead!")
    // Consider: Check proxy quality, adjust FailureThreshold
}
```

### Using ProxyStats() for Per-Proxy Analysis

```go
proxyStats := pool.ProxyStats()

// Find problematic proxies
for _, ps := range proxyStats {
    if ps.State == "dead" {
        log.Printf("Dead proxy: %s (%.1f%% errors, %d requests)",
            ps.ProxyURL, ps.ErrorRate*100, ps.TotalRequests)
    }

    if ps.ErrorRate > 0.3 {
        log.Printf("High error rate: %s (%.1f%%)",
            ps.ProxyURL, ps.ErrorRate*100)
    }
}

// Calculate average error rate
var totalErrors, totalRequests int64
for _, ps := range proxyStats {
    totalErrors += int64(float64(ps.TotalRequests) * ps.ErrorRate)
    totalRequests += ps.TotalRequests
}
avgErrorRate := float64(totalErrors) / float64(totalRequests)
log.Printf("Average error rate: %.1f%%", avgErrorRate*100)
```

---

## Common Performance Issues

### Issue: Low throughput (< 1k RPS)

**Symptoms**:
- Slow request processing
- Low CPU usage
- Queue not full

**Common causes**:
1. `NumWorkers` too low
2. `RateLimitPerProxy` too low
3. `DomainRateLimits` too restrictive
4. High proxy latency

**Diagnosis**:
```go
stats := pool.Stats()
log.Printf("Workers: %d, Queue: %d/%d",
    stats.WorkerCount, stats.QueueSize, stats.QueueCapacity)

proxyStats := pool.ProxyStats()
// Check if proxies are healthy and not rate-limited
```

**Fixes**:
```go
// Increase workers
config.NumWorkers = 2000

// Increase rate limit (if proxies support it)
config.RateLimitPerProxy = 20

// Relax domain limits if too strict
config.DomainRateLimits["api.example.com"] = 200

// Check proxy latency and quality
```

---

### Issue: High memory usage (> 500 MB)

**Symptoms**:
- Memory grows over time
- High GC pressure
- Out of memory errors

**Common causes**:
1. `QueueSize` too large
2. `NumWorkers` too high
3. `MaxIdleConns` too high
4. Request/response bodies accumulating

**Fixes**:
```go
// Reduce queue size
config.QueueSize = 5000

// Reduce workers
config.NumWorkers = 500

// Reduce connection pooling
config.MaxIdleConns = 500
config.IdleConnTimeout = 60 * time.Second
```

---

### Issue: ErrQueueFull errors

**Symptoms**:
- Requests rejected with `ErrQueueFull`
- Happens during bursts

**Causes**:
- Submitting requests faster than workers can process
- `QueueSize` too small

**Fixes**:
```go
// Increase queue buffer
config.QueueSize = 20000

// Increase processing capacity
config.NumWorkers = 2000

// Or implement application-level backpressure
```

---

### Issue: Proxies marked as "dead"

**Symptoms**:
- `ErrAllProxiesBusy` errors
- Many proxies in "dead" state
- High error rates

**Common causes**:
1. `RateLimitPerProxy` too high → proxy blocking
2. `FailureThreshold` too aggressive
3. Poor proxy quality

**Diagnosis**:
```go
proxyStats := pool.ProxyStats()
for _, ps := range proxyStats {
    if ps.State == "dead" {
        log.Printf("%s: %.1f%% errors (%d requests)",
            ps.ProxyURL, ps.ErrorRate*100, ps.TotalRequests)
    }
}
```

**Fixes**:
```go
// Option 1: Reduce rate limit
config.RateLimitPerProxy = 5

// Option 2: More tolerant circuit breaker
config.FailureThreshold = 0.7
config.MinRequestsForTrip = 20

// Option 3: Faster recovery
config.RecoveryTimeout = 30 * time.Second
```

---

## Benchmarking Your Setup

### Custom Benchmark

```go
func BenchmarkYourWorkload(b *testing.B) {
    // Setup
    config := rapidproxy.DefaultConfig()
    config.ProxyURLs = yourProxies
    config.NumWorkers = 1000

    pool, _ := rapidproxy.New(config)
    defer pool.Close()

    // Benchmark
    b.ResetTimer()
    b.RunParallel(func(pb *testing.PB) {
        for pb.Next() {
            req, _ := http.NewRequest("GET", targetURL, nil)
            _, _ = pool.Do(req)
        }
    })

    // Report
    elapsed := b.Elapsed()
    rps := float64(b.N) / elapsed.Seconds()
    b.Logf("Throughput: %.0f RPS", rps)
    b.Logf("Requests: %d in %v", b.N, elapsed)
}
```

### Load Testing

```go
func TestLoadTest(t *testing.T) {
    const (
        numRequests = 10000
        numWorkers  = 1000
    )

    // ... setup pool ...

    var wg sync.WaitGroup
    start := time.Now()

    for i := 0; i < numRequests; i++ {
        wg.Add(1)
        go func() {
            defer wg.Done()
            req, _ := http.NewRequest("GET", url, nil)
            _, _ = pool.Do(req)
        }()
    }

    wg.Wait()
    elapsed := time.Since(start)

    rps := float64(numRequests) / elapsed.Seconds()
    t.Logf("Completed %d requests in %v (%.0f RPS)",
        numRequests, elapsed, rps)
}
```

---

## System-Level Tuning

### File Descriptors

Each connection uses one file descriptor. For >10k RPS:

```bash
# Check current limit
ulimit -n

# Temporary increase
ulimit -n 100000

# Permanent (Linux - /etc/security/limits.conf)
echo "* soft nofile 100000" >> /etc/security/limits.conf
echo "* hard nofile 100000" >> /etc/security/limits.conf
```

### TCP Settings

For high throughput:

```bash
# Increase local port range
sysctl -w net.ipv4.ip_local_port_range="1024 65535"

# Enable TCP connection reuse
sysctl -w net.ipv4.tcp_tw_reuse=1

# Increase connection backlog
sysctl -w net.core.somaxconn=4096
```

---

## Performance Targets

Based on load testing results:

| Metric | Achieved | Target | Status |
|--------|----------|--------|--------|
| Throughput | **29,413 RPS** | 8-10k RPS |  3.7x |
| Memory (10k concurrent) | **53 MB** | < 500 MB |  9.4x |
| Latency p50 | 133 ms | < 100 ms |  (proxy dependent) |
| Latency p99 | **225 ms** | < 500 ms |  2.2x |
| Race Conditions | **0** | 0 |  |
| Goroutine Leaks | **0** | 0 |  |

**Your results will vary based on**:
- **Proxy latency**: Fast proxies = higher RPS
- **Proxy quality**: Reliable proxies = fewer retries
- **Request complexity**: Small requests = lower memory
- **Network conditions**: Local vs remote proxies
- **Target API**: Fast APIs = lower latency

---

## Configuration Templates

### High Throughput (10k+ RPS)

```go
config := rapidproxy.DefaultConfig()
config.NumWorkers = 10000
config.QueueSize = 100000
config.RateLimitPerProxy = 20
config.RequestTimeout = 30 * time.Second
```

### Low Latency (< 100ms p99)

```go
config := rapidproxy.DefaultConfig()
config.NumWorkers = 2000
config.QueueSize = 20000
config.RateLimitPerProxy = 10
config.RequestTimeout = 5 * time.Second
config.DialTimeout = 2 * time.Second
```

### High Reliability (maximize success rate)

```go
config := rapidproxy.DefaultConfig()
config.NumWorkers = 1000
config.QueueSize = 10000
config.RateLimitPerProxy = 5  // Conservative
config.FailureThreshold = 0.7  // Tolerant
config.MinRequestsForTrip = 20
config.RecoveryTimeout = 30 * time.Second
```

### Memory Constrained

```go
config := rapidproxy.DefaultConfig()
config.NumWorkers = 500
config.QueueSize = 5000
config.MaxIdleConns = 500
config.IdleConnTimeout = 60 * time.Second
```

### Per-Domain Rate Limiting

```go
config := rapidproxy.DefaultConfig()
config.NumWorkers = 1000
config.RateLimitPerProxy = 20

// Respect target API limits
config.DomainRateLimits = map[string]int64{
    "api.github.com":    60,   // GitHub API limit
    "api.twitter.com":   100,  // Twitter API limit
    "slow-api.com":      10,   // Slow backend
}
```

---

## Summary

**Key takeaways**:
1. **Start with defaults** - they're tuned for common use cases
2. **Monitor first** - use `Stats()` and `ProxyStats()` to understand behavior
3. **Tune gradually** - change one parameter at a time and measure
4. **Rate limit conservatively** - proxy blocking is hard to recover from
5. **Use domain limits** - respect target API rate limits
6. **System limits matter** - file descriptors, TCP settings, etc.

For troubleshooting specific issues, see [TROUBLESHOOTING.md](TROUBLESHOOTING.md).
