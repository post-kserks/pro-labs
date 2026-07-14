package executor

import (
	"container/list"
	"sync"
	"time"
)

const defaultResultCacheSize = 256
const defaultResultCacheTTL = 30 * time.Second

// CachedResult — cached SELECT query result.
type CachedResult struct {
	result    *Result
	tables    map[string]bool // affected tables (for invalidation)
	createdAt time.Time
}

// ResultCache — LRU cache for SELECT query results.
// Automatically invalidated on INSERT/UPDATE/DELETE.
// Supports TTL for stale entries.
type ResultCache struct {
	mu        sync.RWMutex
	cache     map[string]*list.Element
	lru       *list.List
	maxSize   int
	ttl       time.Duration
	hitCount  int64
	missCount int64
}

type resultCacheEntry struct {
	key   string
	value *CachedResult
}

// NewResultCache creates a new result cache.
func NewResultCache(maxSize int, ttl time.Duration) *ResultCache {
	if maxSize <= 0 {
		maxSize = defaultResultCacheSize
	}
	if ttl <= 0 {
		ttl = defaultResultCacheTTL
	}
	return &ResultCache{
		cache:   make(map[string]*list.Element, maxSize),
		lru:     list.New(),
		maxSize: maxSize,
		ttl:     ttl,
	}
}

// Get returns cached result or nil if not found/expired.
func (rc *ResultCache) Get(key string) *Result {
	rc.mu.Lock()
	defer rc.mu.Unlock()

	elem, ok := rc.cache[key]
	if !ok {
		rc.missCount++
		return nil
	}

	entry := elem.Value.(*resultCacheEntry)

	// Check TTL
	if time.Since(entry.value.createdAt) > rc.ttl {
		rc.removeEntry(elem)
		rc.missCount++
		return nil
	}

	// Move to front of LRU
	rc.lru.MoveToFront(elem)
	rc.hitCount++
	return entry.value.result
}

// Put stores result in cache.
func (rc *ResultCache) Put(key string, result *Result, tables map[string]bool) {
	rc.mu.Lock()
	defer rc.mu.Unlock()

	// If key already exists — update
	if elem, ok := rc.cache[key]; ok {
		entry := elem.Value.(*resultCacheEntry)
		entry.value = &CachedResult{
			result:    result,
			tables:    tables,
			createdAt: time.Now(),
		}
		rc.lru.MoveToFront(elem)
		return
	}

	// If cache is full — evict LRU
	for rc.lru.Len() >= rc.maxSize {
		back := rc.lru.Back()
		if back == nil {
			break
		}
		rc.removeEntry(back)
	}

	entry := &resultCacheEntry{
		key: key,
		value: &CachedResult{
			result:    result,
			tables:    tables,
			createdAt: time.Now(),
		},
	}
	elem := rc.lru.PushFront(entry)
	rc.cache[key] = elem
}

// Invalidate removes all entries affected by the given table.
func (rc *ResultCache) Invalidate(tableName string) {
	rc.mu.Lock()
	defer rc.mu.Unlock()

	var toRemove []*list.Element
	for _, elem := range rc.cache {
		entry := elem.Value.(*resultCacheEntry)
		if entry.value.tables[tableName] {
			toRemove = append(toRemove, elem)
		}
	}
	for _, elem := range toRemove {
		rc.removeEntry(elem)
	}
}

// InvalidateAll clears the entire cache.
func (rc *ResultCache) InvalidateAll() {
	rc.mu.Lock()
	defer rc.mu.Unlock()
	rc.cache = make(map[string]*list.Element)
	rc.lru.Init()
}

// Stats returns cache statistics.
func (rc *ResultCache) Stats() (hits, misses, size int64) {
	rc.mu.RLock()
	defer rc.mu.RUnlock()
	return rc.hitCount, rc.missCount, int64(rc.lru.Len())
}

func (rc *ResultCache) removeEntry(elem *list.Element) {
	entry := elem.Value.(*resultCacheEntry)
	delete(rc.cache, entry.key)
	rc.lru.Remove(elem)
}
