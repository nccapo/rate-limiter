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

		// Check rate limit
		result, err := config.Limiter.Allow(c.Request.Context(), key)
		if err != nil {
			log.Printf("Rate limit error: %v", err)
			// Fail open or closed depending on requirements?
			// Assuming fail closed or open? Original failed closed if Store error in IsRequestAllowed.
			// Let's match original behavior: if store error, we treated as not allowed (IsRequestAllowed returned false).
			// But IsRequestAllowed now returns false on error.
			// Let's handle it gracefully.
			c.JSON(http.StatusInternalServerError, gin.H{"error": "rate limit error"})
			c.Abort()
			return
		}

		c.Header(rrl.HeaderRateLimit, strconv.FormatInt(config.Limiter.MaxTokens, 10))
		c.Header(rrl.HeaderRateLimitRemaining, strconv.FormatInt(result.Remaining, 10))
		c.Header(rrl.HeaderRateLimitRetryAfter, strconv.FormatInt(int64(result.RetryAfter.Seconds()), 10))

		if !result.Allowed {
			log.Printf("Rate limit exceeded for key: %s", key)
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
