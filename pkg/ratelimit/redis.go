package ratelimit

import (
	"sync"

	"github.com/redis/go-redis/v9"
)

var (
	clientsMu sync.Mutex
	clients   = make(map[string]*redis.Client)
)

// getRedisClient returns a cached redis.Client for the given address,
// or creates a new one if it doesn't exist yet.
func getRedisClient(addr string) *redis.Client {
	clientsMu.Lock()
	defer clientsMu.Unlock()

	if client, ok := clients[addr]; ok {
		return client
	}

	client := redis.NewClient(&redis.Options{
		Addr: addr,
	})
	clients[addr] = client
	return client
}
