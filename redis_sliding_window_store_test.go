package rrl

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

func TestRedisSlidingWindowStore(t *testing.T) {
	client, mr := setupRedisClient(t)
	defer mr.Close()

	store := NewRedisSlidingWindowStore(client, false)

	// Deterministic time
	start := time.Now()
	store.timeNow = func() time.Time {
		return start
	}

	ctx := context.Background()
	key := "test-sliding"

	// Config: 2 requests per 1 second.
	// Limit (maxTokens) = 2.
	// RefillInterval = 500ms.
	// WindowSize = 2 * 500ms = 1s.
	limit := int64(2)
	refill := 500 * time.Millisecond

	// 1. First Request: Allowed
	allowed, remaining, _, err := store.Allow(ctx, key, 1, limit, refill)
	assert.NoError(t, err)
	assert.True(t, allowed)
	assert.Equal(t, int64(1), remaining)

	// 2. Second Request: Allowed
	allowed, remaining, _, err = store.Allow(ctx, key, 1, limit, refill)
	assert.True(t, allowed)
	assert.Equal(t, int64(0), remaining) // 0 remaining

	// 3. Third Request: Blocked (Limit reached within 1s window)
	allowed, _, retryAfter, _ := store.Allow(ctx, key, 1, limit, refill)
	assert.False(t, allowed)
	assert.True(t, retryAfter > 0)
	// Retry after should be ~1s (since first req was at 'start')
	// Wait, retryAfter = (oldest + window) - now
	// oldest = start. window = 1s. now = start.
	// retry = 1s.
	assert.InDelta(t, time.Second.Nanoseconds(), retryAfter.Nanoseconds(), float64(100*time.Millisecond))

	// 4. Advance Time by 600ms.
	// Window is [start-1s, start]. (Actually [now-1s, now])
	// New Time: start + 0.6s.
	// Window range: [-0.4s, 0.6s].
	// Oldest req was at 0s. Still in window!
	store.timeNow = func() time.Time {
		return start.Add(600 * time.Millisecond)
	}

	allowed, _, _, _ = store.Allow(ctx, key, 1, limit, refill)
	assert.False(t, allowed, "Should still be blocked because older requests are in the 1s window")

	// 5. Advance Time by 1.1s (Total 1.1 from start)
	// New Time: start + 1.1s.
	// Window range: [0.1s, 1.1s].
	// Oldest req was at 0s. 0s < 0.1s. So it drops out!
	// Next req was at 0s too (we sent 2 at start). Both drop out?
	store.timeNow = func() time.Time {
		return start.Add(1100 * time.Millisecond)
	}

	allowed, remaining, _, _ = store.Allow(ctx, key, 1, limit, refill)
	assert.True(t, allowed, "Should be allowed now that window slid past old requests")
	// Since both previous drops happened at 0s, both are gone.
	// We sent 1 new one. Count is 1. Remaining should be 1.
	assert.Equal(t, int64(1), remaining)
}
