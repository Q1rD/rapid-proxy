package connection

import (
	"context"
	"fmt"
	"net/url"
	"sync"
	"time"
)

// Config contains connection pool manager configuration
type Config struct {
	// Proxy URLs
	ProxyURLs []string

	// Transport configuration
	TransportConfig *TransportConfig

	// HTTP client timeout
	RequestTimeout time.Duration

	// Cleanup
	IdleCleanupInterval time.Duration
	IdleCleanupTimeout  time.Duration
}

// DefaultConfig returns default configuration
func DefaultConfig() *Config {
	return &Config{
		TransportConfig:     DefaultTransportConfig(),
		RequestTimeout:      30 * time.Second,
		IdleCleanupInterval: 2 * time.Minute,
		IdleCleanupTimeout:  90 * time.Second,
	}
}

// Manager manages pool of proxy clients
type Manager struct {
	config *Config

	// Proxy clients (singleton HTTP clients)
	clients    []*ProxyClient
	clientsMap map[string]*ProxyClient // for fast lookup by URL

	// Cleanup
	cleanupTicker *time.Ticker
	cleanupDone   chan struct{}
	wg            sync.WaitGroup

	// Lifecycle
	ctx    context.Context
	cancel context.CancelFunc
}

// NewManager creates new connection pool manager
func NewManager(config *Config) (*Manager, error) {
	if config == nil {
		config = DefaultConfig()
	}

	if len(config.ProxyURLs) == 0 {
		return nil, fmt.Errorf("no proxy URLs provided")
	}

	ctx, cancel := context.WithCancel(context.Background())

	mgr := &Manager{
		config:      config,
		clientsMap:  make(map[string]*ProxyClient),
		cleanupDone: make(chan struct{}),
		ctx:         ctx,
		cancel:      cancel,
	}

	// Initialize proxy clients
	if err := mgr.initClients(); err != nil {
		cancel()
		return nil, err
	}

	// Start background cleanup
	mgr.startCleanup()

	return mgr, nil
}

// initClients creates singleton HTTP client for each proxy
func (m *Manager) initClients() error {
	m.clients = make([]*ProxyClient, 0, len(m.config.ProxyURLs))

	for _, proxyURLStr := range m.config.ProxyURLs {
		// Parse proxy URL
		proxyURL, err := url.Parse(proxyURLStr)
		if err != nil {
			return fmt.Errorf("invalid proxy URL %s: %w", proxyURLStr, err)
		}

		// Create optimized transport (singleton)
		transport := NewTransport(proxyURL, m.config.TransportConfig)

		// Create proxy client with singleton HTTP client
		client := NewProxyClient(
			proxyURLStr,
			transport,
			m.config.RequestTimeout,
		)

		m.clients = append(m.clients, client)
		m.clientsMap[proxyURLStr] = client
	}

	return nil
}

// startCleanup starts background cleanup goroutine
func (m *Manager) startCleanup() {
	if m.config.IdleCleanupInterval <= 0 {
		return // Cleanup disabled
	}

	m.cleanupTicker = time.NewTicker(m.config.IdleCleanupInterval)

	m.wg.Add(1)
	go m.cleanupLoop()
}

// cleanupLoop periodically cleans up idle connections
func (m *Manager) cleanupLoop() {
	defer m.wg.Done()

	for {
		select {
		case <-m.cleanupTicker.C:
			m.cleanup()

		case <-m.cleanupDone:
			return

		case <-m.ctx.Done():
			return
		}
	}
}

// cleanup closes idle connections that exceed timeout
// Note: We don't aggressively close connections - only truly idle ones
func (m *Manager) cleanup() {
	now := time.Now()

	for _, client := range m.clients {
		// Only cleanup if proxy hasn't been used recently
		lastUse := time.Unix(0, client.lastSuccess.Load())
		if now.Sub(lastUse) > m.config.IdleCleanupTimeout {
			// This is safe - it only closes truly idle connections
			// Active connections are not affected
			client.transport.CloseIdleConnections()
		}
	}
}

// GetClients returns all proxy clients
func (m *Manager) GetClients() []*ProxyClient {
	return m.clients
}

// GetClient returns client by proxy URL
func (m *Manager) GetClient(proxyURL string) (*ProxyClient, bool) {
	client, ok := m.clientsMap[proxyURL]
	return client, ok
}

// GetClientCount returns number of proxy clients
func (m *Manager) GetClientCount() int {
	return len(m.clients)
}

// GetHealthyCount returns number of healthy clients
func (m *Manager) GetHealthyCount() int {
	count := 0
	for _, client := range m.clients {
		if client.GetState() == StateHealthy {
			count++
		}
	}
	return count
}

// GetDegradedCount returns number of degraded clients
func (m *Manager) GetDegradedCount() int {
	count := 0
	for _, client := range m.clients {
		if client.GetState() == StateDegraded {
			count++
		}
	}
	return count
}

// GetDeadCount returns number of dead clients
func (m *Manager) GetDeadCount() int {
	count := 0
	for _, client := range m.clients {
		if client.GetState() == StateDead {
			count++
		}
	}
	return count
}

// GetMetrics returns metrics for all proxy clients
func (m *Manager) GetMetrics() []ProxyMetrics {
	metrics := make([]ProxyMetrics, len(m.clients))
	for i, client := range m.clients {
		metrics[i] = client.GetMetrics()
	}
	return metrics
}

// GetStats returns manager statistics
func (m *Manager) GetStats() ManagerStats {
	return ManagerStats{
		TotalProxies:    len(m.clients),
		HealthyProxies:  m.GetHealthyCount(),
		DegradedProxies: m.GetDegradedCount(),
		DeadProxies:     m.GetDeadCount(),
	}
}

// ManagerStats contains manager statistics
type ManagerStats struct {
	TotalProxies    int
	HealthyProxies  int
	DegradedProxies int
	DeadProxies     int
}

// Close gracefully shuts down manager
func (m *Manager) Close() error {
	// Cancel context
	m.cancel()

	// Stop cleanup goroutine
	if m.cleanupTicker != nil {
		m.cleanupTicker.Stop()
		close(m.cleanupDone)
	}

	// Wait for cleanup goroutine to exit
	m.wg.Wait()

	// Cleanup all clients
	for _, client := range m.clients {
		client.Cleanup()
	}

	return nil
}

// SetRateLimiter sets rate limiter for a specific proxy client
func (m *Manager) SetRateLimiter(proxyURL string, limiter RateLimiter) error {
	client, ok := m.clientsMap[proxyURL]
	if !ok {
		return fmt.Errorf("proxy not found: %s", proxyURL)
	}
	client.rateLimiter = limiter
	return nil
}

// SetCircuitBreaker sets circuit breaker for a specific proxy client
func (m *Manager) SetCircuitBreaker(proxyURL string, cb CircuitBreaker) error {
	client, ok := m.clientsMap[proxyURL]
	if !ok {
		return fmt.Errorf("proxy not found: %s", proxyURL)
	}
	client.circuitBreaker = cb
	return nil
}

// SetRateLimiters sets rate limiters for all proxy clients
func (m *Manager) SetRateLimiters(limiters map[string]RateLimiter) {
	for proxyURL, limiter := range limiters {
		if client, ok := m.clientsMap[proxyURL]; ok {
			client.rateLimiter = limiter
		}
	}
}

// SetCircuitBreakers sets circuit breakers for all proxy clients
func (m *Manager) SetCircuitBreakers(cbs map[string]CircuitBreaker) {
	for proxyURL, cb := range cbs {
		if client, ok := m.clientsMap[proxyURL]; ok {
			client.circuitBreaker = cb
		}
	}
}
