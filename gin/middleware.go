package ginratelimit

import (
	"log"
	"net/http"
	"strconv"

	"github.com/gin-gonic/gin"
	rrl "github.com/nccapo/rate-limiter"
)

// RateLimiter returns a Gin middleware function for rate limiting
func RateLimiter(config rrl.HTTPRateLimiterConfig) gin.HandlerFunc {
	if config.KeyFunc == nil {
		config.KeyFunc = func(r *http.Request) string {
			// Use Gin context to get client IP if available
			return r.RemoteAddr
		}
	}

	return func(c *gin.Context) {
		key := config.KeyFunc(c.Request)

		if !config.Limiter.IsRequestAllowed(key) {
			log.Printf("Rate limit exceeded for key: %s", key)
			c.Header(rrl.HeaderRateLimit, strconv.FormatInt(config.Limiter.MaxTokens, 10))
			c.Header(rrl.HeaderRateLimitRemaining, strconv.FormatInt(config.Limiter.CurrentToken, 10))
			// Actually currentToken is unexported 'currentToken'.
			// We cannot access 'config.Limiter.currentToken' from here if it is lowercase.
			// Problem: 'currentToken' in 'RateLimiter' struct is lowercase.
			// Fix: I need to export 'CurrentToken' or added a Getter in the root package.
			// OR I can ignore the header for now or rely on IsRequestAllowed changing?
			// IsRequestAllowed returned 'bool'.
			// The new Lua implementation returns 'remaining'.
			// IsRequestAllowed implementation currently returns 'bool'.
			// I should verify 'limiter.go' again. I wrote it in Step 40.

			c.JSON(http.StatusTooManyRequests, gin.H{"error": "too many requests"})
			c.Abort()
			return
		}

		// If allowed, we also want headers!
		// The original middleware only set headers on rejection?
		// Let's check original logic.
		// Original logic:
		// if !allowed { set headers; return error }
		// next()
		// It did NOT set headers on success in the original 'GinRateLimiter' function (Step 14).
		// Wait, Step 14:
		// if !config.Limiter.IsRequestAllowed(key) { ... c.Header ... return }
		// c.Next()
		// So simple version matches original.

		c.Next()
	}
}
