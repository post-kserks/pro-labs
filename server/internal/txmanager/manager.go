package txmanager

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
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

// ErrTxConflict — конфликт транзакций.
var ErrTxConflict = errors.New("transaction conflict")

const defaultSpillThreshold = 10000

// PendingOp — одна буферизованная операция внутри транзакции.
type PendingOp struct {
	Type    string
	DB      string
	Table   string
	Payload interface{}
	OldRow  interface{}
	Row     interface{}
	Pos     int
}

// Transaction — активная транзакция одной сессии.
type Transaction struct {
	ID        uint64
	StartedAt time.Time
	State     TxState
	Ops       []PendingOp

	TableSnapshots map[string]uint64

	// Spill to disk
	spilled   bool
	spillPath string
	spillDir  string
}

// Manager управляет транзакциями всех сессий.
type Manager struct {
	counter        atomic.Uint64
	SpillThreshold int
	SpillDir       string

	tableVersionsMu sync.RWMutex
	tableVersions   map[string]*atomic.Uint64

	commitLocksMu sync.Mutex
	commitLocks   map[string]*sync.Mutex
}

func NewManager() *Manager {
	return &Manager{
		tableVersions:  make(map[string]*atomic.Uint64),
		commitLocks:    make(map[string]*sync.Mutex),
		SpillThreshold: defaultSpillThreshold,
	}
}

func tableKey(db, table string) string {
	return db + "/" + table
}

func (m *Manager) TableVersion(db, table string) uint64 {
	m.tableVersionsMu.RLock()
	v, ok := m.tableVersions[tableKey(db, table)]
	m.tableVersionsMu.RUnlock()
	if !ok {
		return 0
	}
	return v.Load()
}

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

func (m *Manager) Begin() *Transaction {
	tx := &Transaction{
		ID:             m.counter.Add(1),
		StartedAt:      time.Now(),
		State:          TxActive,
		TableSnapshots: make(map[string]uint64),
		spillDir:       m.SpillDir,
	}
	return tx
}

// AddOp добавляет операцию в буфер транзакции.
// При превышении SpillThreshold сериализует буфер во временный файл.
func (m *Manager) AddOp(tx *Transaction, op PendingOp) {
	key := tableKey(op.DB, op.Table)
	if _, exists := tx.TableSnapshots[key]; !exists {
		tx.TableSnapshots[key] = m.TableVersion(op.DB, op.Table)
	}

	if tx.spilled {
		// Дописываем в существующий файл
		tx.appendOpToFile(op)
	} else {
		tx.Ops = append(tx.Ops, op)
		// Проверяем порог
		if len(tx.Ops) >= m.SpillThreshold && m.SpillDir != "" {
			tx.spillToDisk()
		}
	}
}

func (tx *Transaction) spillToDisk() {
	if tx.spillDir == "" {
		return
	}

	path := filepath.Join(tx.spillDir, fmt.Sprintf("tx_%d.tmp", tx.ID))
	f, err := os.Create(path)
	if err != nil {
		return // silently ignore — continue in-memory
	}
	enc := json.NewEncoder(f)
	for _, op := range tx.Ops {
		if err := enc.Encode(op); err != nil {
			f.Close()
			os.Remove(path)
			return // write error — continue in-memory
		}
	}
	f.Close()

	tx.spilled = true
	tx.spillPath = path
	tx.Ops = nil // освобождаем RAM
}

func (tx *Transaction) appendOpToFile(op PendingOp) {
	f, err := os.OpenFile(tx.spillPath, os.O_APPEND|os.O_WRONLY, 0600)
	if err != nil {
		return
	}
	enc := json.NewEncoder(f)
	_ = enc.Encode(op)
	f.Close()
}

// ReadOps возвращает операции: из памяти или из файла.
func (tx *Transaction) ReadOps() ([]PendingOp, error) {
	if !tx.spilled {
		return tx.Ops, nil
	}

	f, err := os.Open(tx.spillPath)
	if err != nil {
		return nil, fmt.Errorf("open spill file: %w", err)
	}
	defer f.Close()

	var ops []PendingOp
	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		var op PendingOp
		if err := json.Unmarshal(scanner.Bytes(), &op); err != nil {
			continue
		}
		ops = append(ops, op)
	}
	return ops, nil
}

func (m *Manager) lockTables(keys []string) func() {
	sort.Strings(keys)
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
func (m *Manager) Commit(tx *Transaction, applyFn func([]PendingOp) error) error {
	tables := make([]string, 0, len(tx.TableSnapshots))
	for t := range tx.TableSnapshots {
		tables = append(tables, t)
	}
	sort.Strings(tables)

	unlock := m.lockTables(tables)
	defer unlock()

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

	// Читаем ops (из памяти или из файла)
	ops, err := tx.ReadOps()
	if err != nil {
		return fmt.Errorf("read ops: %w", err)
	}

	if err := applyFn(ops); err != nil {
		return err
	}
	return nil
}

// Rollback очищает буфер и удаляет spill файл.
func (tx *Transaction) Rollback() {
	tx.Ops = nil
	tx.State = TxIdle
	if tx.spilled && tx.spillPath != "" {
		os.Remove(tx.spillPath)
		tx.spilled = false
		tx.spillPath = ""
	}
}

// IsCommitted возвращает true, если транзакция с указанным xid считается завершённой.
// Упрощение: все xid < текущего счётчика считаются committed.
func (m *Manager) IsCommitted(xid uint64) bool {
	return xid < m.counter.Load()
}

// EnsureCounterAtLeast гарантирует, что счётчик txid не меньше n.
// Используется при загрузке catalog page engine, чтобы ранее выделенные
// txid считались committed.
func (m *Manager) EnsureCounterAtLeast(n uint64) {
	for {
		cur := m.counter.Load()
		if n <= cur {
			return
		}
		if m.counter.CompareAndSwap(cur, n) {
			return
		}
	}
}

// CleanupSpillFiles удаляет старые spill файлы (вызывается при старте сервера).
func CleanupSpillFiles(dir string) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return
	}
	for _, entry := range entries {
		if !entry.IsDir() && filepath.Ext(entry.Name()) == ".tmp" {
			os.Remove(filepath.Join(dir, entry.Name()))
		}
	}
}
