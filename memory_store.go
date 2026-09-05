package rrl

import (
	"context"
	"math"
	"sync"
	"time"
)

const (
	// defaultSweepInterval is how often Allow reclaims idle buckets.
	defaultSweepInterval = time.Minute

	// minSweepInterval bounds how often a MaxKeys overflow can force a sweep.
	// A sweep is O(len(data)) under the lock, so without this floor a store
	// that is legitimately over its cap would scan the whole map on every call.
	minSweepInterval = time.Second
)

// MemoryStore implements Store using an in-memory map.
// Useful for testing or single-instance applications.
//
// Buckets are reclaimed automatically. A bucket that has refilled to its full
// token capacity is indistinguishable from one that has never been seen: both
// start the next request with maxTokens available. Dropping such an entry is
// therefore behaviour-preserving, and it is what keeps the map bounded. Any
// key that stops sending traffic reaches capacity within
// maxTokens*refillInterval and is evicted by the next sweep.
//
// Without this, the store retained one entry per distinct key - typically
// one per client IP - for the lifetime of the process.
//
// Sweeping happens inline on Allow rather than from a background goroutine,
// so a MemoryStore needs no Close and cannot leak a goroutine. The trade-off
// is that a store receiving no traffic keeps its last set of keys until the
// next call; that is bounded, not growing.
type MemoryStore struct {
	data    map[string]*memoryBucket
	mu      sync.Mutex
	timeNow func() time.Time

	// sweepInterval is the minimum delay between eviction sweeps.
	// Zero disables interval-based sweeping.
	sweepInterval time.Duration

	// maxKeys, when positive, forces a sweep once the map grows beyond it,
	// without waiting for sweepInterval to elapse.
	maxKeys int

	// lastSweep is when the most recent sweep ran.
	lastSweep time.Time
}

type memoryBucket struct {
	tokens     int64
	lastRefill time.Time

	// maxTokens and refillInterval are the limits this bucket was last
	// configured with. They are recorded per bucket so a sweep can tell
	// when the bucket will be full again without touching it, and so
	// eviction stays correct when different keys are limited with
	// different settings through one store.
	maxTokens      int64
	refillInterval time.Duration
}

// isFullAt reports whether the bucket has refilled to capacity by now.
// It answers the question the sweep asks without mutating the bucket:
// the stored token count is only brought up to date when Allow touches
// the key, so an untouched bucket is almost always fuller than it looks.
func (b *memoryBucket) isFullAt(now time.Time) bool {
	missing := b.maxTokens - b.tokens
	if missing <= 0 {
		return true
	}

	intervalNs := b.refillInterval.Nanoseconds()
	if intervalNs <= 0 {
		// No meaningful refill rate; never claim the bucket is full.
		return false
	}
	if missing > math.MaxInt64/intervalNs {
		// Refilling would take longer than a time.Duration can express.
		return false
	}

	return now.Sub(b.lastRefill) >= time.Duration(missing*intervalNs)
}

// MemoryStoreOption configures a MemoryStore.
type MemoryStoreOption func(*MemoryStore)

// WithMemorySweepInterval sets how often idle buckets are reclaimed
// (default one minute). A value <= 0 disables interval-based sweeping,
// leaving WithMemoryMaxKeys as the only trigger.
func WithMemorySweepInterval(interval time.Duration) MemoryStoreOption {
	return func(s *MemoryStore) {
		s.sweepInterval = interval
	}
}

// WithMemoryMaxKeys makes the store sweep as soon as it holds more than n
// keys, instead of waiting for the sweep interval. It is a soft cap: buckets
// still holding depleted state are never dropped, because resetting them
// would hand the corresponding clients a full bucket. It bounds the memory a
// burst of one-off keys can hold between sweeps, not the number of genuinely
// active clients.
func WithMemoryMaxKeys(n int) MemoryStoreOption {
	return func(s *MemoryStore) {
		s.maxKeys = n
	}
}

// NewMemoryStore creates a new MemoryStore.
func NewMemoryStore(opts ...MemoryStoreOption) *MemoryStore {
	s := &MemoryStore{
		data:          make(map[string]*memoryBucket),
		timeNow:       time.Now,
		sweepInterval: defaultSweepInterval,
	}

	for _, opt := range opts {
		opt(s)
	}

	return s
}

// Len returns the number of buckets currently retained.
// Useful for asserting that eviction is keeping the store bounded.
func (s *MemoryStore) Len() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.data)
}

func (s *MemoryStore) Allow(ctx context.Context, key string, cost int64, maxTokens int64, refillInterval time.Duration) (bool, int64, time.Duration, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	now := s.timeNow()

	// Reclaim idle buckets before touching this key, so the bucket we are
	// about to use is never a candidate for this sweep.
	s.maybeSweepLocked(now)

	bucket, exists := s.data[key]
	if !exists {
		bucket = &memoryBucket{
			tokens:     maxTokens,
			lastRefill: now,
		}
		s.data[key] = bucket
	}
	bucket.maxTokens = maxTokens
	bucket.refillInterval = refillInterval

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
	var retryAfter time.Duration

	if bucket.tokens >= cost {
		bucket.tokens -= cost
		allowed = true
	} else {
		// Calculate missing tokens
		needed := cost - bucket.tokens
		retryAfter = time.Duration(needed * refillIntervalNs)
	}

	return allowed, bucket.tokens, retryAfter, nil
}

// maybeSweepLocked runs a sweep if one is due. The caller must hold s.mu.
func (s *MemoryStore) maybeSweepLocked(now time.Time) {
	if s.lastSweep.IsZero() {
		// First call: start the clock rather than sweeping an empty map.
		s.lastSweep = now
		return
	}

	elapsed := now.Sub(s.lastSweep)
	overCap := s.maxKeys > 0 && len(s.data) > s.maxKeys

	switch {
	case overCap && elapsed >= minSweepInterval:
	case s.sweepInterval > 0 && elapsed >= s.sweepInterval:
	default:
		return
	}

	s.sweepLocked(now)
}

// sweepLocked drops every bucket that has refilled to capacity, which is
// exactly the set of buckets a fresh lookup would reconstruct identically.
// The caller must hold s.mu.
func (s *MemoryStore) sweepLocked(now time.Time) {
	for key, bucket := range s.data {
		if bucket.isFullAt(now) {
			delete(s.data, key)
		}
	}
	s.lastSweep = now
}
