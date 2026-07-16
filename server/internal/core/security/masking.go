package security

import (
	"strings"
	"sync"
)

// MaskingPolicy defines how a string should be masked.
type MaskingPolicy struct {
	Prefix int
	Suffix int
	Char   rune
}

var (
	registryMu sync.RWMutex
	// map[dbName.tableName.colName]MaskingPolicy
	registry = make(map[string]MaskingPolicy)
)

// RegisterPolicy registers a masking policy for a specific column.
func RegisterPolicy(db, table, col string, policy MaskingPolicy) {
	registryMu.Lock()
	defer registryMu.Unlock()
	key := db + "." + table + "." + col
	registry[key] = policy
}

// GetPolicy returns the policy for a column and whether it exists.
func GetPolicy(db, table, col string) (MaskingPolicy, bool) {
	registryMu.RLock()
	defer registryMu.RUnlock()
	key := db + "." + table + "." + col
	p, ok := registry[key]
	return p, ok
}

// MaskString applies the MaskingPolicy to the given string.
func MaskString(val string, policy MaskingPolicy) string {
	if val == "" {
		return val
	}
	runes := []rune(val)
	length := len(runes)

	maskLen := length - policy.Prefix - policy.Suffix
	if maskLen <= 0 {
		return val
	}

	var sb strings.Builder
	sb.Grow(length)
	for i := 0; i < policy.Prefix; i++ {
		sb.WriteRune(runes[i])
	}
	for i := 0; i < maskLen; i++ {
		sb.WriteRune(policy.Char)
	}
	for i := length - policy.Suffix; i < length; i++ {
		sb.WriteRune(runes[i])
	}

	return sb.String()
}
