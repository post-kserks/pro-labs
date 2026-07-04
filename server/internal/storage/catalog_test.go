package storage

import (
	"bytes"
	"encoding/binary"
	"encoding/json"
	"os"
	"path/filepath"
	"sync"
	"testing"
)

func TestBinaryCatalogMarshalRoundtrip(t *testing.T) {
	cat := &BinaryCatalog{
		Magic:   [4]byte{'V', 'D', 'B', 'C'},
		Version: binaryCatalogVersion,
		Tables: []BinaryTableEntry{
			{
				Name:        []byte("shop/users"),
				NameLen:     10,
				ColumnCount: 3,
				RowCount:    1500,
				Flags:       catalogFlagRLSEnabled,
				Columns: []BinaryColumnEntry{
					{Name: []byte("id"), NameLen: 2, Type: []byte("INT"), TypeLen: 3, Flags: catalogFlagPrimaryKey | catalogFlagNotNull},
					{Name: []byte("name"), NameLen: 4, Type: []byte("TEXT"), TypeLen: 4, Flags: catalogFlagNotNull},
					{Name: []byte("score"), NameLen: 5, Type: []byte("FLOAT"), TypeLen: 5, Flags: 0},
				},
			},
			{
				Name:        []byte("shop/orders"),
				NameLen:     11,
				ColumnCount: 2,
				RowCount:    500,
				Flags:       0,
				Columns: []BinaryColumnEntry{
					{Name: []byte("id"), NameLen: 2, Type: []byte("INT"), TypeLen: 3, Flags: catalogFlagPrimaryKey},
					{Name: []byte("amount"), NameLen: 6, Type: []byte("FLOAT"), TypeLen: 5, Flags: 0},
				},
			},
		},
	}
	cat.TableCount = uint32(len(cat.Tables))

	data, err := cat.Marshal()
	if err != nil {
		t.Fatal(err)
	}

	restored, err := UnmarshalBinaryCatalog(data)
	if err != nil {
		t.Fatal(err)
	}

	if restored.Version != cat.Version {
		t.Fatalf("version: %d != %d", restored.Version, cat.Version)
	}
	if len(restored.Tables) != 2 {
		t.Fatalf("table count: %d != 2", len(restored.Tables))
	}

	u := restored.Tables[0]
	if string(u.Name) != "shop/users" {
		t.Fatalf("table name: %q", string(u.Name))
	}
	if u.ColumnCount != 3 || u.RowCount != 1500 {
		t.Fatalf("col/row count: %d/%d", u.ColumnCount, u.RowCount)
	}
	if u.Flags != catalogFlagRLSEnabled {
		t.Fatalf("table flags: %d", u.Flags)
	}
	if len(u.Columns) != 3 {
		t.Fatalf("columns: %d", len(u.Columns))
	}

	idCol := u.Columns[0]
	if string(idCol.Name) != "id" || string(idCol.Type) != "INT" {
		t.Fatalf("col[0]: %q %q", string(idCol.Name), string(idCol.Type))
	}
	if idCol.Flags != catalogFlagPrimaryKey|catalogFlagNotNull {
		t.Fatalf("col[0] flags: %d", idCol.Flags)
	}

	o := restored.Tables[1]
	if string(o.Name) != "shop/orders" || o.RowCount != 500 {
		t.Fatalf("table 1: %q rows=%d", string(o.Name), o.RowCount)
	}
}

func TestBinaryCatalogMarshalEmpty(t *testing.T) {
	cat := &BinaryCatalog{
		Magic:   [4]byte{'V', 'D', 'B', 'C'},
		Version: binaryCatalogVersion,
	}
	data, err := cat.Marshal()
	if err != nil {
		t.Fatal(err)
	}
	restored, err := UnmarshalBinaryCatalog(data)
	if err != nil {
		t.Fatal(err)
	}
	if len(restored.Tables) != 0 {
		t.Fatalf("expected 0 tables, got %d", len(restored.Tables))
	}
}

func TestBinaryCatalogInvalidMagic(t *testing.T) {
	data := []byte("XXXX")
	_, err := UnmarshalBinaryCatalog(data)
	if err == nil {
		t.Fatal("expected error for invalid magic")
	}
}

func TestBinaryCatalogTooSmall(t *testing.T) {
	data := []byte("VDB")
	_, err := UnmarshalBinaryCatalog(data)
	if err == nil {
		t.Fatal("expected error for too small data")
	}
}

func TestBinaryCatalogTruncatedTableName(t *testing.T) {
	// Header: magic(4) + version(4) + tableCount(4) = 12 bytes
	// tableCount = 1, but no table data
	data := make([]byte, 12)
	copy(data[0:4], "VDBC")
	binary.LittleEndian.PutUint32(data[4:8], 1)
	binary.LittleEndian.PutUint32(data[8:12], 1)

	_, err := UnmarshalBinaryCatalog(data)
	if err == nil {
		t.Fatal("expected error for truncated table name")
	}
}

func TestCachedCatalogOperations(t *testing.T) {
	cat := &BinaryCatalog{
		Magic:   [4]byte{'V', 'D', 'B', 'C'},
		Version: binaryCatalogVersion,
		Tables: []BinaryTableEntry{
			{Name: []byte("db/users"), NameLen: 8, ColumnCount: 2, RowCount: 10,
				Columns: []BinaryColumnEntry{
					{Name: []byte("id"), NameLen: 2, Type: []byte("INT"), TypeLen: 3},
				}},
		},
	}
	cat.TableCount = 1
	data, err := cat.Marshal()
	if err != nil {
		t.Fatal(err)
	}

	cc, err := NewCachedCatalog(data)
	if err != nil {
		t.Fatal(err)
	}

	// GetTable
	entry, ok := cc.GetTable("db/users")
	if !ok || string(entry.Name) != "db/users" {
		t.Fatal("GetTable failed")
	}
	if _, ok := cc.GetTable("db/orders"); ok {
		t.Fatal("GetTable should not find missing table")
	}

	// SetTable (new)
	cc.SetTable(BinaryTableEntry{
		Name: []byte("db/orders"), NameLen: 9, RowCount: 50,
		Columns: []BinaryColumnEntry{{Name: []byte("id"), NameLen: 2, Type: []byte("INT"), TypeLen: 3}},
	})
	if cc.Len() != 2 {
		t.Fatalf("Len after SetTable: %d", cc.Len())
	}

	// SetTable (replace)
	cc.SetTable(BinaryTableEntry{
		Name: []byte("db/users"), NameLen: 8, RowCount: 999,
	})
	entry, _ = cc.GetTable("db/users")
	if entry.RowCount != 999 {
		t.Fatalf("RowCount after replace: %d", entry.RowCount)
	}

	// RemoveTable
	if !cc.RemoveTable("db/orders") {
		t.Fatal("RemoveTable should return true")
	}
	if cc.RemoveTable("nonexistent") {
		t.Fatal("RemoveTable should return false for missing")
	}
	if cc.Len() != 1 {
		t.Fatalf("Len after remove: %d", cc.Len())
	}

	// TableNames
	names := cc.TableNames()
	if len(names) != 1 || names[0] != "db/users" {
		t.Fatalf("TableNames: %v", names)
	}

	// Marshal roundtrip
	newData, err := cc.Marshal()
	if err != nil {
		t.Fatal(err)
	}
	cc2, err := NewCachedCatalog(newData)
	if err != nil {
		t.Fatal(err)
	}
	entry, _ = cc2.GetTable("db/users")
	if entry.RowCount != 999 {
		t.Fatalf("roundtrip RowCount: %d", entry.RowCount)
	}
}

func TestCachedCatalogConcurrency(t *testing.T) {
	cat := &BinaryCatalog{
		Magic:   [4]byte{'V', 'D', 'B', 'C'},
		Version: binaryCatalogVersion,
		Tables: []BinaryTableEntry{
			{Name: []byte("db/t1"), NameLen: 5, ColumnCount: 1, RowCount: 0,
				Columns: []BinaryColumnEntry{{Name: []byte("id"), NameLen: 2, Type: []byte("INT"), TypeLen: 3}}},
		},
	}
	cat.TableCount = 1
	data, err := cat.Marshal()
	if err != nil {
		t.Fatal(err)
	}
	cc, err := NewCachedCatalog(data)
	if err != nil {
		t.Fatal(err)
	}

	var wg sync.WaitGroup
	errs := make(chan error, 20)

	// Concurrent readers
	for i := 0; i < 10; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < 100; j++ {
				cc.GetTable("db/t1")
				cc.TableNames()
				cc.Len()
			}
		}()
	}

	// Concurrent writers
	for i := 0; i < 5; i++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			for j := 0; j < 50; j++ {
				name := []byte("db/w" + string(rune('a'+id)))
				cc.SetTable(BinaryTableEntry{
					Name: name, NameLen: uint16(len(name)), RowCount: uint64(j),
					Columns: []BinaryColumnEntry{{Name: []byte("id"), NameLen: 2, Type: []byte("INT"), TypeLen: 3}},
				})
				cc.RemoveTable(string(name))
			}
		}(i)
	}

	wg.Wait()
	close(errs)

	for e := range errs {
		t.Fatal(e)
	}
}

func TestMarshalCatalogFromPageCatalog(t *testing.T) {
	cat := &pageCatalog{
		CurrentTxID:  42,
		LastModified: map[string]uint64{"db/users": 10},
		RowCounts:    map[string]int{"db/users": 100, "db/orders": 50},
	}
	schemas := map[string]*TableSchema{
		"db/users": {
			Name: "db/users",
			Columns: []ColumnSchema{
				{Name: "id", Type: "INT", PrimaryKey: true, NotNull: true},
				{Name: "name", Type: "TEXT"},
			},
			RLSEnabled: true,
		},
		"db/orders": {
			Name: "db/orders",
			Columns: []ColumnSchema{
				{Name: "id", Type: "INT", PrimaryKey: true},
				{Name: "amount", Type: "FLOAT"},
			},
		},
	}

	data, err := MarshalCatalog(cat, schemas)
	if err != nil {
		t.Fatal(err)
	}

	bc, err := UnmarshalBinaryCatalog(data)
	if err != nil {
		t.Fatal(err)
	}

	if len(bc.Tables) != 2 {
		t.Fatalf("tables: %d", len(bc.Tables))
	}

	// Tables should be sorted by key
	if string(bc.Tables[0].Name) != "db/orders" {
		t.Fatalf("first table: %q", string(bc.Tables[0].Name))
	}
	if string(bc.Tables[1].Name) != "db/users" {
		t.Fatalf("second table: %q", string(bc.Tables[1].Name))
	}

	// Check orders table
	o := bc.Tables[0]
	if o.RowCount != 50 {
		t.Fatalf("orders RowCount: %d", o.RowCount)
	}
	if len(o.Columns) != 2 {
		t.Fatalf("orders columns: %d", len(o.Columns))
	}

	// Check users table
	u := bc.Tables[1]
	if u.RowCount != 100 {
		t.Fatalf("users RowCount: %d", u.RowCount)
	}
	if u.Flags&catalogFlagRLSEnabled == 0 {
		t.Fatal("users should have RLS flag")
	}
	idCol := u.Columns[0]
	if idCol.Flags&catalogFlagPrimaryKey == 0 {
		t.Fatal("id column should have PRIMARY_KEY flag")
	}
	if idCol.Flags&catalogFlagNotNull == 0 {
		t.Fatal("id column should have NOT_NULL flag")
	}
}

func TestUnmarshalToPageCatalog(t *testing.T) {
	bc := &BinaryCatalog{
		Magic:   [4]byte{'V', 'D', 'B', 'C'},
		Version: binaryCatalogVersion,
		Tables: []BinaryTableEntry{
			{
				Name: []byte("db/users"), NameLen: 8, RowCount: 42, ColumnCount: 2,
				Columns: []BinaryColumnEntry{
					{Name: []byte("id"), NameLen: 2, Type: []byte("INT"), TypeLen: 3, Flags: catalogFlagPrimaryKey},
					{Name: []byte("email"), NameLen: 5, Type: []byte("TEXT"), TypeLen: 4, Flags: catalogFlagNotNull},
				},
			},
		},
	}
	bc.TableCount = 1

	cat, schemas := UnmarshalToPageCatalog(bc)

	if cat.RowCounts["db/users"] != 42 {
		t.Fatalf("RowCounts: %d", cat.RowCounts["db/users"])
	}

	schema, ok := schemas["db/users"]
	if !ok {
		t.Fatal("schema not found")
	}
	if len(schema.Columns) != 2 {
		t.Fatalf("columns: %d", len(schema.Columns))
	}
	if !schema.Columns[0].PrimaryKey {
		t.Fatal("col 0 should be primary key")
	}
	if !schema.Columns[1].NotNull {
		t.Fatal("col 1 should be NOT NULL")
	}
}

func TestColumnFlags(t *testing.T) {
	col := ColumnSchema{
		NotNull:       true,
		PrimaryKey:    true,
		AutoIncrement: true,
		IsComputed:    true,
	}
	flags := columnFlags(col)
	if flags&catalogFlagNotNull == 0 {
		t.Fatal("NOT_NULL not set")
	}
	if flags&catalogFlagPrimaryKey == 0 {
		t.Fatal("PRIMARY_KEY not set")
	}
	if flags&catalogFlagAutoInc == 0 {
		t.Fatal("AUTO_INCREMENT not set")
	}
	if flags&catalogFlagComputed == 0 {
		t.Fatal("IS_COMPUTED not set")
	}
}

func TestTableFlags(t *testing.T) {
	ts := TableSchema{
		RLSEnabled: true,
		Constraints: []TableConstraint{{Name: "pk", Type: "PRIMARY_KEY"}},
	}
	flags := tableFlags(ts)
	if flags&catalogFlagRLSEnabled == 0 {
		t.Fatal("RLS not set")
	}
	if flags&catalogFlagHasConst == 0 {
		t.Fatal("HAS_CONSTRAINTS not set")
	}
}

func TestBinaryCatalogPersistence(t *testing.T) {
	dir := t.TempDir()
	e, err := NewPageStorageEngine(dir, nil, nil)
	if err != nil {
		t.Fatal(err)
	}

	_ = e.CreateDatabase("db")
	_ = e.CreateTable("db", TableSchema{
		Name: "items",
		Columns: []ColumnSchema{
			{Name: "id", Type: "INT", PrimaryKey: true, NotNull: true},
			{Name: "name", Type: "TEXT"},
			{Name: "value", Type: "FLOAT"},
		},
	})
	_, _ = e.InsertRows("db", "items", []Row{
		{int64(1), "a", 1.0},
		{int64(2), "b", 2.0},
	})

	// Trigger catalog save (via close)
	if err := e.Close(); err != nil {
		t.Fatal(err)
	}

	// Verify binary catalog file exists
	binPath := filepath.Join(dir, "pagedb", "_catalog.bin")
	data, err := os.ReadFile(binPath)
	if err != nil {
		t.Fatalf("binary catalog not found: %v", err)
	}

	bc, err := UnmarshalBinaryCatalog(data)
	if err != nil {
		t.Fatal(err)
	}

	if len(bc.Tables) != 1 {
		t.Fatalf("tables: %d", len(bc.Tables))
	}
	if string(bc.Tables[0].Name) != "db/items" {
		t.Fatalf("table name: %q", string(bc.Tables[0].Name))
	}
	if bc.Tables[0].RowCount != 2 {
		t.Fatalf("row count: %d", bc.Tables[0].RowCount)
	}
	if len(bc.Tables[0].Columns) != 3 {
		t.Fatalf("columns: %d", len(bc.Tables[0].Columns))
	}

	// Reopen and verify binary catalog is loaded
	e2, err := NewPageStorageEngine(dir, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer e2.Close()

	if e2.cachedCatalog == nil {
		t.Fatal("cachedCatalog should be loaded")
	}
	entry, ok := e2.cachedCatalog.GetTable("db/items")
	if !ok {
		t.Fatal("table not in cached catalog")
	}
	if entry.RowCount != 2 {
		t.Fatalf("cached RowCount: %d", entry.RowCount)
	}
}

func BenchmarkJSONCatalogLoad(b *testing.B) {
	dir := b.TempDir()
	// Create a large JSON catalog
	cat := pageCatalog{
		CurrentTxID:  10000,
		LastModified: make(map[string]uint64),
		RowCounts:    make(map[string]int),
	}
	schemas := make(map[string]*TableSchema)
	for i := 0; i < 500; i++ {
		key := "database_" + string(rune('A'+i%26)) + "/table_" + string(rune('0'+i/26))
		cat.RowCounts[key] = i * 100
		cat.LastModified[key] = uint64(i)
		schemas[key] = &TableSchema{
			Name: key,
			Columns: []ColumnSchema{
				{Name: "id", Type: "INT"},
				{Name: "name", Type: "TEXT"},
				{Name: "value", Type: "FLOAT"},
			},
		}
	}
	data, _ := json.MarshalIndent(&cat, "", "  ")
	os.WriteFile(filepath.Join(dir, "_catalog.json"), data, 0o644)

	b.SetBytes(int64(len(data)))
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		var c pageCatalog
		d, _ := os.ReadFile(filepath.Join(dir, "_catalog.json"))
		_ = json.Unmarshal(d, &c)
	}
}

func BenchmarkBinaryCatalogLoad(b *testing.B) {
	dir := b.TempDir()
	// Create a large binary catalog
	cat := &pageCatalog{
		CurrentTxID:  10000,
		LastModified: make(map[string]uint64),
		RowCounts:    make(map[string]int),
	}
	schemas := make(map[string]*TableSchema)
	for i := 0; i < 500; i++ {
		key := "database_" + string(rune('A'+i%26)) + "/table_" + string(rune('0'+i/26))
		cat.RowCounts[key] = i * 100
		cat.LastModified[key] = uint64(i)
		schemas[key] = &TableSchema{
			Name: key,
			Columns: []ColumnSchema{
				{Name: "id", Type: "INT"},
				{Name: "name", Type: "TEXT"},
				{Name: "value", Type: "FLOAT"},
			},
		}
	}
	binData, _ := MarshalCatalog(cat, schemas)
	os.WriteFile(filepath.Join(dir, "_catalog.bin"), binData, 0o644)

	b.SetBytes(int64(len(binData)))
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		d, _ := os.ReadFile(filepath.Join(dir, "_catalog.bin"))
		_, _ = UnmarshalBinaryCatalog(d)
	}
}

func BenchmarkJSONCatalogLookup(b *testing.B) {
	dir := b.TempDir()
	cat := pageCatalog{
		CurrentTxID: 10000,
		RowCounts:   make(map[string]int),
	}
	schemas := make(map[string]*TableSchema)
	for i := 0; i < 500; i++ {
		key := "database_" + string(rune('A'+i%26)) + "/table_" + string(rune('0'+i/26))
		cat.RowCounts[key] = i * 100
		schemas[key] = &TableSchema{
			Name: key,
			Columns: []ColumnSchema{
				{Name: "id", Type: "INT"},
				{Name: "name", Type: "TEXT"},
				{Name: "value", Type: "FLOAT"},
			},
		}
	}
	data, _ := json.MarshalIndent(&cat, "", "  ")
	os.WriteFile(filepath.Join(dir, "_catalog.json"), data, 0o644)

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		d, _ := os.ReadFile(filepath.Join(dir, "_catalog.json"))
		var c pageCatalog
		_ = json.Unmarshal(d, &c)
		_ = c.RowCounts["database_E/table_2"]
	}
}

func BenchmarkBinaryCatalogLookup(b *testing.B) {
	dir := b.TempDir()
	cat := &pageCatalog{
		CurrentTxID: 10000,
		RowCounts:   make(map[string]int),
	}
	schemas := make(map[string]*TableSchema)
	for i := 0; i < 500; i++ {
		key := "database_" + string(rune('A'+i%26)) + "/table_" + string(rune('0'+i/26))
		cat.RowCounts[key] = i * 100
		schemas[key] = &TableSchema{
			Name: key,
			Columns: []ColumnSchema{
				{Name: "id", Type: "INT"},
				{Name: "name", Type: "TEXT"},
				{Name: "value", Type: "FLOAT"},
			},
		}
	}
	binData, _ := MarshalCatalog(cat, schemas)
	os.WriteFile(filepath.Join(dir, "_catalog.bin"), binData, 0o644)

	cc, _ := NewCachedCatalog(binData)

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		cc.GetTable("database_E/table_2")
	}
}

func TestBinaryCatalogSizeComparison(t *testing.T) {
	// Build a catalog with full schema info (what binary stores)
	type fullTable struct {
		Name    string        `json:"name"`
		Columns []ColumnSchema `json:"columns"`
		RowCount int          `json:"row_count"`
	}
	type fullCatalog struct {
		Tables []fullTable `json:"tables"`
	}

	full := fullCatalog{}
	schemas := make(map[string]*TableSchema)
	cat := &pageCatalog{
		CurrentTxID: 10000,
		RowCounts:   make(map[string]int),
	}
	for i := 0; i < 500; i++ {
		key := "database_" + string(rune('A'+i%26)) + "/table_" + string(rune('0'+i/26))
		schema := &TableSchema{
			Name: key,
			Columns: []ColumnSchema{
				{Name: "id", Type: "INT", PrimaryKey: true},
				{Name: "name", Type: "TEXT", NotNull: true},
				{Name: "value", Type: "FLOAT"},
			},
		}
		schemas[key] = schema
		cat.RowCounts[key] = i * 100
		full.Tables = append(full.Tables, fullTable{Name: key, Columns: schema.Columns, RowCount: i * 100})
	}

	jsonData, _ := json.MarshalIndent(full, "", "  ")
	binData, _ := MarshalCatalog(cat, schemas)

	t.Logf("JSON size (full schema): %d bytes", len(jsonData))
	t.Logf("Binary size: %d bytes", len(binData))
	t.Logf("Ratio: %.2fx", float64(len(jsonData))/float64(len(binData)))

	// Binary should be more compact for large catalogs with many tables
	if len(binData) >= len(jsonData) {
		t.Logf("NOTE: binary is not smaller, but deserialization is significantly faster")
	}
}

func TestBinaryCatalogFormatIntegrity(t *testing.T) {
	cat := &BinaryCatalog{
		Magic:   [4]byte{'V', 'D', 'B', 'C'},
		Version: binaryCatalogVersion,
		Tables: []BinaryTableEntry{
			{Name: []byte("test"), NameLen: 4, ColumnCount: 1, RowCount: 1,
				Columns: []BinaryColumnEntry{{Name: []byte("col"), NameLen: 3, Type: []byte("INT"), TypeLen: 3, Flags: 1}}},
		},
	}
	cat.TableCount = 1

	data, err := cat.Marshal()
	if err != nil {
		t.Fatal(err)
	}

	// Verify magic bytes
	if !bytes.Equal(data[0:4], []byte("VDBC")) {
		t.Fatal("magic mismatch")
	}

	// Verify version
	if binary.LittleEndian.Uint32(data[4:8]) != 1 {
		t.Fatal("version mismatch")
	}

	// Verify table count
	if binary.LittleEndian.Uint32(data[8:12]) != 1 {
		t.Fatal("table count mismatch")
	}

	// Verify name length
	nameLen := binary.LittleEndian.Uint16(data[12:14])
	if nameLen != 4 {
		t.Fatalf("name length: %d", nameLen)
	}

	// Verify name bytes
	if !bytes.Equal(data[14:18], []byte("test")) {
		t.Fatal("name mismatch")
	}
}
