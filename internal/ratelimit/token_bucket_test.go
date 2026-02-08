package ratelimit

import (
	"context"
	"sync"
	"testing"
	"time"
)

// TestNewTokenBucket tests token bucket creation
func TestNewTokenBucket(t *testing.T) {
	tb := NewTokenBucket(10, 10)

	if tb.Capacity() != 10 {
		t.Errorf("Expected capacity 10, got %d", tb.Capacity())
	}

	if tb.RefillRate() != 10 {
		t.Errorf("Expected refill rate 10, got %d", tb.RefillRate())
	}

	// Should start with full capacity
	available := tb.Available()
	if available != 10 {
		t.Errorf("Expected 10 available tokens, got %d", available)
	}
}

// TestTryAcquire tests non-blocking token acquisition
func TestTryAcquire(t *testing.T) {
	tb := NewTokenBucket(5, 5)

	// Should be able to acquire 5 tokens
	for i := 0; i < 5; i++ {
		if !tb.TryAcquire() {
			t.Errorf("Failed to acquire token %d", i)
		}
	}

	// 6th attempt should fail (no tokens left)
	if tb.TryAcquire() {
		t.Error("Should not be able to acquire 6th token")
	}

	// Check available
	available := tb.Available()
	if available != 0 {
		t.Errorf("Expected 0 available tokens, got %d", available)
	}
}

// TestRefill tests token refill mechanism
func TestRefill(t *testing.T) {
	tb := NewTokenBucket(10, 10) // 10 tokens per second

	// Drain all tokens
	for i := 0; i < 10; i++ {
		tb.TryAcquire()
	}

	// Verify empty
	if tb.Available() != 0 {
		t.Errorf("Expected 0 available after drain, got %d", tb.Available())
	}

	// Wait 1 second for refill
	time.Sleep(1100 * time.Millisecond)

	// Should have refilled to 10 tokens
	available := tb.Available()
	if available < 9 || available > 10 {
		t.Errorf("Expected ~10 tokens after 1 second, got %d", available)
	}

	// Should be able to acquire again
	if !tb.TryAcquire() {
		t.Error("Should be able to acquire after refill")
	}
}

// TestPartialRefill tests partial refill
func TestPartialRefill(t *testing.T) {
	tb := NewTokenBucket(10, 10) // 10 tokens per second

	// Drain all
	for i := 0; i < 10; i++ {
		tb.TryAcquire()
	}

	// Wait 0.5 seconds (should refill ~5 tokens)
	time.Sleep(550 * time.Millisecond)

	available := tb.Available()
	if available < 4 || available > 6 {
		t.Errorf("Expected ~5 tokens after 0.5 seconds, got %d", available)
	}
}

// TestAcquire tests blocking acquisition
func TestAcquire(t *testing.T) {
	tb := NewTokenBucket(1, 1)

	// Acquire first token
	if !tb.TryAcquire() {
		t.Fatal("Failed to acquire first token")
	}

	// Blocking acquire should wait for refill
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	start := time.Now()
	err := tb.Acquire(ctx)
	elapsed := time.Since(start)

	if err != nil {
		t.Errorf("Acquire failed: %v", err)
	}

	// Should have waited ~1 second for refill
	if elapsed < 900*time.Millisecond || elapsed > 1500*time.Millisecond {
		t.Errorf("Expected ~1 second wait, got %v", elapsed)
	}
}

// TestAcquireTimeout tests context cancellation
func TestAcquireTimeout(t *testing.T) {
	tb := NewTokenBucket(1, 1)

	// Drain token
	tb.TryAcquire()

	// Try to acquire with very short timeout
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Millisecond)
	defer cancel()

	err := tb.Acquire(ctx)
	if err == nil {
		t.Error("Expected timeout error")
	}

	if err != context.DeadlineExceeded {
		t.Errorf("Expected DeadlineExceeded, got %v", err)
	}
}

// TestConcurrentAcquire tests concurrent token acquisition
func TestConcurrentAcquire(t *testing.T) {
	tb := NewTokenBucket(1000, 1000)

	var wg sync.WaitGroup
	numGoroutines := 100
	acquiresPerGoroutine := 10

	successCount := int64(0)
	var mu sync.Mutex

	for i := 0; i < numGoroutines; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < acquiresPerGoroutine; j++ {
				if tb.TryAcquire() {
					mu.Lock()
					successCount++
					mu.Unlock()
				}
			}
		}()
	}

	wg.Wait()

	// Should acquire up to capacity (1000)
	if successCount > 1000 {
		t.Errorf("Acquired more tokens than capacity: %d > 1000", successCount)
	}

	// Should acquire close to capacity
	if successCount < 900 {
		t.Errorf("Too few tokens acquired: %d < 900", successCount)
	}
}

// TestRateLimit tests actual rate limiting
func TestRateLimit(t *testing.T) {
	// 10 tokens per second
	tb := NewTokenBucket(10, 10)

	// Drain initial tokens
	for i := 0; i < 10; i++ {
		tb.TryAcquire()
	}

	start := time.Now()
	acquired := 0

	// Try to acquire 20 tokens (should take ~2 seconds as we start empty)
	for acquired < 20 {
		if tb.TryAcquire() {
			acquired++
		} else {
			time.Sleep(10 * time.Millisecond)
		}
	}

	elapsed := time.Since(start)

	// Should take roughly 2 seconds (10 tokens/sec * 2 sec = 20 tokens)
	if elapsed < 1800*time.Millisecond || elapsed > 2500*time.Millisecond {
		t.Errorf("Expected ~2 seconds, got %v", elapsed)
	}
}

// TestSetCapacity tests capacity updates
func TestSetCapacity(t *testing.T) {
	tb := NewTokenBucket(10, 10)

	// Increase capacity
	tb.SetCapacity(20)
	if tb.Capacity() != 20 {
		t.Errorf("Expected capacity 20, got %d", tb.Capacity())
	}

	// Decrease capacity below current tokens
	tb.SetCapacity(5)
	if tb.Capacity() != 5 {
		t.Errorf("Expected capacity 5, got %d", tb.Capacity())
	}

	// Available should be capped at new capacity
	available := tb.Available()
	if available > 5 {
		t.Errorf("Available (%d) exceeds capacity (5)", available)
	}
}

// TestSetRefillRate tests refill rate updates
func TestSetRefillRate(t *testing.T) {
	tb := NewTokenBucket(10, 10)

	tb.SetRefillRate(20)
	if tb.RefillRate() != 20 {
		t.Errorf("Expected refill rate 20, got %d", tb.RefillRate())
	}
}

// TestReset tests bucket reset
func TestReset(t *testing.T) {
	tb := NewTokenBucket(10, 10)

	// Drain some tokens
	for i := 0; i < 5; i++ {
		tb.TryAcquire()
	}

	// Reset
	tb.Reset()

	// Should be back to full capacity
	available := tb.Available()
	if available != 10 {
		t.Errorf("Expected 10 tokens after reset, got %d", available)
	}
}

// TestStats tests statistics collection
func TestStats(t *testing.T) {
	tb := NewTokenBucket(10, 5)

	// Acquire some tokens
	for i := 0; i < 7; i++ {
		tb.TryAcquire()
	}

	stats := tb.Stats()

	if stats.Capacity != 10 {
		t.Errorf("Expected capacity 10, got %d", stats.Capacity)
	}

	if stats.RefillRate != 5 {
		t.Errorf("Expected refill rate 5, got %d", stats.RefillRate)
	}

	if stats.Available != 3 {
		t.Errorf("Expected 3 available, got %d", stats.Available)
	}

	expectedUtilization := 0.7 // 7/10 consumed
	if stats.Utilization < expectedUtilization-0.1 || stats.Utilization > expectedUtilization+0.1 {
		t.Errorf("Expected utilization ~%.2f, got %.2f", expectedUtilization, stats.Utilization)
	}
}

// BenchmarkTryAcquire benchmarks non-blocking acquire
func BenchmarkTryAcquire(b *testing.B) {
	tb := NewTokenBucket(int64(b.N), int64(b.N))

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		tb.TryAcquire()
	}
}

// BenchmarkTryAcquireParallel benchmarks concurrent acquire
func BenchmarkTryAcquireParallel(b *testing.B) {
	tb := NewTokenBucket(1000000, 1000000)

	b.ResetTimer()
	b.RunParallel(func(pb *testing.PB) {
		for pb.Next() {
			tb.TryAcquire()
		}
	})
}

// BenchmarkAvailable benchmarks Available() call
func BenchmarkAvailable(b *testing.B) {
	tb := NewTokenBucket(100, 100)

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		tb.Available()
	}
}
