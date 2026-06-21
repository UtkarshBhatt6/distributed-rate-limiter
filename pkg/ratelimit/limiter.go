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
	subintervals int64
	redisAddr    string
	redisKey     string
	scriptSHA    string
}

// New creates and initializes a new rate Limiter.
// It also pre-loads the Token Bucket Lua script into Redis.
func New(id uint64, resourceHash uint64, subintervals int64, redisAddr string) (*Limiter, error) {
	client := GetRedisClient(redisAddr)

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
		subintervals: subintervals,
		redisAddr:    redisAddr,
		redisKey:     redisKey,
		scriptSHA:    sha,
	}, nil
}

// ShouldAllowRequest determines if a request should be allowed under the Token Bucket rate limit.
// It accepts capacity and windowSize dynamically for each request.
// It returns:
//  - allowed: whether the request is allowed.
//  - remaining: the remaining tokens in the bucket.
//  - resetTime: the Unix timestamp (seconds) when the bucket will be fully refilled.
func (l *Limiter) ShouldAllowRequest(capacity int64, windowSize int64) (bool, int64, int64) {
	resHashStr := strconv.FormatUint(l.resourceHash, 10)
	startTime := time.Now()
	defer func() {
		RedisLatencyHistogram.WithLabelValues(resHashStr).Observe(time.Since(startTime).Seconds())
	}()

	client := GetRedisClient(l.redisAddr)

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	// Execute the Lua script using EVALSHA
	// KEYS[1] is the key for the resource
	// ARGV[1] is the capacity
	// ARGV[2] is the window size in seconds
	result, err := client.EvalSha(ctx, l.scriptSHA, []string{l.redisKey}, capacity, windowSize).Result()
	if err != nil {
		// If the script is missing in cache (e.g. Redis restarted), fallback to EVAL
		if isNoScriptErr(err) {
			result, err = client.Eval(ctx, TokenBucketScript, []string{l.redisKey}, capacity, windowSize).Result()
			if err != nil {
				// Fallback to rate-limiting in case of Redis failure
				RequestsCounter.WithLabelValues(resHashStr, "error").Inc()
				return false, 0, 0
			}
		} else {
			RequestsCounter.WithLabelValues(resHashStr, "error").Inc()
			return false, 0, 0
		}
	}

	// Parse Lua multi-bulk response (returned as Go []interface{})
	var isAllowed bool
	var remaining int64
	var resetTime int64

	if slice, ok := result.([]interface{}); ok && len(slice) >= 3 {
		if allowedVal, ok := slice[0].(int64); ok {
			isAllowed = (allowedVal == 1)
		}
		if remainingVal, ok := slice[1].(int64); ok {
			remaining = remainingVal
		}
		if resetVal, ok := slice[2].(int64); ok {
			resetTime = resetVal
		}
	} else {
		// Fallback in case of parsing issue
		RequestsCounter.WithLabelValues(resHashStr, "error").Inc()
		return false, 0, 0
	}

	status := "allowed"
	if !isAllowed {
		status = "rejected"
	}
	RequestsCounter.WithLabelValues(resHashStr, status).Inc()

	return isAllowed, remaining, resetTime
}

func isNoScriptErr(err error) bool {
	if err == nil {
		return false
	}
	errStr := err.Error()
	return len(errStr) >= 8 && errStr[:8] == "NOSCRIPT"
}
