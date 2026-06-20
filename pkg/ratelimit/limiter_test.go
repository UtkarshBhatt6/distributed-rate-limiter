package ratelimit

import (
	"context"
	"math/rand"
	"testing"
	"time"
)

const testRedisAddr = "localhost:6379"

func TestTokenBucketRateLimiter(t *testing.T) {
	// Clean up any existing state in local Redis for the test keys
	client := getRedisClient(testRedisAddr)
	ctx := context.Background()
	
	resource1 := rand.Uint64()
	resource2 := rand.Uint64()

	// Clean redis keys
	client.Del(ctx, "rate_limit:12345")

	// 1. Create a limiter with capacity 3, window size 3 seconds (refill rate = 1 token/sec)
	limiter, err := New(1, resource1, 3, 3, 1, testRedisAddr)
	if err != nil {
		t.Fatalf("Failed to create limiter: %v", err)
	}

	// 2. Consume all 3 tokens
	for i := 1; i <= 3; i++ {
		if !limiter.ShouldAllowRequest() {
			t.Errorf("Request %d should have been allowed", i)
		}
	}

	// 3. The 4th request should be blocked
	if limiter.ShouldAllowRequest() {
		t.Error("Request 4 should have been blocked (bucket empty)")
	}

	// 4. Wait for 1.1 seconds, which should refill at least 1 token
	time.Sleep(1100 * time.Millisecond)

	if !limiter.ShouldAllowRequest() {
		t.Error("Request should be allowed after waiting for refill")
	}

	// Another immediate request should be blocked
	if limiter.ShouldAllowRequest() {
		t.Error("Request should be blocked again after consuming refilled token")
	}

	// 5. Test separation of resources
	limiter2, err := New(2, resource2, 5, 10, 1, testRedisAddr)
	if err != nil {
		t.Fatalf("Failed to create limiter 2: %v", err)
	}

	// Resource 2 should be allowed even if Resource 1 is rate limited
	if !limiter2.ShouldAllowRequest() {
		t.Error("Resource 2 should be allowed independently")
	}
}
