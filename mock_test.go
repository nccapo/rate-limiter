package rrl

import (
	"io"
	"log"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/redis/go-redis/v9"
	"github.com/stretchr/testify/assert"
)

// setupMockTest creates all the components needed for testing
func setupMockTest(t *testing.T) (*RateLimiter, func(time.Time), func()) {
	// Create a miniredis instance
	mr, err := miniredis.Run()
	if err != nil {
		t.Fatalf("Error creating mock Redis server: %v", err)
	}

	// Create a Redis client that connects to the mock
	client := redis.NewClient(&redis.Options{
		Addr: mr.Addr(),
		DB:   0,
	})

	// Create a logger that doesn't output during tests
	testLogger := log.New(io.Discard, "", 0)

	// Create a mutable time reference for testing
	currentTime := time.Now()

	// Function to advance the mock time
	advanceTime := func(newTime time.Time) {
		currentTime = newTime
	}

	// Create Redis Store
	redisStore := NewRedisStore(client, false)
	redisStore.timeNow = func() time.Time {
		return currentTime
	}

	// Create a rate limiter with extremely restrictive limits for testing
	limiter, _ := NewRateLimiter(
		WithRate(1),
		WithMaxTokens(1),
		WithRefillInterval(1*time.Second),
		WithStore(redisStore),
		WithLogger(testLogger),
	)
	limiter.timeNow = func() time.Time {
		return currentTime
	}

	// Return cleanup function
	cleanup := func() {
		mr.Close()
	}

	return limiter, advanceTime, cleanup
}

// Test standard HTTP middleware
func TestStandardHTTPMiddleware(t *testing.T) {
	limiter, advanceTime, cleanup := setupMockTest(t)
	defer cleanup()

	// Create test handler
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		w.Write([]byte("success"))
	})

	// Apply rate limiting middleware
	rateLimited := HTTPRateLimiter(HTTPRateLimiterConfig{
		Limiter: limiter,
		KeyFunc: func(r *http.Request) string {
			return "fixed-key" // Use a constant key for predictable results
		},
	})(handler)

	// First request should be allowed
	req1 := httptest.NewRequest("GET", "/", nil)
	rec1 := httptest.NewRecorder()
	rateLimited.ServeHTTP(rec1, req1)
	assert.Equal(t, http.StatusOK, rec1.Code, "First request should be allowed")
	assert.Equal(t, "success", rec1.Body.String())

	// Second request should be blocked (rate limit: 1 req/sec)
	req2 := httptest.NewRequest("GET", "/", nil)
	rec2 := httptest.NewRecorder()
	rateLimited.ServeHTTP(rec2, req2)
	assert.Equal(t, http.StatusTooManyRequests, rec2.Code, "Second request should be blocked")

	// Advance time by 2 seconds for the next request
	advanceTime(time.Now().Add(2 * time.Second))

	// After time passes, next request should be allowed
	req3 := httptest.NewRequest("GET", "/", nil)
	rec3 := httptest.NewRecorder()
	rateLimited.ServeHTTP(rec3, req3)
	assert.Equal(t, http.StatusOK, rec3.Code, "Request after time passes should be allowed")
	assert.Equal(t, "success", rec3.Body.String())
}

// Test custom key function
func TestCustomKeyMiddleware(t *testing.T) {
	limiter, _, cleanup := setupMockTest(t)
	defer cleanup()

	// Create middleware with custom key function
	middleware := HTTPRateLimiter(HTTPRateLimiterConfig{
		Limiter: limiter,
		KeyFunc: func(r *http.Request) string {
			return r.Header.Get("User-ID") // Use User-ID header as the key
		},
	})

	// Apply to test handler
	handler := middleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		w.Write([]byte("success"))
	}))

	// Request from User1
	req1 := httptest.NewRequest("GET", "/", nil)
	req1.Header.Set("User-ID", "user1")
	rec1 := httptest.NewRecorder()
	handler.ServeHTTP(rec1, req1)
	assert.Equal(t, http.StatusOK, rec1.Code, "First request from user1 should be allowed")

	// Second request from User1 (should be blocked)
	req2 := httptest.NewRequest("GET", "/", nil)
	req2.Header.Set("User-ID", "user1")
	rec2 := httptest.NewRecorder()
	handler.ServeHTTP(rec2, req2)
	assert.Equal(t, http.StatusTooManyRequests, rec2.Code, "Second request from user1 should be blocked")

	// Request from User2 (different key, should be allowed)
	req3 := httptest.NewRequest("GET", "/", nil)
	req3.Header.Set("User-ID", "user2")
	rec3 := httptest.NewRecorder()
	handler.ServeHTTP(rec3, req3)
	assert.Equal(t, http.StatusOK, rec3.Code, "Request from user2 should be allowed")
}

// Test custom status handler
func TestCustomStatusHandlerMiddleware(t *testing.T) {
	limiter, _, cleanup := setupMockTest(t)
	defer cleanup()

	// Create middleware with custom status handler
	middleware := HTTPRateLimiter(HTTPRateLimiterConfig{
		Limiter: limiter,
		StatusHandler: func(w http.ResponseWriter, r *http.Request, limit, remaining int64) {
			w.Header().Set("Custom-Header", "rate-limited")
			w.WriteHeader(http.StatusForbidden) // Use 403 instead of 429
			w.Write([]byte("custom error message"))
		},
	})

	// Apply to test handler
	handler := middleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		w.Write([]byte("success"))
	}))

	// First request (allowed)
	req1 := httptest.NewRequest("GET", "/", nil)
	rec1 := httptest.NewRecorder()
	handler.ServeHTTP(rec1, req1)
	assert.Equal(t, http.StatusOK, rec1.Code, "First request should be allowed")

	// Second request (rate limited with custom status)
	req2 := httptest.NewRequest("GET", "/", nil)
	rec2 := httptest.NewRecorder()
	handler.ServeHTTP(rec2, req2)
	assert.Equal(t, http.StatusForbidden, rec2.Code, "Second request should return custom status code")
	assert.Equal(t, "custom error message", rec2.Body.String(), "Response should have custom message")
	assert.Equal(t, "rate-limited", rec2.Header().Get("Custom-Header"), "Response should have custom header")
}
