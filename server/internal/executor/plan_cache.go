package executor

import (
	"fmt"
	"hash/fnv"
	"sync"
	"time"
)

// CachedPlan stores a cached execution plan with metadata.
type CachedPlan struct {
	Plan      interface{} // The cached plan (parser.Statement or optimized plan)
	SchemaVer uint64      // Schema version when this plan was cached
	CreatedAt time.Time   // When this plan was cached
	HitCount  int64       // Number of times this plan was reused
	QueryStr  string      // Original query string for debugging
	tableName string      // Table name for backward-compatible invalidation
}

const (
	defaultPlanCacheSize = 1000
	defaultPlanCacheTTL  = 5 * time.Minute
)

// PlanCache is a thread-safe cache for query execution plans with schema version tracking.
type PlanCache struct {
	mu       sync.RWMutex
	plans    map[uint64]*CachedPlan // queryHash → CachedPlan
	maxSize  int
	ttl      time.Duration
	hits     int64
	misses   int64
	evictions int64
}

// NewPlanCache creates a new plan cache with the given size and TTL.
func NewPlanCache(maxSize int) *PlanCache {
	return NewPlanCacheWithTTL(maxSize, defaultPlanCacheTTL)
}

// NewPlanCacheWithTTL creates a new plan cache with explicit TTL.
func NewPlanCacheWithTTL(maxSize int, ttl time.Duration) *PlanCache {
	if maxSize <= 0 {
		maxSize = defaultPlanCacheSize
	}
	if ttl <= 0 {
		ttl = defaultPlanCacheTTL
	}
	return &PlanCache{
		plans:   make(map[uint64]*CachedPlan, maxSize),
		maxSize: maxSize,
		ttl:     ttl,
	}
}

// QueryHash computes a 64-bit hash of the query string and parameters.
func QueryHash(query string, params []interface{}) uint64 {
	h := fnv.New64a()
	h.Write([]byte(query))
	for _, p := range params {
		fmt.Fprintf(h, "%v", p)
	}
	return h.Sum64()
}

// Get retrieves a cached plan if it exists and is valid.
// Returns (plan, true) on hit, (nil, false) on miss.
func (pc *PlanCache) Get(queryHash uint64, schemaVer uint64) (*CachedPlan, bool) {
	pc.mu.Lock()
	defer pc.mu.Unlock()

	plan, ok := pc.plans[queryHash]
	if !ok {
		pc.misses++
		return nil, false
	}

	// Check schema version - invalidate if schema changed
	if plan.SchemaVer != schemaVer {
		delete(pc.plans, queryHash)
		pc.misses++
		return nil, false
	}

	// Check TTL - invalidate if expired
	if time.Since(plan.CreatedAt) > pc.ttl {
		delete(pc.plans, queryHash)
		pc.misses++
		return nil, false
	}

	// Cache hit - update stats
	plan.HitCount++
	pc.hits++
	return plan, true
}

// Put stores a plan in the cache.
func (pc *PlanCache) Put(queryHash uint64, plan *CachedPlan) {
	pc.mu.Lock()
	defer pc.mu.Unlock()

	// Evict oldest if at capacity
	if len(pc.plans) >= pc.maxSize {
		pc.evictOldest()
	}

	pc.plans[queryHash] = plan
}

// Invalidate removes all plans for a given table name (backward compatibility).
func (pc *PlanCache) Invalidate(tableName string) {
	pc.mu.Lock()
	defer pc.mu.Unlock()

	for k, v := range pc.plans {
		if v.tableName == tableName {
			delete(pc.plans, k)
		}
	}
}

// InvalidateByVersion removes all plans with schema version <= the given version.
func (pc *PlanCache) InvalidateByVersion(schemaVer uint64) {
	pc.mu.Lock()
	defer pc.mu.Unlock()

	for hash, plan := range pc.plans {
		if plan.SchemaVer <= schemaVer {
			delete(pc.plans, hash)
		}
	}
}

// evictOldest removes the oldest entry (by creation time).
// Must be called with lock held.
func (pc *PlanCache) evictOldest() {
	var oldestHash uint64
	var oldestTime time.Time
	first := true

	for hash, plan := range pc.plans {
		if first || plan.CreatedAt.Before(oldestTime) {
			oldestHash = hash
			oldestTime = plan.CreatedAt
			first = false
		}
	}

	if !first {
		delete(pc.plans, oldestHash)
		pc.evictions++
	}
}

// Stats returns cache statistics.
func (pc *PlanCache) Stats() (hits, misses, evictions, size int64) {
	pc.mu.RLock()
	defer pc.mu.RUnlock()
	return pc.hits, pc.misses, pc.evictions, int64(len(pc.plans))
}

// Reset clears the cache and resets statistics.
func (pc *PlanCache) Reset() {
	pc.mu.Lock()
	defer pc.mu.Unlock()
	pc.plans = make(map[uint64]*CachedPlan, pc.maxSize)
	pc.hits = 0
	pc.misses = 0
	pc.evictions = 0
}

// Len returns the current number of cached plans.
func (pc *PlanCache) Len() int {
	pc.mu.RLock()
	defer pc.mu.RUnlock()
	return len(pc.plans)
}
