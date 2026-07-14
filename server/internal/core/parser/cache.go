package parser

import (
	"container/list"
	"strings"
	"sync"
)

const defaultStatementCacheCapacity = 256

type cacheEntry struct {
	hash uint64
	key  string
	stmt Statement
}

// StatementCache is a thread-safe LRU cache for parsed SQL statements.
type StatementCache struct {
	mu       sync.RWMutex
	capacity int
	cache    map[uint64]*list.Element
	lru      *list.List
}

// NewStatementCache creates a new StatementCache with the given capacity.
func NewStatementCache(capacity int) *StatementCache {
	if capacity <= 0 {
		capacity = defaultStatementCacheCapacity
	}
	return &StatementCache{
		capacity: capacity,
		cache:    make(map[uint64]*list.Element, capacity),
		lru:      list.New(),
	}
}

// Get retrieves a cached statement by normalized SQL key.
// Returns the statement and true if found; nil and false otherwise.
// On hit, the entry is moved to the front of the LRU list.
func (c *StatementCache) Get(sql string) (Statement, bool) {
	h := fnv1aLower(sql)

	c.mu.Lock()
	defer c.mu.Unlock()

	elem, ok := c.cache[h]
	if !ok {
		return nil, false
	}
	entry := elem.Value.(*cacheEntry)
	if !equalTrimmedAndLower(entry.key, sql) {
		return nil, false
	}
	c.lru.MoveToFront(elem)
	return entry.stmt, true
}

// Put inserts a parsed statement into the cache.
// If the cache is full, the least recently used entry is evicted.
func (c *StatementCache) Put(sql string, stmt Statement) {
	h := fnv1aLower(sql)

	c.mu.Lock()
	defer c.mu.Unlock()

	if elem, ok := c.cache[h]; ok {
		entry := elem.Value.(*cacheEntry)
		if equalTrimmedAndLower(entry.key, sql) {
			c.lru.MoveToFront(elem)
			entry.stmt = stmt
			return
		}
	}

	// Evict if at capacity
	for c.lru.Len() >= c.capacity {
		c.evict()
	}

	entry := &cacheEntry{hash: h, key: trimAndLower(sql), stmt: stmt}
	elem := c.lru.PushFront(entry)
	c.cache[h] = elem
}

// evict removes the least recently used entry. Caller must hold c.mu.
func (c *StatementCache) evict() {
	back := c.lru.Back()
	if back == nil {
		return
	}
	entry := c.lru.Remove(back).(*cacheEntry)
	delete(c.cache, entry.hash)
}

// Len returns the number of entries in the cache.
func (c *StatementCache) Len() int {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.lru.Len()
}

// Clear removes all entries from the cache.
func (c *StatementCache) Clear() {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.cache = make(map[uint64]*list.Element, c.capacity)
	c.lru.Init()
}

func fnv1aLower(s string) uint64 {
	s = strings.TrimSpace(s)
	const (
		offset64 = 14695981039346656037
		prime64  = 1099511628211
	)
	h := uint64(offset64)
	for i := 0; i < len(s); i++ {
		b := s[i]
		if b >= 'A' && b <= 'Z' {
			b += 'a' - 'A'
		}
		h ^= uint64(b)
		h *= prime64
	}
	return h
}

func equalTrimmedAndLower(s1, s2 string) bool {
	s2 = strings.TrimSpace(s2)
	if len(s1) != len(s2) {
		return false
	}
	for i := 0; i < len(s1); i++ {
		c1, c2 := s1[i], s2[i]
		if c2 >= 'A' && c2 <= 'Z' {
			c2 += 'a' - 'A'
		}
		if c1 != c2 {
			return false
		}
	}
	return true
}
