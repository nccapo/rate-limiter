package rrl

import (
	"context"
	"sync"
	"time"
)

// MemoryStore implements Store using an in-memory map.
// Useful for testing or single-instance applications.
type MemoryStore struct {
	data    map[string]*memoryBucket
	mu      sync.Mutex
	timeNow func() time.Time
}

type memoryBucket struct {
	tokens     int64
	lastRefill time.Time
}

// NewMemoryStore creates a new MemoryStore.
func NewMemoryStore() *MemoryStore {
	return &MemoryStore{
		data:    make(map[string]*memoryBucket),
		timeNow: time.Now,
	}
}

func (s *MemoryStore) Allow(ctx context.Context, key string, cost int64, maxTokens int64, refillInterval time.Duration) (bool, int64, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	bucket, exists := s.data[key]
	if !exists {
		bucket = &memoryBucket{
			tokens:     maxTokens,
			lastRefill: s.timeNow(),
		}
		s.data[key] = bucket
	}

	now := s.timeNow()
	refillIntervalNs := refillInterval.Nanoseconds()

	// Refill logic
	elapsed := now.Sub(bucket.lastRefill)
	if elapsed > 0 {
		tokensToAdd := int64(elapsed.Nanoseconds() / refillIntervalNs)
		if tokensToAdd > 0 {
			bucket.tokens = bucket.tokens + tokensToAdd
			if bucket.tokens > maxTokens {
				bucket.tokens = maxTokens
			}
			// Update timestamp to avoid drift
			bucket.lastRefill = bucket.lastRefill.Add(time.Duration(tokensToAdd * refillIntervalNs))
			// Clamp to now
			if bucket.lastRefill.After(now) {
				bucket.lastRefill = now
			}
		}
	}

	allowed := false
	if bucket.tokens >= cost {
		bucket.tokens -= cost
		allowed = true
	}

	return allowed, bucket.tokens, nil
}
