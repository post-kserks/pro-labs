package parser

import (
	"container/list"
	"sync"
)

const defaultStatementCacheCapacity = 256

type cacheEntry struct {
	key  string
	stmt Statement
}

// StatementCache is a thread-safe LRU cache for parsed SQL statements.
type StatementCache struct {
	mu       sync.RWMutex
	capacity int
	cache    map[string]*list.Element
	lru      *list.List
}

// NewStatementCache creates a new StatementCache with the given capacity.
func NewStatementCache(capacity int) *StatementCache {
	if capacity <= 0 {
		capacity = defaultStatementCacheCapacity
	}
	return &StatementCache{
		capacity: capacity,
		cache:    make(map[string]*list.Element, capacity),
		lru:      list.New(),
	}
}

// Get retrieves a cached statement by normalized SQL key.
// Returns the statement and true if found; nil and false otherwise.
// On hit, the entry is moved to the front of the LRU list.
func (c *StatementCache) Get(sql string) (Statement, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()

	key := trimAndLower(sql)
	elem, ok := c.cache[key]
	if !ok {
		return nil, false
	}
	c.lru.MoveToFront(elem)
	return elem.Value.(*cacheEntry).stmt, true
}

// Put inserts a parsed statement into the cache.
// If the cache is full, the least recently used entry is evicted.
func (c *StatementCache) Put(sql string, stmt Statement) {
	c.mu.Lock()
	defer c.mu.Unlock()

	key := trimAndLower(sql)
	if elem, ok := c.cache[key]; ok {
		c.lru.MoveToFront(elem)
		elem.Value.(*cacheEntry).stmt = stmt
		return
	}

	// Evict if at capacity
	for c.lru.Len() >= c.capacity {
		c.evict()
	}

	entry := &cacheEntry{key: key, stmt: stmt}
	elem := c.lru.PushFront(entry)
	c.cache[key] = elem
}

// evict removes the least recently used entry. Caller must hold c.mu.
func (c *StatementCache) evict() {
	back := c.lru.Back()
	if back == nil {
		return
	}
	entry := c.lru.Remove(back).(*cacheEntry)
	delete(c.cache, entry.key)
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
	c.cache = make(map[string]*list.Element, c.capacity)
	c.lru.Init()
}
