package rrl

import (
	"context"
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

func setupTestLimiter(t *testing.T, rate int64, maxTokens int64) (*RateLimiter, func(time.Time)) {
	// Setup miniredis
	mr, err := miniredis.Run()
	if err != nil {
		t.Fatalf("Error creating mock Redis server: %v", err)
	}

	client := redis.NewClient(&redis.Options{
		Addr: mr.Addr(),
		DB:   0,
	})

	// Create a logger that discards output during tests
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

	limiter, _ := NewRateLimiter(
		WithRate(rate),
		WithMaxTokens(maxTokens),
		WithRefillInterval(1*time.Second),
		WithStore(redisStore),
		WithLogger(testLogger),
	)
	// Override limiter timeNow
	if limiter != nil {
		limiter.timeNow = func() time.Time {
			return currentTime
		}
	}
	return limiter, advanceTime
}

func TestHTTPRateLimiter(t *testing.T) {
	limiter, advanceTime := setupTestLimiter(t, 2, 5)

	// Create a simple HTTP handler for testing
	nextHandler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		w.Write([]byte("success"))
	})

	// Create middleware with the limiter
	middleware := HTTPRateLimiter(HTTPRateLimiterConfig{
		Limiter: limiter,
		KeyFunc: func(r *http.Request) string {
			return "test-user" // Use a constant key for testing
		},
	})

	// Apply middleware to our test handler
	handler := middleware(nextHandler)

	// Test cases to verify rate limiting
	tests := []struct {
		name           string
		expectedStatus int
		advanceTime    time.Duration
	}{
		{"First Request", http.StatusOK, 0},
		{"Second Request", http.StatusOK, 0},
		{"Third Request (Limit Exceeded)", http.StatusTooManyRequests, 0},
		{"Wait for Refill", http.StatusOK, 1 * time.Second},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if tt.advanceTime > 0 {
				// Advance time for tests that require it
				advanceTime(time.Now().Add(tt.advanceTime))
			}

			// Create a test request
			req := httptest.NewRequest("GET", "/test", nil)
			rec := httptest.NewRecorder()

			// Process the request
			handler.ServeHTTP(rec, req)

			// Check the response status
			assert.Equal(t, tt.expectedStatus, rec.Code)

			// If not rate limited, we should see the success message
			if tt.expectedStatus == http.StatusOK {
				assert.Equal(t, "success", rec.Body.String())
			}

			// If rate limited, check headers
			if tt.expectedStatus == http.StatusTooManyRequests {
				assert.NotEmpty(t, rec.Header().Get(HeaderRateLimit))
				assert.NotEmpty(t, rec.Header().Get(HeaderRateLimitRemaining))
			}
		})
	}
}

func TestCustomKeyFunction(t *testing.T) {
	limiter, _ := setupTestLimiter(t, 1, 1)

	// Create middleware with custom key function
	middleware := HTTPRateLimiter(HTTPRateLimiterConfig{
		Limiter: limiter,
		KeyFunc: func(r *http.Request) string {
			return r.Header.Get("X-API-Key") // Extract key from header
		},
	})

	handler := middleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	// First request with API key "user1"
	req1 := httptest.NewRequest("GET", "/test", nil)
	req1.Header.Set("X-API-Key", "user1")
	rec1 := httptest.NewRecorder()
	handler.ServeHTTP(rec1, req1)
	assert.Equal(t, http.StatusOK, rec1.Code)

	// Second request with same API key "user1" (should be rate limited)
	req2 := httptest.NewRequest("GET", "/test", nil)
	req2.Header.Set("X-API-Key", "user1")
	rec2 := httptest.NewRecorder()
	handler.ServeHTTP(rec2, req2)
	assert.Equal(t, http.StatusTooManyRequests, rec2.Code)

	// Request with different API key "user2" (should not be rate limited)
	req3 := httptest.NewRequest("GET", "/test", nil)
	req3.Header.Set("X-API-Key", "user2")
	rec3 := httptest.NewRecorder()
	handler.ServeHTTP(rec3, req3)
	assert.Equal(t, http.StatusOK, rec3.Code)
}

func TestCustomStatusHandler(t *testing.T) {
	limiter, _ := setupTestLimiter(t, 1, 1)

	// Create middleware with custom status handler
	middleware := HTTPRateLimiter(HTTPRateLimiterConfig{
		Limiter: limiter,
		StatusHandler: func(w http.ResponseWriter, r *http.Request, limit, remaining int64) {
			w.Header().Set("Custom-Header", "rate-limited")
			w.WriteHeader(http.StatusForbidden) // Use 403 instead of 429
			w.Write([]byte("custom error message"))
		},
	})

	handler := middleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	// First request (allowed)
	req1 := httptest.NewRequest("GET", "/test", nil)
	rec1 := httptest.NewRecorder()
	handler.ServeHTTP(rec1, req1)
	assert.Equal(t, http.StatusOK, rec1.Code)

	// Second request (rate limited)
	req2 := httptest.NewRequest("GET", "/test", nil)
	rec2 := httptest.NewRecorder()
	handler.ServeHTTP(rec2, req2)

	// Verify our custom status handler was used
	assert.Equal(t, http.StatusForbidden, rec2.Code)
	assert.Equal(t, "custom error message", rec2.Body.String())
	assert.Equal(t, "rate-limited", rec2.Header().Get("Custom-Header"))
}

// MiddlewareFailStore for testing circuit breaker in middleware
type MiddlewareFailStore struct {
	shouldFail bool
}

func (f *MiddlewareFailStore) Allow(ctx context.Context, key string, cost int64, maxTokens int64, refillInterval time.Duration) (bool, int64, time.Duration, error) {
	if f.shouldFail {
		return false, 0, 0, assert.AnError
	}
	return true, 10, 0, nil
}

func TestMiddlewareWithCircuitBreaker(t *testing.T) {
	// 1. Setup Circuit Breaker
	backend := &MiddlewareFailStore{shouldFail: true}
	cbStore := NewCircuitBreakerStore(backend, CircuitBreakerConfig{
		Threshold: 1, // Trip immediately
		Timeout:   time.Minute,
	})

	limiter, _ := NewRateLimiter(
		WithStore(cbStore),
	)

	// 2. Trip it (Direct call)
	// First call fails matching Threshold=1
	cbStore.Allow(context.Background(), "test", 1, 1, time.Second)

	// 3. Setup Middleware
	middleware := HTTPRateLimiter(HTTPRateLimiterConfig{
		Limiter: limiter,
	})
	handler := middleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	// 4. Hit it via Middleware
	// The Circuit Breaker is now OPEN. It should return allowed=false, err=nil.
	// Middleware should see this as rate limit exceeded (429), NOT 500.
	req := httptest.NewRequest("GET", "/test", nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	assert.Equal(t, http.StatusTooManyRequests, rec.Code, "Expected 429 when Circuit Breaker is open")
}
