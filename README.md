# Rate Limiter

This project showcases an implementation of a rate limiter middleware in Golang using Redis. The rate limiter restricts the number of requests a user can make to an API within a defined timeframe, helping to manage and control API usage effectively.

## Features

- Easy configuration - During configuration phase, you can decide how many token is used in per request, you can configure maximum quantity of tokens, token refill time. However, you can decide to hash redis key or not.
- Redis Integration - Utilizes Redis for efficient storage and retrieval of rate limiting data, ensuring scalability and performance.
- Understanding Response - Defined response is showed when the rate limit is exceeded.

## Installation

```shell
go get github.com/nccapo/rate-limiter
```

## Usage

Redis-based Rate Limiter

1. Implement the Redis-based rate limiter middleware as shown:

```go
package main

import (
	"net/http"
	"time"

	"github.com/nccapo/rate-limiter"
	"github.com/gin-gonic/gin"
	"github.com/redis/go-redis/v9"
)

func main() {
	r := gin.Default()

	client := redis.NewClient(&redis.Options{
            Addr: "localhost:6379",
	})

	// Call NewRateLimiter function from rrl package.
	limiter, err := rrl.NewRateLimiter(&rrl.RateLimiter{
            Rate:           1, // amount of each request as a token
            MaxTokens:      5, // maximum token quantity for requests
            RefillInterval: 15 * time.Second, // each token fill in 'X' time frame
            Client:         client, // redis client
            HashKey:        false, // make true if you want to hash redis key
	})
	if err != nil {
            log.Fatal(err)
	}

	// Use RateLimiterMiddleware from rrl package and pass limiter.
	// This middleware works for all routes in your application,
	// including static files served when you open a web browser.
	r.Use(rrl.RateLimiterMiddleware(limiter))

	r.GET("/", func(c *gin.Context) {
            c.JSON(http.StatusOK, gin.H{"message": "Welcome!"})
	})

	// Using this way allows the RateLimiterMiddleware to work for only specific routes.
	r.GET("/some", rrl.RateLimiterMiddleware(limiter), func(c *gin.Context) {
            c.JSON(http.StatusOK, gin.H{"message": "Some!"})
	})

	r.Run(":8080")
}

```

## Framework-Agnostic Rate Limiter

This package provides a rate limiter that can be used with any Go HTTP framework or directly with the standard library.

### Standard HTTP Example

```go
package main

import (
    "log"
    "net/http"
    "github.com/redis/go-redis/v9"
    "github.com/yourusername/rrl"
    "time"
)

func main() {
    // Initialize Redis client
    redisClient := redis.NewClient(&redis.Options{
        Addr: "localhost:6379",
    })

    // Create a rate limiter
    limiter, err := rrl.NewRateLimiter(&rrl.RateLimiter{
        Rate:           10,             // 10 requests
        MaxTokens:      100,            // Maximum token capacity
        RefillInterval: time.Second,    // Per second rate limit
        Client:         redisClient,    // Redis client
    })
    if err != nil {
        log.Fatal(err)
    }

    // Create a handler
    mux := http.NewServeMux()
    mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
        w.Write([]byte("Hello, World!"))
    })

    // Apply rate limiting middleware
    rateLimitedHandler := rrl.HTTPRateLimiter(rrl.HTTPRateLimiterConfig{
        Limiter: limiter,
        // Optional: Custom key function
        KeyFunc: func(r *http.Request) string {
            // Use API key instead of IP
            return r.Header.Get("X-API-Key")
        },
        // Optional: Custom handling for rate limited requests
        StatusHandler: func(w http.ResponseWriter, r *http.Request, limit, remaining int64) {
            w.WriteHeader(http.StatusTooManyRequests)
            w.Write([]byte("Rate limit exceeded. Try again later."))
        },
    })(mux)

    // Start server
    log.Println("Server starting on :8080")
    http.ListenAndServe(":8080", rateLimitedHandler)
}
```

### Gin Framework Example

```go
package main

import (
    "github.com/gin-gonic/gin"
    "github.com/redis/go-redis/v9"
    "github.com/yourusername/rrl"
    "log"
    "time"
)

func main() {
    // Initialize Redis client
    redisClient := redis.NewClient(&redis.Options{
        Addr: "localhost:6379",
    })

    // Create a rate limiter
    limiter, err := rrl.NewRateLimiter(&rrl.RateLimiter{
        Rate:           10,             // 10 requests
        MaxTokens:      100,            // Maximum token capacity
        RefillInterval: time.Second,    // Per second rate limit
        Client:         redisClient,    // Redis client
    })
    if err != nil {
        log.Fatal(err)
    }

    // Create Gin router
    r := gin.Default()

    // Apply rate limiting middleware
    r.Use(rrl.GinRateLimiter(rrl.HTTPRateLimiterConfig{
        Limiter: limiter,
    }))

    // Define routes
    r.GET("/", func(c *gin.Context) {
        c.String(200, "Hello, World!")
    })

    // Start server
    r.Run(":8080")
}
```

## Advanced Usage

### Custom Rate Limiting Keys

You can customize how the rate limiter identifies clients by providing a custom key function:

```go
// Limit by user ID from request context
limiterConfig := rrl.HTTPRateLimiterConfig{
    Limiter: limiter,
    KeyFunc: func(r *http.Request) string {
        // Get user ID from context (after authentication)
        userID := r.Context().Value("userID").(string)
        return "user:" + userID
    },
}
```

### Custom Rate Limit Responses

You can customize how rate limit errors are returned to clients:

```go
limiterConfig := rrl.HTTPRateLimiterConfig{
    Limiter: limiter,
    StatusHandler: func(w http.ResponseWriter, r *http.Request, limit, remaining int64) {
        // Return JSON error response
        w.Header().Set("Content-Type", "application/json")
        w.WriteHeader(http.StatusTooManyRequests)
        w.Write([]byte(`{
            "error": "rate_limit_exceeded",
            "message": "Too many requests, please try again later",
            "limit": ` + strconv.FormatInt(limit, 10) + `,
            "remaining": ` + strconv.FormatInt(remaining, 10) + `,
            "retry_after": "1s"
        }`))
    },
}
```

## License

This project is licensed under the MIT License.

## Contributing

Contributions are welcome! Please open an issue or submit a pull request for any improvements or bug fixes ;).
