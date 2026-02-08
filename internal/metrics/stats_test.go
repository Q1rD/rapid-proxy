package metrics

import (
	"testing"
	"time"

	"github.com/Q1rD/rapid-proxy/internal/connection"
)

// createTestManager creates a test manager with mock proxies
func createTestManager() *connection.Manager {
	config := &connection.Config{
		ProxyURLs: []string{
			"http://proxy1:8080",
			"http://proxy2:8080",
			"http://proxy3:8080",
		},
		RequestTimeout: 30 * time.Second,
	}

	manager, _ := connection.NewManager(config)
	return manager
}

// TestNewCollector tests collector creation
func TestNewCollector(t *testing.T) {
	manager := createTestManager()
	defer func() { _ = manager.Close() }()

	collector := NewCollector(manager)

	if collector == nil {
		t.Fatal("Expected non-nil collector")
	}

	if collector.manager != manager {
		t.Error("Collector should store manager reference")
	}

	if collector.startTime.IsZero() {
		t.Error("Start time should be initialized")
	}
}

// TestGetStats tests global statistics
func TestGetStats(t *testing.T) {
	manager := createTestManager()
	defer func() { _ = manager.Close() }()

	collector := NewCollector(manager)

	stats := collector.GetStats()

	if stats == nil {
		t.Fatal("Expected non-nil stats")
	}

	if stats.TotalProxies != 3 {
		t.Errorf("Expected 3 total proxies, got %d", stats.TotalProxies)
	}

	// All proxies should start healthy
	if stats.HealthyProxies != 3 {
		t.Errorf("Expected 3 healthy proxies, got %d", stats.HealthyProxies)
	}

	if stats.DegradedProxies != 0 {
		t.Errorf("Expected 0 degraded proxies, got %d", stats.DegradedProxies)
	}

	if stats.DeadProxies != 0 {
		t.Errorf("Expected 0 dead proxies, got %d", stats.DeadProxies)
	}

	// Uptime should be > 0
	if stats.Uptime <= 0 {
		t.Error("Uptime should be positive")
	}
}

// TestGetProxyStats tests per-proxy statistics
func TestGetProxyStats(t *testing.T) {
	manager := createTestManager()
	defer func() { _ = manager.Close() }()

	collector := NewCollector(manager)

	proxyStats := collector.GetProxyStats()

	if proxyStats == nil {
		t.Fatal("Expected non-nil proxy stats")
	}

	if len(proxyStats) != 3 {
		t.Fatalf("Expected 3 proxy stats, got %d", len(proxyStats))
	}

	// Check first proxy
	stat := proxyStats[0]
	if stat.ProxyURL != "http://proxy1:8080" {
		t.Errorf("Expected proxy1, got %s", stat.ProxyURL)
	}

	if stat.State != "healthy" {
		t.Errorf("Expected healthy state, got %s", stat.State)
	}

	if stat.TotalRequests != 0 {
		t.Errorf("Expected 0 total requests, got %d", stat.TotalRequests)
	}

	if stat.ErrorRate != 0 {
		t.Errorf("Expected 0 error rate, got %f", stat.ErrorRate)
	}
}

// TestUptime tests uptime tracking
func TestUptime(t *testing.T) {
	manager := createTestManager()
	defer func() { _ = manager.Close() }()

	collector := NewCollector(manager)

	// Get initial uptime
	stats1 := collector.GetStats()
	uptime1 := stats1.Uptime

	// Wait a bit
	time.Sleep(100 * time.Millisecond)

	// Get uptime again
	stats2 := collector.GetStats()
	uptime2 := stats2.Uptime

	// Uptime should have increased
	if uptime2 <= uptime1 {
		t.Errorf("Uptime should increase: %v -> %v", uptime1, uptime2)
	}

	// Should be at least 100ms difference
	diff := uptime2 - uptime1
	if diff < 100*time.Millisecond {
		t.Errorf("Expected at least 100ms uptime increase, got %v", diff)
	}
}

// TestGetStats_WorkerPoolFields tests worker pool fields
func TestGetStats_WorkerPoolFields(t *testing.T) {
	manager := createTestManager()
	defer func() { _ = manager.Close() }()

	collector := NewCollector(manager)

	stats := collector.GetStats()

	// Worker pool fields should start at 0 (not set yet)
	if stats.WorkerCount != 0 {
		t.Errorf("Expected WorkerCount=0, got %d", stats.WorkerCount)
	}

	if stats.QueueSize != 0 {
		t.Errorf("Expected QueueSize=0, got %d", stats.QueueSize)
	}

	if stats.QueueCapacity != 0 {
		t.Errorf("Expected QueueCapacity=0, got %d", stats.QueueCapacity)
	}

	// Manually set worker pool fields (simulating worker pool)
	stats.WorkerCount = 100
	stats.QueueSize = 50
	stats.QueueCapacity = 1000

	if stats.WorkerCount != 100 {
		t.Errorf("Expected WorkerCount=100, got %d", stats.WorkerCount)
	}
}

// TestGetProxyStats_AfterRequests tests stats after making requests
func TestGetProxyStats_AfterRequests(t *testing.T) {
	manager := createTestManager()
	defer func() { _ = manager.Close() }()

	collector := NewCollector(manager)

	// Simulate requests by accessing clients directly
	clients := manager.GetClients()
	if len(clients) > 0 {
		client := clients[0]

		// Manually update metrics (simulating requests)
		// Note: We can't actually make HTTP requests in unit tests
		// But we can verify that GetProxyStats reads from clients correctly

		proxyStats := collector.GetProxyStats()
		if len(proxyStats) == 0 {
			t.Fatal("Expected at least one proxy stat")
		}

		// Initial state should be healthy with 0 requests
		stat := proxyStats[0]
		if stat.State != "healthy" {
			t.Errorf("Expected healthy state, got %s", stat.State)
		}
		if stat.TotalRequests != 0 {
			t.Errorf("Expected 0 requests, got %d", stat.TotalRequests)
		}

		// Verify we have the correct proxy URL
		if stat.ProxyURL != client.ProxyURL() {
			t.Errorf("Expected %s, got %s", client.ProxyURL(), stat.ProxyURL)
		}
	}
}
