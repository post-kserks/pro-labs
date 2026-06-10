package txmanager

import (
	"sync"
	"sync/atomic"
	"time"
)

// TxState — состояние транзакции.
type TxState int

const (
	TxIdle   TxState = iota // нет активной транзакции
	TxActive                // BEGIN выполнен, ожидаем COMMIT/ROLLBACK
)

// PendingOp — одна буферизованная операция внутри транзакции.
type PendingOp struct {
	Type    string // "insert", "update", "delete"
	DB      string
	Table   string
	Payload interface{} // зависит от Type (обычно AST узел)
}

// Transaction — активная транзакция одной сессии.
type Transaction struct {
	ID        uint64
	StartedAt time.Time
	State     TxState
	Ops       []PendingOp // буфер операций

	// Snapshot: TxID на момент BEGIN.
	// Используется для обнаружения конфликтов при COMMIT.
	SnapshotTxID uint64
}

// Manager управляет транзакциями всех сессий.
type Manager struct {
	mu      sync.Mutex
	counter atomic.Uint64
}

func NewManager() *Manager {
	return &Manager{}
}

func (m *Manager) Begin(snapshotTxID uint64) *Transaction {
	return &Transaction{
		ID:           m.counter.Add(1),
		StartedAt:    time.Now(),
		State:        TxActive,
		SnapshotTxID: snapshotTxID,
	}
}

// WithCommitLock serializes commit critical sections (conflict check + apply)
// across sessions, so another commit cannot slip in between validation and
// application.
func (m *Manager) WithCommitLock(fn func() error) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	return fn()
}

// AddOp добавляет операцию в буфер транзакции.
func (tx *Transaction) AddOp(op PendingOp) {
	tx.Ops = append(tx.Ops, op)
}

// Rollback очищает буфер без применения.
func (tx *Transaction) Rollback() {
	tx.Ops = nil
	tx.State = TxIdle
}
