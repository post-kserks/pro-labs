package audit

import (
	"encoding/json"
	"fmt"
	"sync"
	"time"

	"vaultdb/internal/storage"
)

const (
	SystemDB       = "system"
	AuditTableName = "vaultdb_audit_log"
)

// TableLog implements Log using a system table in the page engine.
// The table is named "vaultdb_audit_log" in the "system" database.
type TableLog struct {
	mu       sync.Mutex
	storage  storage.StorageEngine
	nextID   uint64
	lastHash string
}

// NewTableLog creates a new table-based audit log.
func NewTableLog(store storage.StorageEngine) *TableLog {
	return &TableLog{storage: store}
}

// EnsureTable creates the audit log table and database if they don't exist.
func (t *TableLog) EnsureTable() error {
	if !t.storage.DatabaseExists(SystemDB) {
		if err := t.storage.CreateDatabase(SystemDB); err != nil {
			return fmt.Errorf("create system database: %w", err)
		}
	}
	if !t.storage.TableExists(SystemDB, AuditTableName) {
		schema := storage.TableSchema{
			Name: AuditTableName,
			Columns: []storage.ColumnSchema{
				{Name: "id", Type: "INT", PrimaryKey: true, AutoIncrement: true},
				{Name: "occurred_at", Type: "TIMESTAMP"},
				{Name: "actor", Type: "VARCHAR", VarcharLen: 255},
				{Name: "action", Type: "VARCHAR", VarcharLen: 255},
				{Name: "target", Type: "VARCHAR", VarcharLen: 512},
				{Name: "detail", Type: "TEXT"},
				{Name: "prev_hash", Type: "VARCHAR", VarcharLen: 64},
				{Name: "entry_hash", Type: "VARCHAR", VarcharLen: 64},
				{Name: "data", Type: "TEXT"},
			},
		}
		if err := t.storage.CreateTable(SystemDB, schema); err != nil {
			return fmt.Errorf("create audit log table: %w", err)
		}
	}
	return nil
}

// Append adds an entry to the audit log with hash-chain integrity.
func (t *TableLog) Append(entry Entry) error {
	t.mu.Lock()
	defer t.mu.Unlock()

	if entry.ID == 0 {
		t.nextID++
		entry.ID = t.nextID
	}
	if entry.OccurredAt.IsZero() {
		entry.OccurredAt = time.Now().UTC()
	}

	// Compute hash chain
	entry.PrevHash = t.lastHash
	entry.EntryHash = entry.HashChain(t.lastHash)

	// Serialize entry as JSON for storage
	data, err := json.Marshal(entry)
	if err != nil {
		return fmt.Errorf("marshal audit entry: %w", err)
	}

	// Insert into system table
	row := storage.Row{
		int64(entry.ID),       // id
		entry.OccurredAt,      // occurred_at
		entry.Actor,           // actor
		entry.Action,          // action
		entry.Target,          // target
		entry.Detail,          // detail
		entry.PrevHash,        // prev_hash
		entry.EntryHash,       // entry_hash
		string(data),          // data (full entry as JSON)
	}

	if _, err := t.storage.InsertRows(SystemDB, AuditTableName, []storage.Row{row}); err != nil {
		return fmt.Errorf("insert audit entry: %w", err)
	}

	t.lastHash = entry.EntryHash
	return nil
}

// ReadAll reads all audit log entries.
func (t *TableLog) ReadAll() ([]Entry, error) {
	rows, err := t.storage.ReadCurrentRows(SystemDB, AuditTableName)
	if err != nil {
		return nil, err
	}

	var entries []Entry
	for _, row := range rows {
		if len(row) < 9 {
			continue
		}
		var entry Entry
		if data, ok := row[8].(string); ok {
			if err := json.Unmarshal([]byte(data), &entry); err != nil {
				continue
			}
		}
		entries = append(entries, entry)
	}
	return entries, nil
}

// LastHash returns the hash of the most recent entry.
func (t *TableLog) LastHash() (string, error) {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.lastHash != "" {
		return t.lastHash, nil
	}
	// Read from table
	entries, err := t.ReadAll()
	if err != nil {
		return "", err
	}
	if len(entries) == 0 {
		return "", nil
	}
	return entries[len(entries)-1].EntryHash, nil
}

// VerifyChain checks the integrity of the entire hash chain.
func (t *TableLog) VerifyChain() (bool, int, error) {
	entries, err := t.ReadAll()
	if err != nil {
		return false, 0, err
	}

	prevHash := ""
	for i, e := range entries {
		expected := e.HashChain(prevHash)
		if expected != e.EntryHash {
			return false, i, fmt.Errorf("chain broken at entry %d: expected %s, got %s", e.ID, expected, e.EntryHash)
		}
		prevHash = e.EntryHash
	}
	return true, len(entries), nil
}
