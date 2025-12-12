# Rate Limiter

A robust, thread-safe, and distributed rate limiter for Go, designed for high-throughput applications. It implements the **Token Bucket** algorithm and supports both **Redis** (for distributed systems) and **In-Memory** (for single-instance apps) backends.

![License](https://img.shields.io/badge/license-MIT-blue.svg)
![Go Version](https://img.shields.io/badge/go-%3E%3D1.20-blue)

## 🚀 Features

*   **🛡️ Atomic Operations**: Leverages Redis Lua scripts to ensure strict rate limiting without race conditions in distributed environments.
*   **💾 Pluggable Storage**:
    *   **Redis**: First-class support for `go-redis/v9`. Ideal for microservices and load-balanced APIs.
    *   **In-Memory**: fast, thread-safe local storage. Perfect for unit tests or standalone binaries.
*   **⚙️ Functional Options**: Clean, idiomatic Go API for configuration (`WithRate`, `WithStore`, etc.).
*   **🔌 Middleware Ready**:
    *   Standard `net/http` middleware included.
    *   Specialized `Gin` middleware available in a sub-package.
*   **🧠 Memory Safe**: Automatic TTL management for Redis keys prevents zombie data and memory leaks.

## 📦 Installation

```bash
go get github.com/nccapo/rate-limiter
```

## 🛠️ Configuration & Usage

The library uses the **Functional Options** pattern for valid, flexible configuration.

### 1. Using Redis Storage (Recommended for Production)

Use this mode when running multiple instances of your application (e.g., behind a load balancer), so they share the same rate limit quotas.

```go
package main

import (
	"log"
	"time"

	"github.com/redis/go-redis/v9"
	rrl "github.com/nccapo/rate-limiter"
)

func main() {
	// 1. Initialize your Redis client
	rdb := redis.NewClient(&redis.Options{
		Addr:     "localhost:6379",
		Password: "", // no password set
		DB:       0,  // use default DB
	})

	// 2. Configure the Rate Limiter
	// NewRedisStore(client, hashKey)
	// - client: your redis connection
	// - hashKey: if true, keys are base64 encoded to avoid issues with special chars
	store := rrl.NewRedisStore(rdb, true)

	limiter, err := rrl.NewRateLimiter(
		rrl.WithRate(10),                    // Cost: 10 tokens per request (or use 1 for standard counting)
		rrl.WithMaxTokens(100),              // Capacity: Bucket holds 100 tokens max
		rrl.WithRefillInterval(time.Second), // Refill: Add query cost back continuously (e.g. 1 token/sec implied by rate logic if cost=1)
        // Wait, clarification on Refill logic: 
        // WithRate(N) sets the COST per request.
        // WithRefillInterval(d) sets how long it takes to refill ONE token.
        // CHECK: If you want 10 reqs/sec:
        // MaxTokens: 10
        // RefillInterval: 100ms (1s / 10)
        // Rate (Cost): 1
		rrl.WithStore(store),
	)
	if err != nil {
		log.Fatalf("Failed to create limiter: %v", err)
	}
}
```

### 2. Using In-Memory Storage

Use this mode for local development, testing, or simple single-instance applications where shared state isn't required.

```go
package main

import (
	"log"
	"time"
	rrl "github.com/nccapo/rate-limiter"
)

func main() {
	// No Redis needed!
	store := rrl.NewMemoryStore()

	limiter, err := rrl.NewRateLimiter(
		rrl.WithRate(1),                     // Cost of 1 token per check
		rrl.WithMaxTokens(50),               // Allow burst of 50 requests
		rrl.WithRefillInterval(time.Second), // Refill 1 token every second (1 req/sec sustainable)
		rrl.WithStore(store),
	)
	if err != nil {
		log.Fatal(err)
	}
}
```

### 📋 Available Options

| Option | Description | Default |
|--------|-------------|---------|
| `WithRate(int64)` | The number of tokens required for a single request (Cost). | `1` |
| `WithMaxTokens(int64)` | The maximum capacity of the bucket (Burst size). | `10` |
| `WithRefillInterval(duration)` | The time it takes to refill **one** token. | `1s` |
| `WithStore(Store)` | The storage backend (`RedisStore` or `MemoryStore`). | **Required** |
| `WithLogger(*log.Logger)` | Custom logger for debug/error events. | `os.Stderr` |

---

## 🚦 Middleware Usage

### Standard `net/http`

```go
import (
	"net/http"
	rrl "github.com/nccapo/rate-limiter"
)

func main() {
	// ... create limiter ...

	mux := http.NewServeMux()
	mux.HandleFunc("/", handler)

	// Wrap specific handlers or the entire mux
	mw := rrl.HTTPRateLimiter(rrl.HTTPRateLimiterConfig{
		Limiter: limiter,
		// Optional: Custom key function (IP is default)
		KeyFunc: func(r *http.Request) string {
			return r.Header.Get("X-API-Key")
		},
		// Optional: Custom rejection handler
		StatusHandler: func(w http.ResponseWriter, r *http.Request, limit, remaining int64) {
			w.WriteHeader(429)
			w.Write([]byte("Slow down!"))
		},
	})

	http.ListenAndServe(":8080", mw(mux))
}
```

### Gin Framework

The Gin middleware is decoupled into a separate package to keep the core library dependency-free.

```bash
go get github.com/nccapo/rate-limiter/gin
```

```go
import (
	"github.com/gin-gonic/gin"
	rrl "github.com/nccapo/rate-limiter"
	ginratelimit "github.com/nccapo/rate-limiter/gin"
)

func main() {
	// ... create limiter ...

	r := gin.Default()

	r.Use(ginratelimit.RateLimiter(rrl.HTTPRateLimiterConfig{
		Limiter: limiter,
		KeyFunc: func(r *http.Request) string {
			return r.ClientIP()
		},
	}))

	r.GET("/ping", func(c *gin.Context) {
		c.JSON(200, gin.H{"message": "pong"})
	})
	
	r.Run()
}
```

## 🤝 Contributing

Pull requests are welcome! For major changes, please open an issue first to discuss what you would like to change.

## 📄 License

[MIT](https://choosealicense.com/licenses/mit/)
