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
func setupRateLimiter(t *testing.T, rate, maxTokens int64, interval time.Duration) (*RateLimiter, func(time.Time), func()) {
	client, mr := setupMockRedis(t)

	// Create a logger that discards output during tests
	testLogger := log.New(io.Discard, "", 0)

	// Create a mutable time reference for testing
	currentTime := time.Now()

	// Function to advance the mock time
	advanceTime := func(newTime time.Time) {
		currentTime = newTime
	}

	limiter, _ := NewRateLimiter(&RateLimiter{
		Rate:           rate,
		MaxTokens:      maxTokens,
		RefillInterval: interval,
		Client:         client,
		HashKey:        false,
		logger:         testLogger,
		timeNow: func() time.Time {
			return currentTime
		},
	})

	// Return cleanup function
	cleanup := func() {
		mr.Close()
	}

	return limiter, advanceTime, cleanup
}

// TestStandardHTTPIntegration tests integration with standard http package
func TestStandardHTTPIntegration(t *testing.T) {
	// Create a rate limiter with 1 request per second
	limiter, advanceTime, cleanup := setupRateLimiter(t, 1, 1, time.Second)
	defer cleanup()

	// Create a test handler
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		w.Write([]byte("success"))
	})

	// Apply rate limiting middleware
	rateLimitedHandler := HTTPRateLimiter(HTTPRateLimiterConfig{
		Limiter: limiter,
	})(handler)

	// First request should be allowed
	req1 := httptest.NewRequest("GET", "/", nil)
	rec1 := httptest.NewRecorder()
	rateLimitedHandler.ServeHTTP(rec1, req1)
	assert.Equal(t, http.StatusOK, rec1.Code, "First request should be allowed")

	// Second request should be blocked
	req2 := httptest.NewRequest("GET", "/", nil)
	rec2 := httptest.NewRecorder()
	rateLimitedHandler.ServeHTTP(rec2, req2)
	assert.Equal(t, http.StatusTooManyRequests, rec2.Code, "Second request should be blocked")

	// Advance time by 3 seconds
	advanceTime(time.Now().Add(3 * time.Second))

	// Try again after refill
	req3 := httptest.NewRequest("GET", "/", nil)
	rec3 := httptest.NewRecorder()
	rateLimitedHandler.ServeHTTP(rec3, req3)
	assert.Equal(t, http.StatusOK, rec3.Code, "Request after refill should be allowed")
}

// TestUserBasedRateLimiting tests using different keys for rate limiting
func TestUserBasedRateLimiting(t *testing.T) {
	// Create a rate limiter with 1 request per key
	limiter, _, cleanup := setupRateLimiter(t, 1, 1, time.Second)
	defer cleanup()

	// Create middleware with user-based rate limiting
	middleware := HTTPRateLimiter(HTTPRateLimiterConfig{
		Limiter: limiter,
		KeyFunc: func(r *http.Request) string {
			return r.Header.Get("User-ID")
		},
	})

	// Create a test handler
	handler := middleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		w.Write([]byte("success"))
	}))

	// Request for User 1
	req1 := httptest.NewRequest("GET", "/", nil)
	req1.Header.Set("User-ID", "user1")
	rec1 := httptest.NewRecorder()
	handler.ServeHTTP(rec1, req1)
	assert.Equal(t, http.StatusOK, rec1.Code, "First request for user1 should be allowed")

	// Second request for User 1 (should be blocked)
	req2 := httptest.NewRequest("GET", "/", nil)
	req2.Header.Set("User-ID", "user1")
	rec2 := httptest.NewRecorder()
	handler.ServeHTTP(rec2, req2)
	assert.Equal(t, http.StatusTooManyRequests, rec2.Code, "Second request for user1 should be blocked")

	// Request for User 2 (should be allowed)
	req3 := httptest.NewRequest("GET", "/", nil)
	req3.Header.Set("User-ID", "user2")
	rec3 := httptest.NewRecorder()
	handler.ServeHTTP(rec3, req3)
	assert.Equal(t, http.StatusOK, rec3.Code, "First request for user2 should be allowed")
}

// TestMultipleKeysRateLimiting tests using composite keys for rate limiting
func TestMultipleKeysRateLimiting(t *testing.T) {
	// Create a rate limiter with 1 request per key
	limiter, _, cleanup := setupRateLimiter(t, 1, 1, time.Second)
	defer cleanup()

	// Create middleware with composite key (endpoint + user)
	middleware := HTTPRateLimiter(HTTPRateLimiterConfig{
		Limiter: limiter,
		KeyFunc: func(r *http.Request) string {
			// Composite key: endpoint + user ID
			return r.URL.Path + ":" + r.Header.Get("User-ID")
		},
	})

	// Create a test handler
	handler := middleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		w.Write([]byte("success"))
	}))

	// Request for endpoint1 by user1
	req1 := httptest.NewRequest("GET", "/endpoint1", nil)
	req1.Header.Set("User-ID", "user1")
	rec1 := httptest.NewRecorder()
	handler.ServeHTTP(rec1, req1)
	assert.Equal(t, http.StatusOK, rec1.Code, "First request for endpoint1+user1 should be allowed")

	// Second request for endpoint1 by user1 (should be blocked)
	req2 := httptest.NewRequest("GET", "/endpoint1", nil)
	req2.Header.Set("User-ID", "user1")
	rec2 := httptest.NewRecorder()
	handler.ServeHTTP(rec2, req2)
	assert.Equal(t, http.StatusTooManyRequests, rec2.Code, "Second request for endpoint1+user1 should be blocked")

	// Request for endpoint2 by user1 (should be allowed)
	req3 := httptest.NewRequest("GET", "/endpoint2", nil)
	req3.Header.Set("User-ID", "user1")
	rec3 := httptest.NewRecorder()
	handler.ServeHTTP(rec3, req3)
	assert.Equal(t, http.StatusOK, rec3.Code, "Request for endpoint2+user1 should be allowed")

	// Request for endpoint1 by user2 (should be allowed)
	req4 := httptest.NewRequest("GET", "/endpoint1", nil)
	req4.Header.Set("User-ID", "user2")
	rec4 := httptest.NewRecorder()
	handler.ServeHTTP(rec4, req4)
	assert.Equal(t, http.StatusOK, rec4.Code, "Request for endpoint1+user2 should be allowed")
}
