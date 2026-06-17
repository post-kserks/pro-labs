package httpserver

import (
	"encoding/json"
	"net"
	"net/http"
	"strings"
	"sync"
	"time"
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
}

// tokenBucket — token bucket for a single key.
type tokenBucket struct {
	tokens    float64
	lastTime  time.Time
	maxTokens float64
}

// NewRateLimiter создаёт новый rate limiter.
func NewRateLimiter(rate int, burst int) *RateLimiter {
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
	}

	go rl.cleanupLoop()

	return rl
}

// Allow проверяет, разрешён ли запрос для данного ключа (IP address).
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
		return true
	}

	return false
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
	}
}

// extractClientIP extracts the real client IP from the request.
// trustedProxies is a list of CIDR ranges of trusted reverse proxies.
// If the request comes from a trusted proxy, X-Forwarded-For is used.
// Otherwise, RemoteAddr is used directly — spoofed headers are ignored.
func extractClientIP(r *http.Request, trustedProxies []net.IPNet) string {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		host = r.RemoteAddr
	}

	clientIP := net.ParseIP(host)
	isTrusted := false
	if clientIP != nil {
		for _, cidr := range trustedProxies {
			if cidr.Contains(clientIP) {
				isTrusted = true
				break
			}
		}
	}

	if isTrusted {
		if xff := r.Header.Get("X-Forwarded-For"); xff != "" {
			parts := strings.Split(xff, ",")
			trimmed := strings.TrimSpace(parts[0])
			if trimmed != "" {
				return trimmed
			}
		}
		if xri := r.Header.Get("X-Real-IP"); xri != "" {
			return strings.TrimSpace(xri)
		}
	}

	return host
}

// Middleware возвращает HTTP middleware для rate limiting.
func (rl *RateLimiter) Middleware(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		key := extractClientIP(r, nil)

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

// cleanupLoop периодически очищает неиспользуемые токены.
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

// Close останавливает фоновый cleanupLoop.
func (rl *RateLimiter) Close() {
	close(rl.stopCh)
}

// doCleanup удаляет токены bucket которые не использовались более 5 минут.
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
