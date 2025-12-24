package rrl

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

// MockStore for testing circuit breaker
type FailStore struct {
	shouldFail bool
}

func (f *FailStore) Allow(ctx context.Context, key string, cost int64, maxTokens int64, refillInterval time.Duration) (bool, int64, time.Duration, error) {
	if f.shouldFail {
		return false, 0, 0, errors.New("backend connection lost")
	}
	return true, 10, 0, nil
}

func TestCircuitBreakerStore(t *testing.T) {
	mock := &FailStore{shouldFail: false}

	// Config: Trip after 2 failures, 1s timeout
	config := CircuitBreakerConfig{
		Threshold: 2,
		Timeout:   1 * time.Second,
	}

	cb := NewCircuitBreakerStore(mock, config)

	// Mock time
	start := time.Now()
	cb.timeNow = func() time.Time { return start }

	ctx := context.Background()

	// 1. Success (Closed)
	allowed, _, _, err := cb.Allow(ctx, "k", 1, 1, time.Second)
	assert.NoError(t, err)
	assert.True(t, allowed)
	assert.Equal(t, StateClosed, cb.state)

	// 2. Failure 1 (Closed)
	mock.shouldFail = true
	_, _, _, err = cb.Allow(ctx, "k", 1, 1, time.Second)
	assert.Error(t, err)
	assert.Equal(t, int64(1), cb.failures)
	assert.Equal(t, StateClosed, cb.state)

	// 3. Failure 2 (Trip -> Open)
	_, _, _, err = cb.Allow(ctx, "k", 1, 1, time.Second)
	assert.Error(t, err)
	assert.Equal(t, StateOpen, cb.state)

	// 4. Open State (Fail Fast)
	// Even if backend fixes itself, CB blocks calls
	mock.shouldFail = false
	allowed, _, _, err = cb.Allow(ctx, "k", 1, 1, time.Second)
	// 4. Open State (Fail Fast)
	// Even if backend fixes itself, CB blocks calls
	mock.shouldFail = false
	allowed, _, _, err = cb.Allow(ctx, "k", 1, 1, time.Second)
	assert.NoError(t, err, "Should not error when fail fast")
	assert.False(t, allowed)

	// 5. Half-Open (After Timeout)
	// Advance time by 1.1s
	cb.timeNow = func() time.Time { return start.Add(1100 * time.Millisecond) }

	// Probe Request (Should succeed now since mock.shouldFail=false)
	allowed, _, _, err = cb.Allow(ctx, "k", 1, 1, time.Second)
	assert.NoError(t, err)
	assert.True(t, allowed)

	// Should be Closed again
	assert.Equal(t, StateClosed, cb.state)
	assert.Equal(t, int64(0), cb.failures)
}

func TestCircuitBreakerStore_Fallback(t *testing.T) {
	mock := &FailStore{shouldFail: true}

	// Config: FallbackAllow = true (Fail Open)
	config := CircuitBreakerConfig{
		Threshold:     1,
		Timeout:       time.Second,
		FallbackAllow: true,
	}

	cb := NewCircuitBreakerStore(mock, config)

	// Trip it
	cb.Allow(context.Background(), "k", 1, 1, time.Second) // Fail 1
	assert.Equal(t, StateOpen, cb.state)

	// Now call again. Should trigger Fallback (Allow=true, No Error)
	allowed, _, _, err := cb.Allow(context.Background(), "k", 1, 1, time.Second)
	assert.NoError(t, err)
	assert.True(t, allowed, "Should be allowed by fallback")
}
