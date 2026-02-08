# Troubleshooting Guide

## Quick Diagnostics

Run this to check pool health:

```go
stats := pool.Stats()
fmt.Printf("Health: %d/%d healthy, %d degraded, %d dead\n",
    stats.HealthyProxies, stats.TotalProxies,
    stats.DegradedProxies, stats.DeadProxies)
fmt.Printf("Queue: %d/%d\n", stats.QueueSize, stats.QueueCapacity)

proxyStats := pool.ProxyStats()
for _, ps := range proxyStats {
    if ps.ErrorRate > 0.1 {  // More than 10% errors
        fmt.Printf("Problem proxy: %s (%.1f%% errors, %s)\n",
            ps.ProxyURL, ps.ErrorRate*100, ps.State)
    }
}
```

## Common Errors

### ErrNoProxies
**Error:** `no proxy URLs provided`

**Cause:** ProxyURLs slice is empty in config

**Fix:**
```go
config := rapidproxy.DefaultConfig()
config.ProxyURLs = []string{"http://proxy1:8080", "http://proxy2:8080"}
```

### ErrAllProxiesBusy
**Error:** `all proxies are busy or unhealthy`

**Cause:** All proxies are either:
- Dead (circuit breaker tripped)
- Rate-limited (hit RateLimitPerProxy)

**Diagnosis:**
```go
proxyStats := pool.ProxyStats()
for _, ps := range proxyStats {
    fmt.Printf("%s: state=%s, errors=%.1f%%\n",
        ps.ProxyURL, ps.State, ps.ErrorRate*100)
}
```

**Fix:**
- If many proxies are "dead": Check proxy quality, increase RecoveryTimeout
- If rate-limited: Increase RateLimitPerProxy or add more proxies

### ErrQueueFull
**Error:** `worker pool queue is full`

**Cause:** Submitting requests faster than workers can process

**Diagnosis:** Check queue utilization
```go
stats := pool.Stats()
utilization := float64(stats.QueueSize) / float64(stats.QueueCapacity)
fmt.Printf("Queue utilization: %.1f%%\n", utilization*100)
```

**Fix:**
```go
config.QueueSize = 20000  // Increase buffer
config.NumWorkers = 2000  // Increase processing
```

### ErrPoolClosed
**Error:** `pool is closed`

**Cause:** Trying to use pool after Close() was called

**Fix:** Don't use pool after shutdown
```go
defer pool.Close()
// All requests must complete before Close() is called
```

### context.DeadlineExceeded
**Error:** Request timeout in result.Error

**Cause:** Request exceeded context deadline

**Fix:**
```go
ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
defer cancel()
result, _ := pool.DoWithContext(ctx, req)
// Or increase config.RequestTimeout
```

---

## Domain Rate Limiting Issues

### Domain rate limit too restrictive

**Symptoms:**
- Requests to specific domains are slow
- High latency for certain domains
- Requests blocking for too long

**Diagnosis:**
```go
// Check if domain has rate limit configured
if pool.domainLimiter != nil {
    domains := pool.domainLimiter.GetDomains()
    fmt.Printf("Domains with limits: %v\n", domains)
}
```

**Fix:**
```go
// Increase domain rate limit
config.DomainRateLimits = map[string]int64{
    "api.example.com": 200,  // Increased from 100
}

// Or remove domain limit entirely
delete(config.DomainRateLimits, "api.example.com")
```

### Domain rate limit not working

**Symptoms:**
- Too many requests going to target domain
- Target API returning 429 errors
- No apparent rate limiting

**Diagnosis:**
```go
// Verify domain is configured
fmt.Printf("Domain limits: %v\n", config.DomainRateLimits)

// Check hostname extraction
req, _ := http.NewRequest("GET", "http://api.example.com/path", nil)
hostname := req.URL.Hostname()
fmt.Printf("Hostname: %s\n", hostname)  // Should be "api.example.com"
```

**Common mistakes:**
- Domain name mismatch (check exact hostname)
- Port included in domain name (should be excluded)
- Using URL path instead of hostname

**Fix:**
```go
// Use exact hostname (no port, no path)
config.DomainRateLimits = map[string]int64{
    "api.example.com": 100,  // Correct
    // NOT: "api.example.com:443"
    // NOT: "api.example.com/v1"
}
```

---

## Circuit Breaker Issues

### All proxies marked as "dead"
**Symptoms:** ErrAllProxiesBusy, all ProxyStats show state="dead"

**Causes:**
1. RateLimitPerProxy too high → proxy blocking → high error rate → circuit breaker trips
2. FailureThreshold too low → normal errors trigger circuit breaker
3. Poor proxy quality → legitimate high error rate

**Diagnosis:**
```go
for _, ps := range pool.ProxyStats() {
    fmt.Printf("%s: errors=%.1f%%, requests=%d\n",
        ps.ProxyURL, ps.ErrorRate*100, ps.TotalRequests)
}
```

**Fix:**
```go
// Option 1: More conservative rate limit
config.RateLimitPerProxy = 5  // Lower from 10

// Option 2: More tolerant circuit breaker
config.FailureThreshold = 0.7  // Trip at 70% instead of 50%
config.MinRequestsForTrip = 20  // Need more samples

// Option 3: Faster recovery
config.RecoveryTimeout = 30 * time.Second  // Retry sooner
```

### Circuit breaker not tripping when it should
**Symptoms:** Proxies stay "healthy" despite high error rates

**Cause:** MinRequestsForTrip not reached yet

**Fix:**
```go
config.MinRequestsForTrip = 5  // Lower threshold (but less stable)
```

---

## Connection Issues

### "dial tcp: too many open files"
**Cause:** Hit OS file descriptor limit

**Fix:**
```bash
# Temporary
ulimit -n 100000

# Permanent (Linux)
echo "* soft nofile 100000" >> /etc/security/limits.conf
echo "* hard nofile 100000" >> /etc/security/limits.conf
```

### "connection reset by peer"
**Cause:** Proxy closed connection (rate limiting, proxy failure)

**Fix:**
- Check proxy health with ProxyStats()
- Reduce RateLimitPerProxy
- Verify proxy credentials

### "proxyconnect tcp: dial tcp: i/o timeout"
**Cause:** Proxy unreachable or slow to connect

**Fix:**
```go
config.DialTimeout = 10 * time.Second  // Increase from 5s
// Or remove unreachable proxies from config.ProxyURLs
```

### "proxy authentication required"
**Cause:** Proxy requires authentication but credentials not provided

**Fix:**
```go
// Include credentials in proxy URL
config.ProxyURLs = []string{
    "http://username:password@proxy.example.com:8080",
}

// URL encode special characters in password
// Example: password "p@ss" becomes "p%40ss"
```

---

## Performance Issues

### Low throughput (< 1k RPS)
See [PERFORMANCE.md](PERFORMANCE.md) for detailed tuning guide.

**Quick fixes:**
```go
config.NumWorkers = 2000
config.RateLimitPerProxy = 50

// Check if domain limits are bottleneck
config.DomainRateLimits = map[string]int64{
    "api.example.com": 500,  // Increase if too low
}
```

### High latency (p99 > 1s)
**Diagnosis:**
```go
// Track latencies
var latencies []time.Duration
for i := 0; i < 100; i++ {
    start := time.Now()
    result, _ := pool.Do(req)
    latencies = append(latencies, time.Since(start))
}
sort.Slice(latencies, func(i, j int) bool {
    return latencies[i] < latencies[j]
})
p99 := latencies[int(0.99*float64(len(latencies)))]
fmt.Printf("p99 latency: %v\n", p99)
```

**Causes:**
- Slow proxies (check proxy latency separately)
- Queue queueing delay (increase NumWorkers)
- Circuit breaker delays (check for degraded proxies)
- Domain rate limiting (check DomainRateLimits)

**Fix:**
```go
// Increase workers to reduce queue time
config.NumWorkers = 2000

// Increase timeouts if proxies are legitimately slow
config.RequestTimeout = 60 * time.Second

// Relax domain limits if too strict
config.DomainRateLimits["api.example.com"] = 200
```

---

## Debugging Techniques

### Enable verbose logging
```go
// In your application
log.SetFlags(log.LstdFlags | log.Lmicroseconds)
log.SetOutput(os.Stdout)
```

### Monitor stats in real-time
```go
go func() {
    ticker := time.NewTicker(10 * time.Second)
    defer ticker.Stop()

    for range ticker.C {
        stats := pool.Stats()
        log.Printf("[STATS] Queue: %d/%d, Proxies: %d healthy, %d degraded, %d dead",
            stats.QueueSize, stats.QueueCapacity,
            stats.HealthyProxies, stats.DegradedProxies, stats.DeadProxies)
    }
}()
```

### Test individual proxy
```go
// Test proxy directly without pool
client := &http.Client{
    Transport: &http.Transport{
        Proxy: http.ProxyURL(mustParse("http://proxy:8080")),
    },
    Timeout: 10 * time.Second,
}

resp, err := client.Get("http://example.com")
if err != nil {
    log.Printf("Proxy failed: %v", err)
} else {
    log.Printf("Proxy works: %d", resp.StatusCode)
}
```

### Test domain rate limiting
```go
// Verify domain limiter is working
start := time.Now()
for i := 0; i < 20; i++ {
    req, _ := http.NewRequest("GET", "http://api.example.com/test", nil)
    pool.Do(req)
}
elapsed := time.Since(start)
actualRPS := 20.0 / elapsed.Seconds()

fmt.Printf("Actual RPS to api.example.com: %.1f\n", actualRPS)
// Should be close to DomainRateLimits["api.example.com"]
```

---

## Common Mistakes

### 1. Not handling pool-level errors
**Bad:**
```go
result, _ := pool.Do(req)  // Ignoring error
if result.Error != nil {
    // Handle error
}
```

**Good:**
```go
result, err := pool.Do(req)
if err != nil {
    // Handle pool-level error (ErrQueueFull, ErrAllProxiesBusy, etc.)
    return err
}
if result.Error != nil {
    // Handle request-level error
    return result.Error
}
```

### 2. Setting RateLimitPerProxy too high
**Problem:** RateLimitPerProxy too high → proxies get blocked

**Bad:**
```go
config.RateLimitPerProxy = 100  // Will get blocked!
```

**Good:**
```go
config.RateLimitPerProxy = 10   // Conservative
// Monitor ProxyStats() and increase gradually
```

### 3. Not configuring domain limits for rate-limited APIs
**Problem:** Overwhelming target API with too many requests

**Bad:**
```go
// No domain limits - all 1000 proxies hit api.example.com
config.RateLimitPerProxy = 10
// Total: 10,000 RPS to api.example.com (API can only handle 100!)
```

**Good:**
```go
config.RateLimitPerProxy = 10
config.DomainRateLimits = map[string]int64{
    "api.example.com": 100,  // Respect API's limit
}
// Now: 100 RPS to api.example.com (within API limits)
```

### 4. Using pool after Close()
**Bad:**
```go
pool.Close()
pool.Do(req)  // ErrPoolClosed
```

**Good:**
```go
defer pool.Close()
// Use pool before Close() is called
```

### 5. Not checking both error types
**Bad:**
```go
result, _ := pool.Do(req)
if result.Response.StatusCode >= 400 {
    // Only checking HTTP status
}
```

**Good:**
```go
result, err := pool.Do(req)
if err != nil {
    return err  // Pool-level error
}
if result.Error != nil {
    return result.Error  // Request-level error
}
if result.Response.StatusCode >= 400 {
    // HTTP error
}
```

### 6. Wrong domain name in DomainRateLimits
**Bad:**
```go
config.DomainRateLimits = map[string]int64{
    "http://api.example.com": 100,     // Wrong - includes scheme
    "api.example.com:443":    100,     // Wrong - includes port
    "api.example.com/v1":     100,     // Wrong - includes path
}
```

**Good:**
```go
config.DomainRateLimits = map[string]int64{
    "api.example.com": 100,  // Correct - hostname only
}
```

---

## Useful Commands

```bash
# Run tests with race detector
go test -race ./rapidproxy/

# Run with verbose logging
go test -v ./rapidproxy/

# Profile CPU usage
go test -cpuprofile=cpu.prof -bench=.
go tool pprof cpu.prof

# Profile memory usage
go test -memprofile=mem.prof -bench=.
go tool pprof mem.prof

# Check for goroutine leaks
go test -v -run TestClose
```
