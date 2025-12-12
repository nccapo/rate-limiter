package ginratelimit

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/gin-gonic/gin"
	rrl "github.com/nccapo/rate-limiter"
	"github.com/redis/go-redis/v9"
	"github.com/stretchr/testify/assert"
)

// setupMockRedis creates a new miniredis server for testing
func setupMockRedis(t *testing.T) (*redis.Client, *miniredis.Miniredis) {
	mr, err := miniredis.Run()
	if err != nil {
		t.Fatalf("Error creating mock Redis server: %v", err)
	}

	client := redis.NewClient(&redis.Options{
		Addr: mr.Addr(),
		DB:   0,
	})

	return client, mr
}

// setupRateLimiter creates a new rate limiter for testing
func setupRateLimiter(t *testing.T, rate, maxTokens int64, interval time.Duration) (*rrl.RateLimiter, func()) {
	client, mr := setupMockRedis(t)

	// Note: We need a way to inject time into the limiter.
	// The original NewRateLimiter used a functional option style on the struct but not exported safely?
	// The struct 'RateLimiter' has 'timeNow'. In the original package it was unexported field 'timeNow'.
	// We cannot set unexported fields from this external package!
	// This is a problem. 'limiter_test.go' was in package 'rrl' so it could access unexported fields.
	// Here we are in 'ginratelimit'.
	//
	// Workaround: For this integration test, we might strictly rely on real time or expose 'timeNow' via a legitimate config option.
	// BUT, adding functional options was Phase 2 Step 3.
	//
	// Alternative: Just rely on miniredis behavior?
	// The Lua script uses ARGV[4] for 'now_ns'. The Go code calls `now()`.
	// The `now()` comes from `rl.timeNow`.
	// If I cannot set `rl.timeNow`, I cannot time-travel easily from outside.
	//
	// However, `TestGinIntegration` in `integration_test.go` used `setupRateLimiter` which was in the same package `rrl`.
	//
	// Option 1: Export `TimeNow` field in `RateLimiter` (quick fix, slightly ugly API).
	// Option 2: Put `gin` tests in `rrl` package? No, because `gin` middleware is in `ginratelimit`.
	// Option 3: Use `time.Sleep` instead of time travel (slow tests).
	// Option 4: Add `WithTimeFunc` option to `NewRateLimiter` (Cleanest).
	//
	// Let's go with Option 4 or Option 1. Option 1 is faster refactor.
	// Let's change `timeNow` to `TimeNow` in `limiter.go`.

	limiter, _ := rrl.NewRateLimiter(
		rrl.WithRate(rate),
		rrl.WithMaxTokens(maxTokens),
		rrl.WithRefillInterval(interval),
		rrl.WithStore(rrl.NewRedisStore(client, false)),
	)

	// We need to fix visibility of logger and timeNow in 'limiter.go' first.
	// Or we just test "black box" style (wait 1 second).

	// Let's sleep for 1s in the test. It's only 1 test.
	// Or, let's export them.

	// Return cleanup function
	cleanup := func() {
		mr.Close()
	}

	return limiter, cleanup
}

func TestGinIntegration(t *testing.T) {
	// Create a rate limiter with 1 request per second
	// Using real time sleeping for now to avoid extensive refactoring of 'rrl' internals right now.
	limiter, cleanup := setupRateLimiter(t, 1, 1, time.Second)
	defer cleanup()

	// Set Gin to test mode
	gin.SetMode(gin.TestMode)

	// Create a Gin router
	router := gin.New()

	// Apply rate limiter middleware
	router.Use(RateLimiter(rrl.HTTPRateLimiterConfig{
		Limiter: limiter,
		KeyFunc: func(r *http.Request) string {
			return "test-key" // Use a fixed key for testing
		},
	}))

	// Add a test route
	router.GET("/test", func(c *gin.Context) {
		c.String(http.StatusOK, "success")
	})

	// First request should be allowed
	req1, _ := http.NewRequest("GET", "/test", nil)
	rec1 := httptest.NewRecorder()
	router.ServeHTTP(rec1, req1)
	assert.Equal(t, http.StatusOK, rec1.Code, "First request should be allowed")
	assert.Equal(t, "success", rec1.Body.String())

	// Second request should be blocked (immediate)
	req2, _ := http.NewRequest("GET", "/test", nil)
	rec2 := httptest.NewRecorder()
	router.ServeHTTP(rec2, req2)
	assert.Equal(t, http.StatusTooManyRequests, rec2.Code, "Second request should be blocked")
}
