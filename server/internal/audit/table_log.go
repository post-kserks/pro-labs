package audit

import (
	"encoding/json"
	"fmt"
	"log/slog"
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

	verifierDone chan struct{}
	stopOnce     sync.Once
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
			RLSEnabled: true,
			Policies: []storage.RLSPolicy{
				{
					Name:      "audit_admin_all",
					ToUser:    "admin",
					UsingExpr: "true",
				},
				{
					Name:      "audit_user_own_entries",
					ToUser:    "nonadmin",
					UsingExpr: "actor = current_user",
				},
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
		int64(entry.ID),  // id
		entry.OccurredAt, // occurred_at
		entry.Actor,      // actor
		entry.Action,     // action
		entry.Target,     // target
		entry.Detail,     // detail
		entry.PrevHash,   // prev_hash
		entry.EntryHash,  // entry_hash
		string(data),     // data (full entry as JSON)
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

// TruncateKeepLast removes all entries except the last n from the audit log.
// If n is 0, all entries are removed.
func (t *TableLog) TruncateKeepLast(n int) error {
	t.mu.Lock()
	defer t.mu.Unlock()

	entries, err := t.readAllUnlocked()
	if err != nil {
		return err
	}

	// Truncate the table
	if err := t.storage.TruncateTable(SystemDB, AuditTableName); err != nil {
		return fmt.Errorf("truncate audit table: %w", err)
	}

	// Re-insert kept entries
	if n > 0 && n < len(entries) {
		entries = entries[len(entries)-n:]
	} else if n == 0 {
		entries = nil
	}

	// Reset hash chain state
	t.lastHash = ""
	t.nextID = 0

	for _, e := range entries {
		if e.ID > t.nextID {
			t.nextID = e.ID
		}
		// Re-insert with fresh hash chain
		e.PrevHash = t.lastHash
		e.EntryHash = e.HashChain(t.lastHash)
		data, err := json.Marshal(e)
		if err != nil {
			return fmt.Errorf("marshal audit entry: %w", err)
		}
		row := storage.Row{
			int64(e.ID),
			e.OccurredAt,
			e.Actor,
			e.Action,
			e.Target,
			e.Detail,
			e.PrevHash,
			e.EntryHash,
			string(data),
		}
		if _, err := t.storage.InsertRows(SystemDB, AuditTableName, []storage.Row{row}); err != nil {
			return fmt.Errorf("re-insert audit entry: %w", err)
		}
		t.lastHash = e.EntryHash
	}

	return nil
}

// readAllUnlocked reads all entries without acquiring the mutex (caller must hold it).
func (t *TableLog) readAllUnlocked() ([]Entry, error) {
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

// StartVerifier launches a background goroutine that periodically checks chain
// integrity. It stops when StopVerifier is called.
func (t *TableLog) StartVerifier(interval time.Duration, logger *slog.Logger) {
	if interval < 10*time.Second {
		interval = 10 * time.Second
	}
	t.verifierDone = make(chan struct{})
	go t.verifyLoop(interval, logger)
}

func (t *TableLog) verifyLoop(interval time.Duration, logger *slog.Logger) {
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-t.verifierDone:
			return
		case <-ticker.C:
			ok, count, err := t.VerifyChain()
			if err != nil {
				logger.Error("audit chain verification failed",
					"entry_count", count,
					"error", err,
				)
			} else if !ok {
				logger.Error("audit chain integrity broken",
					"entry_count", count,
				)
			} else {
				logger.Debug("audit chain verified OK", "entry_count", count)
			}
		}
	}
}

// StopVerifier stops the background verifier goroutine.
// Safe to call multiple times.
func (t *TableLog) StopVerifier() {
	t.stopOnce.Do(func() {
		if t.verifierDone != nil {
			close(t.verifierDone)
		}
	})
}
