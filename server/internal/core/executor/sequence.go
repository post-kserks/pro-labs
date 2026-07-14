package executor

import "sync"

// Sequence state — canonical implementations are in types.GetNextAutoIncrement etc.
// These unexported vars are kept so that sequence_test.go can reset state between tests.
var (
	sequenceMu       sync.Mutex
	sequenceCounters = make(map[string]int64) // key: "db.table.col" -> next value
)
