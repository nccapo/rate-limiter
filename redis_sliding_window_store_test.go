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

func TestRedisSlidingWindowStore_RetryAfter_CostGreaterThanOne(t *testing.T) {
	client, mr := setupRedisClient(t)
	defer mr.Close()

	store := NewRedisSlidingWindowStore(client, false)

	// Config: 10 requests per 10 seconds.
	// Limit = 10.
	// Refill = 1s.
	// Window = 10s.
	limit := int64(10)
	refill := 1 * time.Second

	ctx := context.Background()
	key := "test-retry-cost"

	start := time.Now()
	store.timeNow = func() time.Time { return start }

	// 1. Fill the window with 10 requests (cost 1 each)
	for i := 0; i < 10; i++ {
		store.timeNow = func() time.Time { return start.Add(time.Duration(i) * time.Second) }
		allowed, _, _, _ := store.Allow(ctx, key, 1, limit, refill)
		assert.True(t, allowed, "Request %d should be allowed", i)
	}

	// Now at T=9. Window contains starts [0, 1, ..., 9]. Expirations: [10, 11, ..., 19].
	// Limit=10. Count=10.

	// Try allow with Cost=3 at T=9.5.
	store.timeNow = func() time.Time { return start.Add(9500 * time.Millisecond) }
	allowed, _, retryAfter, _ := store.Allow(ctx, key, 3, limit, refill)
	assert.False(t, allowed)

	// Expectation: The CORRECT behavior would be to wait for 3rd oldest slot (T=2) to expire.
	// 3rd oldest expires at T=12.
	// Current time T=9.5.
	// Expected wait = 12 - 9.5 = 2.5s.

	t.Logf("RetryAfter: %v", retryAfter)

	expectedWait := 2500 * time.Millisecond
	assert.InDelta(t, expectedWait.Nanoseconds(), retryAfter.Nanoseconds(), float64(100*time.Millisecond))

	// Verify that waiting THIS time IS enough (just barely)
	store.timeNow = func() time.Time { return start.Add(9500 * time.Millisecond).Add(retryAfter).Add(10 * time.Millisecond) }

	allowed, _, _, _ = store.Allow(ctx, key, 3, limit, refill)
	assert.True(t, allowed, "Should be allowed after waiting the correct retryAfter")
}

// TestRedisSlidingWindowStore_UniqueMembersAtSameInstant pins the clock so
// every request lands on the same sorted-set score. Each request must still
// occupy its own ZSET member, which is what the per-request UUID guarantees:
// if two requests ever produced the same id, ZADD would overwrite instead of
// insert and the limiter would silently admit more than the limit.
func TestRedisSlidingWindowStore_UniqueMembersAtSameInstant(t *testing.T) {
	client, mr := setupRedisClient(t)
	defer mr.Close()

	store := NewRedisSlidingWindowStore(client, false)

	frozen := time.Now()
	store.timeNow = func() time.Time { return frozen }

	ctx := context.Background()
	key := "same-instant"
	limit := int64(50)
	refill := 20 * time.Millisecond

	for i := int64(0); i < limit; i++ {
		allowed, remaining, _, err := store.Allow(ctx, key, 1, limit, refill)
		assert.NoError(t, err)
		assert.True(t, allowed, "request %d should be admitted", i)
		assert.Equal(t, limit-i-1, remaining)
	}

	allowed, _, retryAfter, err := store.Allow(ctx, key, 1, limit, refill)
	assert.NoError(t, err)
	assert.False(t, allowed, "the limit must hold even when every request shares a timestamp")
	assert.Positive(t, retryAfter)
}

// TestRedisSlidingWindowStore_UniqueMembersWithCost covers the same property
// for a multi-token request, where one call adds several members at once.
func TestRedisSlidingWindowStore_UniqueMembersWithCost(t *testing.T) {
	client, mr := setupRedisClient(t)
	defer mr.Close()

	store := NewRedisSlidingWindowStore(client, false)

	frozen := time.Now()
	store.timeNow = func() time.Time { return frozen }

	ctx := context.Background()
	key := "same-instant-cost"
	limit := int64(10)
	refill := 100 * time.Millisecond

	allowed, remaining, _, err := store.Allow(ctx, key, 4, limit, refill)
	assert.NoError(t, err)
	assert.True(t, allowed)
	assert.Equal(t, int64(6), remaining)

	allowed, remaining, _, err = store.Allow(ctx, key, 6, limit, refill)
	assert.NoError(t, err)
	assert.True(t, allowed)
	assert.Equal(t, int64(0), remaining, "all 10 members must be distinct entries")

	allowed, _, _, err = store.Allow(ctx, key, 1, limit, refill)
	assert.NoError(t, err)
	assert.False(t, allowed)
}
