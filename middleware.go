package rrl

import (
	"log"
	"net"
	"net/http"
	"strconv"
)

const (
	HeaderRateLimit           = "X-RateLimit-Limit"
	HeaderRateLimitRemaining  = "X-RateLimit-Remaining"
	HeaderRateLimitRetryAfter = "X-RateLimit-Retry-After"
)

// HTTPRateLimiterConfig defines configuration options for the rate limiter middleware
type HTTPRateLimiterConfig struct {
	// Limiter is the rate limiter instance
	Limiter *RateLimiter

	// KeyFunc extracts a key from the request (defaults to client IP if not provided)
	KeyFunc func(r *http.Request) string

	// StatusHandler is called when a request is rejected (defaults to JSON response if not provided)
	StatusHandler func(w http.ResponseWriter, r *http.Request, limit, remaining int64)
}

// DefaultKeyFunc returns the client IP address from a request
func DefaultKeyFunc(r *http.Request) string {
	ip, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr // Fall back to full remote address if we can't parse it
	}
	return ip
}

// DefaultStatusHandler sends a standard HTTP 429 Too Many Requests response
func DefaultStatusHandler(w http.ResponseWriter, r *http.Request, limit, remaining int64) {
	w.Header().Set(HeaderRateLimit, strconv.FormatInt(limit, 10))
	w.Header().Set(HeaderRateLimitRemaining, strconv.FormatInt(remaining, 10))
	w.WriteHeader(http.StatusTooManyRequests)
	w.Write([]byte(`{"error":"too many requests"}`))
}

// HTTPRateLimiter returns a standard http middleware function for rate limiting
func HTTPRateLimiter(config HTTPRateLimiterConfig) func(http.Handler) http.Handler {
	if config.KeyFunc == nil {
		config.KeyFunc = DefaultKeyFunc
	}

	if config.StatusHandler == nil {
		config.StatusHandler = DefaultStatusHandler
	}

	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			key := config.KeyFunc(r)

			// Check rate limit
			result, err := config.Limiter.Allow(r.Context(), key)

			// Handle implementation errors (fail open/closed)
			if err != nil {
				log.Printf("Rate limit error: %v", err)
				http.Error(w, "Internal Server Error", http.StatusInternalServerError)
				return
			}

			// Set headers on success AND failure if possible, but middleware logic usually sets on rejection?
			// Standard behavior: Always set limits if known.
			// Let's modify StatusHandler to take Result or just call it here?
			// The StatusHandler signature is (w, r, limit, remaining).
			// We should probably update StatusHandler signature too, but that's a breaking change for the config struct.
			// However, this package is likely internal or v1.
			// Let's keep signature for now but pass result.Remaining.

			if !result.Allowed {
				log.Printf("Rate limit exceeded for key: %s", key)
				config.StatusHandler(w, r, config.Limiter.MaxTokens, result.Remaining)
				return
			}

			next.ServeHTTP(w, r)
		})
	}
}
