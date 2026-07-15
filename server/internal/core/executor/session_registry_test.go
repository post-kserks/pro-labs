package executor

import (
	"context"
	"fmt"
	"sync"
	"testing"
	"time"
)

func TestSessionRegistry_NewAndRegister(t *testing.T) {
	reg := NewSessionRegistry()
	if reg == nil {
		t.Fatal("NewSessionRegistry() returned nil")
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	id := uint64(101)
	info := reg.RegisterSession(id, "alice", "prod_db", "SELECT 1;", 500, cancel)
	if info == nil {
		t.Fatal("RegisterSession() returned nil")
	}

	if info.ID != id || info.User != "alice" || info.DBName != "prod_db" || info.Query != "SELECT 1;" || info.TxID != 500 {
		t.Errorf("RegisterSession() returned unexpected fields: %+v", info)
	}
	if info.State != StateRunning {
		t.Errorf("expected state %s, got %s", StateRunning, info.State)
	}
	if info.StartedAt.IsZero() {
		t.Error("expected StartedAt to be non-zero")
	}
	if ctx.Err() != nil {
		t.Errorf("expected ctx.Err() to be nil, got %v", ctx.Err())
	}

	// Verify GlobalRegistry initialization
	if GlobalRegistry == nil {
		t.Error("GlobalRegistry should not be nil")
	}
}

func TestSessionRegistry_Unregister(t *testing.T) {
	reg := NewSessionRegistry()
	_, cancel := context.WithCancel(context.Background())
	defer cancel()

	reg.RegisterSession(1, "bob", "test_db", "SELECT * FROM users;", 100, cancel)
	sessions := reg.GetActiveSessions()
	if len(sessions) != 1 {
		t.Fatalf("expected 1 session, got %d", len(sessions))
	}

	reg.UnregisterSession(1)
	sessions = reg.GetActiveSessions()
	if len(sessions) != 0 {
		t.Fatalf("expected 0 sessions after unregister, got %d", len(sessions))
	}

	// Unregistering a non-existent ID should be a safe no-op
	reg.UnregisterSession(999)
}

func TestSessionRegistry_UpdateQueryAndIdle(t *testing.T) {
	reg := NewSessionRegistry()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	id := uint64(42)
	reg.RegisterSession(id, "carol", "analytics", "SELECT count(*) FROM logs;", 123, cancel)

	// Update to a new active query
	reg.UpdateQuery(id, "SELECT * FROM orders;", StateRunning, 124)
	sessions := reg.GetActiveSessions()
	if len(sessions) != 1 {
		t.Fatalf("expected 1 active session, got %d", len(sessions))
	}
	if sessions[0].Query != "SELECT * FROM orders;" || sessions[0].State != StateRunning || sessions[0].TxID != 124 {
		t.Errorf("UpdateQuery did not update fields correctly: %+v", sessions[0])
	}

	// Requirement 3: UpdateQuery(id, "", StateIdle, 0) sets cancelCtx = nil
	// and KillSession returns false when cancelCtx == nil.
	reg.UpdateQuery(id, "", StateIdle, 0)

	sessions = reg.GetActiveSessions()
	if len(sessions) != 1 {
		t.Fatalf("expected 1 active session, got %d", len(sessions))
	}
	if sessions[0].Query != "" || sessions[0].State != StateIdle || sessions[0].TxID != 0 {
		t.Errorf("expected session to be idle with empty query and txID=0, got: %+v", sessions[0])
	}

	// Verify that KillSession returns false when cancelCtx == nil
	killed := reg.KillSession(id)
	if killed {
		t.Error("expected KillSession to return false when cancelCtx is nil after transition to StateIdle")
	}

	// Verify the original context wasn't prematurely canceled just by setting cancelCtx = nil
	if ctx.Err() != nil {
		t.Errorf("expected ctx.Err() to be nil, got %v", ctx.Err())
	}

	// Updating non-existent ID should not panic
	reg.UpdateQuery(999, "SELECT 1;", StateRunning, 1)
}

func TestSessionRegistry_GetActiveSessions_SortedOrder(t *testing.T) {
	reg := NewSessionRegistry()
	_, cancel := context.WithCancel(context.Background())
	defer cancel()

	// Register sessions with unordered IDs
	ids := []uint64{100, 5, 42, 1, 999, 50, 15}
	for _, id := range ids {
		reg.RegisterSession(id, "user", "db", fmt.Sprintf("Query %d", id), id, cancel)
	}

	sessions := reg.GetActiveSessions()
	if len(sessions) != len(ids) {
		t.Fatalf("expected %d sessions, got %d", len(ids), len(sessions))
	}

	// Requirement 4: Make sure GetActiveSessions() returns SessionInfo structs sorted ascending by ID (list[i].ID < list[j].ID)
	for i := 0; i < len(sessions)-1; i++ {
		if !(sessions[i].ID < sessions[i+1].ID) {
			t.Errorf("sessions not sorted strictly ascending: index %d has ID %d, index %d has ID %d",
				i, sessions[i].ID, i+1, sessions[i+1].ID)
		}
	}
}

func TestSessionRegistry_KillSession(t *testing.T) {
	reg := NewSessionRegistry()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	id := uint64(10)
	reg.RegisterSession(id, "dave", "app_db", "Long running query...", 1, cancel)

	// Attempting to kill non-existent session
	if reg.KillSession(999) {
		t.Error("expected KillSession(999) to return false for non-existent session")
	}

	// Kill valid session
	if !reg.KillSession(id) {
		t.Error("expected KillSession(id) to return true for active session")
	}

	// Verify context is actually canceled
	select {
	case <-ctx.Done():
		if ctx.Err() != context.Canceled {
			t.Errorf("expected context.Canceled error, got %v", ctx.Err())
		}
	case <-time.After(100 * time.Millisecond):
		t.Error("context was not canceled by KillSession")
	}
}

// TestSessionRegistry_Concurrent launches 20+ goroutines concurrently registering sessions,
// updating queries, calling GetActiveSessions(), and calling KillSession(),
// verifying zero race conditions or panics under -race.
func TestSessionRegistry_Concurrent(t *testing.T) {
	reg := NewSessionRegistry()
	const numGoroutines = 30
	const iterationsPerGoroutine = 50

	var wg sync.WaitGroup
	wg.Add(numGoroutines)

	for g := 0; g < numGoroutines; g++ {
		go func(gID int) {
			defer wg.Done()

			// Each goroutine operates across a shared pool of IDs and its own specific ID
			myID := uint64(gID + 1)
			sharedID := uint64((gID % 5) + 100) // 5 shared IDs where contention happens

			for i := 0; i < iterationsPerGoroutine; i++ {
				_, cancel := context.WithCancel(context.Background())

				// 1. Register own session
				reg.RegisterSession(myID, "user", "db", "SELECT 1;", uint64(i), cancel)

				// 2. Also register/overwrite on shared session ID to stress concurrency
				_, sharedCancel := context.WithCancel(context.Background())
				reg.RegisterSession(sharedID, "shared_user", "db", "SELECT * FROM shared;", uint64(i), sharedCancel)

				// 3. Update queries on both IDs
				reg.UpdateQuery(myID, "UPDATE items SET active = true;", StateRunning, uint64(i+1000))
				if i%2 == 0 {
					reg.UpdateQuery(sharedID, "", StateIdle, 0)
				} else {
					reg.UpdateQuery(sharedID, "SELECT sleep(1);", StateLockWait, uint64(i))
				}

				// 4. Read active sessions and verify invariant: sorted strictly ascending by ID
				sessions := reg.GetActiveSessions()
				for j := 0; j < len(sessions)-1; j++ {
					if sessions[j].ID >= sessions[j+1].ID {
						t.Errorf("concurrent read violated sorted order: ID %d >= ID %d", sessions[j].ID, sessions[j+1].ID)
					}
				}

				// 5. Kill session
				if i%3 == 0 {
					reg.KillSession(myID)
				}
				if i%4 == 0 {
					reg.KillSession(sharedID)
				}

				// 6. Unregister
				if i%5 == 0 {
					reg.UnregisterSession(myID)
				}

				// Cleanup contexts to avoid leak warnings in tests
				cancel()
				sharedCancel()
			}
		}(g)
	}

	wg.Wait()
}
