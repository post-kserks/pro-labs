package txmanager

import (
	"errors"
	"fmt"
	"runtime"
	"sync"
	"testing"
	"time"
)

func TestCommitConflictDetected(t *testing.T) {
	m := NewManager()

	tx1 := m.Begin()
	tx2 := m.Begin()

	m.AddOp(tx1, PendingOp{Type: "update", DB: "shop", Table: "users"})
	m.AddOp(tx2, PendingOp{Type: "update", DB: "shop", Table: "users"})

	// tx1 коммитится первой; executor поднимает версию таблицы при применении
	if err := m.Commit(tx1, func(ops []PendingOp) error {
		m.BumpTableVersion("shop", "users")
		return nil
	}); err != nil {
		t.Fatalf("first commit failed: %v", err)
	}

	// tx2 видела старую версию таблицы — должен быть конфликт
	err := m.Commit(tx2, func(ops []PendingOp) error {
		t.Fatal("applyFn must not run on conflict")
		return nil
	})
	if !errors.Is(err, ErrTxConflict) {
		t.Fatalf("expected ErrTxConflict, got %v", err)
	}
}

func TestCommitsOnDifferentTablesDoNotConflict(t *testing.T) {
	m := NewManager()

	tx1 := m.Begin()
	tx2 := m.Begin()
	m.AddOp(tx1, PendingOp{Type: "insert", DB: "shop", Table: "users"})
	m.AddOp(tx2, PendingOp{Type: "insert", DB: "shop", Table: "orders"})

	for _, tx := range []*Transaction{tx1, tx2} {
		tx := tx
		if err := m.Commit(tx, func(ops []PendingOp) error {
			for _, op := range ops {
				m.BumpTableVersion(op.DB, op.Table)
			}
			return nil
		}); err != nil {
			t.Fatalf("commit failed: %v", err)
		}
	}
}

func TestParallelCommitsSameTableExactlyOneWins(t *testing.T) {
	m := NewManager()

	const n = 8
	txs := make([]*Transaction, n)
	for i := range txs {
		txs[i] = m.Begin()
		m.AddOp(txs[i], PendingOp{Type: "update", DB: "db", Table: "t"})
	}

	var wg sync.WaitGroup
	var mu sync.Mutex
	committed := 0
	conflicts := 0

	for _, tx := range txs {
		wg.Add(1)
		go func(tx *Transaction) {
			defer wg.Done()
			err := m.Commit(tx, func(ops []PendingOp) error {
				m.BumpTableVersion("db", "t")
				return nil
			})
			mu.Lock()
			defer mu.Unlock()
			if err == nil {
				committed++
			} else if errors.Is(err, ErrTxConflict) {
				conflicts++
			}
		}(tx)
	}
	wg.Wait()

	if committed != 1 || conflicts != n-1 {
		t.Fatalf("expected exactly 1 commit and %d conflicts, got %d/%d", n-1, committed, conflicts)
	}
}

func TestSortedLockingAvoidsDeadlock(t *testing.T) {
	m := NewManager()

	// Две транзакции трогают одни и те же таблицы в разном порядке
	tx1 := m.Begin()
	m.AddOp(tx1, PendingOp{DB: "db", Table: "a"})
	m.AddOp(tx1, PendingOp{DB: "db", Table: "b"})

	tx2 := m.Begin()
	m.AddOp(tx2, PendingOp{DB: "db", Table: "b"})
	m.AddOp(tx2, PendingOp{DB: "db", Table: "a"})

	var wg sync.WaitGroup
	for _, tx := range []*Transaction{tx1, tx2} {
		wg.Add(1)
		go func(tx *Transaction) {
			defer wg.Done()
			_ = m.Commit(tx, func(ops []PendingOp) error {
				for _, op := range ops {
					m.BumpTableVersion(op.DB, op.Table)
				}
				return nil
			})
		}(tx)
	}
	wg.Wait() // тест зависнет при deadlock — go test упадёт по таймауту
}

// TestSpillReadOpsNoTruncationAndError — Bug #4: spill читается без ограничения
// в 64KB (bufio.Scanner) и распространяет ошибки записи/чтения.
func TestSpillReadOpsNoTruncationAndError(t *testing.T) {
	dir := t.TempDir()
	m := NewManager()
	m.SpillDir = dir
	m.SpillThreshold = 1 // спиллим сразу

	tx := m.Begin()

	// Операция с payload > 64KB (через дефолтный JSON-кодек: строка в Payload).
	big := make([]byte, 100*1024)
	for i := range big {
		big[i] = 'x'
	}
	m.AddOp(tx, PendingOp{Type: "noop", DB: "db", Table: "t", Payload: string(big)})
	m.AddOp(tx, PendingOp{Type: "noop", DB: "db", Table: "t", Payload: "small"})

	if !tx.spilled {
		t.Fatalf("expected tx to spill")
	}
	if tx.spillErr != nil {
		t.Fatalf("unexpected spill error: %v", tx.spillErr)
	}

	ops, err := tx.ReadOps()
	if err != nil {
		t.Fatalf("ReadOps failed: %v", err)
	}
	if len(ops) != 2 {
		t.Fatalf("expected 2 ops read back from spill, got %d", len(ops))
	}
	if s, _ := ops[0].Payload.(string); len(s) != 100*1024 {
		t.Fatalf("large op truncated/lost on spill: got len %d", len(s))
	}

	// Симулируем ошибку записи: липкая spillErr должна вернуться из ReadOps.
	tx2 := m.Begin()
	tx2.spilled = true
	tx2.spillPath = dir + "/does-not-exist.tmp"
	tx2.spillErr = errors.New("boom")
	if _, err := tx2.ReadOps(); err == nil {
		t.Fatalf("expected ReadOps to propagate sticky spillErr")
	}
}

// TestSavepointTruncation — RollbackToSavepoint усекает буфер и сбрасывает
// маркеры, созданные позже.
func TestSavepointTruncation(t *testing.T) {
	m := NewManager()
	tx := m.Begin()
	m.AddOp(tx, PendingOp{Type: "insert", DB: "db", Table: "t"})
	tx.Savepoint("s1")
	m.AddOp(tx, PendingOp{Type: "insert", DB: "db", Table: "t"})
	tx.Savepoint("s2")
	m.AddOp(tx, PendingOp{Type: "insert", DB: "db", Table: "t"})

	if err := tx.RollbackToSavepoint("s1"); err != nil {
		t.Fatalf("rollback to savepoint: %v", err)
	}
	ops, _ := tx.ReadOps()
	if len(ops) != 1 {
		t.Fatalf("expected 1 op after rollback to s1, got %d", len(ops))
	}
	// s2 создан позже s1 — должен быть удалён.
	if err := tx.RollbackToSavepoint("s2"); err == nil {
		t.Fatalf("expected error rolling back to dropped savepoint s2")
	}
	// Неизвестный savepoint.
	if err := tx.RollbackToSavepoint("nope"); err == nil {
		t.Fatalf("expected error for unknown savepoint")
	}
}

// --- OCC Retry Tests ---

func TestCommitWithRetrySuccess(t *testing.T) {
	m := NewManager()
	m.OCCConfig.BaseDelay = time.Millisecond // fast tests

	// Two transactions on the same table. tx2 will conflict on first attempt,
	// but the applyFn bumps the version so a retry sees the new version.
	tx1 := m.Begin()
	tx2 := m.Begin()
	m.AddOp(tx1, PendingOp{Type: "update", DB: "db", Table: "t"})
	m.AddOp(tx2, PendingOp{Type: "update", DB: "db", Table: "t"})

	// tx1 commits first (manually, to bump version).
	if err := m.Commit(tx1, func(ops []PendingOp) error {
		m.BumpTableVersion("db", "t")
		return nil
	}); err != nil {
		t.Fatalf("tx1 commit: %v", err)
	}

	// tx2 uses CommitWithRetry. It will conflict, then retry.
	// The applyFn bumps version on success, so subsequent attempts also succeed.
	err := m.CommitWithRetry(tx2, func(ops []PendingOp) error {
		m.BumpTableVersion("db", "t")
		return nil
	})
	if err != nil {
		t.Fatalf("CommitWithRetry should succeed: %v", err)
	}
}

func TestCommitWithRetryExhaustion(t *testing.T) {
	m := NewManager()
	m.OCCConfig.MaxRetries = 3
	m.OCCConfig.BaseDelay = 10 * time.Millisecond

	tx := m.Begin()
	m.AddOp(tx, PendingOp{Type: "update", DB: "db", Table: "t"})

	// Ensure first attempt conflicts.
	m.BumpTableVersion("db", "t")

	// Bump the version in a goroutine that runs for the duration of the test.
	// This ensures the version changes between snapshot refresh and the next
	// Commit check.
	done := make(chan struct{})
	go func() {
		for {
			select {
			case <-done:
				return
			default:
				m.BumpTableVersion("db", "t")
				runtime.Gosched()
			}
		}
	}()

	err := m.CommitWithRetry(tx, func(ops []PendingOp) error {
		t.Fatal("applyFn must not run when conflict persists")
		return nil
	})
	close(done)

	if err == nil {
		t.Fatal("expected error after retries exhausted")
	}
	if !errors.Is(err, ErrTxConflict) {
		t.Fatalf("expected ErrTxConflict wrapped, got: %v", err)
	}
}

func TestNonConflictErrorNotRetried(t *testing.T) {
	m := NewManager()
	m.OCCConfig.MaxRetries = 3
	m.OCCConfig.BaseDelay = time.Millisecond

	tx := m.Begin()
	m.AddOp(tx, PendingOp{Type: "insert", DB: "db", Table: "t"})

	customErr := fmt.Errorf("disk full")
	attempts := 0

	err := m.CommitWithRetry(tx, func(ops []PendingOp) error {
		attempts++
		return customErr
	})
	if !errors.Is(err, customErr) {
		t.Fatalf("expected customErr, got: %v", err)
	}
	if attempts != 1 {
		t.Fatalf("non-conflict error should not be retried, got %d attempts", attempts)
	}
}

func TestIsConflictError(t *testing.T) {
	tests := []struct {
		err  error
		want bool
	}{
		{fmt.Errorf("table \"t\" was modified: conflict detected"), true},
		{fmt.Errorf("OCC version mismatch"), true},
		{fmt.Errorf("version mismatch for table x"), true},
		{fmt.Errorf("disk full"), false},
		{fmt.Errorf("table not found"), false},
		{nil, false},
	}
	for _, tt := range tests {
		got := IsConflictError(tt.err)
		if got != tt.want {
			t.Errorf("IsConflictError(%v) = %v, want %v", tt.err, got, tt.want)
		}
	}
}

func TestParallelCommitsWithRetryAllSucceed(t *testing.T) {
	m := NewManager()
	m.OCCConfig.MaxRetries = 5
	m.OCCConfig.BaseDelay = time.Millisecond

	const n = 10
	var wg sync.WaitGroup
	var mu sync.Mutex
	succeeded := 0

	for i := 0; i < n; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			tx := m.Begin()
			m.AddOp(tx, PendingOp{Type: "update", DB: "db", Table: "t"})
			err := m.CommitWithRetry(tx, func(ops []PendingOp) error {
				m.BumpTableVersion("db", "t")
				return nil
			})
			mu.Lock()
			defer mu.Unlock()
			if err == nil {
				succeeded++
			}
		}()
	}
	wg.Wait()

	// With retry, all 10 should eventually succeed (serialized by retry).
	if succeeded != n {
		t.Fatalf("expected all %d transactions to succeed with retry, got %d", n, succeeded)
	}
}

func TestOCCBackoffTiming(t *testing.T) {
	m := NewManager()
	m.OCCConfig = OCCConfig{
		MaxRetries:    3,
		BaseDelay:     10 * time.Millisecond,
		MaxDelay:      100 * time.Millisecond,
		BackoffFactor: 2.0,
	}

	tx := m.Begin()
	m.AddOp(tx, PendingOp{Type: "update", DB: "db", Table: "t"})

	// Pre-bump so first attempt conflicts. Then a goroutine keeps bumping
	// to ensure every retry also conflicts.
	m.BumpTableVersion("db", "t")

	done := make(chan struct{})
	go func() {
		for {
			select {
			case <-done:
				return
			default:
				m.BumpTableVersion("db", "t")
				runtime.Gosched()
			}
		}
	}()

	start := time.Now()
	err := m.CommitWithRetry(tx, func(ops []PendingOp) error {
		t.Fatal("applyFn must not run when conflict persists")
		return nil
	})
	elapsed := time.Since(start)
	close(done)

	if err == nil {
		t.Fatal("expected error after retries exhausted")
	}

	// Expected total backoff delay (without jitter) for 3 retries:
	// attempt 0: 10ms, attempt 1: 20ms, attempt 2: 40ms = 70ms minimum
	// With jitter added, expect at least 70ms total.
	minExpected := 70 * time.Millisecond
	if elapsed < minExpected {
		t.Fatalf("backoff too fast: elapsed %v, expected at least %v", elapsed, minExpected)
	}
}
