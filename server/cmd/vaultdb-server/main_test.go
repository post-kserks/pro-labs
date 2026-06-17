package main

import (
	"testing"
	"time"
)

func TestConnectionRateLimiter(t *testing.T) {
	limiter := NewConnectionRateLimiter(10, 5)
	if limiter.rate != 10 {
		t.Errorf("rate = %v, want 10", limiter.rate)
	}
	if limiter.maxTokens != 5 {
		t.Errorf("maxTokens = %v, want 5", limiter.maxTokens)
	}
	if limiter.tokens != 5 {
		t.Errorf("tokens = %v, want 5", limiter.tokens)
	}

	for i := 0; i < 5; i++ {
		if !limiter.Allow() {
			t.Fatalf("Allow() = false on request %d, want true", i+1)
		}
	}

	if limiter.Allow() {
		t.Error("Allow() = true after burst exhausted, want false")
	}
}

func TestConnectionRateLimiterTokenRefill(t *testing.T) {
	limiter := NewConnectionRateLimiter(100, 1)

	limiter.Allow()

	limiter.lastTime = time.Now().Add(-100 * time.Millisecond)

	if !limiter.Allow() {
		t.Error("Allow() = false after refill period, want true")
	}
}

func TestConnectionRateLimiterBurstCap(t *testing.T) {
	limiter := NewConnectionRateLimiter(1000, 5)

	limiter.lastTime = time.Now().Add(-10 * time.Second)

	if !limiter.Allow() {
		t.Error("Allow() = false, want true")
	}

	if limiter.tokens > limiter.maxTokens {
		t.Errorf("tokens = %v exceeded maxTokens = %v", limiter.tokens, limiter.maxTokens)
	}
}
