package ratelimit

import (
	"context"
	"sync/atomic"
	"time"
)

// TokenBucket implements lock-free token bucket rate limiter
type TokenBucket struct {
	// Configuration
	capacity   int64 // Maximum tokens
	refillRate int64 // Tokens per second

	// State (atomic for lock-free access)
	tokens     atomic.Int64 // Current available tokens
	lastRefill atomic.Int64 // Last refill timestamp (unix nano)
}

// NewTokenBucket creates new token bucket rate limiter
func NewTokenBucket(capacity, refillRate int64) *TokenBucket {
	if capacity <= 0 {
		capacity = 10 // Default to 10 RPS
	}
	if refillRate <= 0 {
		refillRate = capacity
	}

	tb := &TokenBucket{
		capacity:   capacity,
		refillRate: refillRate,
	}

	// Initialize with full capacity
	tb.tokens.Store(capacity)
	tb.lastRefill.Store(time.Now().UnixNano())

	return tb
}

// TryAcquire attempts to acquire a token without blocking
// Returns true if token was acquired, false otherwise
// This is lock-free and uses atomic compare-and-swap
func (tb *TokenBucket) TryAcquire() bool {
	// Refill tokens if needed
	tb.maybeRefill()

	// Try to acquire token (atomic CAS loop)
	for {
		current := tb.tokens.Load()
		if current <= 0 {
			return false // No tokens available
		}

		// Try to decrement token count
		if tb.tokens.CompareAndSwap(current, current-1) {
			return true // Successfully acquired token
		}

		// CAS failed, retry (another goroutine got the token first)
	}
}

// Acquire acquires a token, blocking if necessary until one is available
// Returns error if context is cancelled
func (tb *TokenBucket) Acquire(ctx context.Context) error {
	for {
		if tb.TryAcquire() {
			return nil
		}

		// Wait a bit before retrying
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(10 * time.Millisecond):
			// Retry
		}
	}
}

// AcquireWithTimeout acquires a token with timeout
// Returns true if acquired, false if timeout
func (tb *TokenBucket) AcquireWithTimeout(timeout time.Duration) bool {
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()

	return tb.Acquire(ctx) == nil
}

// maybeRefill refills tokens based on elapsed time
// This is called automatically before each TryAcquire
func (tb *TokenBucket) maybeRefill() {
	now := time.Now().UnixNano()
	last := tb.lastRefill.Load()

	elapsed := now - last
	if elapsed <= 0 {
		return // No time elapsed
	}

	// Calculate new tokens based on elapsed time
	// tokens = (elapsed_seconds * refillRate)
	const nanosPerSec = int64(time.Second)
	newTokens := (elapsed * tb.refillRate) / nanosPerSec

	if newTokens <= 0 {
		return // Not enough time elapsed to add tokens
	}

	// Try to update lastRefill and add tokens atomically
	if !tb.lastRefill.CompareAndSwap(last, now) {
		// Another goroutine updated lastRefill, they will handle refill
		return
	}

	// Add tokens (up to capacity)
	for {
		current := tb.tokens.Load()
		newCount := current + newTokens
		if newCount > tb.capacity {
			newCount = tb.capacity
		}

		if tb.tokens.CompareAndSwap(current, newCount) {
			break // Successfully updated
		}
		// Retry if CAS failed
	}
}

// Available returns current number of available tokens
func (tb *TokenBucket) Available() int64 {
	tb.maybeRefill()
	return tb.tokens.Load()
}

// Capacity returns maximum token capacity
func (tb *TokenBucket) Capacity() int64 {
	return tb.capacity
}

// RefillRate returns refill rate (tokens per second)
func (tb *TokenBucket) RefillRate() int64 {
	return tb.refillRate
}

// Reset resets the token bucket to full capacity
func (tb *TokenBucket) Reset() {
	tb.tokens.Store(tb.capacity)
	tb.lastRefill.Store(time.Now().UnixNano())
}

// SetCapacity updates capacity
func (tb *TokenBucket) SetCapacity(capacity int64) {
	if capacity <= 0 {
		return
	}
	tb.capacity = capacity

	// Adjust current tokens if exceeds new capacity
	for {
		current := tb.tokens.Load()
		if current <= capacity {
			break
		}
		if tb.tokens.CompareAndSwap(current, capacity) {
			break
		}
	}
}

// SetRefillRate updates refill rate
func (tb *TokenBucket) SetRefillRate(rate int64) {
	if rate <= 0 {
		return
	}
	tb.refillRate = rate
}

// Stats returns token bucket statistics
func (tb *TokenBucket) Stats() TokenBucketStats {
	available := tb.Available()
	utilization := 1.0 - (float64(available) / float64(tb.capacity))

	return TokenBucketStats{
		Capacity:    tb.capacity,
		RefillRate:  tb.refillRate,
		Available:   available,
		Utilization: utilization,
		LastRefill:  time.Unix(0, tb.lastRefill.Load()),
	}
}

// TokenBucketStats contains token bucket statistics
type TokenBucketStats struct {
	Capacity    int64
	RefillRate  int64
	Available   int64
	Utilization float64   // 0.0 = empty, 1.0 = full
	LastRefill  time.Time
}
