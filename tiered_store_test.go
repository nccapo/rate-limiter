package rrl

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

func TestTieredStore_Allow(t *testing.T) {
	// Setup
	// Primary: Low capacity (blocks early)
	primary := NewMemoryStore()
	// Secondary: High capacity
	secondary := NewMemoryStore()

	tiered := NewTieredStore(primary, secondary)
	ctx := context.Background()
	key := "test-tiered"

	// Scenario 1: Both allow
	// Primary: 10 capacity
	// Secondary: 20 capacity
	// We use SAME maxTokens in Allow call, so we must rely on the Stores' internal state?
	// Wait, Store.Allow TAKES maxTokens as arg.
	// So both stores will see the SAME maxTokens.
	// How to test "Primary blocks first"?
	// We can manually exhaust the primary store beforehand if we have access to it.
	// MemoryStore is thread-safe map.

	// Let's use same settings for both for now (Synchronization test)
	// 5 tokens max
	maxTokens := int64(5)
	refill := time.Hour // No refill

	// 1. Consume 5 tokens. Both should allow.
	for i := 0; i < 5; i++ {
		allowed, _, _, err := tiered.Allow(ctx, key, 1, maxTokens, refill)
		assert.NoError(t, err)
		assert.True(t, allowed, "Request %d should be allowed", i)
	}

	// 2. 6th request.
	// Primary (empty) -> Deny. Secondary (empty) -> Not reached (optimized) or Deny.
	allowed, _, retryAfter, err := tiered.Allow(ctx, key, 1, maxTokens, refill)
	assert.NoError(t, err)
	assert.False(t, allowed)
	assert.True(t, retryAfter > 0)
}

func TestTieredStore_PrimaryFailsFirst(t *testing.T) {
	// To test that Primary blocks BEFORE Secondary, we need Primary to satisfy condition but Secondary NOT?
	// No, TieredStore uses Primary as "Local Cache".
	// If Primary says NO, we stop.
	// If Primary says YES, we ask Secondary.

	// Case: Primary Full, Secondary Empty.
	// This happens if this specific node sent many requests, but other nodes didn't.
	primary := NewMemoryStore()
	secondary := NewMemoryStore()
	tiered := NewTieredStore(primary, secondary)
	ctx := context.Background()
	key := "test-primary-full"

	// Exhaust Primary ONLY
	// We can do this by using a different key or manually modifying it?
	// MemoryStore doesn't expose internals easily.
	// We can just call primary.Allow directly!

	// 1. Exhaust PRIMARY store directly with 10 tokens
	for i := 0; i < 10; i++ {
		primary.Allow(ctx, key, 1, 10, time.Hour)
	}

	// 2. Now call Tiered with same limits
	// Primary has 0 tokens left (for capacity 10).
	// Secondary has 10 tokens left (for capacity 10).
	allowed, _, _, _ := tiered.Allow(ctx, key, 1, 10, time.Hour)

	// Should be BLOCKED by Primary
	assert.False(t, allowed, "Should be blocked by primary")
}

func TestTieredStore_SecondaryFails(t *testing.T) {
	// Case: Primary Empty, Secondary Full.
	// This happens if OTHER nodes exhausted the Redis quota.
	primary := NewMemoryStore()
	secondary := NewMemoryStore()
	tiered := NewTieredStore(primary, secondary)
	ctx := context.Background()
	key := "test-secondary-full"

	// 1. Exhaust SECONDARY store directly
	for i := 0; i < 10; i++ {
		secondary.Allow(ctx, key, 1, 10, time.Hour)
	}

	// 2. Call Tiered
	// Primary has 10 tokens.
	// Secondary has 0 tokens.
	allowed, _, _, _ := tiered.Allow(ctx, key, 1, 10, time.Hour)

	// Primary returns YES.
	// Secondary returns NO.
	// Result should be NO.
	assert.False(t, allowed, "Should be blocked by secondary")

	// NOTE: This consumes 1 token from Primary! (Because Primary said YES)
	// Verify Primary has 9 left
	pAllowed, pRemaining, _, _ := primary.Allow(ctx, key, 0, 10, time.Hour) // Cost 0 check
	assert.True(t, pAllowed)
	assert.Equal(t, int64(9), pRemaining, "Primary should have consumed a token even if Secondary failed")
}
