package ratelimit

import (
	"context"
	"sync"
)

// DomainLimiter manages rate limiters for multiple domains
type DomainLimiter struct {
	limiters map[string]*TokenBucket // domain -> rate limiter
	mu       sync.RWMutex
}

// NewDomainLimiter creates a new domain rate limiter manager
// domainLimits: map of domain -> RPS limit
func NewDomainLimiter(domainLimits map[string]int64) *DomainLimiter {
	if len(domainLimits) == 0 {
		return nil // No domain limits configured
	}

	dl := &DomainLimiter{
		limiters: make(map[string]*TokenBucket, len(domainLimits)),
	}

	// Create token bucket for each domain
	for domain, rps := range domainLimits {
		if rps <= 0 {
			continue // Skip invalid rates
		}
		dl.limiters[domain] = NewTokenBucket(rps, rps*2) // burst = 2x rate
	}

	return dl
}

// TryAcquire attempts to acquire token for domain (non-blocking)
// Returns true if token acquired, false if rate limit exceeded
func (dl *DomainLimiter) TryAcquire(domain string) bool {
	if dl == nil {
		return true // No domain limits configured
	}

	dl.mu.RLock()
	limiter, exists := dl.limiters[domain]
	dl.mu.RUnlock()

	if !exists {
		return true // No limit for this domain
	}

	return limiter.TryAcquire()
}

// Acquire waits for token to become available for domain (blocking with context)
// Returns error if context cancelled before token acquired
func (dl *DomainLimiter) Acquire(ctx context.Context, domain string) error {
	if dl == nil {
		return nil // No domain limits configured
	}

	dl.mu.RLock()
	limiter, exists := dl.limiters[domain]
	dl.mu.RUnlock()

	if !exists {
		return nil // No limit for this domain
	}

	return limiter.Acquire(ctx)
}

// GetLimiter returns the rate limiter for a specific domain (for testing/monitoring)
func (dl *DomainLimiter) GetLimiter(domain string) *TokenBucket {
	if dl == nil {
		return nil
	}

	dl.mu.RLock()
	defer dl.mu.RUnlock()
	return dl.limiters[domain]
}

// HasLimit checks if domain has a rate limit configured
func (dl *DomainLimiter) HasLimit(domain string) bool {
	if dl == nil {
		return false
	}

	dl.mu.RLock()
	defer dl.mu.RUnlock()
	_, exists := dl.limiters[domain]
	return exists
}

// GetDomains returns list of domains with rate limits configured
func (dl *DomainLimiter) GetDomains() []string {
	if dl == nil {
		return nil
	}

	dl.mu.RLock()
	defer dl.mu.RUnlock()

	domains := make([]string, 0, len(dl.limiters))
	for domain := range dl.limiters {
		domains = append(domains, domain)
	}
	return domains
}
