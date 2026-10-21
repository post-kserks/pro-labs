//go:build chaos

package txmanager

import (
	"math/rand"
	"sync"
	"testing"
)

func TestChaosHighConcurrency(t *testing.T) {
	m := NewManager()

	const numRoutines = 500
	var wg sync.WaitGroup
	wg.Add(numRoutines)

	// Simulate 10 tables to increase collision chance
	tables := []string{"t1", "t2", "t3", "t4", "t5", "t6", "t7", "t8", "t9", "t10"}

	for i := 0; i < numRoutines; i++ {
		go func(routineID int) {
			defer wg.Done()

			// Each routine runs multiple transactions
			for j := 0; j < 10; j++ {
				tx := m.Begin()

				// Pick 1-3 random tables to modify
				numOps := rand.Intn(3) + 1

				for k := 0; k < numOps; k++ {
					tbl := tables[rand.Intn(len(tables))]
					opType := "update"
					if rand.Intn(100) < 30 {
						opType = "insert"
					} else if rand.Intn(100) < 20 {
						opType = "delete"
					}

					op := PendingOp{Type: opType, DB: "chaos", Table: tbl}
					m.AddOp(tx, op)
					m.RecordAccess(tx, "chaos", tbl)
				}

				err := m.Commit(tx, func(pendingOps []PendingOp) error {
					for _, op := range pendingOps {
						m.BumpTableVersion(op.DB, op.Table)
					}
					return nil
				})

				if err != nil {
					// Rollback if conflict occurs
					m.Rollback(tx, nil)
				}
			}
		}(i)
	}

	wg.Wait()
	t.Log("High concurrency chaos test finished successfully")
}
