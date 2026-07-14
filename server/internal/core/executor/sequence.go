package executor

import "vaultdb/internal/core/executor/types"

// resetSequenceCounters resets auto-increment state (used by sequence_test.go).
func resetSequenceCounters() {
	types.ResetSequenceCounters()
}
