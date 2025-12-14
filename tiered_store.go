package rrl

import (
	"context"
	"time"
)

// TieredStore implements a two-level rate limiting strategy.
// It checks a primary (usually local/in-memory) store first.
// If the request is allowed locally, it then checks the secondary (usually remote/Redis) store.
// This significantly reduces load on the remote store during high-traffic bursts.
type TieredStore struct {
	primary   Store
	secondary Store
}

// NewTieredStore creates a new TieredStore.
// primary: The first line of defense (e.g., MemoryStore).
// secondary: The authoritative distributed store (e.g., RedisStore).
func NewTieredStore(primary, secondary Store) *TieredStore {
	return &TieredStore{
		primary:   primary,
		secondary: secondary,
	}
}

// Allow checks the primary store first, then the secondary store.
func (s *TieredStore) Allow(ctx context.Context, key string, cost int64, maxTokens int64, refillInterval time.Duration) (bool, int64, time.Duration, error) {
	// 1. Check Primary (Local)
	allowed, _, retryAfter, err := s.primary.Allow(ctx, key, cost, maxTokens, refillInterval)
	if err != nil {
		// If primary fails, we should fail safe or log?
		// For now, let's treat primary error as critical or maybe skip to secondary?
		// Safer to return error.
		return false, 0, 0, err
	}

	if !allowed {
		// Blocked locally! Save the network call.
		return false, 0, retryAfter, nil
	}

	// 2. Check Secondary (Remote)
	// We use the same 'cost' here.
	// Note: 'remaining' will be the value from the REMOTE store, which is the authoritative one.
	return s.secondary.Allow(ctx, key, cost, maxTokens, refillInterval)
}
