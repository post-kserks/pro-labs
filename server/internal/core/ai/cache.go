package ai

import (
	"container/list"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"sync"
	"time"
)

const defaultCacheCapacity = 1000

type cacheEntry struct {
	key       string
	value     []float64
	createdAt time.Time
}

// CachedEmbedder wraps an Embedder with an LRU cache.
type CachedEmbedder struct {
	inner    Embedder
	capacity int
	ttl      time.Duration
	mu       sync.RWMutex
	cache    map[string]*list.Element
	lru      *list.List
}

// NewCachedEmbedder creates a cached embedder wrapping the given inner embedder.
// capacity sets the maximum number of cached entries (0 uses default 1000).
// ttl sets the time-to-live for cache entries; zero means no expiration.
func NewCachedEmbedder(inner Embedder, capacity int, ttl time.Duration) *CachedEmbedder {
	if capacity <= 0 {
		capacity = defaultCacheCapacity
	}
	return &CachedEmbedder{
		inner:    inner,
		capacity: capacity,
		ttl:      ttl,
		cache:    make(map[string]*list.Element, capacity),
		lru:      list.New(),
	}
}

func cacheKey(text string) string {
	h := sha256.Sum256([]byte(text))
	return hex.EncodeToString(h[:])
}

func (c *CachedEmbedder) Embed(ctx context.Context, text string) ([]float64, error) {
	key := cacheKey(text)

	c.mu.RLock()
	if elem, ok := c.cache[key]; ok {
		entry := elem.Value.(*cacheEntry)
		if c.ttl == 0 || time.Since(entry.createdAt) < c.ttl {
			c.lru.MoveToFront(elem)
			c.mu.RUnlock()
			return entry.value, nil
		}
		c.mu.RUnlock()

		// Expired — evict under write lock
		c.mu.Lock()
		c.removeEntry(elem)
		c.mu.Unlock()
	} else {
		c.mu.RUnlock()
	}

	vec, err := c.inner.Embed(ctx, text)
	if err != nil {
		return nil, err
	}

	c.mu.Lock()
	defer c.mu.Unlock()

	// Re-check in case another goroutine inserted while we were calling inner
	if elem, ok := c.cache[key]; ok {
		entry := elem.Value.(*cacheEntry)
		if c.ttl == 0 || time.Since(entry.createdAt) < c.ttl {
			c.lru.MoveToFront(elem)
			return entry.value, nil
		}
		c.removeEntry(elem)
	}

	for c.lru.Len() >= c.capacity {
		c.evict()
	}

	entry := &cacheEntry{key: key, value: vec, createdAt: time.Now()}
	elem := c.lru.PushFront(entry)
	c.cache[key] = elem

	return vec, nil
}

func (c *CachedEmbedder) evict() {
	back := c.lru.Back()
	if back == nil {
		return
	}
	c.removeEntry(back)
}

func (c *CachedEmbedder) removeEntry(elem *list.Element) {
	entry := elem.Value.(*cacheEntry)
	c.lru.Remove(elem)
	delete(c.cache, entry.key)
}

// Len returns the current number of entries in the cache.
func (c *CachedEmbedder) Len() int {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.lru.Len()
}

// Purge removes all entries from the cache.
func (c *CachedEmbedder) Purge() {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.cache = make(map[string]*list.Element, c.capacity)
	c.lru.Init()
}
