package rrl

import (
	"log"
	"math/rand"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/redis/go-redis/v9"
	"github.com/stretchr/testify/assert"
)

func setupConcurrentRedis(t *testing.T) (*redis.Client, *miniredis.Miniredis) {
	mr, err := miniredis.Run()
	if err != nil {
		t.Fatalf("Error creating mock Redis server: %v", err)
	}
	// miniredis implementation of Lua is atomic, so it simulates Redis behavior correctly.

	client := redis.NewClient(&redis.Options{
		Addr: mr.Addr(),
		DB:   0,
	})

	return client, mr
}

func TestRateLimiter_Concurrency(t *testing.T) {
	client, mr := setupConcurrentRedis(t)
	defer mr.Close()

	// Config: 50 tokens max, refill very slowly (so no refill during test)
	maxTokens := int64(50)
	limiter, err := NewRateLimiter(&RateLimiter{
		Rate:           1,
		MaxTokens:      maxTokens,
		RefillInterval: 1 * time.Hour, // effectively no refill during test
		Client:         client,
		HashKey:        false,
		logger:         log.Default(),
	})
	assert.NoError(t, err)

	// We'll spawn 100 concurrent requests.
	// Only 50 should succeed.
	totalRequests := 100
	var allowedCount int32

	var wg sync.WaitGroup
	wg.Add(totalRequests)

	// Barrier to try to start them all at once
	startTrigger := make(chan struct{})

	for i := 0; i < totalRequests; i++ {
		go func() {
			defer wg.Done()
			<-startTrigger // Wait for start signal
			// Add random jitter to ensure we are hitting it slightly realistically but still concurrently
			time.Sleep(time.Duration(rand.Intn(100)) * time.Microsecond)

			if limiter.IsRequestAllowed("concurrent-user") {
				atomic.AddInt32(&allowedCount, 1)
			}
		}()
	}

	close(startTrigger) // Unleash all goroutines
	wg.Wait()

	t.Logf("Allowed: %d, Expected: %d", allowedCount, maxTokens)
	assert.Equal(t, maxTokens, int64(allowedCount), "Only exactly MaxTokens requests should be allowed in a race-free implementation")
}
