package httpserver

import (
	"encoding/json"
	"net"
	"net/http"
	"strings"
	"sync"
	"time"
)

// RateLimiter — token bucket rate limiter.
type RateLimiter struct {
	mu       sync.Mutex
	tokens   map[string]*tokenBucket
	rate     int           // tokens per second
	burst    int           // max tokens
	cleanupInterval time.Duration // cleanup interval
	stopCh   chan struct{}
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
		rate = 100 // по умолчанию 100 rps
	}
	if burst <= 0 {
		burst = rate * 2 // burst = 2x rate
	}

	rl := &RateLimiter{
		tokens:  make(map[string]*tokenBucket),
		rate:    rate,
		burst:   burst,
		cleanupInterval: 5 * time.Minute,
		stopCh:  make(chan struct{}),
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
		bucket = &tokenBucket{
			tokens:    float64(rl.burst),
			lastTime:  time.Now(),
			maxTokens: float64(rl.burst),
		}
		rl.tokens[key] = bucket
	}

	// Пополняем токены
	now := time.Now()
	elapsed := now.Sub(bucket.lastTime).Seconds()
	bucket.tokens += elapsed * float64(rl.rate)
	if bucket.tokens > bucket.maxTokens {
		bucket.tokens = bucket.maxTokens
	}
	bucket.lastTime = now

	// Проверяем наличие токена
	if bucket.tokens >= 1 {
		bucket.tokens--
		return true
	}

	return false
}

// extractClientIP returns the real client IP, preferring X-Forwarded-For
// and X-Real-IP headers over RemoteAddr.
func extractClientIP(r *http.Request) string {
	if xff := r.Header.Get("X-Forwarded-For"); xff != "" {
		parts := strings.Split(xff, ",")
		return strings.TrimSpace(parts[0])
	}
	if xri := r.Header.Get("X-Real-IP"); xri != "" {
		return xri
	}
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return host
}

// Middleware возвращает HTTP middleware для rate limiting.
func (rl *RateLimiter) Middleware(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		key := extractClientIP(r)

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
