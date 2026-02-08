package selector

import (
	"context"
	"errors"
	"sync/atomic"

	"github.com/Q1rD/rapid-proxy/internal/connection"
)

// Errors
var (
	ErrAllProxiesBusy = errors.New("all proxies are busy or unhealthy")
	ErrNoProxies      = errors.New("no proxies available")
)

// ProxySelector defines the interface for proxy selection strategies
type ProxySelector interface {
	Select(ctx context.Context) (*connection.ProxyClient, error)
	GetStats() SelectorStats
}

// SelectorStats contains selector statistics
type SelectorStats struct {
	TotalSelections   int64
	SuccessSelections int64
	FailedSelections  int64
	SuccessRate       float64
}

// Config contains selector configuration
type Config struct {
	// MaxRetries is the maximum number of proxies to try before giving up
	// Default: len(clients) (try all proxies once)
	MaxRetries int
}

// DefaultConfig returns default selector configuration
func DefaultConfig() *Config {
	return &Config{
		MaxRetries: 0, // Will be set to len(clients) if 0
	}
}

// RoundRobinSelector implements health-aware round-robin proxy selection
type RoundRobinSelector struct {
	clients []*connection.ProxyClient

	// Lock-free round-robin counter
	currentIndex atomic.Uint64

	// Configuration
	maxRetries int

	// Metrics (atomic for thread-safety)
	totalSelections   atomic.Int64
	successSelections atomic.Int64
	failedSelections  atomic.Int64
}

// NewRoundRobinSelector creates a new round-robin selector
func NewRoundRobinSelector(clients []*connection.ProxyClient, config *Config) (*RoundRobinSelector, error) {
	if len(clients) == 0 {
		return nil, ErrNoProxies
	}

	if config == nil {
		config = DefaultConfig()
	}

	maxRetries := config.MaxRetries
	if maxRetries == 0 {
		maxRetries = len(clients)
	}

	return &RoundRobinSelector{
		clients:    clients,
		maxRetries: maxRetries,
	}, nil
}

// Select selects the next available healthy proxy with rate limit checking
// It performs a health-aware round-robin selection:
// 1. Starts at next index in rotation
// 2. Checks circuit breaker health
// 3. Checks rate limiter availability
// 4. Returns first available proxy or ErrAllProxiesBusy
func (s *RoundRobinSelector) Select(ctx context.Context) (*connection.ProxyClient, error) {
	s.totalSelections.Add(1)

	// Get starting index for round-robin
	startIdx := s.currentIndex.Add(1) % uint64(len(s.clients))

	// Try each proxy once (full rotation)
	for i := 0; i < s.maxRetries; i++ {
		// Check context cancellation
		select {
		case <-ctx.Done():
			s.failedSelections.Add(1)
			return nil, ctx.Err()
		default:
		}

		// Calculate current proxy index
		idx := (startIdx + uint64(i)) % uint64(len(s.clients))
		client := s.clients[idx]

		// Check 1: Circuit breaker health
		if client.GetCircuitBreaker() != nil {
			if !client.GetCircuitBreaker().IsHealthy() {
				continue // Skip unhealthy proxy
			}
		}

		// Check 2: Rate limit (non-blocking)
		if client.GetRateLimiter() != nil {
			if !client.GetRateLimiter().TryAcquire() {
				continue // Skip rate-limited proxy
			}
		}

		// Found available proxy
		s.successSelections.Add(1)
		return client, nil
	}

	// All proxies are busy or unhealthy
	s.failedSelections.Add(1)
	return nil, ErrAllProxiesBusy
}

// GetStats returns current selector statistics
func (s *RoundRobinSelector) GetStats() SelectorStats {
	total := s.totalSelections.Load()
	success := s.successSelections.Load()
	failed := s.failedSelections.Load()

	var successRate float64
	if total > 0 {
		successRate = float64(success) / float64(total)
	}

	return SelectorStats{
		TotalSelections:   total,
		SuccessSelections: success,
		FailedSelections:  failed,
		SuccessRate:       successRate,
	}
}
