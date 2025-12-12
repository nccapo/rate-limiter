package rrl

import (
	"context"
	"time"
)

// Store defines the interface for rate limiter storage backends.
// It abstracts the atomic "check-and-update" logic.
type Store interface {
	// Allow checks if a request is allowed and consumes tokens if so.
	//
	// key: The unique identifier for the user/client.
	// cost: Number of tokens to consume (usually 1).
	// maxTokens: The bucket capacity (burst limit).
	// refillInterval: The time to refill 1 token.
	//
	// Returns:
	// allowed: true if request is within limits.
	// remaining: tokens left in the bucket.
	// err: any backend error.
	Allow(ctx context.Context, key string, cost int64, maxTokens int64, refillInterval time.Duration) (allowed bool, remaining int64, err error)
}
