package rrl

import (
	"log"
	"net"
	"net/http"
	"strconv"
)

const (
	HeaderRateLimit          = "X-RateLimit-Limit"
	HeaderRateLimitRemaining = "X-RateLimit-Remaining"
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

			if !config.Limiter.IsRequestAllowed(key) {
				log.Printf("Rate limit exceeded for key: %s", key)
				config.StatusHandler(w, r, config.Limiter.MaxTokens, config.Limiter.CurrentToken)
				return
			}

			next.ServeHTTP(w, r)
		})
	}
}
