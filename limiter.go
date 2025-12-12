package rrl

import (
	"context"
	b64 "encoding/base64"
	"errors"
	"log"
	"os"
	"time"
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

	// Store defines the storage backend for the rate limiter
	Store Store

	// logger for logging rate limit events
	logger *log.Logger

	// For testing - override the current time
	timeNow func() time.Time
}

// Option defines a functional configuration option for RateLimiter.
type Option func(*RateLimiter)

// WithRate sets the number of tokens consumed per request (default 1).
func WithRate(rate int64) Option {
	return func(rl *RateLimiter) {
		rl.Rate = rate
	}
}

// WithMaxTokens sets the maximum bucket size (burst capacity).
func WithMaxTokens(maxTokens int64) Option {
	return func(rl *RateLimiter) {
		rl.MaxTokens = maxTokens
	}
}

// WithRefillInterval sets the duration to refill one token.
func WithRefillInterval(interval time.Duration) Option {
	return func(rl *RateLimiter) {
		rl.RefillInterval = interval
	}
}

// WithStore sets the storage backend.
func WithStore(store Store) Option {
	return func(rl *RateLimiter) {
		rl.Store = store
	}
}

// WithLogger sets a custom logger.
func WithLogger(logger *log.Logger) Option {
	return func(rl *RateLimiter) {
		rl.logger = logger
	}
}

// encodeKey function encodes received value parameter with base64
func encodeKey(value string) string {
	return b64.StdEncoding.EncodeToString([]byte(value))
}

// NewRateLimiter creates a new RateLimiter with the given options.
func NewRateLimiter(opts ...Option) (*RateLimiter, error) {
	// Default configuration
	rl := &RateLimiter{
		Rate:           1,
		MaxTokens:      10,
		RefillInterval: time.Second,
		timeNow:        time.Now,
		logger:         log.New(os.Stderr, "rate-limiter: ", log.LstdFlags),
	}

	// Apply options
	for _, opt := range opts {
		opt(rl)
	}

	// Validate configuration
	if rl.Store == nil {
		return nil, errors.New("rate limiter store is required (use WithStore)")
	}
	if rl.Rate <= 0 {
		return nil, errors.New("rate must be greater than 0")
	}
	if rl.MaxTokens <= 0 {
		return nil, errors.New("max tokens must be greater than 0")
	}
	if rl.RefillInterval <= 0 {
		return nil, errors.New("refill interval must be greater than 0")
	}

	return rl, nil
}

// IsRequestAllowed checks if the request is allowed for the given key.
func (rl *RateLimiter) IsRequestAllowed(key string) bool {
	if rl.Store == nil {
		rl.logger.Printf("Store is not initialized")
		return false
	}

	// Delegate to the Store
	allowed, remaining, err := rl.Store.Allow(context.Background(), key, rl.Rate, rl.MaxTokens, rl.RefillInterval)
	if err != nil {
		rl.logger.Printf("Rate limit storage error: %v", err)
		return false // Fail safe
	}

	// Update local state for headers (best effort)
	rl.CurrentToken = remaining

	return allowed
}
