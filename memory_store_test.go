package rrl

import (
	"context"
	"fmt"
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
	allowed, remaining, _, err := store.Allow(ctx, key, 1, maxTokens, refillInterval)
	assert.NoError(t, err)
	assert.True(t, allowed)
	assert.Equal(t, int64(4), remaining)

	// 2. Consume all tokens
	for i := 0; i < 4; i++ {
		allowed, _, _, _ := store.Allow(ctx, key, 1, maxTokens, refillInterval)
		assert.True(t, allowed)
	}

	// 3. Should be empty
	allowed, remaining, retryAfter, _ := store.Allow(ctx, key, 1, maxTokens, refillInterval)
	assert.False(t, allowed)
	assert.Equal(t, int64(0), remaining)
	assert.True(t, retryAfter > 0) // Should have a wait time

	// 4. Advance time by 1 second (1 token refill)
	currentTime = currentTime.Add(1 * time.Second)

	allowed, remaining, _, _ = store.Allow(ctx, key, 1, maxTokens, refillInterval)
	assert.True(t, allowed)
	assert.Equal(t, int64(0), remaining) // Consumed the refilled token

	// 5. Advance time by 10 second (Full refill)
	currentTime = currentTime.Add(10 * time.Second)

	allowed, remaining, _, _ = store.Allow(ctx, key, 1, maxTokens, refillInterval)
	assert.True(t, allowed)
	assert.Equal(t, int64(4), remaining) // 5 max - 1 consumed = 4
}

// TestMemoryStore_EvictsIdleKeys covers the unbounded-growth leak: before
// eviction existed, every distinct key kept a bucket alive for the lifetime
// of the process. Retention must now stay proportional to the sweep window,
// not to the total number of keys ever seen.
func TestMemoryStore_EvictsIdleKeys(t *testing.T) {
	now := time.Now()
	const sweepInterval = 10 * time.Second
	store := NewMemoryStore(WithMemorySweepInterval(sweepInterval))
	store.timeNow = func() time.Time { return now }

	ctx := context.Background()
	const (
		maxTokens = int64(10)
		refill    = time.Second
		total     = 50_000 // one new key per simulated millisecond, so 50s of traffic
	)

	peak := 0
	for i := 0; i < total; i++ {
		// Every request comes from a different client, and is never repeated.
		_, _, _, err := store.Allow(ctx, fmt.Sprintf("client-%d", i), 1, maxTokens, refill)
		assert.NoError(t, err)
		if n := store.Len(); n > peak {
			peak = n
		}
		now = now.Add(time.Millisecond)
	}

	// Each key is drained by a single token, so it is back at capacity one
	// refill interval (1s) later. With sweeps every 10s, no more than ~11s
	// of arrivals can be held at any moment - regardless of how many
	// distinct keys the store has seen in total.
	const bound = 12_000 // 12s at one new key per millisecond
	assert.Less(t, peak, bound, "retained buckets must be bounded by the sweep window, not by the number of distinct keys")
	assert.Less(t, store.Len(), bound, "idle buckets must be reclaimed")
}

// TestMemoryStore_EvictionPreservesBehaviour asserts the eviction rule is a
// no-op from the caller's point of view: a bucket is only dropped once a
// fresh one would behave identically.
func TestMemoryStore_EvictionPreservesBehaviour(t *testing.T) {
	now := time.Now()
	store := NewMemoryStore(WithMemorySweepInterval(time.Minute))
	store.timeNow = func() time.Time { return now }

	ctx := context.Background()
	maxTokens := int64(5)
	refill := time.Minute // slow enough that a sweep interval does not refill the bucket

	// Drain the bucket completely.
	for i := int64(0); i < maxTokens; i++ {
		allowed, _, _, _ := store.Allow(ctx, "kept", 1, maxTokens, refill)
		assert.True(t, allowed)
	}
	allowed, _, _, _ := store.Allow(ctx, "kept", 1, maxTokens, refill)
	assert.False(t, allowed, "bucket should be empty")

	// Advance past the sweep interval but not far enough for a full refill:
	// 2 of 5 tokens are back, so the bucket still carries state and must survive.
	now = now.Add(2 * refill)
	store.Allow(ctx, "other", 1, maxTokens, refill) // triggers a sweep

	_, remaining, _, _ := store.Allow(ctx, "kept", 1, maxTokens, refill)
	assert.Equal(t, int64(1), remaining, "a partially refilled bucket must not be reset by a sweep")
	assert.Contains(t, store.data, "kept")

	// Now let it refill completely and sweep again; dropping it is safe
	// because a new bucket starts full too.
	now = now.Add(time.Duration(maxTokens) * refill)
	store.Allow(ctx, "other", 1, maxTokens, refill)
	assert.NotContains(t, store.data, "kept", "a fully refilled bucket should be reclaimed")

	_, remaining, _, _ = store.Allow(ctx, "kept", 1, maxTokens, refill)
	assert.Equal(t, maxTokens-1, remaining, "the reclaimed key must behave exactly like a full bucket")
}

// TestMemoryStore_EvictionRespectsPerKeyCapacity guards the case where one
// store limits different keys with different settings: a bucket must be
// judged against its own capacity, not whichever call happens to sweep.
func TestMemoryStore_EvictionRespectsPerKeyCapacity(t *testing.T) {
	now := time.Now()
	store := NewMemoryStore(WithMemorySweepInterval(time.Minute))
	store.timeNow = func() time.Time { return now }

	ctx := context.Background()
	refill := time.Second

	// "big" has a capacity of 100 and is drained to 10, so it needs 90s to refill.
	store.Allow(ctx, "big", 90, 100, refill)

	// "small" has a capacity of 5 and refills fully in 5s, so it is evictable
	// at the sweep below. "big" is not, and must not be judged by "small"'s
	// capacity just because "small" is the call that triggers the sweep.
	now = now.Add(time.Minute + time.Second)
	store.Allow(ctx, "small", 1, 5, refill)

	assert.Contains(t, store.data, "big", "a bucket must be measured against its own capacity")

	// Once "big" has had its own 90s to refill, it becomes evictable too.
	now = now.Add(2 * time.Minute)
	store.Allow(ctx, "small", 1, 5, refill)
	assert.NotContains(t, store.data, "big", "a fully refilled bucket should be reclaimed whatever its capacity")
}

// TestMemoryStore_MaxKeysForcesSweep verifies the soft cap reclaims memory
// without waiting for the sweep interval.
func TestMemoryStore_MaxKeysForcesSweep(t *testing.T) {
	now := time.Now()
	store := NewMemoryStore(
		WithMemorySweepInterval(time.Hour), // interval sweeps are effectively off
		WithMemoryMaxKeys(100),
	)
	store.timeNow = func() time.Time { return now }

	ctx := context.Background()
	maxTokens := int64(2)
	refill := 10 * time.Millisecond // buckets refill quickly, so they go idle fast

	for i := 0; i < 5_000; i++ {
		store.Allow(ctx, fmt.Sprintf("client-%d", i), 1, maxTokens, refill)
		now = now.Add(time.Second) // well past minSweepInterval each iteration
	}

	assert.LessOrEqual(t, store.Len(), 101, "MaxKeys should force sweeps ahead of the interval")
}

// TestMemoryStore_SweepIsBounded checks that a store held over its soft cap
// by genuinely active clients does not rescan the map on every call.
func TestMemoryStore_SweepIsBounded(t *testing.T) {
	now := time.Now()
	store := NewMemoryStore(WithMemoryMaxKeys(1))
	store.timeNow = func() time.Time { return now }

	ctx := context.Background()
	// Two keys drained to empty: neither is evictable.
	store.Allow(ctx, "a", 1, 1, time.Hour)
	store.Allow(ctx, "b", 1, 1, time.Hour)
	now = now.Add(minSweepInterval)
	store.Allow(ctx, "a", 1, 1, time.Hour)
	first := store.lastSweep

	// Still over cap, but within minSweepInterval: no second scan.
	now = now.Add(minSweepInterval / 2)
	store.Allow(ctx, "a", 1, 1, time.Hour)
	assert.Equal(t, first, store.lastSweep, "an over-cap store must back off between sweeps")
}
