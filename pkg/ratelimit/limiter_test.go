package ratelimit

import (
	"context"
	"math/rand"
	"strconv"
	"testing"
	"time"

	"github.com/prometheus/client_golang/prometheus/testutil"
)

const testRedisAddr = "localhost:6379"

func TestTokenBucketRateLimiter(t *testing.T) {
	// Clean up any existing state in local Redis for the test keys
	client := GetRedisClient(testRedisAddr)
	ctx := context.Background()

	resource1 := rand.Uint64()
	resource2 := rand.Uint64()

	// Clean redis keys
	client.Del(ctx, "rate_limit:12345")

	// 1. Create a limiter
	limiter, err := New(1, resource1, 1, testRedisAddr)
	if err != nil {
		t.Fatalf("Failed to create limiter: %v", err)
	}

	// 2. Consume all 3 tokens (using capacity 3, window size 3)
	allowed, remaining, resetTime, _ := limiter.ShouldAllowRequest(3, 3)
	if !allowed {
		t.Error("Request 1 should have been allowed")
	}
	if remaining != 2 {
		t.Errorf("Request 1: expected remaining=2, got %d", remaining)
	}
	now := time.Now().Unix()
	if resetTime < now || resetTime > now+3 {
		t.Errorf("Request 1: unexpected resetTime %d (now=%d)", resetTime, now)
	}

	allowed, remaining, _, _ = limiter.ShouldAllowRequest(3, 3)
	if !allowed || remaining != 1 {
		t.Errorf("Request 2: allowed=%v, remaining=%d (expected remaining=1)", allowed, remaining)
	}

	allowed, remaining, _, _ = limiter.ShouldAllowRequest(3, 3)
	if !allowed || remaining != 0 {
		t.Errorf("Request 3: allowed=%v, remaining=%d (expected remaining=0)", allowed, remaining)
	}

	// 3. The 4th request should be blocked
	allowed, remaining, resetTime, retryAfter := limiter.ShouldAllowRequest(3, 3)
	if allowed {
		t.Error("Request 4 should have been blocked (bucket empty)")
	}
	if remaining != 0 {
		t.Errorf("Request 4: expected remaining=0, got %d", remaining)
	}
	if retryAfter != 1 {
		t.Errorf("Request 4: expected retryAfter=1, got %d", retryAfter)
	}

	// Check metrics
	res1Str := strconv.FormatUint(resource1, 10)
	allowedCount := testutil.ToFloat64(RequestsCounter.WithLabelValues(res1Str, "allowed"))
	if allowedCount != 3 {
		t.Errorf("Expected 3 allowed requests in metrics, got %f", allowedCount)
	}

	rejectedCount := testutil.ToFloat64(RequestsCounter.WithLabelValues(res1Str, "rejected"))
	if rejectedCount != 1 {
		t.Errorf("Expected 1 rejected request in metrics, got %f", rejectedCount)
	}

	// 4. Wait for 1.1 seconds, which should refill at least 1 token
	time.Sleep(1100 * time.Millisecond)

	allowed, _, _, _ = limiter.ShouldAllowRequest(3, 3)
	if !allowed {
		t.Error("Request should be allowed after waiting for refill")
	}

	// Another immediate request should be blocked
	allowed, _, _, _ = limiter.ShouldAllowRequest(3, 3)
	if allowed {
		t.Error("Request should be blocked again after consuming refilled token")
	}

	// 5. Test separation of resources
	limiter2, err := New(2, resource2, 1, testRedisAddr)
	if err != nil {
		t.Fatalf("Failed to create limiter 2: %v", err)
	}

	// Resource 2 should be allowed even if Resource 1 is rate limited
	allowed, _, _, _ = limiter2.ShouldAllowRequest(5, 10)
	if !allowed {
		t.Error("Resource 2 should be allowed independently")
	}
}
