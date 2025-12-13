package rrl

import (
	"context"
	"io"
	"log"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/redis/go-redis/v9"
	"github.com/stretchr/testify/assert"
)

func setupRedisClient(t *testing.T) (*redis.Client, *miniredis.Miniredis) {
	mr, err := miniredis.Run()
	if err != nil {
		t.Fatalf("Error creating mock Redis server: %v", err)
	}

	client := redis.NewClient(&redis.Options{
		Addr: mr.Addr(),
		DB:   0,
	})

	return client, mr
}

func TestRateLimiter_Allow(t *testing.T) {
	client, mr := setupRedisClient(t)
	defer mr.Close()

	// Create a logger that discards output during tests
	testLogger := log.New(io.Discard, "", 0)

	// Create a mutable time reference for testing
	currentTime := time.Now()

	// Create Redis Store
	redisStore := NewRedisStore(client, false)
	redisStore.timeNow = func() time.Time {
		return currentTime
	}

	limiter, err := NewRateLimiter(
		WithRate(1),
		WithMaxTokens(5),
		WithRefillInterval(1*time.Second),
		WithStore(redisStore),
		WithLogger(testLogger),
	)
	// Override limiter timeNow as well
	if limiter != nil {
		limiter.timeNow = func() time.Time {
			return currentTime
		}
	}
	if err != nil {
		t.Fatal(err)
	}

	tests := []struct {
		name     string
		key      string
		expected bool
		advance  time.Duration
	}{
		{"First Request", "user1", true, 0},
		{"Second Request", "user1", true, 0},
		{"Third Request", "user1", true, 0},
		{"Fourth Request", "user1", true, 0},
		{"Fifth Request", "user1", true, 0},
		{"Sixth Request", "user1", false, 0},
		{"Wait for Refill", "user1", true, 1 * time.Second},
	}

	for _, tt := range tests {
		if tt.advance > 0 {
			// Advance the time for the test
			currentTime = currentTime.Add(tt.advance)
		}
		t.Run(tt.name, func(t *testing.T) {
			result := limiter.IsRequestAllowed(tt.key)
			assert.Equal(t, tt.expected, result)
		})
	}
}

func TestRateLimiter_Wait(t *testing.T) {
	client, mr := setupRedisClient(t)
	defer mr.Close()

	// 1 token per second, max 1
	store := NewRedisStore(client, false)
	// We need to inject timeNow into store if we want deterministic wait,
	// but Wait uses time.After/Timer which depends on real time.
	// So we can only test that it blocks approximately correctly or use a very short interval.

	limiter, err := NewRateLimiter(
		WithRate(1),
		WithMaxTokens(1),
		WithRefillInterval(100*time.Millisecond), // Fast refill for testing
		WithStore(store),
	)

	if err != nil {
		t.Fatalf("Failed to create limiter: %v", err)
	}

	ctx := context.Background()

	// 1. First request immediate
	start := time.Now()
	err = limiter.Wait(ctx, "wait-user")
	assert.NoError(t, err)
	assert.True(t, time.Since(start) < 50*time.Millisecond)

	// 2. Second request should wait ~100ms
	start = time.Now()
	err = limiter.Wait(ctx, "wait-user")
	assert.NoError(t, err)
	elapsed := time.Since(start)
	assert.True(t, elapsed >= 80*time.Millisecond, "Should wait at least 80ms, got %v", elapsed)
}
