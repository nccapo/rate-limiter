package rrl

import (
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

	limiter, err := NewRateLimiter(&RateLimiter{
		Rate:           1,
		MaxTokens:      5,
		RefillInterval: 1 * time.Second,
		Client:         client,
		HashKey:        false,
		logger:         testLogger,
		timeNow: func() time.Time {
			return currentTime
		},
	})
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
