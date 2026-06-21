package ratelimit

import "github.com/prometheus/client_golang/prometheus"

var (
	// RequestsCounter tracks total requests evaluated, partitioned by resource hash and status (allowed, rejected, error)
	RequestsCounter = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Name: "ratelimit_requests_total",
			Help: "Total number of requests evaluated by the rate limiter.",
		},
		[]string{"resource_hash", "status"},
	)

	// RedisLatencyHistogram tracks execution time of the Redis operation (Lua script)
	RedisLatencyHistogram = prometheus.NewHistogramVec(
		prometheus.HistogramOpts{
			Name:    "ratelimit_redis_latency_seconds",
			Help:    "Latency of redis operations in seconds.",
			Buckets: prometheus.DefBuckets,
		},
		[]string{"resource_hash"},
	)
)

func init() {
	prometheus.MustRegister(RequestsCounter)
	prometheus.MustRegister(RedisLatencyHistogram)
}
