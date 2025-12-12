package rrl

import (
	"context"
	"time"

	"github.com/redis/go-redis/v9"
)

// luaRequest is a Redis Lua script to process rate limiting atomically.
// KEYS[1]: tokens_key
// KEYS[2]: last_refill_key
// ARGV[1]: cost (tokens needed for request)
// ARGV[2]: max_tokens
// ARGV[3]: refill_interval_ns (nanoseconds to refill 1 token)
// ARGV[4]: current_time_ns (current unix time in nanoseconds)
// ARGV[5]: expiration_seconds (TTL for keys)
var luaRequest = redis.NewScript(`
	local tokens_key = KEYS[1]
	local last_refill_key = KEYS[2]
	
	local cost = tonumber(ARGV[1])
	local max_tokens = tonumber(ARGV[2])
	local refill_interval_ns = tonumber(ARGV[3])
	local now_ns = tonumber(ARGV[4])
	local expiration_seconds = tonumber(ARGV[5])

	local current_tokens = tonumber(redis.call("get", tokens_key))
	local last_refill_ns = tonumber(redis.call("get", last_refill_key))

	if not current_tokens then
		current_tokens = max_tokens
	end
	
	if not last_refill_ns then
		last_refill_ns = now_ns
	end

	-- Calculate elapsed time and tokens to add
	local elapsed_ns = now_ns - last_refill_ns
	if elapsed_ns < 0 then elapsed_ns = 0 end -- clock skew protection

	local tokens_to_add = math.floor(elapsed_ns / refill_interval_ns)
	
	if tokens_to_add > 0 then
		current_tokens = math.min(max_tokens, current_tokens + tokens_to_add)
		-- Update last_refill_ns to the time of the most recent token addition to avoid drift
		last_refill_ns = last_refill_ns + (tokens_to_add * refill_interval_ns)
		if last_refill_ns > now_ns then
			last_refill_ns = now_ns
		end
	end

	local allowed = 0
	local remaining = current_tokens

	if current_tokens >= cost then
		current_tokens = current_tokens - cost
		allowed = 1
		remaining = current_tokens
	else
		allowed = 0
	end

	-- Save state with TTL
	redis.call("set", tokens_key, current_tokens, "EX", expiration_seconds)
	redis.call("set", last_refill_key, last_refill_ns, "EX", expiration_seconds)

	return {allowed, remaining}
`)

// RedisStore implements Store using Redis.
type RedisStore struct {
	client  redis.UniversalClient
	hashKey bool
	// Optional: time function for testing (normally time.Now)
	timeNow func() time.Time
}

// NewRedisStore creates a new RedisStore.
// client can be *redis.Client, *redis.ClusterClient, or *redis.Ring.
func NewRedisStore(client redis.UniversalClient, hashKey bool) *RedisStore {
	return &RedisStore{
		client:  client,
		hashKey: hashKey,
		timeNow: time.Now,
	}
}

func (s *RedisStore) Allow(ctx context.Context, key string, cost int64, maxTokens int64, refillInterval time.Duration) (bool, int64, error) {
	now := s.timeNow()
	nowNs := now.UnixNano()

	var sEnc string
	if s.hashKey {
		sEnc = keyPrefix + encodeKey(key)
	} else {
		sEnc = keyPrefix + key
	}
	lastRefillKey := sEnc + lastRefillPrefix

	// Calculate TTL
	ttlSeconds := int64(time.Hour.Seconds())
	refillCycle := int64(refillInterval.Seconds()) * maxTokens * 2
	if refillCycle > ttlSeconds {
		ttlSeconds = refillCycle
	}

	result, err := luaRequest.Run(ctx, s.client,
		[]string{sEnc, lastRefillKey},
		cost,
		maxTokens,
		refillInterval.Nanoseconds(),
		nowNs,
		ttlSeconds,
	).Slice()

	if err != nil {
		return false, 0, err
	}

	allowed := result[0].(int64) == 1
	remaining := result[1].(int64)

	return allowed, remaining, nil
}
