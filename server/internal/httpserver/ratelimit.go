package httpserver

import (
	"encoding/json"
	"net/http"
	"sync"
	"time"

	"vaultdb/internal/iputil"
)

const maxRateLimitKeys = 100000

// RateLimiter — token bucket rate limiter.
type RateLimiter struct {
	mu              sync.Mutex
	tokens          map[string]*tokenBucket
	rate            int
	burst           int
	cleanupInterval time.Duration
	stopCh          chan struct{}
	maxKeys         int
	collector       metricsCollector
}

// metricsCollector is a minimal interface to avoid importing the metrics package.
type metricsCollector interface {
	IncRatelimitBlocked()
	SetRatelimitActiveKeys(int64)
	IncRatelimitEvictions()
}

// tokenBucket — token bucket for a single key.
type tokenBucket struct {
	tokens    float64
	lastTime  time.Time
	maxTokens float64
}

// NewRateLimiter creates a new rate limiter.
func NewRateLimiter(rate int, burst int) *RateLimiter {
	return NewRateLimiterWithCollector(rate, burst, nil)
}

// NewRateLimiterWithCollector creates a rate limiter with an optional metrics collector.
func NewRateLimiterWithCollector(rate int, burst int, c metricsCollector) *RateLimiter {
	if rate <= 0 {
		rate = 100
	}
	if burst <= 0 {
		burst = rate * 2
	}

	rl := &RateLimiter{
		tokens:          make(map[string]*tokenBucket),
		rate:            rate,
		burst:           burst,
		cleanupInterval: 5 * time.Minute,
		stopCh:          make(chan struct{}),
		maxKeys:         maxRateLimitKeys,
		collector:       c,
	}

	go rl.cleanupLoop()

	return rl
}

// Allow checks whether a request is allowed for the given key (IP address).
func (rl *RateLimiter) Allow(key string) bool {
	rl.mu.Lock()
	defer rl.mu.Unlock()

	bucket, ok := rl.tokens[key]
	if !ok {
		if len(rl.tokens) >= rl.maxKeys {
			rl.evictOldest()
		}
		bucket = &tokenBucket{
			tokens:    float64(rl.burst),
			lastTime:  time.Now(),
			maxTokens: float64(rl.burst),
		}
		rl.tokens[key] = bucket
	}

	now := time.Now()
	elapsed := now.Sub(bucket.lastTime).Seconds()
	bucket.tokens += elapsed * float64(rl.rate)
	if bucket.tokens > bucket.maxTokens {
		bucket.tokens = bucket.maxTokens
	}
	bucket.lastTime = now

	if bucket.tokens >= 1 {
		bucket.tokens--
		rl.updateActiveKeysMetric()
		return true
	}

	if rl.collector != nil {
		rl.collector.IncRatelimitBlocked()
	}
	rl.updateActiveKeysMetric()
	return false
}

// updateActiveKeysMetric updates the gauge for currently tracked keys.
func (rl *RateLimiter) updateActiveKeysMetric() {
	if rl.collector != nil {
		rl.collector.SetRatelimitActiveKeys(int64(len(rl.tokens)))
	}
}

// evictOldest removes the least recently used token bucket.
func (rl *RateLimiter) evictOldest() {
	var oldestKey string
	var oldestTime time.Time
	for key, bucket := range rl.tokens {
		if oldestKey == "" || bucket.lastTime.Before(oldestTime) {
			oldestKey = key
			oldestTime = bucket.lastTime
		}
	}
	if oldestKey != "" {
		delete(rl.tokens, oldestKey)
		if rl.collector != nil {
			rl.collector.IncRatelimitEvictions()
		}
	}
}

// Middleware returns HTTP middleware for rate limiting.
func (rl *RateLimiter) Middleware(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		key := iputil.ExtractClientIP(r, nil)

		if !rl.Allow(key) {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusTooManyRequests)
			_ = json.NewEncoder(w).Encode(map[string]interface{}{
				"status":  "error",
				"message": "rate limit exceeded",
			})
			return
		}

		next(w, r)
	}
}

// cleanupLoop periodically cleans up unused tokens.
func (rl *RateLimiter) cleanupLoop() {
	ticker := time.NewTicker(rl.cleanupInterval)
	defer ticker.Stop()

	for {
		select {
		case <-rl.stopCh:
			return
		case <-ticker.C:
			rl.doCleanup()
		}
	}
}

// Close stops the background cleanupLoop.
func (rl *RateLimiter) Close() {
	close(rl.stopCh)
}

// doCleanup removes token buckets that have not been used for more than 5 minutes.
func (rl *RateLimiter) doCleanup() {
	rl.mu.Lock()
	defer rl.mu.Unlock()

	now := time.Now()
	for key, bucket := range rl.tokens {
		if now.Sub(bucket.lastTime) > 5*time.Minute {
			delete(rl.tokens, key)
		}
	}
}
