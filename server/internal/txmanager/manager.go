package txmanager

import (
	"bufio"
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
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

	// opCounter — сквозной счётчик добавленных операций. Растёт в AddOp вне
	// зависимости от того, лежат ли операции в памяти или в spill-файле.
	// Используется savepoint'ами как стабильный маркер позиции.
	opCounter int

	// savepoints: имя → opCounter на момент создания; savepointOrder хранит
	// порядок создания, чтобы при ROLLBACK TO удалять savepoint'ы, созданные
	// позже указанного.
	savepoints     map[string]int
	savepointOrder []string

	// Spill to disk
	spilled   bool
	spillPath string
	spillDir  string

	// spillErr — «липкая» ошибка spill'а. Если запись на диск не удалась,
	// ReadOps (а значит и Commit) вернёт эту ошибку, чтобы коммит упал, а не
	// потерял часть операций молча (Bug #4).
	spillErr error
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
		savepoints:     make(map[string]int),
		spillDir:       m.SpillDir,
	}
	return tx
}

// RecordAccess фиксирует версию таблицы при ПЕРВОМ обращении (чтении или
// записи). Снимок берётся только если его ещё нет. Благодаря этому любая
// таблица, которую транзакция читала ИЛИ писала, проверяется на конкурентную
// модификацию во время Commit (Bug #2a).
func (m *Manager) RecordAccess(tx *Transaction, db, table string) {
	if tx == nil {
		return
	}
	key := tableKey(db, table)
	if _, exists := tx.TableSnapshots[key]; !exists {
		tx.TableSnapshots[key] = m.TableVersion(db, table)
	}
}

// TableKey возвращает ключ таблицы в том же формате, что использует Commit для
// commit-локов. Нужен внешним пакетам, чтобы брать тот же per-table lock.
func TableKey(db, table string) string {
	return tableKey(db, table)
}

// LockTables берёт commit-локи на указанные ключи таблиц и возвращает функцию
// разблокировки. Публичная обёртка над lockTables: позволяет autocommit-записям
// сериализоваться с коммитами транзакций (Bug #2b).
func (m *Manager) LockTables(keys []string) func() {
	return m.lockTables(keys)
}

// AddOp добавляет операцию в буфер транзакции.
// При превышении SpillThreshold сериализует буфер во временный файл.
func (m *Manager) AddOp(tx *Transaction, op PendingOp) {
	key := tableKey(op.DB, op.Table)
	if _, exists := tx.TableSnapshots[key]; !exists {
		tx.TableSnapshots[key] = m.TableVersion(op.DB, op.Table)
	}

	tx.opCounter++

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

// EncodePendingOp/DecodePendingOp — точки расширения для сериализации операций
// при spill'е. Пакет txmanager не знает о типах parser/storage, поэтому executor
// регистрирует здесь кодек, умеющий восстанавливать типизированный Payload
// (parser.*Statement). Если кодек не задан — используется обычный JSON.
var (
	EncodePendingOp func(op PendingOp) ([]byte, error)
	DecodePendingOp func(data []byte) (PendingOp, error)
)

func encodeOp(op PendingOp) ([]byte, error) {
	if EncodePendingOp != nil {
		return EncodePendingOp(op)
	}
	return json.Marshal(op)
}

func decodeOp(data []byte) (PendingOp, error) {
	if DecodePendingOp != nil {
		return DecodePendingOp(data)
	}
	var op PendingOp
	err := json.Unmarshal(data, &op)
	return op, err
}

// writeOpsToFile сериализует операции построчно (по одному JSON-объекту в
// строке). Возвращает ошибку, чтобы вызывающий мог зафиксировать spillErr и не
// потерять операции молча (Bug #4).
func writeOpsToFile(path string, ops []PendingOp) error {
	f, err := os.Create(path)
	if err != nil {
		return err
	}
	w := bufio.NewWriter(f)
	for _, op := range ops {
		b, err := encodeOp(op)
		if err != nil {
			f.Close()
			os.Remove(path)
			return err
		}
		if _, err := w.Write(append(b, '\n')); err != nil {
			f.Close()
			os.Remove(path)
			return err
		}
	}
	if err := w.Flush(); err != nil {
		f.Close()
		os.Remove(path)
		return err
	}
	return f.Close()
}

func (tx *Transaction) spillToDisk() {
	if tx.spillDir == "" {
		return
	}

	path := filepath.Join(tx.spillDir, fmt.Sprintf("tx_%d.tmp", tx.ID))
	if err := writeOpsToFile(path, tx.Ops); err != nil {
		tx.spillErr = fmt.Errorf("spill to disk: %w", err)
		return
	}

	tx.spilled = true
	tx.spillPath = path
	tx.Ops = nil // освобождаем RAM
}

func (tx *Transaction) appendOpToFile(op PendingOp) {
	b, err := encodeOp(op)
	if err != nil {
		tx.spillErr = fmt.Errorf("spill append encode: %w", err)
		return
	}
	f, err := os.OpenFile(tx.spillPath, os.O_APPEND|os.O_WRONLY, 0600)
	if err != nil {
		tx.spillErr = fmt.Errorf("spill append open: %w", err)
		return
	}
	if _, err := f.Write(append(b, '\n')); err != nil {
		f.Close()
		tx.spillErr = fmt.Errorf("spill append write: %w", err)
		return
	}
	if err := f.Close(); err != nil {
		tx.spillErr = fmt.Errorf("spill append close: %w", err)
	}
}

// ReadOps возвращает операции: из памяти или из файла. Если spill завершился
// ошибкой — возвращает её (а не усечённый/пустой набор), чтобы Commit упал.
func (tx *Transaction) ReadOps() ([]PendingOp, error) {
	if tx.spillErr != nil {
		return nil, tx.spillErr
	}
	if !tx.spilled {
		return tx.Ops, nil
	}

	f, err := os.Open(tx.spillPath)
	if err != nil {
		return nil, fmt.Errorf("open spill file: %w", err)
	}
	defer f.Close()

	var ops []PendingOp
	// bufio.Reader.ReadBytes растёт под произвольный размер строки — нет
	// ограничения в 64KB, как у bufio.Scanner по умолчанию (Bug #4).
	r := bufio.NewReader(f)
	for {
		line, rerr := r.ReadBytes('\n')
		if trimmed := bytes.TrimRight(line, "\n"); len(trimmed) > 0 {
			op, derr := decodeOp(trimmed)
			if derr != nil {
				return nil, fmt.Errorf("decode spill op: %w", derr)
			}
			ops = append(ops, op)
		}
		if rerr != nil {
			if errors.Is(rerr, io.EOF) {
				break
			}
			return nil, fmt.Errorf("read spill file: %w", rerr)
		}
	}
	return ops, nil
}

// Savepoint фиксирует текущую позицию буфера под именем name. Повторное имя
// перезаписывает прежний маркер (семантика SQL).
func (tx *Transaction) Savepoint(name string) {
	if tx.savepoints == nil {
		tx.savepoints = make(map[string]int)
	}
	if _, exists := tx.savepoints[name]; !exists {
		tx.savepointOrder = append(tx.savepointOrder, name)
	}
	tx.savepoints[name] = tx.opCounter
}

// ReleaseSavepoint удаляет маркер savepoint'а. Возвращает false, если имя
// неизвестно. Буферизованные операции сохраняются.
func (tx *Transaction) ReleaseSavepoint(name string) bool {
	if _, ok := tx.savepoints[name]; !ok {
		return false
	}
	delete(tx.savepoints, name)
	for i, n := range tx.savepointOrder {
		if n == name {
			tx.savepointOrder = append(tx.savepointOrder[:i], tx.savepointOrder[i+1:]...)
			break
		}
	}
	return true
}

// RollbackToSavepoint усекает буфер до позиции savepoint'а и удаляет
// savepoint'ы, созданные позже. Транзакция остаётся активной.
func (tx *Transaction) RollbackToSavepoint(name string) error {
	n, ok := tx.savepoints[name]
	if !ok {
		return fmt.Errorf("savepoint %q does not exist", name)
	}

	ops, err := tx.ReadOps()
	if err != nil {
		return err
	}
	if n > len(ops) {
		n = len(ops)
	}
	kept := append([]PendingOp(nil), ops[:n]...)

	if tx.spilled {
		if len(kept) == 0 {
			if tx.spillPath != "" {
				os.Remove(tx.spillPath)
			}
			tx.spilled = false
			tx.spillPath = ""
			tx.Ops = nil
		} else if err := writeOpsToFile(tx.spillPath, kept); err != nil {
			tx.spillErr = fmt.Errorf("rollback to savepoint rewrite: %w", err)
			return tx.spillErr
		}
	} else {
		tx.Ops = kept
	}
	tx.opCounter = n

	// Удаляем savepoint'ы, созданные позже указанного (по порядку создания).
	idx := -1
	for i, sn := range tx.savepointOrder {
		if sn == name {
			idx = i
			break
		}
	}
	if idx >= 0 {
		for _, sn := range tx.savepointOrder[idx+1:] {
			delete(tx.savepoints, sn)
		}
		tx.savepointOrder = tx.savepointOrder[:idx+1]
	}
	return nil
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
	tx.opCounter = 0
	tx.savepoints = make(map[string]int)
	tx.savepointOrder = nil
	tx.spillErr = nil
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
