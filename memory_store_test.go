package rrl

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

func TestMemoryStore(t *testing.T) {
	store := NewMemoryStore()

	// Override time for deterministic testing
	currentTime := time.Now()
	store.timeNow = func() time.Time {
		return currentTime
	}

	ctx := context.Background()
	key := "user1"
	maxTokens := int64(5)
	refillInterval := 1 * time.Second

	// 1. Initial State: Full bucket
	allowed, remaining, err := store.Allow(ctx, key, 1, maxTokens, refillInterval)
	assert.NoError(t, err)
	assert.True(t, allowed)
	assert.Equal(t, int64(4), remaining)

	// 2. Consume all tokens
	for i := 0; i < 4; i++ {
		allowed, _, _ := store.Allow(ctx, key, 1, maxTokens, refillInterval)
		assert.True(t, allowed)
	}

	// 3. Should be empty
	allowed, remaining, _ = store.Allow(ctx, key, 1, maxTokens, refillInterval)
	assert.False(t, allowed)
	assert.Equal(t, int64(0), remaining)

	// 4. Advance time by 1 second (1 token refill)
	currentTime = currentTime.Add(1 * time.Second)

	allowed, remaining, _ = store.Allow(ctx, key, 1, maxTokens, refillInterval)
	assert.True(t, allowed)
	assert.Equal(t, int64(0), remaining) // Consumed the refilled token

	// 5. Advance time by 10 second (Full refill)
	currentTime = currentTime.Add(10 * time.Second)

	allowed, remaining, _ = store.Allow(ctx, key, 1, maxTokens, refillInterval)
	assert.True(t, allowed)
	assert.Equal(t, int64(4), remaining) // 5 max - 1 consumed = 4
}
