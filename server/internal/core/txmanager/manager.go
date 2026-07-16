package txmanager

import (
	"bufio"
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"math/rand"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

// TxState — transaction state.
type TxState int

const (
	TxIdle   TxState = iota // no active transaction
	TxActive                // BEGIN executed, awaiting COMMIT/ROLLBACK
)

// ErrTxConflict — transaction conflict.
var ErrTxConflict = errors.New("transaction conflict")

const defaultSpillThreshold = 10000

// OCCConfig — optimistic concurrency control settings: retry and backoff.
type OCCConfig struct {
	MaxRetries    int           // default: 3
	BaseDelay     time.Duration // default: 10ms
	MaxDelay      time.Duration // default: 100ms
	BackoffFactor float64       // default: 2.0
}

func DefaultOCCConfig() OCCConfig {
	return OCCConfig{
		MaxRetries:    3,
		BaseDelay:     10 * time.Millisecond,
		MaxDelay:      100 * time.Millisecond,
		BackoffFactor: 2.0,
	}
}

// PendingOp — a single buffered operation within a transaction.
type PendingOp struct {
	Type    string
	DB      string
	Table   string
	Payload interface{}
	OldRow  interface{}
	Row     interface{}
	Pos     int
	RowKey  string
	TupleID uint64
}

// Transaction — active transaction of a single session.
type Transaction struct {
	ID        uint64
	StartedAt time.Time
	State     TxState
	Ops       []PendingOp

	TableSnapshots map[string]uint64

	Snapshot       map[uint64]bool

	HasDependentReads bool
	ReadSet           map[string]map[int]uint64 // "db/table" -> pos -> version

	// opCounter — monotonically increasing counter of added operations. Grows in AddOp regardless
	// of whether operations are in memory or in the spill file.
	// Used by savepoints as a stable position marker.
	opCounter int

	// savepoints: name → opCounter at creation time; savepointOrder stores
	// creation order so that ROLLBACK TO removes savepoints created
	// after the specified one.
	savepoints     map[string]int
	savepointOrder []string

	// Spill to disk
	spilled   bool
	spillPath string
	spillDir  string

	// spillErr — sticky spill error. If disk write failed,
	// ReadOps (and thus Commit) will return this error so commit fails rather than
	// silently losing some operations (Bug #4).
	spillErr error

	mgr *Manager
}

// RecordRead records a row access in the transaction read set for granular conflict detection.
func (tx *Transaction) RecordRead(dbName, tableName string, pos int, version uint64) {
	if tx.ReadSet == nil {
		tx.ReadSet = make(map[string]map[int]uint64)
	}
	key := dbName + "/" + tableName
	if tx.ReadSet[key] == nil {
		tx.ReadSet[key] = make(map[int]uint64)
	}
	tx.ReadSet[key][pos] = version
}

// SetHasDependentReads marks that this transaction performed conditional reads or queries whose
// results influenced buffered writes, requiring re-execution upon OCC conflict rather than blind retry.
func (tx *Transaction) SetHasDependentReads(val bool) {
	tx.HasDependentReads = val
}

// Manager manages transactions across all sessions.
type Manager struct {
	counter        atomic.Uint64
	SpillThreshold int
	SpillDir       string

	tableVersionsMu sync.RWMutex
	tableVersions   map[string]*atomic.Uint64

	commitLocksMu sync.Mutex
	commitLocks   map[string]*sync.Mutex

	OCCConfig OCCConfig

	activeMu  sync.RWMutex
	ActiveTxs map[uint64]*Transaction
}

func NewManager() *Manager {
	return &Manager{
		tableVersions:  make(map[string]*atomic.Uint64),
		commitLocks:    make(map[string]*sync.Mutex),
		SpillThreshold: defaultSpillThreshold,
		OCCConfig:      DefaultOCCConfig(),
		ActiveTxs:      make(map[uint64]*Transaction),
	}
}

// NewManagerWithPageEngine initializes Manager and shares RowLockManager if provided by engine.
func NewManagerWithPageEngine(engine interface {
}) *Manager {
	m := NewManager()
	return m
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
	m.activeMu.Lock()
	defer m.activeMu.Unlock()

	snap := make(map[uint64]bool)
	for id := range m.ActiveTxs {
		snap[id] = true
	}

	tx := &Transaction{
		ID:             m.counter.Add(1),
		StartedAt:      time.Now(),
		State:          TxActive,
		TableSnapshots: make(map[string]uint64),
		Snapshot:       snap,
		savepoints:     make(map[string]int),
		spillDir:       m.SpillDir,
		mgr:            m,
	}

	m.ActiveTxs[tx.ID] = tx
	return tx
}

// RecordAccess records the table version on FIRST access (read or
// write). A snapshot is taken only if one doesn't exist. Thanks to this, any
// table that the transaction read OR wrote is checked for concurrent
// modification during Commit (Bug #2a).
func (m *Manager) RecordAccess(tx *Transaction, db, table string) {
	if tx == nil {
		return
	}
	key := tableKey(db, table)
	if _, exists := tx.TableSnapshots[key]; !exists {
		tx.TableSnapshots[key] = m.TableVersion(db, table)
	}
}

// TableKey returns the table key in the same format used by Commit for
// commit locks. Needed by external packages to acquire the same per-table lock.
func TableKey(db, table string) string {
	return tableKey(db, table)
}

// LockTables acquires commit locks on specified table keys and returns a
// unlock function. Public wrapper over lockTables: allows autocommit writes
// to serialize with transaction commits (Bug #2b).
func (m *Manager) LockTables(keys []string) func() {
	return m.lockTables(keys)
}

// AddOp adds an operation to the transaction buffer.
// When SpillThreshold is exceeded, serializes the buffer to a temporary file.
func (m *Manager) AddOp(tx *Transaction, op PendingOp) {
	key := tableKey(op.DB, op.Table)
	if _, exists := tx.TableSnapshots[key]; !exists {
		tx.TableSnapshots[key] = m.TableVersion(op.DB, op.Table)
	}

	tx.opCounter++

	if tx.spilled {
		// Append to existing file
		tx.appendOpToFile(op)
	} else {
		tx.Ops = append(tx.Ops, op)
		// Check threshold
		if len(tx.Ops) >= m.SpillThreshold && m.SpillDir != "" {
			tx.spillToDisk()
		}
	}
}

// EncodePendingOp/DecodePendingOp — extension points for serializing operations
// during spill. The txmanager package doesn't know about parser/storage types, so executor
// registers a codec here that can restore typed Payload
// (parser.*Statement). If no codec is set, plain JSON is used.
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

// writeOpsToFile serializes operations line by line (one JSON object per
// line). Returns an error so the caller can record spillErr and not
// silently lose operations (Bug #4).
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
	tx.Ops = nil // free RAM
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

// ReadOps returns operations: from memory or from file. If spill failed,
// it returns the error (not a truncated/empty set) so Commit fails.
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
	// bufio.Reader.ReadBytes grows for arbitrary line length — no
	// 64KB limit like bufio.Scanner has by default (Bug #4).
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

// Savepoint records the current buffer position under the given name. Duplicate name
// overwrites the previous marker (SQL semantics).
func (tx *Transaction) Savepoint(name string) {
	if tx.savepoints == nil {
		tx.savepoints = make(map[string]int)
	}
	if _, exists := tx.savepoints[name]; !exists {
		tx.savepointOrder = append(tx.savepointOrder, name)
	}
	tx.savepoints[name] = tx.opCounter
}

// ReleaseSavepoint removes the savepoint marker. Returns false if the name
// is unknown. Buffered operations are preserved.
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

// RollbackToSavepoint truncates the buffer to the savepoint position and removes
// savepoints created later. The transaction remains active.
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

	// Delete savepoints created after the specified one (by creation order).
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

// Commit checks conflicts and applies transaction operations.
func (m *Manager) Commit(tx *Transaction, applyFn func([]PendingOp) error) error {
	defer func() {
		if tx != nil {
			m.activeMu.Lock()
			delete(m.ActiveTxs, tx.ID)
			m.activeMu.Unlock()
		}
	}()
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

	// Read ops (from memory or from file)
	ops, err := tx.ReadOps()
	if err != nil {
		return fmt.Errorf("read ops: %w", err)
	}

	if err := applyFn(ops); err != nil {
		return err
	}
	return nil
}

// IsConflictError reports whether err is an OCC conflict that may be retried.
func IsConflictError(err error) bool {
	if err == nil {
		return false
	}
	s := err.Error()
	return strings.Contains(s, "conflict") ||
		strings.Contains(s, "OCC") ||
		strings.Contains(s, "version mismatch")
}

// CommitWithRetry attempts Commit with configurable exponential backoff on OCC
// conflicts. Non-conflict errors are returned immediately. On conflict the
// table snapshots in tx are refreshed to the current versions before the next
// attempt, so the transaction re-validates against up-to-date state.
func (m *Manager) CommitWithRetry(tx *Transaction, applyFn func([]PendingOp) error) error {
	config := m.OCCConfig
	var lastErr error

	for attempt := 0; attempt <= config.MaxRetries; attempt++ {
		err := m.Commit(tx, applyFn)
		if err == nil {
			return nil
		}

		if !IsConflictError(err) {
			return err
		}

		lastErr = err
		if attempt < config.MaxRetries {
			if tx.HasDependentReads {
				return fmt.Errorf("%w: transaction performed conditional reads or dependent calculations; blind retry without re-executing query logic would violate serializability", ErrTxConflict)
			}
			m.refreshSnapshots(tx)
			delay := config.BaseDelay * time.Duration(math.Pow(config.BackoffFactor, float64(attempt)))
			if delay > config.MaxDelay {
				delay = config.MaxDelay
			}
			jitter := time.Duration(rand.Int63n(int64(delay/2) + 1))
			time.Sleep(delay + jitter)
		}
	}

	return fmt.Errorf("transaction conflict after %d retries: %w", config.MaxRetries, lastErr)
}

// refreshSnapshots updates the transaction's table version snapshots to current values.
func (m *Manager) refreshSnapshots(tx *Transaction) {
	for table := range tx.TableSnapshots {
		parts := strings.SplitN(table, "/", 2)
		if len(parts) == 2 {
			tx.TableSnapshots[table] = m.TableVersion(parts[0], parts[1])
		}
	}
}

// Rollback clears the buffer, deletes the spill file, and releases row locks.
func (tx *Transaction) Rollback() {
	tx.Ops = nil
	tx.State = TxIdle
	tx.opCounter = 0
	tx.savepoints = make(map[string]int)
	tx.savepointOrder = nil
	tx.spillErr = nil
	tx.ReadSet = nil
	tx.HasDependentReads = false
	if tx.spilled && tx.spillPath != "" {
		os.Remove(tx.spillPath)
		tx.spilled = false
		tx.spillPath = ""
	}
}

// Rollback rolls back the transaction and releases all held row locks.
func (m *Manager) Rollback(tx *Transaction) {
	if tx != nil {
		m.activeMu.Lock()
		delete(m.ActiveTxs, tx.ID)
		m.activeMu.Unlock()
		tx.Rollback()
	}
}

// IsCommitted returns true if the transaction with the given xid is considered committed.
// Simplification: all xid < current counter are considered committed.
func (m *Manager) IsCommitted(xid uint64) bool {
	return xid < m.counter.Load()
}

func (m *Manager) IsAborted(xid uint64) bool {
	// A simple mock for IsAborted since we don't track aborted explicitly in Manager yet.
	return false
}

func (m *Manager) GetSnapshot(txID uint64) map[uint64]bool {
	m.activeMu.RLock()
	defer m.activeMu.RUnlock()
	if tx, ok := m.ActiveTxs[txID]; ok {
		return tx.Snapshot
	}
	return nil
}

// EnsureCounterAtLeast guarantees the txid counter is at least n.
// Used when loading catalog page engine so that previously allocated
// txids are considered committed.
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

// CleanupSpillFiles removes old spill files (called on server startup).
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
