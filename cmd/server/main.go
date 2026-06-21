// A simple HTTP server to demonstrate expected usage for the `ratelimit` package.
package main

import (
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

type state struct {
	mu             sync.RWMutex
	rateLimiterMap map[uint64]*ratelimit.Limiter
	redisAddr      string
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

	// initialize limiter to allow 100 requests/window, with sliding window of 60 seconds and subintervals of 5 seconds.
	limiter, err = ratelimit.New(rand.Uint64(), resourceHash, 100, 60, 5, state.redisAddr)
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

	allowed, remaining, resetTime := limiter.ShouldAllowRequest()

	// Write standard HTTP Rate-limiting headers
	w.Header().Set("X-RateLimit-Limit", strconv.FormatInt(limiter.GetCapacity(), 10))
	w.Header().Set("X-RateLimit-Remaining", strconv.FormatInt(remaining, 10))
	w.Header().Set("X-RateLimit-Reset", strconv.FormatInt(resetTime, 10))

	if !allowed {
		now := time.Now().Unix()
		retryAfter := resetTime - now
		if retryAfter < 0 {
			retryAfter = 0
		}
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
	http.Handle("/metrics", promhttp.Handler())
	http.Handle("/", &state)
	fmt.Println("Starting server on :8091")
	err := http.ListenAndServe(":8091", nil)
	fmt.Println(err)
}
