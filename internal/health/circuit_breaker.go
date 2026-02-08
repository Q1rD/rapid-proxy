package health

import (
	"sync/atomic"
	"time"

	"github.com/Q1rD/rapid-proxy/internal/ratelimit"
)

// State constants for circuit breaker
const (
	StateHealthy  int32 = 0
	StateDegraded int32 = 1
	StateDead     int32 = 2
)

// CircuitBreaker implements circuit breaker pattern for proxy health monitoring
type CircuitBreaker struct {
	// State
	state atomic.Int32

	// Sliding windows for metrics
	requests *ratelimit.SlidingWindow
	failures *ratelimit.SlidingWindow

	// Configuration
	failureThreshold  float64       // Error rate threshold to trip (e.g., 0.5 = 50%)
	degradedThreshold float64       // Error rate for degraded state (e.g., 0.2 = 20%)
	minRequests       int64         // Minimum requests before tripping
	recoveryTimeout   time.Duration // Time before attempting recovery
	windowSize        time.Duration // Size of sliding window for metrics

	// State transitions
	lastStateChange atomic.Int64 // Unix nano timestamp
	lastFailure     atomic.Int64 // Unix nano timestamp
	lastSuccess     atomic.Int64 // Unix nano timestamp
}

// Config contains circuit breaker configuration
type Config struct {
	FailureThreshold  float64       // Default: 0.5 (50% error rate)
	DegradedThreshold float64       // Default: 0.2 (20% error rate)
	MinRequests       int64         // Default: 10
	RecoveryTimeout   time.Duration // Default: 60 seconds
	WindowSize        time.Duration // Default: 60 seconds
}

// DefaultConfig returns default circuit breaker configuration
func DefaultConfig() *Config {
	return &Config{
		FailureThreshold:  0.5,
		DegradedThreshold: 0.2,
		MinRequests:       10,
		RecoveryTimeout:   60 * time.Second,
		WindowSize:        60 * time.Second,
	}
}

// NewCircuitBreaker creates new circuit breaker
func NewCircuitBreaker(config *Config) *CircuitBreaker {
	if config == nil {
		config = DefaultConfig()
	}

	cb := &CircuitBreaker{
		failureThreshold:  config.FailureThreshold,
		degradedThreshold: config.DegradedThreshold,
		minRequests:       config.MinRequests,
		recoveryTimeout:   config.RecoveryTimeout,
		windowSize:        config.WindowSize,
		requests:          ratelimit.NewSlidingWindow(config.WindowSize, time.Second),
		failures:          ratelimit.NewSlidingWindow(config.WindowSize, time.Second),
	}

	// Initialize state
	cb.state.Store(StateHealthy)
	cb.lastStateChange.Store(time.Now().UnixNano())
	cb.lastSuccess.Store(time.Now().UnixNano())

	return cb
}

// RecordSuccess records successful request
func (cb *CircuitBreaker) RecordSuccess() {
	cb.requests.Increment()
	cb.lastSuccess.Store(time.Now().UnixNano())

	// Check if should recover to healthy state
	if cb.shouldRecover() {
		cb.transitionTo(StateHealthy)
	}
}

// RecordFailure records failed request
func (cb *CircuitBreaker) RecordFailure(err error) {
	cb.requests.Increment()
	cb.failures.Increment()
	cb.lastFailure.Store(time.Now().UnixNano())

	// Check if should trip
	if cb.shouldTrip() {
		cb.transitionTo(StateDead)
	} else if cb.shouldDegrade() {
		cb.transitionTo(StateDegraded)
	}
}

// IsHealthy returns true if circuit breaker is in healthy or degraded state
func (cb *CircuitBreaker) IsHealthy() bool {
	state := cb.GetState()

	// If dead, check if recovery timeout has passed
	if state == StateDead {
		return cb.canAttemptRecovery()
	}

	return state == StateHealthy || state == StateDegraded
}

// GetState returns current state
func (cb *CircuitBreaker) GetState() int32 {
	return cb.state.Load()
}

// GetStateName returns human-readable state name
func (cb *CircuitBreaker) GetStateName() string {
	switch cb.GetState() {
	case StateHealthy:
		return "healthy"
	case StateDegraded:
		return "degraded"
	case StateDead:
		return "dead"
	default:
		return "unknown"
	}
}

// GetErrorRate returns current error rate (0.0 to 1.0)
func (cb *CircuitBreaker) GetErrorRate() float64 {
	totalRequests := cb.requests.Count()
	if totalRequests == 0 {
		return 0
	}

	totalFailures := cb.failures.Count()
	return float64(totalFailures) / float64(totalRequests)
}

// shouldTrip checks if circuit breaker should trip to dead state
func (cb *CircuitBreaker) shouldTrip() bool {
	totalRequests := cb.requests.Count()

	// Need minimum requests before tripping
	if totalRequests < cb.minRequests {
		return false
	}

	errorRate := cb.GetErrorRate()
	return errorRate >= cb.failureThreshold
}

// shouldDegrade checks if circuit breaker should move to degraded state
func (cb *CircuitBreaker) shouldDegrade() bool {
	state := cb.GetState()
	if state != StateHealthy {
		return false // Only transition from healthy to degraded
	}

	totalRequests := cb.requests.Count()
	if totalRequests < cb.minRequests {
		return false
	}

	errorRate := cb.GetErrorRate()
	return errorRate >= cb.degradedThreshold && errorRate < cb.failureThreshold
}

// shouldRecover checks if circuit breaker should recover to healthy state
func (cb *CircuitBreaker) shouldRecover() bool {
	state := cb.GetState()
	if state == StateHealthy {
		return false // Already healthy
	}

	// Check if error rate is below degraded threshold
	errorRate := cb.GetErrorRate()
	if errorRate > cb.degradedThreshold {
		return false
	}

	// Need some successful requests to recover
	totalRequests := cb.requests.Count()
	return totalRequests >= cb.minRequests/2
}

// canAttemptRecovery checks if enough time has passed for recovery attempt
func (cb *CircuitBreaker) canAttemptRecovery() bool {
	lastChange := time.Unix(0, cb.lastStateChange.Load())
	return time.Since(lastChange) > cb.recoveryTimeout
}

// transitionTo transitions to new state
func (cb *CircuitBreaker) transitionTo(newState int32) {
	oldState := cb.state.Swap(newState)

	if oldState != newState {
		cb.lastStateChange.Store(time.Now().UnixNano())

		// Reset metrics on recovery
		if newState == StateHealthy {
			cb.Reset()
		}
	}
}

// Reset resets circuit breaker metrics
func (cb *CircuitBreaker) Reset() {
	cb.requests.Reset()
	cb.failures.Reset()
}

// GetStats returns circuit breaker statistics
func (cb *CircuitBreaker) GetStats() CircuitBreakerStats {
	totalRequests := cb.requests.Count()
	totalFailures := cb.failures.Count()
	errorRate := cb.GetErrorRate()

	return CircuitBreakerStats{
		State:           cb.GetStateName(),
		TotalRequests:   totalRequests,
		TotalFailures:   totalFailures,
		ErrorRate:       errorRate,
		LastSuccess:     time.Unix(0, cb.lastSuccess.Load()),
		LastFailure:     time.Unix(0, cb.lastFailure.Load()),
		LastStateChange: time.Unix(0, cb.lastStateChange.Load()),
		WindowSize:      cb.windowSize,
	}
}

// CircuitBreakerStats contains circuit breaker statistics
type CircuitBreakerStats struct {
	State           string
	TotalRequests   int64
	TotalFailures   int64
	ErrorRate       float64
	LastSuccess     time.Time
	LastFailure     time.Time
	LastStateChange time.Time
	WindowSize      time.Duration
}
