# Rate Limiter

A robust, thread-safe, and distributed rate limiter for Go, designed for high-throughput applications. It implements the **Token Bucket** algorithm and supports both **Redis** (for distributed systems) and **In-Memory** (for single-instance apps) backends.

![License](https://img.shields.io/badge/license-MIT-blue.svg)
![Go Version](https://img.shields.io/badge/go-%3E%3D1.20-blue)
[![Go Report Card](https://goreportcard.com/badge/github.com/nccapo/rate-limiter)](https://goreportcard.com/report/github.com/nccapo/rate-limiter)
[![GoDoc](https://godoc.org/github.com/nccapo/rate-limiter?status.svg)](https://godoc.org/github.com/nccapo/rate-limiter)
[![Build Status](https://github.com/nccapo/rate-limiter/actions/workflows/go.yml/badge.svg)](https://github.com/nccapo/rate-limiter/actions)
[![codecov](https://codecov.io/gh/nccapo/rate-limiter/branch/master/graph/badge.svg)](https://codecov.io/gh/nccapo/rate-limiter)

## 🚀 Features

*   **🛡️ Atomic Operations**: Leverages Redis Lua scripts to ensure strict rate limiting without race conditions in distributed environments.
*   **💾 Pluggable Storage**:
    *   **Redis**: First-class support for `go-redis/v9`. Ideal for microservices and load-balanced APIs.
    *   **In-Memory**: fast, thread-safe local storage. Perfect for unit tests or standalone binaries.
*   **⚙️ Functional Options**: Clean, idiomatic Go API for configuration (`WithRate`, `WithStore`, etc.).
*   **⏮️ Blocking Support**: `Wait(ctx, key)` method for client-side throttling (like `uber-go/ratelimit`'s `Take`).
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
	// - client: your redis connection (UniversalClient: supports Cluster/Ring)
	// - hashKey: if true, keys are base64 encoded to avoid issues with special chars
	store := rrl.NewRedisStore(rdb, true)

	limiter, err := rrl.NewRateLimiter(
		rrl.WithRate(10),                    // Cost: 10 tokens per request (or use 1 for standard counting)
		rrl.WithMaxTokens(100),              // Capacity: Bucket holds 100 tokens max
		rrl.WithRefillInterval(time.Second), // Refill: Add query cost back continuously
		rrl.WithStore(store),
	)
	if err != nil {
		log.Fatalf("Failed to create limiter: %v", err)
	}
}
```

### 2. Client-Side Throttling (Blocking)

If you are writing a worker or client that sends requests, you can use `Wait()` to automatically sleep until a token is available. This mimics `uber-go/ratelimit`'s `Take()` behavior.

```go
func worker(ctx context.Context, limiter *rrl.RateLimiter) {
    for {
        // Blocks until request is allowed
        if err := limiter.Wait(ctx, "worker-id"); err != nil {
            return // Context cancelled
        }
        
        // Do heavy work...
        performTask()
    }
}
```

### 3. Strict Pacing (Leaky Bucket Style)

To enforce strict spacing between requests (no bursts), use `WithStrictPacing()`.

```go
limiter, _ := rrl.NewRateLimiter(
    rrl.WithRate(1),
    rrl.WithRefillInterval(100 * time.Millisecond), // 10 reqs/sec
    rrl.WithStrictPacing(), // MaxTokens = 1 (No bursts!)
    rrl.WithStore(store),
)
```

### 4. Unlimited (Testing)

For tests where you want to disable blocking completely:

```go
limiter := rrl.NewUnlimited()
```

### 📋 Available Options

| Option | Description | Default |
|--------|-------------|---------|
| `WithRate(int64)` | The number of tokens required for a single request (Cost). | `1` |
| `WithMaxTokens(int64)` | The maximum capacity of the bucket (Burst size). | `10` |
| `WithStrictPacing()` | Sets `MaxTokens` to 1. Disables bursts, ensuring strict spacing. | `false` |
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

## 📊 Benchmarks

Hardware: Apple M1 Pro

```text
BenchmarkMemoryStore_Allow-10    13665328        85.44 ns/op       0 B/op       0 allocs/op
BenchmarkRedisStore_Allow-10       14238     85246 ns/op      208 B/op       6 allocs/op
BenchmarkMemoryStore_Wait-10      5834898       197.6 ns/op      48 B/op       1 allocs/op
```

*   **MemoryStore**: Ultra-low latency (~85ns), zero allocations.
*   **RedisStore**: Dependent on network (mocked here, showing ~85µs overhead for client/lua parsing).

## 🆚 Comparison

| Feature | `nccapo/rate-limiter` | `uber-go/ratelimit` |
| :--- | :---: | :---: |
| **Algorithm** | Token Bucket (Allow Bursts) | Leaky Bucket (Smooth) |
| **Distributed** | ✅ Yes (Redis) | ❌ No (Local only) |
| **Atomic** | ✅ Yes (Lua Scripts) | ✅ Yes (Atomic CAS) |
| **Blocking Wait** | ✅ Yes (`Wait`) | ✅ Yes (`Take`) |
| **Strict Pacing** | ✅ Yes (`WithStrictPacing`) | ✅ Yes (`WithoutSlack`) |
| **Middleware** | ✅ Yes (Http & Gin) | ❌ No |
