package ratelimit

// TokenBucketScript is a Redis Lua script implementing the Token Bucket algorithm.
// KEYS[1]: Redis key representing the rate limiter bucket for the resource (hash).
// ARGV[1]: Capacity of the bucket (max tokens allowed, e.g. 100).
// ARGV[2]: Window size in seconds (e.g. 60).
//
// The script retrieves the current state (tokens and last_updated timestamp).
// It then calculates the tokens refilled since last_updated, caps them at capacity,
// checks if there's at least 1 token available, decrements the token count if allowed,
// and saves the updated state back to Redis with an expiration TTL.
// It returns 1 if allowed, or 0 if rate limited.
const TokenBucketScript = `
local key = KEYS[1]
local capacity = tonumber(ARGV[1])
local window = tonumber(ARGV[2])
local refill_rate = capacity / window -- tokens per second

-- Get current time from Redis server
local time = redis.call('TIME')
local now = tonumber(time[1]) + (tonumber(time[2]) / 1000000)

-- Get existing bucket state
local data = redis.call('HMGET', key, 'tokens', 'last_updated')
local tokens = tonumber(data[1])
local last_updated = tonumber(data[2])

if not tokens then
    -- Bucket doesn't exist yet, initialize it to capacity
    tokens = capacity
    last_updated = now
else
    -- Calculate refilled tokens based on time elapsed
    local elapsed = now - last_updated
    if elapsed > 0 then
        local refill = elapsed * refill_rate
        tokens = math.min(capacity, tokens + refill)
        last_updated = now
    end
end

-- Check if we have at least 1 token available
local allowed = 0
if tokens >= 1 then
    tokens = tokens - 1
    allowed = 1
    -- Save the updated state
    redis.call('HMSET', key, 'tokens', tokens, 'last_updated', last_updated)
    -- Set TTL to ensure key cleanup when idle
    redis.call('EXPIRE', key, math.ceil(window))
end

return allowed
`
