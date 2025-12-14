package rrl

import (
	"context"
	"errors"
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
	state := s.state

	// Check if we need to switch from Open to Half-Open
	if state == StateOpen {
		if s.timeNow().Sub(s.lastFailure) > s.config.Timeout {
			state = StateHalfOpen
			s.state = StateHalfOpen
		}
	}
	s.mu.Unlock()

	// Logic based on state
	switch state {
	case StateOpen:
		// Fail Fast
		if s.config.FallbackAllow {
			return true, maxTokens, 0, nil // Degraded mode: Allow everything (risky?) or just bypass?
			// Usually rate limiter fallback -> Allow Open (= No Limit) or Block (= Total Outage).
			// Let's assume user wants to 'Fail Open' (Allow) to keep service running, OR 'Fail Closed' (Block) to protect downstream.
			// Configurable.
		}
		// Return error or explicit "blocked by circuit breaker"
		return false, 0, s.config.Timeout, errors.New("circuit breaker open")

	case StateHalfOpen:
		// Attempt one request (Probe)
		// We could use a lock to ensure only 1 probe, but simple optimization is: just let it pass.
		// If it succeeds, Close. If fail, Re-Open.
		return s.attemptRequest(ctx, key, cost, maxTokens, refillInterval)

	case StateClosed:
		// Normal operation
		return s.attemptRequest(ctx, key, cost, maxTokens, refillInterval)
	}

	return false, 0, 0, nil
}

func (s *CircuitBreakerStore) attemptRequest(ctx context.Context, key string, cost int64, maxTokens int64, refillInterval time.Duration) (bool, int64, time.Duration, error) {
	allowed, remaining, retryAfter, err := s.backend.Allow(ctx, key, cost, maxTokens, refillInterval)

	s.mu.Lock()
	defer s.mu.Unlock()

	if err != nil {
		s.failures++
		s.lastFailure = s.timeNow()

		if s.state == StateHalfOpen || s.failures >= s.config.Threshold {
			s.state = StateOpen
		}
		return allowed, remaining, retryAfter, err
	}

	// Success! Reset.
	if s.state == StateHalfOpen || s.failures > 0 {
		s.failures = 0
		s.state = StateClosed
	}

	return allowed, remaining, retryAfter, nil
}
