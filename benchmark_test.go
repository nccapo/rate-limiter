package rrl

import (
	"context"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/redis/go-redis/v9"
)

func BenchmarkMemoryStore_Allow(b *testing.B) {
	store := NewMemoryStore()
	limiter, _ := NewRateLimiter(
		WithRate(100),
		WithMaxTokens(100),
		WithRefillInterval(time.Second),
		WithStore(store),
	)

	key := "benchmark-key"

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		limiter.IsRequestAllowed(key)
	}
}

func BenchmarkRedisStore_Allow(b *testing.B) {
	// Setup miniredis for benchmarking overhead (not real network latency)
	mr, err := miniredis.Run()
	if err != nil {
		b.Fatal(err)
	}
	defer mr.Close()

	client := redis.NewClient(&redis.Options{
		Addr: mr.Addr(),
	})
	defer client.Close()

	store := NewRedisStore(client, false)
	limiter, _ := NewRateLimiter(
		WithRate(100),
		WithMaxTokens(100),
		WithRefillInterval(time.Second),
		WithStore(store),
	)

	key := "benchmark-key"

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		limiter.IsRequestAllowed(key)
	}
}

func BenchmarkMemoryStore_Wait(b *testing.B) {
	store := NewMemoryStore()
	limiter, _ := NewRateLimiter(
		WithRate(1000000), // High rate to avoid actual waiting
		WithMaxTokens(1000000),
		WithRefillInterval(time.Millisecond),
		WithStore(store),
	)

	ctx := context.Background()
	key := "benchmark-key"

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		limiter.Wait(ctx, key)
	}
}
