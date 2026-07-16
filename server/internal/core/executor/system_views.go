package executor

import (
	"time"

	"vaultdb/internal/core/executor/types"
	"vaultdb/internal/core/storage"
)

func init() {
	types.GetPGStatActivityRowsFunc = GetPGStatActivityRows
	types.GetPGLocksRowsFunc = GetPGLocksRows
}

// GetPGStatActivityRows returns current active sessions as system view rows.
// Schema: [id, user, db, state, query, duration_ms, tx_id]
func GetPGStatActivityRows() []storage.Row {
	sessions := GlobalRegistry.GetActiveSessions()
	rows := make([]storage.Row, len(sessions))
	now := time.Now()
	for i, s := range sessions {
		var durationMs int64
		if !s.StartedAt.IsZero() && s.State != StateIdle {
			durationMs = now.Sub(s.StartedAt).Milliseconds()
		}
		rows[i] = storage.Row{
			int64(s.ID),
			s.User,
			s.DBName,
			string(s.State),
			s.Query,
			durationMs,
			int64(s.TxID),
		}
	}
	return rows
}

// GetPGLocksRows returns active row locks as system view rows.
// With MVCC, row-level locks are no longer used; this returns an empty result.
func GetPGLocksRows(_ interface{}) []storage.Row {
	return []storage.Row{}
}
