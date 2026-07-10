package httpserver

import (
	"fmt"
	"sync/atomic"
	"testing"
)

// mockMetricsCollector implements metricsCollector for testing.
type mockMetricsCollector struct {
	blocked    atomic.Int64
	activeKeys atomic.Int64
	evictions  atomic.Int64
}

func (m *mockMetricsCollector) IncRatelimitBlocked()           { m.blocked.Add(1) }
func (m *mockMetricsCollector) SetRatelimitActiveKeys(n int64) { m.activeKeys.Store(n) }
func (m *mockMetricsCollector) IncRatelimitEvictions()         { m.evictions.Add(1) }

func TestRateLimiterMemoryDoS(t *testing.T) {
	rl := &RateLimiter{
		tokens:  make(map[string]*tokenBucket),
		rate:    100,
		burst:   200,
		maxKeys: 1000,
		stopCh:  make(chan struct{}),
	}
	defer rl.Close()

	for i := 0; i < 1000; i++ {
		rl.Allow(fmt.Sprintf("10.0.0.%d", i))
	}

	if len(rl.tokens) != 1000 {
		t.Fatalf("expected 1000 keys, got %d", len(rl.tokens))
	}

	rl.Allow("10.0.0.new")

	if len(rl.tokens) != 1000 {
		t.Fatalf("expected 1000 keys after eviction, got %d", len(rl.tokens))
	}

	if _, ok := rl.tokens["10.0.0.new"]; !ok {
		t.Fatal("expected new key to be present after eviction")
	}
}

func TestRateLimiterEvictionPicksOldest(t *testing.T) {
	rl := &RateLimiter{
		tokens:  make(map[string]*tokenBucket),
		rate:    100,
		burst:   200,
		maxKeys: 3,
		stopCh:  make(chan struct{}),
	}
	defer rl.Close()

	rl.Allow("first")
	rl.Allow("second")
	rl.Allow("third")

	if len(rl.tokens) != 3 {
		t.Fatalf("expected 3 keys, got %d", len(rl.tokens))
	}

	rl.Allow("fourth")

	if len(rl.tokens) != 3 {
		t.Fatalf("expected 3 keys after eviction, got %d", len(rl.tokens))
	}

	if _, ok := rl.tokens["first"]; ok {
		t.Fatal("expected 'first' to be evicted (oldest)")
	}

	for _, key := range []string{"second", "third", "fourth"} {
		if _, ok := rl.tokens[key]; !ok {
			t.Fatalf("expected key %q to be present", key)
		}
	}
}

func TestRateLimiterMetricsBlocked(t *testing.T) {
	mc := &mockMetricsCollector{}
	rl := NewRateLimiterWithCollector(1, 1, mc)
	defer rl.Close()

	// First request should succeed (burst=1).
	if !rl.Allow("ip1") {
		t.Fatal("first request should be allowed")
	}

	// Second request should be blocked.
	if rl.Allow("ip1") {
		t.Fatal("second request should be blocked")
	}

	if got := mc.blocked.Load(); got != 1 {
		t.Fatalf("blocked count = %d, want 1", got)
	}
}

func TestRateLimiterMetricsActiveKeys(t *testing.T) {
	mc := &mockMetricsCollector{}
	rl := NewRateLimiterWithCollector(100, 200, mc)
	defer rl.Close()

	rl.Allow("10.0.0.1")
	rl.Allow("10.0.0.2")

	if got := mc.activeKeys.Load(); got != 2 {
		t.Fatalf("active keys = %d, want 2", got)
	}
}

func TestRateLimiterMetricsEvictions(t *testing.T) {
	mc := &mockMetricsCollector{}
	rl := NewRateLimiterWithCollector(100, 200, mc)
	rl.maxKeys = 3
	defer rl.Close()

	rl.Allow("first")
	rl.Allow("second")
	rl.Allow("third")
	rl.Allow("fourth") // triggers eviction

	if got := mc.evictions.Load(); got != 1 {
		t.Fatalf("evictions = %d, want 1", got)
	}
}
