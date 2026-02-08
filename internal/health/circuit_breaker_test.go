package health

import (
	"testing"
	"time"
)

// TestNewCircuitBreaker tests circuit breaker creation
func TestNewCircuitBreaker(t *testing.T) {
	cb := NewCircuitBreaker(nil) // Use default config

	if cb.GetState() != StateHealthy {
		t.Errorf("Expected initial state healthy, got %s", cb.GetStateName())
	}

	if !cb.IsHealthy() {
		t.Error("Expected IsHealthy() = true initially")
	}
}

// TestRecordSuccess tests success recording
func TestRecordSuccess(t *testing.T) {
	cb := NewCircuitBreaker(nil)

	// Record some successes
	for i := 0; i < 10; i++ {
		cb.RecordSuccess()
	}

	stats := cb.GetStats()
	if stats.TotalRequests != 10 {
		t.Errorf("Expected 10 total requests, got %d", stats.TotalRequests)
	}

	if stats.TotalFailures != 0 {
		t.Errorf("Expected 0 failures, got %d", stats.TotalFailures)
	}

	if stats.ErrorRate != 0 {
		t.Errorf("Expected 0%% error rate, got %.2f", stats.ErrorRate*100)
	}
}

// TestRecordFailure tests failure recording
func TestRecordFailure(t *testing.T) {
	cb := NewCircuitBreaker(nil)

	// Record some failures
	for i := 0; i < 5; i++ {
		cb.RecordFailure(nil)
	}

	stats := cb.GetStats()
	if stats.TotalRequests != 5 {
		t.Errorf("Expected 5 total requests, got %d", stats.TotalRequests)
	}

	if stats.TotalFailures != 5 {
		t.Errorf("Expected 5 failures, got %d", stats.TotalFailures)
	}

	if stats.ErrorRate != 1.0 {
		t.Errorf("Expected 100%% error rate, got %.2f", stats.ErrorRate*100)
	}
}

// TestTripToDead tests transition to dead state
func TestTripToDead(t *testing.T) {
	config := &Config{
		FailureThreshold:  0.5, // 50% error rate
		MinRequests:       10,
		RecoveryTimeout:   1 * time.Second,
		WindowSize:        60 * time.Second,
	}

	cb := NewCircuitBreaker(config)

	// Record 5 successes and 5 failures (50% error rate)
	for i := 0; i < 5; i++ {
		cb.RecordSuccess()
		cb.RecordFailure(nil)
	}

	// Should trip to dead state
	if cb.GetState() != StateDead {
		t.Errorf("Expected dead state, got %s", cb.GetStateName())
	}

	// IsHealthy should be false
	if cb.IsHealthy() {
		t.Error("Expected IsHealthy() = false after trip")
	}
}

// TestDegradedState tests degraded state transition
func TestDegradedState(t *testing.T) {
	config := &Config{
		FailureThreshold:  0.5,  // 50% to trip
		DegradedThreshold: 0.2,  // 20% to degrade
		MinRequests:       10,
		RecoveryTimeout:   1 * time.Second,
		WindowSize:        60 * time.Second,
	}

	cb := NewCircuitBreaker(config)

	// Record 8 successes and 2 failures (20% error rate)
	for i := 0; i < 8; i++ {
		cb.RecordSuccess()
	}
	for i := 0; i < 2; i++ {
		cb.RecordFailure(nil)
	}

	// Should be in degraded state
	if cb.GetState() != StateDegraded {
		t.Errorf("Expected degraded state, got %s", cb.GetStateName())
	}

	// Should still be healthy (usable)
	if !cb.IsHealthy() {
		t.Error("Expected IsHealthy() = true for degraded state")
	}
}

// TestRecovery tests recovery from dead state
func TestRecovery(t *testing.T) {
	config := &Config{
		FailureThreshold:  0.5,
		MinRequests:       10,
		RecoveryTimeout:   500 * time.Millisecond,
		WindowSize:        10 * time.Second,
	}

	cb := NewCircuitBreaker(config)

	// Trip to dead state
	for i := 0; i < 10; i++ {
		cb.RecordFailure(nil)
	}

	if cb.GetState() != StateDead {
		t.Fatal("Circuit breaker should be dead")
	}

	// Should not be healthy immediately
	if cb.IsHealthy() {
		t.Error("Should not be healthy immediately after trip")
	}

	// Wait for recovery timeout
	time.Sleep(600 * time.Millisecond)

	// Should allow recovery attempt now
	if !cb.IsHealthy() {
		t.Error("Should allow recovery attempt after timeout")
	}

	// Record some successes to trigger recovery
	cb.Reset() // Reset metrics for clean recovery
	for i := 0; i < 10; i++ {
		cb.RecordSuccess()
	}

	// Should recover to healthy
	if cb.GetState() != StateHealthy {
		t.Errorf("Expected healthy state after recovery, got %s", cb.GetStateName())
	}
}

// TestMinRequests tests that circuit breaker doesn't trip with too few requests
func TestMinRequests(t *testing.T) {
	config := &Config{
		FailureThreshold:  0.5,
		MinRequests:       10,
		RecoveryTimeout:   1 * time.Second,
		WindowSize:        60 * time.Second,
	}

	cb := NewCircuitBreaker(config)

	// Record only 4 failures (100% error rate but below minRequests)
	for i := 0; i < 4; i++ {
		cb.RecordFailure(nil)
	}

	// Should NOT trip (not enough requests)
	if cb.GetState() != StateHealthy {
		t.Errorf("Should not trip with too few requests, got state %s", cb.GetStateName())
	}
}

// TestErrorRate tests error rate calculation
func TestErrorRate(t *testing.T) {
	cb := NewCircuitBreaker(nil)

	// Record 7 successes and 3 failures (30% error rate)
	for i := 0; i < 7; i++ {
		cb.RecordSuccess()
	}
	for i := 0; i < 3; i++ {
		cb.RecordFailure(nil)
	}

	errorRate := cb.GetErrorRate()
	expected := 0.3 // 3/10

	if errorRate < expected-0.01 || errorRate > expected+0.01 {
		t.Errorf("Expected error rate ~%.2f, got %.2f", expected, errorRate)
	}
}

// TestStateTransitions tests all state transitions
func TestStateTransitions(t *testing.T) {
	config := &Config{
		FailureThreshold:  0.5,
		DegradedThreshold: 0.2,
		MinRequests:       10,
		RecoveryTimeout:   100 * time.Millisecond,
		WindowSize:        10 * time.Second,
	}

	cb := NewCircuitBreaker(config)

	// 1. Start: Healthy
	if cb.GetState() != StateHealthy {
		t.Error("Should start healthy")
	}

	// 2. Transition: Healthy -> Degraded (20-50% error)
	for i := 0; i < 8; i++ {
		cb.RecordSuccess()
	}
	for i := 0; i < 2; i++ {
		cb.RecordFailure(nil)
	}

	if cb.GetState() != StateDegraded {
		t.Errorf("Should be degraded, got %s", cb.GetStateName())
	}

	// 3. Transition: Degraded -> Dead (>50% error)
	// Total now: 10 (8 success + 2 failures)
	// Need to add enough failures to reach >50%
	// Adding 9 more failures: 2+9=11 failures, 8 success = 11/19 = 58% > 50%
	for i := 0; i < 9; i++ {
		cb.RecordFailure(nil)
	}

	if cb.GetState() != StateDead {
		t.Errorf("Should be dead, got %s (error rate: %.2f)", cb.GetStateName(), cb.GetErrorRate())
	}

	// 4. Wait for recovery timeout
	time.Sleep(150 * time.Millisecond)

	// 5. Transition: Dead -> Healthy (successful requests + timeout)
	cb.Reset()
	for i := 0; i < 10; i++ {
		cb.RecordSuccess()
	}

	if cb.GetState() != StateHealthy {
		t.Errorf("Should recover to healthy, got %s", cb.GetStateName())
	}
}

// TestConcurrentAccess tests concurrent access to circuit breaker
func TestConcurrentAccess(t *testing.T) {
	cb := NewCircuitBreaker(nil)

	// Concurrent success and failure recording
	done := make(chan bool)

	go func() {
		for i := 0; i < 100; i++ {
			cb.RecordSuccess()
		}
		done <- true
	}()

	go func() {
		for i := 0; i < 100; i++ {
			cb.RecordFailure(nil)
		}
		done <- true
	}()

	go func() {
		for i := 0; i < 100; i++ {
			_ = cb.GetErrorRate()
			_ = cb.IsHealthy()
		}
		done <- true
	}()

	// Wait for all goroutines
	<-done
	<-done
	<-done

	// Verify no corruption
	stats := cb.GetStats()
	if stats.TotalRequests != 200 {
		t.Errorf("Expected 200 total requests, got %d", stats.TotalRequests)
	}
}

// TestReset tests metrics reset
func TestReset(t *testing.T) {
	cb := NewCircuitBreaker(nil)

	// Record some data
	for i := 0; i < 10; i++ {
		cb.RecordSuccess()
		cb.RecordFailure(nil)
	}

	// Verify data exists
	stats := cb.GetStats()
	if stats.TotalRequests == 0 {
		t.Error("Should have requests before reset")
	}

	// Reset
	cb.Reset()

	// Verify data cleared
	stats = cb.GetStats()
	if stats.TotalRequests != 0 {
		t.Errorf("Expected 0 requests after reset, got %d", stats.TotalRequests)
	}
	if stats.TotalFailures != 0 {
		t.Errorf("Expected 0 failures after reset, got %d", stats.TotalFailures)
	}
}

// BenchmarkRecordSuccess benchmarks success recording
func BenchmarkRecordSuccess(b *testing.B) {
	cb := NewCircuitBreaker(nil)

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		cb.RecordSuccess()
	}
}

// BenchmarkRecordFailure benchmarks failure recording
func BenchmarkRecordFailure(b *testing.B) {
	cb := NewCircuitBreaker(nil)

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		cb.RecordFailure(nil)
	}
}

// BenchmarkIsHealthy benchmarks health check
func BenchmarkIsHealthy(b *testing.B) {
	cb := NewCircuitBreaker(nil)

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = cb.IsHealthy()
	}
}

// BenchmarkGetErrorRate benchmarks error rate calculation
func BenchmarkGetErrorRate(b *testing.B) {
	cb := NewCircuitBreaker(nil)

	// Add some data
	for i := 0; i < 100; i++ {
		cb.RecordSuccess()
		cb.RecordFailure(nil)
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = cb.GetErrorRate()
	}
}
