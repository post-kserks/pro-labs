package executor

import (
	"sync"
	"vaultdb/internal/core/parser"
)

const defaultASTCacheSize = 256

// ASTCache — потокобезопасный LRU-кэш для распарсенных SQL AST.
type ASTCache struct {
	mu       sync.Mutex
	capacity int
	entries  map[string]*astEntry
	order    []string // LRU order: newest at end, oldest at beginning
}

type astEntry struct {
	statement parser.Statement
}

func NewASTCache(capacity int) *ASTCache {
	if capacity <= 0 {
		capacity = defaultASTCacheSize
	}
	return &ASTCache{
		capacity: capacity,
		entries:  make(map[string]*astEntry, capacity),
		order:    make([]string, 0, capacity),
	}
}

// Get возвращает закэшированный AST или nil.
func (c *ASTCache) Get(sql string) parser.Statement {
	c.mu.Lock()
	defer c.mu.Unlock()

	entry, ok := c.entries[sql]
	if !ok {
		return nil
	}

	// Move to end (most recently used)
	c.moveToEnd(sql)
	return entry.statement
}

// Put добавляет AST в кэш. При превышении capacity вытесняет самый старый.
func (c *ASTCache) Put(sql string, stmt parser.Statement) {
	c.mu.Lock()
	defer c.mu.Unlock()

	if _, exists := c.entries[sql]; exists {
		c.moveToEnd(sql)
		c.entries[sql] = &astEntry{statement: stmt}
		return
	}

	// Evict oldest if at capacity
	if len(c.order) >= c.capacity {
		oldest := c.order[0]
		c.order = c.order[1:]
		delete(c.entries, oldest)
	}

	c.entries[sql] = &astEntry{statement: stmt}
	c.order = append(c.order, sql)
}

func (c *ASTCache) moveToEnd(sql string) {
	for i, s := range c.order {
		if s == sql {
			c.order = append(c.order[:i], c.order[i+1:]...)
			c.order = append(c.order, sql)
			return
		}
	}
}
