package executor

import (
	"sync"
)

type CachedPlan struct {
	tableName string
}

const defaultPlanCacheSize = 1000

type PlanCache struct {
	mu      sync.RWMutex
	plans   map[string]*CachedPlan
	maxSize int
}

func NewPlanCache(maxSize int) *PlanCache {
	if maxSize <= 0 {
		maxSize = defaultPlanCacheSize
	}
	return &PlanCache{
		plans:   make(map[string]*CachedPlan),
		maxSize: maxSize,
	}
}

func (pc *PlanCache) Get(key string) *CachedPlan {
	if key == "" {
		return nil
	}
	pc.mu.RLock()
	defer pc.mu.RUnlock()
	return pc.plans[key]
}

func (pc *PlanCache) Put(key string, plan *CachedPlan) {
	pc.mu.Lock()
	defer pc.mu.Unlock()
	if len(pc.plans) >= pc.maxSize {
		for k := range pc.plans {
			delete(pc.plans, k)
			break
		}
	}
	pc.plans[key] = plan
}

func (pc *PlanCache) Invalidate(tableName string) {
	pc.mu.Lock()
	defer pc.mu.Unlock()
	for k, v := range pc.plans {
		if v.tableName == tableName {
			delete(pc.plans, k)
		}
	}
}
