package rrl

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

// MockMetrics captures recorded metrics for assertions
type MockMetrics struct {
	called   bool
	key      string
	allowed  bool
	err      error
	duration time.Duration
}

func (m *MockMetrics) Record(ctx context.Context, key string, allowed bool, duration time.Duration, err error) {
	m.called = true
	m.key = key
	m.allowed = allowed
	m.err = err
	m.duration = duration
}

func TestMetricsIntegration_Allowed(t *testing.T) {
	store := NewMemoryStore()
	mockMetrics := &MockMetrics{}

	limiter, _ := NewRateLimiter(
		WithStore(store),
		WithMetrics(mockMetrics),
	)

	key := "test-metrics"
	allowed := limiter.IsRequestAllowed(key)

	assert.True(t, allowed)
	assert.True(t, mockMetrics.called)
	assert.Equal(t, key, mockMetrics.key)
	assert.True(t, mockMetrics.allowed)
	assert.NoError(t, mockMetrics.err)
	assert.True(t, mockMetrics.duration > 0)
}

func TestMetricsIntegration_Blocked(t *testing.T) {
	store := NewMemoryStore() // Default 10 tokens
	mockMetrics := &MockMetrics{}

	limiter, _ := NewRateLimiter(
		WithStore(store),
		WithMaxTokens(1),
		WithRefillInterval(time.Hour), // Very slow refill
		WithMetrics(mockMetrics),
	)

	key := "test-metrics-blocked"

	// 1. Consume 1 token (Allowed)
	limiter.IsRequestAllowed(key)

	// Reset mock
	mockMetrics.called = false

	// 2. Consume 2nd token (Blocked)
	allowed := limiter.IsRequestAllowed(key)

	assert.False(t, allowed)
	assert.True(t, mockMetrics.called)
	assert.False(t, mockMetrics.allowed)
	assert.NoError(t, mockMetrics.err)
}

type ErrorStore struct{}

func (e *ErrorStore) Allow(ctx context.Context, key string, cost int64, maxTokens int64, refillInterval time.Duration) (bool, int64, time.Duration, error) {
	return false, 0, 0, errors.New("store error")
}

func TestMetricsIntegration_Error(t *testing.T) {
	mockMetrics := &MockMetrics{}

	limiter, _ := NewRateLimiter(
		WithStore(&ErrorStore{}),
		WithMetrics(mockMetrics),
	)

	key := "test-metrics-error"
	allowed := limiter.IsRequestAllowed(key)

	assert.False(t, allowed)
	assert.True(t, mockMetrics.called)
	assert.Error(t, mockMetrics.err)
	assert.Equal(t, "store error", mockMetrics.err.Error())
}
