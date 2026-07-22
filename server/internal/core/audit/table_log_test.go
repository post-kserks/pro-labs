package audit

import (
	"fmt"
	"log/slog"
	"os"
	"sync"
	"testing"
	"time"

	"vaultdb/internal/core/index"
	"vaultdb/internal/core/storage"
)

// mockStorage implements storage.StorageEngine for testing.
type mockStorage struct {
	mu        sync.Mutex
	databases map[string]map[string]*mockTable
}

type mockTable struct {
	schema storage.TableSchema
	rows   []storage.Row
}

func newMockStorage() *mockStorage {
	return &mockStorage{
		databases: make(map[string]map[string]*mockTable),
	}
}

func (m *mockStorage) CreateDatabase(name string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, exists := m.databases[name]; exists {
		return fmt.Errorf("database %q already exists", name)
	}
	m.databases[name] = make(map[string]*mockTable)
	return nil
}

func (m *mockStorage) DropDatabase(name string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	delete(m.databases, name)
	return nil
}

func (m *mockStorage) DatabaseExists(name string) bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	_, exists := m.databases[name]
	return exists
}

func (m *mockStorage) ListDatabases() ([]string, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var dbs []string
	for k := range m.databases {
		dbs = append(dbs, k)
	}
	return dbs, nil
}

func (m *mockStorage) CreateTable(dbName string, schema storage.TableSchema) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	db, ok := m.databases[dbName]
	if !ok {
		return fmt.Errorf("database %q not found", dbName)
	}
	db[schema.Name] = &mockTable{schema: schema}
	return nil
}

func (m *mockStorage) CreateTableDirect(dbName string, schema storage.TableSchema) error {
	return m.CreateTable(dbName, schema)
}

func (m *mockStorage) DropTable(dbName, tableName string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	db, ok := m.databases[dbName]
	if !ok {
		return fmt.Errorf("database %q not found", dbName)
	}
	delete(db, tableName)
	return nil
}

func (m *mockStorage) TableExists(dbName, tableName string) bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	db, ok := m.databases[dbName]
	if !ok {
		return false
	}
	_, exists := db[tableName]
	return exists
}

func (m *mockStorage) ListTables(dbName string) ([]storage.TableInfo, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	db, ok := m.databases[dbName]
	if !ok {
		return nil, fmt.Errorf("database %q not found", dbName)
	}
	var tables []storage.TableInfo
	for name := range db {
		tables = append(tables, storage.TableInfo{
			Name:        name,
		})
	}
	return tables, nil
}

func (m *mockStorage) GetTableSchema(dbName, tableName string) (*storage.TableSchema, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	db, ok := m.databases[dbName]
	if !ok {
		return nil, fmt.Errorf("database %q not found", dbName)
	}
	t, ok := db[tableName]
	if !ok {
		return nil, fmt.Errorf("table %q not found", tableName)
	}
	s := t.schema
	return &s, nil
}

func (m *mockStorage) SelectRows(dbName, tableName string) ([]storage.Row, error) {
	return m.ReadCurrentRows(dbName, tableName)
}

func (m *mockStorage) SelectRowsVM(dbName, tableName string, predicate func(rawTuple []byte) (bool, error)) ([]storage.Row, error) {
	return m.SelectRows(dbName, tableName)
}

func (m *mockStorage) ReadCurrentRows(dbName, tableName string) ([]storage.Row, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	db, ok := m.databases[dbName]
	if !ok {
		return nil, fmt.Errorf("database %q not found", dbName)
	}
	t, ok := db[tableName]
	if !ok {
		return nil, fmt.Errorf("table %q not found", tableName)
	}
	out := make([]storage.Row, len(t.rows))
	copy(out, t.rows)
	return out, nil
}

func (m *mockStorage) ReadRowsAsOf(dbName, tableName string, txID uint64) ([]storage.Row, error) {
	return nil, nil
}

func (m *mockStorage) ReadRowsByPositions(dbName, tableName string, positions []int) ([]storage.Row, error) {
	return nil, nil
}

func (m *mockStorage) CountRows(dbName, tableName string) (int, error) {
	return 0, nil
}

func (m *mockStorage) TxIDAtTimestamp(dbName, ts string) (uint64, error) {
	return 0, nil
}

func (m *mockStorage) RowHistory(dbName, tableName string, pkValue interface{}) ([]storage.VersionedRow, error) {
	return nil, nil
}

func (m *mockStorage) AllRowHistory(dbName, tableName string) ([]storage.VersionedRow, error) {
	return nil, nil
}

func (m *mockStorage) TableVersionStats(dbName, tableName string) (*storage.TableVersionStats, error) {
	return nil, nil
}

func (m *mockStorage) TableModifiedSince(db, table string, txID uint64) (bool, error) {
	return false, nil
}

func (m *mockStorage) CurrentTxID() uint64 {
	return 0
}

func (m *mockStorage) SchemaVersion() uint64 {
	return 0
}

func (m *mockStorage) ListIndexes(dbName, tableName string) ([]string, error) {
	return nil, nil
}

func (m *mockStorage) FindIndexForColumn(dbName, tableName, column string) (string, bool) {
	return "", false
}

func (m *mockStorage) GetIndex(dbName, tableName, indexName string) (index.Index, bool) {
	return nil, false
}

func (m *mockStorage) IndexLookup(dbName, tableName, column, value string) ([]int, bool) {
	return nil, false
}

func (m *mockStorage) IndexRangeLookup(dbName, tableName, column, low, high string) ([]int, bool) {
	return nil, false
}

func (m *mockStorage) IndexFTSLookup(dbName, tableName, column, query string) ([]int, bool) {
	return nil, false
}

func (m *mockStorage) ReadSampleRows(dbName, tableName string, limit int) ([]storage.Row, error) {
	return nil, nil
}



func (m *mockStorage) InsertRows(dbName, tableName string, rows []storage.Row) (int, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	db, ok := m.databases[dbName]
	if !ok {
		return 0, fmt.Errorf("database %q not found", dbName)
	}
	t, ok := db[tableName]
	if !ok {
		return 0, fmt.Errorf("table %q not found", tableName)
	}
	t.rows = append(t.rows, rows...)
	return len(rows), nil
}

func (m *mockStorage) UpdateRows(dbName, tableName string, indices []int, updates map[string]storage.Value) (int, error) {
	return 0, nil
}

func (m *mockStorage) UpdateRowsDirect(dbName, tableName string, indices []int, newValues []storage.Row) (int, error) {
	return 0, nil
}

func (m *mockStorage) UpdateRowsVM(dbName, tableName string, positions []int, predicate func(rawTuple []byte) (bool, error), updateFn func(storage.Row) (storage.Row, error), validateFn func([]int, []storage.Row) error) (int, error) {
	return 0, nil
}

func (m *mockStorage) DeleteRows(dbName, tableName string, indices []int) (int, error) {
	return 0, nil
}

func (m *mockStorage) DeleteRowsVM(dbName, tableName string, positions []int, predicate func(rawTuple []byte) (bool, error), preDelete func([]int, []storage.Row) error) (int, error) {
	return 0, nil
}

func (m *mockStorage) TruncateTable(dbName, tableName string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if db, ok := m.databases[dbName]; ok {
		if t, ok := db[tableName]; ok {
			t.rows = nil
		}
	}
	return nil
}

func (m *mockStorage) Vacuum(dbName, tableName string) (*storage.VacuumStats, error) {
	return nil, nil
}

func (m *mockStorage) AlterTableAddColumn(dbName, tableName string, col storage.ColumnSchema, defaultVal storage.Value) error {
	return nil
}

func (m *mockStorage) AlterTableDropColumn(dbName, tableName string, colName string) error {
	return nil
}

func (m *mockStorage) AlterTableRenameColumn(dbName, tableName, oldName, newName string) error {
	return nil
}

func (m *mockStorage) SetTableRLS(dbName, tableName string, enabled bool) error {
	return nil
}

func (m *mockStorage) AddPolicy(dbName, tableName string, policy storage.RLSPolicy) error {
	return nil
}

func (m *mockStorage) AlterTableRenameTable(dbName, oldName, newName string) error {
	return nil
}

func (m *mockStorage) CreateIndex(dbName, tableName, indexName, column, indexType string) error {
	return nil
}

func (m *mockStorage) CreateIndexMulti(dbName, tableName, indexName string, columns []string) error {
	return nil
}

func (m *mockStorage) CreateIndexUnique(dbName, tableName, indexName, column, indexType string) error {
	return nil
}

func (m *mockStorage) CreateIndexMultiUnique(dbName, tableName, indexName string, columns []string) error {
	return nil
}

func (m *mockStorage) DropIndex(dbName, indexName string) error {
	return nil
}



func (m *mockStorage) FinalCheckpoint() error { return nil }
func (m *mockStorage) Close() error           { return nil }
func (m *mockStorage) DataDir() string        { return "" }

func TestHashChainComputation(t *testing.T) {
	entry := Entry{
		ID:         1,
		OccurredAt: time.Date(2025, 1, 15, 10, 30, 0, 0, time.UTC),
		Actor:      "admin",
		Action:     "CREATE_TABLE",
		Target:     "users",
		Detail:     "CREATE TABLE users (id INT)",
	}
	hash1 := entry.HashChain("")
	hash2 := entry.HashChain("")

	if hash1 != hash2 {
		t.Fatal("same input should produce same hash")
	}
	if len(hash1) != 64 {
		t.Fatalf("expected 64-char hex hash, got %d chars", len(hash1))
	}

	// Different prev hash should produce different hash
	hash3 := entry.HashChain("abc123")
	if hash1 == hash3 {
		t.Fatal("different prev hash should produce different entry hash")
	}
}

func TestAppendAndReadBack(t *testing.T) {
	store := newMockStorage()
	log := NewTableLog(store)
	if err := log.EnsureTable(); err != nil {
		t.Fatal(err)
	}

	entry1 := Entry{
		Actor:  "admin",
		Action: "CREATE_TABLE",
		Target: "users",
		Detail: "CREATE TABLE users (id INT)",
	}
	if err := log.Append(entry1); err != nil {
		t.Fatal(err)
	}

	entry2 := Entry{
		Actor:  "admin",
		Action: "INSERT",
		Target: "users",
		Detail: "INSERT INTO users VALUES (1)",
	}
	if err := log.Append(entry2); err != nil {
		t.Fatal(err)
	}

	entries, err := log.ReadAll()
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 2 {
		t.Fatalf("expected 2 entries, got %d", len(entries))
	}

	// Verify first entry
	if entries[0].ID != 1 {
		t.Errorf("expected ID 1, got %d", entries[0].ID)
	}
	if entries[0].Actor != "admin" {
		t.Errorf("expected actor 'admin', got %q", entries[0].Actor)
	}
	if entries[0].Action != "CREATE_TABLE" {
		t.Errorf("expected action 'CREATE_TABLE', got %q", entries[0].Action)
	}
	if entries[0].PrevHash != "" {
		t.Errorf("expected empty prev hash for first entry, got %q", entries[0].PrevHash)
	}
	if entries[0].EntryHash == "" {
		t.Error("expected non-empty entry hash")
	}

	// Verify second entry links to first
	if entries[1].PrevHash != entries[0].EntryHash {
		t.Errorf("second entry prev_hash should match first entry hash")
	}
	if entries[1].ID != 2 {
		t.Errorf("expected ID 2, got %d", entries[1].ID)
	}
}

func TestChainVerificationValid(t *testing.T) {
	store := newMockStorage()
	log := NewTableLog(store)
	if err := log.EnsureTable(); err != nil {
		t.Fatal(err)
	}

	for i := 0; i < 5; i++ {
		entry := Entry{
			Actor:  fmt.Sprintf("user%d", i),
			Action: "INSERT",
			Target: "test",
		}
		if err := log.Append(entry); err != nil {
			t.Fatal(err)
		}
	}

	valid, count, err := log.VerifyChain()
	if err != nil {
		t.Fatal(err)
	}
	if !valid {
		t.Error("expected valid chain")
	}
	if count != 5 {
		t.Errorf("expected 5 entries, got %d", count)
	}
}

func TestChainVerificationBroken(t *testing.T) {
	store := newMockStorage()
	log := NewTableLog(store)
	if err := log.EnsureTable(); err != nil {
		t.Fatal(err)
	}

	// Add two valid entries
	for i := 0; i < 2; i++ {
		entry := Entry{
			Actor:  fmt.Sprintf("user%d", i),
			Action: "INSERT",
			Target: "test",
		}
		if err := log.Append(entry); err != nil {
			t.Fatal(err)
		}
	}

	// Tamper: manually insert a row with a broken hash
	store.mu.Lock()
	db := store.databases[SystemDB]
	tbl := db[AuditTableName]
	tbl.rows = append(tbl.rows, storage.Row{
		int64(3),
		time.Now().UTC(),
		"attacker",
		"DROP",
		"everything",
		"",
		"fake_prev",
		"fake_hash",
		`{"id":3}`,
	})
	store.mu.Unlock()

	valid, pos, err := log.VerifyChain()
	if err == nil {
		t.Fatal("expected error from broken chain")
	}
	if valid {
		t.Error("expected invalid chain")
	}
	if pos != 2 {
		t.Errorf("expected break at position 2, got %d", pos)
	}
}

func TestEmptyLogVerification(t *testing.T) {
	store := newMockStorage()
	log := NewTableLog(store)
	if err := log.EnsureTable(); err != nil {
		t.Fatal(err)
	}

	valid, count, err := log.VerifyChain()
	if err != nil {
		t.Fatal(err)
	}
	if !valid {
		t.Error("empty chain should be valid")
	}
	if count != 0 {
		t.Errorf("expected 0 entries, got %d", count)
	}
}

func TestLastHash(t *testing.T) {
	store := newMockStorage()
	log := NewTableLog(store)
	if err := log.EnsureTable(); err != nil {
		t.Fatal(err)
	}

	// Empty log
	hash, err := log.LastHash()
	if err != nil {
		t.Fatal(err)
	}
	if hash != "" {
		t.Errorf("expected empty hash, got %q", hash)
	}

	// After append
	entry := Entry{Actor: "admin", Action: "CREATE", Target: "t"}
	if err := log.Append(entry); err != nil {
		t.Fatal(err)
	}
	hash, err = log.LastHash()
	if err != nil {
		t.Fatal(err)
	}
	if hash == "" {
		t.Error("expected non-empty hash after append")
	}
}

func TestTruncateKeepLast(t *testing.T) {
	store := newMockStorage()
	log := NewTableLog(store)
	if err := log.EnsureTable(); err != nil {
		t.Fatal(err)
	}

	// Add 10 entries
	for i := 0; i < 10; i++ {
		entry := Entry{
			Actor:  fmt.Sprintf("user%d", i),
			Action: "INSERT",
			Target: "test",
		}
		if err := log.Append(entry); err != nil {
			t.Fatal(err)
		}
	}

	// Keep last 3
	if err := log.TruncateKeepLast(3); err != nil {
		t.Fatal(err)
	}

	entries, err := log.ReadAll()
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 3 {
		t.Fatalf("expected 3 entries, got %d", len(entries))
	}

	// Verify the kept entries are the last 3
	if entries[0].Actor != "user7" {
		t.Errorf("expected actor 'user7', got %q", entries[0].Actor)
	}
	if entries[2].Actor != "user9" {
		t.Errorf("expected actor 'user9', got %q", entries[2].Actor)
	}

	// Verify hash chain is valid
	valid, count, err := log.VerifyChain()
	if err != nil {
		t.Fatal(err)
	}
	if !valid {
		t.Error("chain should be valid after truncation")
	}
	if count != 3 {
		t.Errorf("expected 3 entries in chain, got %d", count)
	}
}

func TestTruncateKeepLastZero(t *testing.T) {
	store := newMockStorage()
	log := NewTableLog(store)
	if err := log.EnsureTable(); err != nil {
		t.Fatal(err)
	}

	// Add 5 entries
	for i := 0; i < 5; i++ {
		entry := Entry{
			Actor:  fmt.Sprintf("user%d", i),
			Action: "INSERT",
			Target: "test",
		}
		if err := log.Append(entry); err != nil {
			t.Fatal(err)
		}
	}

	// Keep 0 = truncate all
	if err := log.TruncateKeepLast(0); err != nil {
		t.Fatal(err)
	}

	entries, err := log.ReadAll()
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 0 {
		t.Fatalf("expected 0 entries, got %d", len(entries))
	}
}

func TestVerifierStartAndStop(t *testing.T) {
	store := newMockStorage()
	log := NewTableLog(store)
	if err := log.EnsureTable(); err != nil {
		t.Fatal(err)
	}

	logger := slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelDebug}))

	// Start verifier with a short interval
	log.StartVerifier(100*time.Millisecond, logger)

	// Append some entries
	for i := 0; i < 3; i++ {
		entry := Entry{
			Actor:  fmt.Sprintf("user%d", i),
			Action: "INSERT",
			Target: "test",
		}
		if err := log.Append(entry); err != nil {
			t.Fatal(err)
		}
	}

	// Give the verifier time to run at least once
	time.Sleep(200 * time.Millisecond)

	// Stop should not panic or hang
	log.StopVerifier()
}

func TestVerifierDetectsBrokenChain(t *testing.T) {
	store := newMockStorage()
	log := NewTableLog(store)
	if err := log.EnsureTable(); err != nil {
		t.Fatal(err)
	}

	logger := slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelDebug}))

	// Add valid entries
	for i := 0; i < 2; i++ {
		entry := Entry{
			Actor:  fmt.Sprintf("user%d", i),
			Action: "INSERT",
			Target: "test",
		}
		if err := log.Append(entry); err != nil {
			t.Fatal(err)
		}
	}

	// Tamper with the chain
	store.mu.Lock()
	db := store.databases[SystemDB]
	tbl := db[AuditTableName]
	tbl.rows = append(tbl.rows, storage.Row{
		int64(3),
		time.Now().UTC(),
		"attacker",
		"DROP",
		"everything",
		"",
		"fake_prev",
		"fake_hash",
		`{"id":3}`,
	})
	store.mu.Unlock()

	// VerifyChain should detect the break
	valid, count, err := log.VerifyChain()
	if err == nil {
		t.Fatal("expected error from broken chain")
	}
	if valid {
		t.Error("expected invalid chain")
	}
	if count != 2 {
		t.Errorf("expected break at position 2, got %d", count)
	}

	// Start/stop verifier (should not hang even with broken chain)
	log.StartVerifier(50*time.Millisecond, logger)
	time.Sleep(120 * time.Millisecond)
	log.StopVerifier()
}

func TestVerifierStopIdempotent(t *testing.T) {
	store := newMockStorage()
	log := NewTableLog(store)
	if err := log.EnsureTable(); err != nil {
		t.Fatal(err)
	}

	logger := slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelDebug}))

	log.StartVerifier(100*time.Millisecond, logger)
	time.Sleep(50 * time.Millisecond)

	// Double stop should be safe
	log.StopVerifier()
	log.StopVerifier()
}

func TestAuditRLSEnabledOnTable(t *testing.T) {
	store := newMockStorage()
	log := NewTableLog(store)
	if err := log.EnsureTable(); err != nil {
		t.Fatal(err)
	}

	schema, err := store.GetTableSchema(SystemDB, AuditTableName)
	if err != nil {
		t.Fatalf("GetTableSchema: %v", err)
	}
	if !schema.RLSEnabled {
		t.Fatal("expected RLS to be enabled on audit log table")
	}
	if len(schema.Policies) != 2 {
		t.Fatalf("expected 2 RLS policies, got %d", len(schema.Policies))
	}

	// Verify admin policy allows all
	adminPolicy := schema.Policies[0]
	if adminPolicy.Name != "audit_admin_all" {
		t.Errorf("expected admin policy name 'audit_admin_all', got %q", adminPolicy.Name)
	}
	if adminPolicy.ToUser != "admin" {
		t.Errorf("expected admin policy ToUser 'admin', got %q", adminPolicy.ToUser)
	}
	if adminPolicy.UsingExpr != "true" {
		t.Errorf("expected admin policy UsingExpr 'true', got %q", adminPolicy.UsingExpr)
	}

	// Verify non-admin policy restricts by actor
	userPolicy := schema.Policies[1]
	if userPolicy.Name != "audit_user_own_entries" {
		t.Errorf("expected user policy name 'audit_user_own_entries', got %q", userPolicy.Name)
	}
	if userPolicy.ToUser != "nonadmin" {
		t.Errorf("expected user policy ToUser 'nonadmin', got %q", userPolicy.ToUser)
	}
	if userPolicy.UsingExpr != "actor = current_user" {
		t.Errorf("expected user policy UsingExpr 'actor = current_user', got %q", userPolicy.UsingExpr)
	}
}
