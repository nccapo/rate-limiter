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

	// MaxTokens represents the max tokens capacity that the bucket can hold
	MaxTokens int64

	// RefillInterval is the duration to refill one token
	RefillInterval time.Duration

	// Store defines the storage backend for the rate limiter
	Store Store

	// logger for logging rate limit events
	logger *log.Logger

	// For testing - override the current time
	timeNow func() time.Time

	// metrics recorder for observability
	metrics MetricsRecorder
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

// WithMetrics sets the metrics recorder.
func WithMetrics(m MetricsRecorder) Option {
	return func(rl *RateLimiter) {
		rl.metrics = m
	}
}

// WithStrictPacing configures the limiter to strict Leaky Bucket mode (no bursts).
// Effectively sets MaxTokens to 1 (or match Rate), ensuring requests are spaced evenly.
func WithStrictPacing() Option {
	return func(rl *RateLimiter) {
		// Strict pacing means we don't allow bursts.
		// If Rate is 1, MaxTokens should be 1.
		// If Rate is N, MaxTokens should be N to allow batch of size N but no more?
		// Usually "WithoutSlack" in uber-go/ratelimit means per() is enforced strictly.
		// For TokenBucket, setting MaxTokens = Rate is the closest approximation to "allow 1 batch" but no accumulation.
		// BETTER: MaxTokens = 1 means we can only ever hold 1 token.
		rl.MaxTokens = 1
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
		metrics:        &NoOpMetrics{},
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

// NewUnlimited creates a RateLimiter that allows all requests.
// Useful for testing or disabling limits.
func NewUnlimited() *RateLimiter {
	return &RateLimiter{
		Store: &noopStore{},
	}
}

// RateLimitResult holds the result of a rate limit check
type RateLimitResult struct {
	Allowed    bool
	Remaining  int64
	RetryAfter time.Duration
}

// Allow checks if the request is allowed for the given key and returns detailed info.
func (rl *RateLimiter) Allow(ctx context.Context, key string) (*RateLimitResult, error) {
	start := time.Now()
	if rl.Store == nil {
		rl.logger.Printf("Store is not initialized")
		return nil, errors.New("store is not initialized")
	}

	// Delegate to the Store
	allowed, remaining, retryAfter, err := rl.Store.Allow(ctx, key, rl.Rate, rl.MaxTokens, rl.RefillInterval)
	duration := time.Since(start)

	if rl.metrics != nil {
		rl.metrics.Record(ctx, key, allowed, duration, err)
	}

	if err != nil {
		rl.logger.Printf("Rate limit storage error: %v", err)
		return nil, err
	}

	return &RateLimitResult{
		Allowed:    allowed,
		Remaining:  remaining,
		RetryAfter: retryAfter,
	}, nil
}

// IsRequestAllowed checks if the request is allowed for the given key.
// Deprecated: Use Allow() instead for thread safety and detailed results.
func (rl *RateLimiter) IsRequestAllowed(key string) bool {
	res, err := rl.Allow(context.Background(), key)
	if err != nil {
		return false
	}
	return res.Allowed
}

// Wait blocks until the request is allowed or the context is cancelled.
// It uses the Store to determine how long to wait.
func (rl *RateLimiter) Wait(ctx context.Context, key string) error {
	for {
		// Check if context is already done
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}

		allowed, _, retryAfter, err := rl.Store.Allow(ctx, key, rl.Rate, rl.MaxTokens, rl.RefillInterval)
		if err != nil {
			return err
		}

		if allowed {
			return nil
		}

		// Wait for the required duration
		if retryAfter > 0 {
			timer := time.NewTimer(retryAfter)
			select {
			case <-ctx.Done():
				timer.Stop()
				return ctx.Err()
			case <-timer.C:
				// Retry loop
			}
		} else {
			// Should ideally not happen if allowed=false, but prevent busy loop
			time.Sleep(10 * time.Millisecond)
		}
	}
}

// noopStore is a store that allows everything
type noopStore struct{}

func (n *noopStore) Allow(ctx context.Context, key string, cost int64, maxTokens int64, refillInterval time.Duration) (bool, int64, time.Duration, error) {
	return true, maxTokens, 0, nil
}
