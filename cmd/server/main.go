// A simple HTTP server to demonstrate expected usage for the `ratelimit` package.
package main

import (
	"context"
	"crypto/sha1"
	"encoding/binary"
	"fmt"
	"math/rand"
	"net"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/prometheus/client_golang/prometheus/promhttp"

	"github.com/UtkarshBhatt6/distributed-rate-limiter/pkg/ratelimit"
)

func hashString(str string) uint64 {
	hash := sha1.Sum([]byte(str))
	return binary.LittleEndian.Uint64(hash[0:8])
}

type RateLimitConfig struct {
	Capacity int64
	Window   int64
}

type state struct {
	mu               sync.RWMutex
	rateLimiterMap   map[uint64]*ratelimit.Limiter
	redisAddr        string
	configs          map[string]RateLimitConfig
	configLastLoaded time.Time
}

func (state *state) getRateLimiter(resourceHash uint64) (limiter *ratelimit.Limiter, err error) {
	state.mu.RLock()
	limiter, ok := state.rateLimiterMap[resourceHash]
	state.mu.RUnlock()
	if ok {
		return limiter, nil
	}

	state.mu.Lock()
	defer state.mu.Unlock()

	// Double-checked lock
	if limiter, ok = state.rateLimiterMap[resourceHash]; ok {
		return limiter, nil
	}

	// initialize limiter
	limiter, err = ratelimit.New(rand.Uint64(), resourceHash, 5, state.redisAddr)
	if err == nil {
		state.rateLimiterMap[resourceHash] = limiter
	}
	return
}

func getClientIP(r *http.Request) string {
	// Check X-Forwarded-For header
	xff := r.Header.Get("X-Forwarded-For")
	if xff != "" {
		ips := strings.Split(xff, ",")
		return strings.TrimSpace(ips[0])
	}
	// Check X-Real-IP header
	xrip := r.Header.Get("X-Real-IP")
	if xrip != "" {
		return xrip
	}
	// Fallback to RemoteAddr
	ip, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return ip
}

func seedRateLimitConfigs(redisAddr string) error {
	client := ratelimit.GetRedisClient(redisAddr)
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	// Seed dynamic configs:
	// - /login: 5 requests per minute
	// - /api: 10 requests per minute
	// - default: 100 requests per minute
	err := client.HSet(ctx, "rate_limit_configs", map[string]interface{}{
		"/login":  "5:60",
		"/api":    "10:60",
		"default": "100:60",
	}).Err()
	if err != nil {
		return fmt.Errorf("failed to seed configurations: %w", err)
	}
	return nil
}

func (state *state) resolveConfig(path string) RateLimitConfig {
	state.mu.Lock()
	defer state.mu.Unlock()

	now := time.Now()
	// Reload configuration from Redis every 10 seconds to keep it dynamic
	if state.configs == nil || now.Sub(state.configLastLoaded) > 10*time.Second {
		client := ratelimit.GetRedisClient(state.redisAddr)
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()

		configs, err := client.HGetAll(ctx, "rate_limit_configs").Result()
		if err == nil {
			newConfigs := make(map[string]RateLimitConfig)
			for key, val := range configs {
				parts := strings.Split(val, ":")
				if len(parts) == 2 {
					capVal, _ := strconv.ParseInt(parts[0], 10, 64)
					winVal, _ := strconv.ParseInt(parts[1], 10, 64)
					newConfigs[key] = RateLimitConfig{
						Capacity: capVal,
						Window:   winVal,
					}
				}
			}
			state.configs = newConfigs
			state.configLastLoaded = now
		}
	}

	// Longest prefix match against config prefixes
	bestMatch := ""
	for prefix := range state.configs {
		if prefix == "default" {
			continue
		}
		if strings.HasPrefix(path, prefix) {
			if len(prefix) > len(bestMatch) {
				bestMatch = prefix
			}
		}
	}

	if bestMatch != "" {
		return state.configs[bestMatch]
	}

	// Fallback to default config
	if def, ok := state.configs["default"]; ok {
		return def
	}

	// Hardcoded fallback
	return RateLimitConfig{Capacity: 100, Window: 60}
}

func (h *state) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	clientIP := getClientIP(r)
	resource := r.URL.Path
	keyString := fmt.Sprintf("%s:%s", clientIP, resource)
	resourceHash := hashString(keyString)

	limiter, err := h.getRateLimiter(resourceHash)
	if err != nil {
		http.Error(w, http.StatusText(http.StatusInternalServerError), http.StatusInternalServerError)
		return
	}

	// Resolve the dynamic quota configuration for the current path
	cfg := h.resolveConfig(resource)

	allowed, remaining, resetTime, retryAfter := limiter.ShouldAllowRequest(cfg.Capacity, cfg.Window)

	// Write standard HTTP Rate-limiting headers
	w.Header().Set("X-RateLimit-Limit", strconv.FormatInt(cfg.Capacity, 10))
	w.Header().Set("X-RateLimit-Remaining", strconv.FormatInt(remaining, 10))
	w.Header().Set("X-RateLimit-Reset", strconv.FormatInt(resetTime, 10))

	if !allowed {
		w.Header().Set("Retry-After", strconv.FormatInt(retryAfter, 10))
		http.Error(w, http.StatusText(http.StatusTooManyRequests), http.StatusTooManyRequests)
		return
	}

	fmt.Fprintf(w, "Allowed request for resource %s for client %s\n", resource, clientIP)
}

func main() {
	// initialize handler state
	state := state{
		rateLimiterMap: make(map[uint64]*ratelimit.Limiter),
		redisAddr:      "localhost:6379",
	}

	// Seed rate limiting configurations in Redis on startup
	if err := seedRateLimitConfigs(state.redisAddr); err != nil {
		fmt.Printf("Warning: failed to seed dynamic configs: %v\n", err)
	}

	http.Handle("/metrics", promhttp.Handler())
	http.Handle("/", &state)
	fmt.Println("Starting server on :8091")
	err := http.ListenAndServe(":8091", nil)
	fmt.Println(err)
}
