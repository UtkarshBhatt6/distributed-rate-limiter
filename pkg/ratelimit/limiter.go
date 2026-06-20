package ratelimit

import (
	"context"
	"fmt"
	"strconv"
	"time"
)

type Limiter struct {
	id           uint64
	resourceHash uint64
	capacity     int64
	windowSize   int64
	subintervals int64
	redisAddr    string
	redisKey     string
	scriptSHA    string
}

// New creates and initializes a new rate Limiter.
// It also pre-loads the Token Bucket Lua script into Redis.
func New(id uint64, resourceHash uint64, capacity int64, windowSize int64, subintervals int64, redisAddr string) (*Limiter, error) {
	client := getRedisClient(redisAddr)

	// Context for loading the Lua script
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	// Load the Lua script into Redis to obtain its SHA
	sha, err := client.ScriptLoad(ctx, TokenBucketScript).Result()
	if err != nil {
		return nil, fmt.Errorf("failed to load token bucket lua script: %w", err)
	}

	redisKey := fmt.Sprintf("rate_limit:%d", resourceHash)

	return &Limiter{
		id:           id,
		resourceHash: resourceHash,
		capacity:     capacity,
		windowSize:   windowSize,
		subintervals: subintervals,
		redisAddr:    redisAddr,
		redisKey:     redisKey,
		scriptSHA:    sha,
	}, nil
}

// ShouldAllowRequest determines if a request should be allowed under the Token Bucket rate limit.
func (l *Limiter) ShouldAllowRequest() bool {
	client := getRedisClient(l.redisAddr)

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	// Execute the Lua script using EVALSHA
	// KEYS[1] is the key for the resource
	// ARGV[1] is the capacity
	// ARGV[2] is the window size in seconds
	result, err := client.EvalSha(ctx, l.scriptSHA, []string{l.redisKey}, l.capacity, l.windowSize).Result()
	if err != nil {
		// If the script is missing in cache (e.g. Redis restarted), fallback to EVAL
		if isNoScriptErr(err) {
			result, err = client.Eval(ctx, TokenBucketScript, []string{l.redisKey}, l.capacity, l.windowSize).Result()
			if err != nil {
				// Fallback to rate-limiting in case of Redis failure
				return false
			}
		} else {
			return false
		}
	}

	// Result should be 1 if allowed, 0 otherwise
	allowed, ok := result.(int64)
	if !ok {
		// Redis might return it as interface{} containing int or float
		if val, err := strconv.ParseInt(fmt.Sprintf("%v", result), 10, 64); err == nil {
			return val == 1
		}
		return false
	}

	return allowed == 1
}

func isNoScriptErr(err error) bool {
	if err == nil {
		return false
	}
	errStr := err.Error()
	return len(errStr) >= 8 && errStr[:8] == "NOSCRIPT"
}
