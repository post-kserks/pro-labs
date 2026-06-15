package executor

import (
	"fmt"
	"sync"
	"vaultdb/internal/parser"
)

type CachedPlan struct {
	stmt      parser.Statement
	cmd       Command
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

func planCacheKey(stmt parser.Statement) string {
	return fmt.Sprintf("%T:%s", stmt, stmt.StatementType())
}

func tableNameFromStmt(stmt parser.Statement) string {
	switch s := stmt.(type) {
	case *parser.SelectStatement:
		return s.TableName
	case *parser.InsertStatement:
		return s.TableName
	case *parser.UpdateStatement:
		return s.TableName
	case *parser.DeleteStatement:
		return s.TableName
	}
	return ""
}
