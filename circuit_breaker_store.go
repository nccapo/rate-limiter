package rrl

import (
	"context"
	"sync"
	"time"
)

// State represents the state of the circuit breaker.
type State int

const (
	StateClosed State = iota
	StateOpen
	StateHalfOpen
)

// CircuitBreakerConfig holds configuration for the circuit breaker.
type CircuitBreakerConfig struct {
	// Threshold is the number of consecutive errors to open the circuit.
	Threshold int64
	// Timeout is the duration to wait before trying to close the circuit (Half-Open).
	Timeout time.Duration
	// FallbackAllow determines if we should allow requests (true) or block them (false) when Open.
	// Default: false (Block/Fail Fast).
	FallbackAllow bool
}

// CircuitBreakerStore wraps another Store and prevents calls when the underlying Store fails repeatedly.
type CircuitBreakerStore struct {
	backend Store
	config  CircuitBreakerConfig

	mu          sync.Mutex
	state       State
	failures    int64
	lastFailure time.Time
	probing     bool // Ensures only one request probes in Half-Open state

	// timeNow allows mocking time for tests
	timeNow func() time.Time
}

// NewCircuitBreakerStore creates a new CircuitBreakerStore.
func NewCircuitBreakerStore(backend Store, config CircuitBreakerConfig) *CircuitBreakerStore {
	if config.Threshold <= 0 {
		config.Threshold = 5
	}
	if config.Timeout <= 0 {
		config.Timeout = 5 * time.Second
	}

	return &CircuitBreakerStore{
		backend: backend,
		config:  config,
		state:   StateClosed,
		timeNow: time.Now,
	}
}

// Allow delegates to the backend store, implementing the Circuit Breaker logic.
func (s *CircuitBreakerStore) Allow(ctx context.Context, key string, cost int64, maxTokens int64, refillInterval time.Duration) (bool, int64, time.Duration, error) {
	s.mu.Lock()

	// Check timeout to switch Open -> HalfOpen
	if s.state == StateOpen {
		if s.timeNow().Sub(s.lastFailure) > s.config.Timeout {
			s.state = StateHalfOpen
		}
	}

	state := s.state
	shouldProbe := false

	if state == StateHalfOpen {
		if !s.probing {
			s.probing = true
			shouldProbe = true
		}
	}
	s.mu.Unlock()

	// 1. If we are the chosen prober (Half-Open)
	if shouldProbe {
		return s.attemptProbe(ctx, key, cost, maxTokens, refillInterval)
	}

	// 2. Closed State -> Normal operation
	if state == StateClosed {
		return s.attemptRequest(ctx, key, cost, maxTokens, refillInterval)
	}

	// 3. Open or Half-Open (but not probing) -> Fail Fast
	if s.config.FallbackAllow {
		return true, maxTokens, 0, nil
	}
	// Do NOT return error, just return allowed=false with RetryAfter = timeout
	// This ensures Wait() can backoff and Middleware returns 429 instead of 500.
	return false, 0, s.config.Timeout, nil
}

// attemptRequest is for StateClosed (Normal)
func (s *CircuitBreakerStore) attemptRequest(ctx context.Context, key string, cost int64, maxTokens int64, refillInterval time.Duration) (bool, int64, time.Duration, error) {
	allowed, remaining, retryAfter, err := s.backend.Allow(ctx, key, cost, maxTokens, refillInterval)

	s.mu.Lock()
	defer s.mu.Unlock()

	if err != nil {
		s.recordFailure()
		return allowed, remaining, retryAfter, err
	}

	// Success
	s.reset()
	return allowed, remaining, retryAfter, nil
}

// attemptProbe is for StateHalfOpen
func (s *CircuitBreakerStore) attemptProbe(ctx context.Context, key string, cost int64, maxTokens int64, refillInterval time.Duration) (bool, int64, time.Duration, error) {
	// Ensure we reset probing state even if panic occurs
	defer func() {
		s.mu.Lock()
		s.probing = false
		s.mu.Unlock()
	}()

	allowed, remaining, retryAfter, err := s.backend.Allow(ctx, key, cost, maxTokens, refillInterval)

	s.mu.Lock()
	defer s.mu.Unlock()

	if err != nil {
		// Probe failed -> Re-Open
		s.state = StateOpen
		s.lastFailure = s.timeNow() // Reset timeout
		return allowed, remaining, retryAfter, err
	}

	// Probe success -> Close
	s.state = StateClosed
	s.failures = 0
	return allowed, remaining, retryAfter, nil
}

func (s *CircuitBreakerStore) recordFailure() {
	s.failures++
	s.lastFailure = s.timeNow()
	if s.failures >= s.config.Threshold {
		s.state = StateOpen
	}
}

func (s *CircuitBreakerStore) reset() {
	s.failures = 0
	s.state = StateClosed
}
