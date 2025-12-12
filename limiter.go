package rrl

import (
	"context"
	b64 "encoding/base64"
	"log"
	"os"
	"time"

	"github.com/redis/go-redis/v9"
)

// define constant variable of keyPrefix to avoid duplicate key in Redis
const (
	keyPrefix        = "ls_prefix:"
	lastRefillPrefix = "_lastRefillTime"
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
		-- Update last_refill_ns to the time of the most recent token addition to avoid drift, 
		-- or just set to now. Setting to 'now' is simpler but can slightly under-fill.
		-- Better strategy: advance time by the amount of tokens added.
		last_refill_ns = last_refill_ns + (tokens_to_add * refill_interval_ns)
		-- Clamp timestamp to now to prevent future timestamps if tokens were capped
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

// RateLimiter is struct based on Redis
type RateLimiter struct {
	// represents the cost of a request (how many tokens it consumes)
	Rate int64

	// represents the max tokens capacity that the bucket can hold
	MaxTokens int64

	// tokens currently present in the bucket at any time (local cache for headers)
	currentToken int64

	// RefillInterval is the duration to refill one token
	RefillInterval time.Duration

	// client is redis Client
	Client *redis.Client

	// decide to hash redis key
	HashKey bool

	// logger for logging rate limit events
	logger *log.Logger

	// For testing - override the current time
	timeNow func() time.Time
}

// encodeKey function encodes received value parameter with base64
func encodeKey(value string) string {
	return b64.StdEncoding.EncodeToString([]byte(value))
}

// NewRateLimiter to received and define new RateLimiter struct
func NewRateLimiter(config *RateLimiter) (*RateLimiter, error) {
	// Initialize default logger if none is provided
	if config.logger == nil {
		config.logger = log.New(os.Stderr, "rate-limiter: ", log.LstdFlags)
	}

	// Default time function if not set
	if config.timeNow == nil {
		config.timeNow = time.Now
	}

	return config, nil
}

// IsRequestAllowed function is a method of the RateLimiter struct. It is responsible for determining whether a specific request should be allowed based on the rate limiting rules.
// This function interacts with Redis using an atomic Lua script to track and enforce the rate limit for a given key.
//
// Parameters:
//
// key (string): A unique identifier for the request, typically representing the client making the request, such as an IP address.
//
// Returns:
//
// bool: Returns true if the request is allowed, false otherwise.
func (rl *RateLimiter) IsRequestAllowed(key string) bool {
	// Local mutex is removed in favor of Redis atomicity

	// Ensure we have a logger to avoid nil pointer dereference
	logger := rl.logger
	if logger == nil {
		logger = log.New(os.Stderr, "rate-limiter: ", log.LstdFlags)
	}

	// Get current time (may be overridden for testing)
	now := time.Now
	if rl.timeNow != nil {
		now = rl.timeNow
	}
	nowNs := now().UnixNano()

	var sEnc string
	if rl.HashKey {
		sEnc = keyPrefix + encodeKey(key)
	} else {
		sEnc = keyPrefix + key
	}
	lastRefillKey := sEnc + lastRefillPrefix

	// Calculate TTL (Time To Live) to prevent memory leaks
	// Expire keys when the bucket would be fully refilled + some buffer.
	// Example: If it takes 1s to refill 1 token, and max is 10, it takes 10s to fill.
	// We'll set TTL to a safety margins, e.g., (RefillInterval * MaxTokens) + 60 seconds.
	// Or at least 1 hour if that calculation is small, to avoid threshing.
	// For simplicity here: Max(1 hour, RefillInterval * MaxTokens * 2)
	ttlSeconds := int64(time.Hour.Seconds())
	refillCycle := int64(rl.RefillInterval.Seconds()) * rl.MaxTokens * 2
	if refillCycle > ttlSeconds {
		ttlSeconds = refillCycle
	}

	// Execute Lua Script
	// ARGV[1]: cost (tokens needed for request) -> rl.Rate
	// ARGV[2]: max_tokens -> rl.MaxTokens
	// ARGV[3]: refill_interval_ns -> rl.RefillInterval.Nanoseconds()
	// ARGV[4]: current_time_ns -> nowNs
	// ARGV[5]: expiration_seconds -> ttlSeconds
	result, err := luaRequest.Run(context.Background(), rl.Client,
		[]string{sEnc, lastRefillKey},
		rl.Rate,
		rl.MaxTokens,
		rl.RefillInterval.Nanoseconds(),
		nowNs,
		ttlSeconds,
	).Slice()

	if err != nil {
		logger.Printf("Error running rate limit script: %v", err)
		return false // Fail safe: deny if Redis fails
	}

	// Lua returns {allowed (0/1), remaining_tokens}
	allowed := result[0].(int64) == 1
	remaining := result[1].(int64)

	// Update local state for headers (best effort, as this might be stale immediately)
	rl.currentToken = remaining

	return allowed
}

// refill and refillWithTime are no longer needed as logic is moved to Lua script
