package ratelimit

import (
	"context"
	"testing"
	"time"
)

func TestNewDomainLimiter(t *testing.T) {
	// Test with nil config
	limiter := NewDomainLimiter(nil)
	if limiter != nil {
		t.Error("Expected nil limiter for nil config")
	}

	// Test with empty config
	limiter = NewDomainLimiter(map[string]int64{})
	if limiter != nil {
		t.Error("Expected nil limiter for empty config")
	}

	// Test with valid config
	limits := map[string]int64{
		"api.example.com": 10,
		"slow.example.com": 5,
	}
	limiter = NewDomainLimiter(limits)
	if limiter == nil {
		t.Fatal("Expected non-nil limiter")
	}

	// Check domains
	domains := limiter.GetDomains()
	if len(domains) != 2 {
		t.Errorf("Expected 2 domains, got %d", len(domains))
	}

	// Check has limit
	if !limiter.HasLimit("api.example.com") {
		t.Error("Expected HasLimit true for api.example.com")
	}
	if !limiter.HasLimit("slow.example.com") {
		t.Error("Expected HasLimit true for slow.example.com")
	}
	if limiter.HasLimit("unknown.com") {
		t.Error("Expected HasLimit false for unknown.com")
	}
}

func TestDomainLimiter_TryAcquire(t *testing.T) {
	limits := map[string]int64{
		"api.example.com": 5, // 5 RPS
	}
	limiter := NewDomainLimiter(limits)

	// Should allow initial burst (5 tokens available)
	for i := 0; i < 5; i++ {
		if !limiter.TryAcquire("api.example.com") {
			t.Errorf("Request %d should be allowed (burst)", i)
		}
	}

	// 6th request should be rate limited
	if limiter.TryAcquire("api.example.com") {
		t.Error("6th request should be rate limited")
	}

	// Unknown domain should always be allowed
	for i := 0; i < 10; i++ {
		if !limiter.TryAcquire("unknown.com") {
			t.Error("Unknown domain should not be rate limited")
		}
	}
}

func TestDomainLimiter_Acquire(t *testing.T) {
	limits := map[string]int64{
		"api.example.com": 10, // 10 RPS
	}
	limiter := NewDomainLimiter(limits)

	ctx := context.Background()

	// Should acquire immediately for burst
	for i := 0; i < 10; i++ {
		if err := limiter.Acquire(ctx, "api.example.com"); err != nil {
			t.Errorf("Request %d should succeed: %v", i, err)
		}
	}

	// Next request should block briefly, then succeed
	start := time.Now()
	if err := limiter.Acquire(ctx, "api.example.com"); err != nil {
		t.Errorf("Acquire should eventually succeed: %v", err)
	}
	elapsed := time.Since(start)

	// Should have waited at least 50ms (1/10 second for 10 RPS with some margin)
	if elapsed < 50*time.Millisecond {
		t.Errorf("Expected to wait at least 50ms, waited %v", elapsed)
	}
}

func TestDomainLimiter_Acquire_ContextCancellation(t *testing.T) {
	limits := map[string]int64{
		"api.example.com": 5, // 5 RPS
	}
	limiter := NewDomainLimiter(limits)

	// Exhaust tokens
	for i := 0; i < 5; i++ {
		limiter.TryAcquire("api.example.com")
	}

	// Create context with short timeout
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Millisecond)
	defer cancel()

	// Should fail with context error
	err := limiter.Acquire(ctx, "api.example.com")
	if err == nil {
		t.Error("Expected context timeout error")
	}
	if err != context.DeadlineExceeded {
		t.Errorf("Expected DeadlineExceeded, got %v", err)
	}
}

func TestDomainLimiter_NilLimiter(t *testing.T) {
	var limiter *DomainLimiter

	// All operations should be no-ops and not panic
	if limiter.TryAcquire("any.com") != true {
		t.Error("Nil limiter should always allow")
	}

	if err := limiter.Acquire(context.Background(), "any.com"); err != nil {
		t.Errorf("Nil limiter should always allow: %v", err)
	}

	if limiter.HasLimit("any.com") {
		t.Error("Nil limiter should report no limits")
	}

	if len(limiter.GetDomains()) != 0 {
		t.Error("Nil limiter should have no domains")
	}
}

func TestDomainLimiter_MultipleDomains(t *testing.T) {
	limits := map[string]int64{
		"fast.example.com": 100, // 100 RPS
		"slow.example.com": 5,   // 5 RPS
	}
	limiter := NewDomainLimiter(limits)

	// Fast domain should handle burst easily
	for i := 0; i < 100; i++ {
		if !limiter.TryAcquire("fast.example.com") {
			t.Errorf("Fast domain request %d should succeed", i)
		}
	}

	// Slow domain should be limited after 5
	for i := 0; i < 5; i++ {
		if !limiter.TryAcquire("slow.example.com") {
			t.Errorf("Slow domain request %d should succeed", i)
		}
	}
	if limiter.TryAcquire("slow.example.com") {
		t.Error("Slow domain 6th request should be rate limited")
	}

	// Domains should be independent - fast domain exhausted, but slow domain still limited
	if limiter.TryAcquire("fast.example.com") {
		t.Error("Fast domain should be exhausted after 100 requests")
	}

	// Slow domain should still be rate limited (not affected by fast domain)
	if limiter.TryAcquire("slow.example.com") {
		t.Error("Slow domain should still be rate limited (independent from fast domain)")
	}
}

func TestDomainLimiter_InvalidRates(t *testing.T) {
	limits := map[string]int64{
		"valid.com":   10,
		"zero.com":    0,  // Should be skipped
		"negative.com": -5, // Should be skipped
	}
	limiter := NewDomainLimiter(limits)

	// Only valid.com should have a limit
	if !limiter.HasLimit("valid.com") {
		t.Error("valid.com should have limit")
	}
	if limiter.HasLimit("zero.com") {
		t.Error("zero.com should not have limit")
	}
	if limiter.HasLimit("negative.com") {
		t.Error("negative.com should not have limit")
	}

	domains := limiter.GetDomains()
	if len(domains) != 1 {
		t.Errorf("Expected 1 valid domain, got %d", len(domains))
	}
}
