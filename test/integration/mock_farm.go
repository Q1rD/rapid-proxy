package integration

import (
	"fmt"
	"math/rand"
	"sync"
	"time"
)

// FarmConfig configures the proxy farm
type FarmConfig struct {
	NumProxies      int           // Default: 100
	BaseLatency     time.Duration // Default: 10ms
	LatencyVariance time.Duration // Default: 5ms (random ±5ms)
	FailureRate     float64       // Default: 0.05 (5% failures)
	SlowProxyRatio  float64       // Default: 0.1 (10% of proxies are slow)
	SlowLatency     time.Duration // Default: 500ms
}

// DefaultFarmConfig returns default configuration for a proxy farm
func DefaultFarmConfig() FarmConfig {
	return FarmConfig{
		NumProxies:      100,
		BaseLatency:     10 * time.Millisecond,
		LatencyVariance: 5 * time.Millisecond,
		FailureRate:     0.05, // 5% failure rate
		SlowProxyRatio:  0.1,  // 10% slow proxies
		SlowLatency:     500 * time.Millisecond,
	}
}

// FarmStats contains aggregate statistics for the entire farm
type FarmStats struct {
	TotalProxies    int
	TotalRequests   int64
	SuccessRequests int64
	FailedRequests  int64
	TimeoutRequests int64
	SuccessRate     float64
	FailureRate     float64
}

// MockProxyFarm manages multiple mock proxy servers
type MockProxyFarm struct {
	Config  FarmConfig
	Proxies []*MockProxy
	urls    []string

	mu     sync.Mutex
	closed bool
}

// NewMockProxyFarm creates a new mock proxy farm
func NewMockProxyFarm(config FarmConfig) *MockProxyFarm {
	// Apply defaults if needed
	if config.NumProxies == 0 {
		config.NumProxies = 100
	}
	if config.BaseLatency == 0 {
		config.BaseLatency = 10 * time.Millisecond
	}

	return &MockProxyFarm{
		Config:  config,
		Proxies: make([]*MockProxy, 0, config.NumProxies),
		urls:    make([]string, 0, config.NumProxies),
	}
}

// Start starts all proxy servers in the farm
func (f *MockProxyFarm) Start() error {
	f.mu.Lock()
	defer f.mu.Unlock()

	if len(f.Proxies) > 0 {
		return fmt.Errorf("farm already started")
	}

	// Calculate proxy distribution
	numSlow := int(float64(f.Config.NumProxies) * f.Config.SlowProxyRatio)
	numNormal := f.Config.NumProxies - numSlow

	// Create normal proxies
	for i := 0; i < numNormal; i++ {
		// Calculate random latency with variance
		var variance time.Duration
		if f.Config.LatencyVariance > 0 {
			varianceRange := int(f.Config.LatencyVariance * 2)
			variance = time.Duration(rand.Intn(varianceRange)) - f.Config.LatencyVariance
		}
		latency := f.Config.BaseLatency + variance

		config := ProxyConfig{
			Name:        fmt.Sprintf("proxy-%d", i),
			Latency:     latency,
			FailureRate: f.Config.FailureRate,
		}

		proxy := NewMockProxy(config)
		proxy.Start()

		f.Proxies = append(f.Proxies, proxy)
		f.urls = append(f.urls, proxy.URL)
	}

	// Create slow proxies
	for i := 0; i < numSlow; i++ {
		config := ProxyConfig{
			Name:            fmt.Sprintf("proxy-slow-%d", i),
			Latency:         f.Config.SlowLatency,
			FailureRate:     f.Config.FailureRate * 2, // Slower proxies fail more often
			SlowRequestRate: 0.5,                       // 50% of requests are extra slow
			SlowLatency:     f.Config.SlowLatency * 2,
		}

		proxy := NewMockProxy(config)
		proxy.Start()

		f.Proxies = append(f.Proxies, proxy)
		f.urls = append(f.urls, proxy.URL)
	}

	return nil
}

// Stop stops all proxy servers in the farm
func (f *MockProxyFarm) Stop() {
	f.mu.Lock()
	defer f.mu.Unlock()

	if f.closed {
		return
	}

	for _, proxy := range f.Proxies {
		proxy.Stop()
	}

	f.closed = true
}

// URLs returns the list of proxy URLs
func (f *MockProxyFarm) URLs() []string {
	f.mu.Lock()
	defer f.mu.Unlock()

	// Return a copy to prevent external modification
	urls := make([]string, len(f.urls))
	copy(urls, f.urls)
	return urls
}

// GetTotalStats returns aggregate statistics for the entire farm
func (f *MockProxyFarm) GetTotalStats() FarmStats {
	f.mu.Lock()
	defer f.mu.Unlock()

	var stats FarmStats
	stats.TotalProxies = len(f.Proxies)

	for _, proxy := range f.Proxies {
		proxyStats := proxy.Stats()
		stats.TotalRequests += proxyStats.RequestCount
		stats.SuccessRequests += proxyStats.SuccessCount
		stats.FailedRequests += proxyStats.FailureCount
		stats.TimeoutRequests += proxyStats.TimeoutCount
	}

	if stats.TotalRequests > 0 {
		stats.SuccessRate = float64(stats.SuccessRequests) / float64(stats.TotalRequests)
		stats.FailureRate = float64(stats.FailedRequests) / float64(stats.TotalRequests)
	}

	return stats
}

// GetProxiesByType returns proxies filtered by type
// types: "normal", "slow", "all"
func (f *MockProxyFarm) GetProxiesByType(typ string) []*MockProxy {
	f.mu.Lock()
	defer f.mu.Unlock()

	if typ == "all" {
		// Return copy
		result := make([]*MockProxy, len(f.Proxies))
		copy(result, f.Proxies)
		return result
	}

	var result []*MockProxy
	for _, proxy := range f.Proxies {
		if typ == "normal" && proxy.Config.SlowRequestRate == 0 {
			result = append(result, proxy)
		} else if typ == "slow" && proxy.Config.SlowRequestRate > 0 {
			result = append(result, proxy)
		}
	}

	return result
}

// KillRandomProxies stops a random selection of proxies
// Useful for chaos testing
func (f *MockProxyFarm) KillRandomProxies(count int) []*MockProxy {
	f.mu.Lock()
	defer f.mu.Unlock()

	if count > len(f.Proxies) {
		count = len(f.Proxies)
	}

	// Select random proxies to kill
	indices := rand.Perm(len(f.Proxies))[:count]
	killed := make([]*MockProxy, 0, count)

	for _, idx := range indices {
		proxy := f.Proxies[idx]
		proxy.Stop()
		killed = append(killed, proxy)
	}

	return killed
}

// RestartProxies restarts previously stopped proxies
func (f *MockProxyFarm) RestartProxies(proxies []*MockProxy) {
	f.mu.Lock()
	defer f.mu.Unlock()

	for _, proxy := range proxies {
		// Reset closed flag and restart
		proxy.mu.Lock()
		proxy.closed = false
		proxy.mu.Unlock()

		proxy.Start()
	}
}

// ResetAllStats resets statistics for all proxies
func (f *MockProxyFarm) ResetAllStats() {
	f.mu.Lock()
	defer f.mu.Unlock()

	for _, proxy := range f.Proxies {
		proxy.Reset()
	}
}
