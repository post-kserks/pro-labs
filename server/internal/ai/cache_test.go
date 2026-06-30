package ai

import (
	"context"
	"sync/atomic"
	"testing"
	"time"
)

func TestCacheHit(t *testing.T) {
	var callCount int32
	inner := &countingEmbedder{count: &callCount}
	cached := NewCachedEmbedder(inner, 100, 0)

	ctx := context.Background()
	vec1, err := cached.Embed(ctx, "hello")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	vec2, err := cached.Embed(ctx, "hello")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if atomic.LoadInt32(&callCount) != 1 {
		t.Errorf("expected 1 call for cache hit, got %d", callCount)
	}

	for i := range vec1 {
		if vec1[i] != vec2[i] {
			t.Errorf("index %d: cached value differs", i)
		}
	}
}

func TestCacheMiss(t *testing.T) {
	var callCount int32
	inner := &countingEmbedder{count: &callCount}
	cached := NewCachedEmbedder(inner, 100, 0)

	ctx := context.Background()
	cached.Embed(ctx, "hello")
	cached.Embed(ctx, "world")

	if atomic.LoadInt32(&callCount) != 2 {
		t.Errorf("expected 2 calls for different texts, got %d", callCount)
	}
}

func TestCacheEviction(t *testing.T) {
	var callCount int32
	inner := &countingEmbedder{count: &callCount}
	cached := NewCachedEmbedder(inner, 3, 0)

	ctx := context.Background()
	cached.Embed(ctx, "a")
	cached.Embed(ctx, "b")
	cached.Embed(ctx, "c")

	// Cache is full (capacity=3). "a" is now the oldest.
	cached.Embed(ctx, "d")

	// "a" should have been evicted
	if cached.Len() != 3 {
		t.Errorf("expected 3 entries after eviction, got %d", cached.Len())
	}

	atomic.StoreInt32(&callCount, 0)
	cached.Embed(ctx, "a")
	if atomic.LoadInt32(&callCount) != 1 {
		t.Error("expected cache miss for evicted entry 'a'")
	}
}

func TestCacheTTL(t *testing.T) {
	var callCount int32
	inner := &countingEmbedder{count: &callCount}
	cached := NewCachedEmbedder(inner, 100, 50*time.Millisecond)

	ctx := context.Background()
	cached.Embed(ctx, "hello")

	if atomic.LoadInt32(&callCount) != 1 {
		t.Fatalf("expected 1 call, got %d", callCount)
	}

	time.Sleep(60 * time.Millisecond)

	atomic.StoreInt32(&callCount, 0)
	cached.Embed(ctx, "hello")

	if atomic.LoadInt32(&callCount) != 1 {
		t.Error("expected cache miss after TTL expiry")
	}
}

func TestCachePurge(t *testing.T) {
	var callCount int32
	inner := &countingEmbedder{count: &callCount}
	cached := NewCachedEmbedder(inner, 100, 0)

	ctx := context.Background()
	cached.Embed(ctx, "hello")

	if cached.Len() != 1 {
		t.Fatalf("expected 1 entry, got %d", cached.Len())
	}

	cached.Purge()

	if cached.Len() != 0 {
		t.Errorf("expected 0 entries after purge, got %d", cached.Len())
	}
}

// countingEmbedder returns a deterministic vector and counts calls.
type countingEmbedder struct {
	count *int32
}

func (e *countingEmbedder) Embed(_ context.Context, text string) ([]float64, error) {
	atomic.AddInt32(e.count, 1)
	var h uint32
	for _, b := range []byte(text) {
		h = h*31 + uint32(b)
	}
	vec := make([]float64, 4)
	for i := range vec {
		vec[i] = float64(int(h>>(i*8))&0xFF) / 255.0
	}
	return vec, nil
}
