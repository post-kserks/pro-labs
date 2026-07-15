package executor

import (
	"fmt"
	"sort"
	"strings"
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
// Schema: [key, mode, holders, waiters]
func GetPGLocksRows(rowLocks *storage.RowLockManager) []storage.Row {
	if rowLocks == nil {
		return []storage.Row{}
	}
	locks := rowLocks.GetActiveLocks()
	if len(locks) == 0 {
		return []storage.Row{}
	}

	sort.Slice(locks, func(i, j int) bool {
		return locks[i].Key < locks[j].Key
	})

	rows := make([]storage.Row, len(locks))
	for i, l := range locks {
		if l == nil {
			continue
		}
		modeStr := "UNKNOWN"
		if l.Mode == storage.LockShared {
			modeStr = "SHARED"
		} else if l.Mode == storage.LockExclusive {
			modeStr = "EXCLUSIVE"
		}

		holderIDs := make([]uint64, 0, len(l.Holders))
		for id, held := range l.Holders {
			if held {
				holderIDs = append(holderIDs, id)
			}
		}
		sort.Slice(holderIDs, func(a, b int) bool { return holderIDs[a] < holderIDs[b] })

		holderStrs := make([]string, len(holderIDs))
		for j, id := range holderIDs {
			holderStrs[j] = fmt.Sprintf("%d", id)
		}
		holdersStr := strings.Join(holderStrs, ",")

		rows[i] = storage.Row{
			l.Key,
			modeStr,
			holdersStr,
			int64(len(l.Waiters)),
		}
	}
	return rows
}
