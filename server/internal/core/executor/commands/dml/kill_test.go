package dml_test

import (
	"context"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"vaultdb/internal/core/executor"
	"vaultdb/internal/core/executor/types"
	"vaultdb/internal/core/parser"
)

// ensureKillSessionFuncInitialized guarantees that types.KillSessionFunc is hooked
// to GlobalRegistry.KillSession (normally initialized by importing executor).
func ensureKillSessionFuncInitialized() {
	if types.KillSessionFunc == nil {
		types.KillSessionFunc = executor.GlobalRegistry.KillSession
	}
}

// createKillCommand constructs a Command for KILL via executor command factory.
func createKillCommand(t *testing.T, sessionID uint64) types.Command {
	t.Helper()
	cmd, err := executor.CommandFactory(&parser.KillStatement{SessionID: sessionID})
	if err != nil {
		t.Fatalf("unexpected error creating KillCommand for session %d: %v", sessionID, err)
	}
	return cmd
}

// TestKillCommand_DirectExecute verifies direct invocation of KillCommand.Execute.
func TestKillCommand_DirectExecute(t *testing.T) {
	ensureKillSessionFuncInitialized()

	t.Run("successful kill via direct execute", func(t *testing.T) {
		const sessionID = uint64(12345)
		ctx, cancelFunc := context.WithCancel(context.Background())
		defer cancelFunc()

		// Register target session in global registry with active cancellation context
		executor.GlobalRegistry.RegisterSession(sessionID, "alice", "mydb", "SELECT 1;", 0, cancelFunc)
		defer executor.GlobalRegistry.UnregisterSession(sessionID)

		cmd := createKillCommand(t, sessionID)
		res, err := cmd.Execute(&types.ExecutionContext{})
		if err != nil {
			t.Fatalf("unexpected error during Execute: %v", err)
		}

		// Verify cancelFunc was invoked
		if err := ctx.Err(); err != context.Canceled {
			t.Errorf("expected context.Canceled, got %v", err)
		}

		// Verify returned Row{"killed session 12345"} and affected count 1
		if len(res.Rows) != 1 || len(res.Rows[0]) != 1 || res.Rows[0][0] != "killed session 12345" {
			t.Errorf("expected Rows: [][]{{\"killed session 12345\"}}, got %v", res.Rows)
		}
		if res.Affected != 1 {
			t.Errorf("expected Affected: 1, got %d", res.Affected)
		}
		if res.Schema == nil || res.Schema.Name != "kill" || len(res.Schema.Columns) != 1 {
			t.Errorf("expected schema with name 'kill' and 1 column, got %v", res.Schema)
		} else if res.Schema.Columns[0].Name != "status" || res.Schema.Columns[0].Type != "TEXT" {
			t.Errorf("expected schema column status/TEXT, got %+v", res.Schema.Columns[0])
		}
	})

	t.Run("kill session not available when func is nil", func(t *testing.T) {
		origFunc := types.KillSessionFunc
		types.KillSessionFunc = nil
		defer func() { types.KillSessionFunc = origFunc }()

		cmd := createKillCommand(t, 12345)
		_, err := cmd.Execute(&types.ExecutionContext{})
		if err == nil || err.Error() != "session kill not available" {
			t.Fatalf("expected 'session kill not available', got %v", err)
		}
	})
}

// TestKillCommand_ExecuteViaSQL verifies parsing and executing KILL QUERY statements via SQL.
func TestKillCommand_ExecuteViaSQL(t *testing.T) {
	ensureKillSessionFuncInitialized()

	session := executor.SetupSession(t)
	const targetSessionID = uint64(12345)

	ctx, cancelFunc := context.WithCancel(context.Background())
	defer cancelFunc()

	executor.GlobalRegistry.RegisterSession(targetSessionID, "alice", "mydb", "SELECT 1;", 0, cancelFunc)
	defer executor.GlobalRegistry.UnregisterSession(targetSessionID)

	res := executor.ExecuteSQL(t, session, "KILL QUERY 12345;")

	if err := ctx.Err(); err != context.Canceled {
		t.Errorf("expected context.Canceled after SQL execution, got %v", err)
	}
	if len(res.Rows) != 1 || len(res.Rows[0]) != 1 || res.Rows[0][0] != "killed session 12345" {
		t.Errorf("expected Rows: [][]{{\"killed session 12345\"}}, got %v", res.Rows)
	}
	if res.Affected != 1 {
		t.Errorf("expected Affected count 1, got %d", res.Affected)
	}
}

// TestKillCommand_NonExistentOrIdleSession verifies error handling for non-existent or idle sessions (`session not found`).
func TestKillCommand_NonExistentOrIdleSession(t *testing.T) {
	ensureKillSessionFuncInitialized()

	t.Run("non-existent session returns error via direct execute", func(t *testing.T) {
		cmd := createKillCommand(t, 99999)
		_, err := cmd.Execute(&types.ExecutionContext{})
		if err == nil || err.Error() != "session not found" {
			t.Fatalf("expected 'session not found' for non-existent session, got %v", err)
		}
	})

	t.Run("non-existent session returns error via SQL execute", func(t *testing.T) {
		session := executor.SetupSession(t)
		executor.ExecuteSQLExpectError(t, session, "KILL QUERY 99999;")
	})

	t.Run("idle session without cancelFunc returns error", func(t *testing.T) {
		const idleSessionID = uint64(99999)
		// Register session without cancelFunc (nil) representing idle/non-cancellable query
		executor.GlobalRegistry.RegisterSession(idleSessionID, "alice", "mydb", "", 0, nil)
		defer executor.GlobalRegistry.UnregisterSession(idleSessionID)

		cmd := createKillCommand(t, idleSessionID)
		_, err := cmd.Execute(&types.ExecutionContext{})
		if err == nil || err.Error() != "session not found" {
			t.Fatalf("expected 'session not found' for idle session without cancelFunc, got %v", err)
		}

		session := executor.SetupSession(t)
		executor.ExecuteSQLExpectError(t, session, "KILL QUERY 99999;")
	})
}

// TestKillCommand_Concurrent tests concurrent execution of KILL QUERY statements and session registrations (`-race` compliant).
func TestKillCommand_Concurrent(t *testing.T) {
	ensureKillSessionFuncInitialized()

	const numSessions = 50
	const numGoroutines = 10
	baseID := uint64(200000)

	var wg sync.WaitGroup
	var cancelledCount int64

	// Concurrently register sessions and execute KILL commands across goroutines.
	for i := 0; i < numGoroutines; i++ {
		wg.Add(1)
		go func(workerID int) {
			defer wg.Done()
			execCtx := &types.ExecutionContext{}

			for j := 0; j < numSessions; j++ {
				// Generate unique session ID for target
				sessionID := baseID + uint64(workerID*numSessions+j)
				ctx, cancel := context.WithCancel(context.Background())

				executor.GlobalRegistry.RegisterSession(sessionID, "alice", "mydb", "SELECT sleep(10);", 0, cancel)

				// Concurrently execute KILL command directly
				cmd, err := executor.CommandFactory(&parser.KillStatement{SessionID: sessionID})
				if err != nil {
					t.Errorf("worker %d unexpected error creating cmd: %v", workerID, err)
					continue
				}
				res, err := cmd.Execute(execCtx)
				if err != nil {
					t.Errorf("worker %d unexpected error killing session %d: %v", workerID, sessionID, err)
				} else if res != nil && res.Affected != 1 {
					t.Errorf("worker %d expected Affected=1, got %d", workerID, res.Affected)
				}

				if ctx.Err() == context.Canceled {
					atomic.AddInt64(&cancelledCount, 1)
				} else {
					t.Errorf("worker %d expected session %d to be canceled", workerID, sessionID)
				}

				// Also test killing an already killed target (should safely return session not found or no-op)
				_, _ = cmd.Execute(execCtx)

				executor.GlobalRegistry.UnregisterSession(sessionID)
			}
		}(i)
	}

	// Additional concurrent killers targeting random sessions within the active range
	for k := 0; k < 5; k++ {
		wg.Add(1)
		go func(killerID int) {
			defer wg.Done()
			execCtx := &types.ExecutionContext{}
			for m := 0; m < numSessions*2; m++ {
				targetID := baseID + uint64((killerID*m)%(numSessions*numGoroutines))
				cmd, err := executor.CommandFactory(&parser.KillStatement{SessionID: targetID})
				if err == nil {
					_, _ = cmd.Execute(execCtx)
				}
				time.Sleep(100 * time.Microsecond)
			}
		}(k)
	}

	wg.Wait()

	expectedCount := int64(numSessions * numGoroutines)
	if atomic.LoadInt64(&cancelledCount) != expectedCount {
		t.Fatalf("expected %d successfully canceled sessions, got %d", expectedCount, atomic.LoadInt64(&cancelledCount))
	}
}
