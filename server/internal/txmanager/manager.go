package txmanager

import (
	"errors"
	"fmt"
	"sort"
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

// ErrTxConflict — конфликт транзакций: таблица изменена другой транзакцией
// между BEGIN и COMMIT. Клиент должен выполнить ROLLBACK и повторить.
var ErrTxConflict = errors.New("transaction conflict")

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

	// TableSnapshots — версии таблиц на момент первого обращения транзакции
	// к каждой таблице (ключ — "db/table"). Используется для обнаружения
	// конфликтов при COMMIT.
	TableSnapshots map[string]uint64
}

// Manager управляет транзакциями всех сессий.
//
// Вместо одного глобального commit-лока используется версионный счётчик на
// таблицу: коммит блокирует только таблицы, затронутые транзакцией, поэтому
// транзакции по разным таблицам коммитятся параллельно.
type Manager struct {
	counter atomic.Uint64

	// Версия каждой таблицы; инкрементируется при каждой записи.
	tableVersionsMu sync.RWMutex
	tableVersions   map[string]*atomic.Uint64 // "db/table" → версия

	// Per-table commit-локи (берутся в отсортированном порядке,
	// чтобы исключить deadlock при пересекающихся транзакциях).
	commitLocksMu sync.Mutex
	commitLocks   map[string]*sync.Mutex
}

func NewManager() *Manager {
	return &Manager{
		tableVersions: make(map[string]*atomic.Uint64),
		commitLocks:   make(map[string]*sync.Mutex),
	}
}

func tableKey(db, table string) string {
	return db + "/" + table
}

// TableVersion возвращает текущую версию таблицы.
func (m *Manager) TableVersion(db, table string) uint64 {
	m.tableVersionsMu.RLock()
	v, ok := m.tableVersions[tableKey(db, table)]
	m.tableVersionsMu.RUnlock()
	if !ok {
		return 0
	}
	return v.Load()
}

// BumpTableVersion инкрементирует версию таблицы. Вызывается executor'ом
// после каждой применённой записи (в том числе вне транзакций), чтобы
// конфликт-детекция видела все изменения.
func (m *Manager) BumpTableVersion(db, table string) {
	key := tableKey(db, table)
	m.tableVersionsMu.RLock()
	v, ok := m.tableVersions[key]
	m.tableVersionsMu.RUnlock()
	if !ok {
		m.tableVersionsMu.Lock()
		if v, ok = m.tableVersions[key]; !ok {
			v = &atomic.Uint64{}
			m.tableVersions[key] = v
		}
		m.tableVersionsMu.Unlock()
	}
	v.Add(1)
}

// Begin создаёт новую транзакцию. Версии таблиц фиксируются лениво —
// при первом обращении транзакции к таблице (см. AddOp).
func (m *Manager) Begin() *Transaction {
	return &Transaction{
		ID:             m.counter.Add(1),
		StartedAt:      time.Now(),
		State:          TxActive,
		TableSnapshots: make(map[string]uint64),
	}
}

// AddOp добавляет операцию в буфер транзакции и фиксирует версию таблицы
// при первом обращении к ней.
func (m *Manager) AddOp(tx *Transaction, op PendingOp) {
	key := tableKey(op.DB, op.Table)
	if _, exists := tx.TableSnapshots[key]; !exists {
		tx.TableSnapshots[key] = m.TableVersion(op.DB, op.Table)
	}
	tx.Ops = append(tx.Ops, op)
}

// lockTables берёт commit-локи всех таблиц в отсортированном порядке
// и возвращает функцию разблокировки.
func (m *Manager) lockTables(keys []string) func() {
	locks := make([]*sync.Mutex, 0, len(keys))
	for _, key := range keys {
		m.commitLocksMu.Lock()
		l, ok := m.commitLocks[key]
		if !ok {
			l = &sync.Mutex{}
			m.commitLocks[key] = l
		}
		m.commitLocksMu.Unlock()
		l.Lock()
		locks = append(locks, l)
	}
	return func() {
		for i := len(locks) - 1; i >= 0; i-- {
			locks[i].Unlock()
		}
	}
}

// Commit проверяет конфликты и применяет операции транзакции.
// Блокируются только таблицы, затронутые этой транзакцией.
func (m *Manager) Commit(tx *Transaction, applyFn func([]PendingOp) error) error {
	tables := make([]string, 0, len(tx.TableSnapshots))
	for t := range tx.TableSnapshots {
		tables = append(tables, t)
	}
	sort.Strings(tables)

	unlock := m.lockTables(tables)
	defer unlock()

	// Конфликты: не изменились ли таблицы с момента первого обращения?
	for _, t := range tables {
		m.tableVersionsMu.RLock()
		v, ok := m.tableVersions[t]
		m.tableVersionsMu.RUnlock()

		currentVersion := uint64(0)
		if ok {
			currentVersion = v.Load()
		}
		snapshotVersion := tx.TableSnapshots[t]

		if currentVersion != snapshotVersion {
			return fmt.Errorf(
				"%w: table %q was modified by another transaction "+
					"(snapshot version=%d, current=%d); ROLLBACK and retry",
				ErrTxConflict, t, snapshotVersion, currentVersion)
		}
	}

	// Конфликтов нет — применяем. Версии таблиц инкрементирует сам
	// executor при применении каждой операции (BumpTableVersion).
	if err := applyFn(tx.Ops); err != nil {
		return err
	}
	return nil
}

// Rollback очищает буфер без применения.
func (tx *Transaction) Rollback() {
	tx.Ops = nil
	tx.State = TxIdle
}
