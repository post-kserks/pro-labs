package httpserver

import (
	"fmt"
	"testing"
)

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
