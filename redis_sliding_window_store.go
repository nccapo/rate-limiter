package rrl

import (
	"context"
	"time"

	"github.com/redis/go-redis/v9"
)

// luaSlidingWindow handles the sliding window logic atomically using sorted sets.
// KEYS[1]: zset_key
// ARGV[1]: window_size_ns (total duration of the window)
// ARGV[2]: max_requests (limit)
// ARGV[3]: now_ns (current time)
// ARGV[4]: cost
// ARGV[5]: expiration_seconds (TTL)
var luaSlidingWindow = redis.NewScript(`
	local key = KEYS[1]
	local window_size_ns = tonumber(ARGV[1])
	local limit = tonumber(ARGV[2])
	local now_ns = tonumber(ARGV[3])
	local cost = tonumber(ARGV[4])
	local ttl = tonumber(ARGV[5])

	-- 1. Remove entries older than (now - window_size)
	local window_start = now_ns - window_size_ns
	redis.call("ZREMRANGEBYSCORE", key, "-inf", window_start)

	-- 2. Count current entries
	local current_count = redis.call("ZCARD", key)
	
	local allowed = 0
	local remaining = 0
	local retry_after_ns = 0

	if current_count + cost <= limit then
		-- ALLOWED
		allowed = 1
		remaining = limit - (current_count + cost)
		
		-- Add 'cost' members. We need unique members if they have same timestamp.
		-- Use math.random() to avoid collisions for same-timestamp requests.
		for i = 1, cost do
			local member = tostring(now_ns) .. ":" .. i .. ":" .. tostring(math.random(1000000))
			redis.call("ZADD", key, now_ns, member)
		end
	else
		-- BLOCKED
		allowed = 0
		remaining = limit - current_count
		if remaining < 0 then remaining = 0 end

		-- Calculate retry_after.
		-- We need to wait until the Oldest timestamp expires.
		-- Fetch the oldest entry (smallest score).
		local oldest_entries = redis.call("ZRANGE", key, 0, 0, "WITHSCORES")
		if #oldest_entries > 0 then
			-- Redis returns array [member, score]. Go-redis Slice/Map depends on version/flags.
			-- If passing "WITHSCORES", response is usually flat array.
			-- entry 1: member, entry 2: score.
			local oldest_score = tonumber(oldest_entries[2]) 
			
			-- Time when this oldest entry falls out of window = oldest_score + window_size
			-- Retry after = (oldest_score + window_size) - now
			local available_at = oldest_score + window_size_ns
			if available_at > now_ns then
				retry_after_ns = available_at - now_ns
			end
		end
	end

	-- Refresh TTL
	redis.call("EXPIRE", key, ttl)

	return {allowed, remaining, retry_after_ns}
`)

// RedisSlidingWindowStore implements Store using Redis ZSETs for strict sliding window.
type RedisSlidingWindowStore struct {
	client  redis.UniversalClient
	hashKey bool
	timeNow func() time.Time
}

// NewRedisSlidingWindowStore creates a new store for strict rolling windows.
func NewRedisSlidingWindowStore(client redis.UniversalClient, hashKey bool) *RedisSlidingWindowStore {
	return &RedisSlidingWindowStore{
		client:  client,
		hashKey: hashKey,
		timeNow: time.Now,
	}
}

func (s *RedisSlidingWindowStore) Allow(ctx context.Context, key string, cost int64, maxTokens int64, refillInterval time.Duration) (bool, int64, time.Duration, error) {
	now := s.timeNow()
	nowNs := now.UnixNano()

	var sEnc string
	if s.hashKey {
		sEnc = keyPrefix + encodeKey(key) + ":sw" // Suffix to distinguish from TokenBucket
	} else {
		sEnc = keyPrefix + key + ":sw"
	}

	// Logic Mapping:
	// TokenBucket: Capacity=maxTokens, Rate=1/refillInterval
	// SlidingWindow: Limit=maxTokens, WindowSize=maxTokens*refillInterval ?
	// Example: Rate=10, Max=100 (Burst). Refill=100ms.
	// We want strict window.
	// Usually strict window is defined as "Limit N per Window W".
	// If user passes `WithMaxTokens(100)` and `WithRefillInterval(time.Second)`:
	// Interpretation A: 100 reqs per 100 seconds? (1 Token refill = 1 sec).
	// Interpretation B: RefillInterval is irrelevant/forced?

	// Convention we agreed: WindowSize = Limit * RefillInterval.
	// If Limit=10, Refill=1s (1 req/s average). Window = 10s.
	// "10 requests per 10 seconds". This matches the average rate.
	windowSizeNs := maxTokens * refillInterval.Nanoseconds()

	// TTL: Window Size + Buffer (e.g. 1 min or 2x window)
	ttlSeconds := int64(time.Duration(windowSizeNs).Seconds()) + 60
	if ttlSeconds < 60 {
		ttlSeconds = 60
	}

	result, err := luaSlidingWindow.Run(ctx, s.client,
		[]string{sEnc},
		windowSizeNs,
		maxTokens, // Limit
		nowNs,
		cost,
		ttlSeconds,
	).Slice()

	if err != nil {
		return false, 0, 0, err
	}

	allowed := result[0].(int64) == 1
	remaining := result[1].(int64)
	retryAfterNs := result[2].(int64)

	return allowed, remaining, time.Duration(retryAfterNs), nil
}
