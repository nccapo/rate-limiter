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

// RateLimiter is struct based on Redis
type RateLimiter struct {
	// represents the cost of a request (how many tokens it consumes)
	Rate int64

	// represents the max tokens capacity that the bucket can hold
	MaxTokens int64

	// CurrentToken tokens currently present in the bucket at any time (local cache for headers)
	CurrentToken int64

	// RefillInterval is the duration to refill one token
	RefillInterval time.Duration

	// Client is redis Client
	// Deprecated: Use Store instead. If Client is set, a RedisStore will be created automatically.
	Client *redis.Client

	// Store defines the storage backend for the rate limiter
	Store Store

	// decide to hash redis key
	// Deprecated: Use valid Store configuration. HasKey logic is now handled by RedisStore.
	HashKey bool

	// logger for logging rate limit events
	logger *log.Logger

	// For testing - override the current time
	// Deprecated: This logic will primarily affect default RedisStore.
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

	// Backward Compatibility: If Store is not set but Client is, create a RedisStore
	if config.Store == nil && config.Client != nil {
		redisStore := NewRedisStore(config.Client, config.HashKey)
		// Inject testing time if present
		if config.timeNow != nil {
			redisStore.timeNow = config.timeNow
		}
		config.Store = redisStore
	}

	return config, nil
}

// IsRequestAllowed function is a method of the RateLimiter struct. It is responsible for determining whether a specific request should be allowed based on the rate limiting rules.
// This function interacts with the configured Store to enforce the rate limit.
//
// Parameters:
//
// key (string): A unique identifier for the request, typically representing the client making the request, such as an IP address.
//
// Returns:
//
// bool: Returns true if the request is allowed, false otherwise.
func (rl *RateLimiter) IsRequestAllowed(key string) bool {
	// Ensure we have a logger to avoid nil pointer dereference
	logger := rl.logger
	if logger == nil {
		logger = log.New(os.Stderr, "rate-limiter: ", log.LstdFlags)
	}

	if rl.Store == nil {
		logger.Printf("Store is not initialized")
		return false
	}

	// Delegate to the Store
	allowed, remaining, err := rl.Store.Allow(context.Background(), key, rl.Rate, rl.MaxTokens, rl.RefillInterval)
	if err != nil {
		logger.Printf("Rate limit storage error: %v", err)
		return false // Fail safe
	}

	// Update local state for headers (best effort)
	rl.CurrentToken = remaining

	return allowed
}
