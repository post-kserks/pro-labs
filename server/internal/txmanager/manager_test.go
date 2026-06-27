package txmanager

import (
	"errors"
	"sync"
	"testing"
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
