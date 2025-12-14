package rrl

import (
	"context"
	"time"
)

// MetricsRecorder defines the interface for recording rate limiter metrics.
// Users can implement this to plug in Prometheus, StatsD, or logging.
type MetricsRecorder interface {
	// Record is called after a rate limit decision is made.
	// key: The rate limit key (e.g., user IP, API key)
	// allowed: Whether the request was allowed (true) or blocked (false)
	// duration: How long the check took (latency)
	// err: Any error that occurred during the check
	Record(ctx context.Context, key string, allowed bool, duration time.Duration, err error)
}

// NoOpMetrics is a default implementation that does nothing.
type NoOpMetrics struct{}

func (n *NoOpMetrics) Record(ctx context.Context, key string, allowed bool, duration time.Duration, err error) {
	// No-op
}
