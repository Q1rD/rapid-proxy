package metrics

import (
	"time"

	"github.com/Q1rD/rapid-proxy/internal/connection"
)

// Stats contains global pool statistics
type Stats struct {
	// Proxy counts
	TotalProxies    int
	HealthyProxies  int
	DegradedProxies int
	DeadProxies     int

	// Worker pool stats (set by worker pool)
	WorkerCount   int
	QueueSize     int
	QueueCapacity int

	// Uptime
	Uptime time.Duration
}

// ProxyStats contains per-proxy statistics
type ProxyStats struct {
	ProxyURL        string
	State           string // "healthy", "degraded", "dead"
	TotalRequests   int64
	SuccessRequests int64
	FailedRequests  int64
	ErrorRate       float64
	LastSuccess     time.Time
	LastFailure     time.Time
	LastError       error
}

// Collector collects metrics from various components
type Collector struct {
	manager   *connection.Manager
	startTime time.Time
}

// NewCollector creates a new metrics collector
func NewCollector(manager *connection.Manager) *Collector {
	return &Collector{
		manager:   manager,
		startTime: time.Now(),
	}
}

// GetStats returns current global statistics
func (c *Collector) GetStats() *Stats {
	managerStats := c.manager.GetStats()

	return &Stats{
		TotalProxies:    managerStats.TotalProxies,
		HealthyProxies:  managerStats.HealthyProxies,
		DegradedProxies: managerStats.DegradedProxies,
		DeadProxies:     managerStats.DeadProxies,
		Uptime:          time.Since(c.startTime),
		// WorkerCount, QueueSize, QueueCapacity will be set by caller
	}
}

// GetProxyStats returns per-proxy statistics
func (c *Collector) GetProxyStats() []*ProxyStats {
	metrics := c.manager.GetMetrics()
	stats := make([]*ProxyStats, len(metrics))

	for i, m := range metrics {
		stats[i] = &ProxyStats{
			ProxyURL:        m.ProxyURL,
			State:           m.State,
			TotalRequests:   m.TotalRequests,
			SuccessRequests: m.SuccessRequests,
			FailedRequests:  m.FailedRequests,
			ErrorRate:       m.ErrorRate,
			LastSuccess:     m.LastSuccess,
			LastFailure:     m.LastFailure,
			LastError:       m.LastError,
		}
	}

	return stats
}
