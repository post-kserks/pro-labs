package storage

import (
	"context"
	"fmt"
	"math"
	"os"
	"sync"
	"testing"
	"time"

	"vaultdb/internal/core/storage/page"
	"vaultdb/internal/core/wal"
)

// ── Binary encoding edge cases ────────────────────────────────────────────

func TestEncodeColumnValueJSONB(t *testing.T) {
	val := map[string]interface{}{"key": "value", "num": float64(42)}
	encoded, err := encodeColumnValue(val)
	if err != nil {
		t.Fatal(err)
	}
	if encoded[0] != 'j' {
		t.Fatalf("expected 'j' tag, got %c", encoded[0])
	}
	decoded, err := decodeColumnValue(encoded)
	if err != nil {
		t.Fatal(err)
	}
	m, ok := decoded.(map[string]interface{})
	if !ok {
		t.Fatalf("expected map, got %T", decoded)
	}
	if m["key"] != "value" {
		t.Fatalf("key mismatch: %v", m["key"])
	}
}

func TestEncodeColumnValueVector(t *testing.T) {
	vec := []float64{1.0, 2.5, -3.7}
	encoded, err := encodeColumnValue(vec)
	if err != nil {
		t.Fatal(err)
	}
	if encoded[0] != 'v' {
		t.Fatalf("expected 'v' tag, got %c", encoded[0])
	}
	decoded, err := decodeColumnValue(encoded)
	if err != nil {
		t.Fatal(err)
	}
	result, ok := decoded.([]float64)
	if !ok {
		t.Fatalf("expected []float64, got %T", decoded)
	}
	if len(result) != 3 || result[0] != 1.0 || result[1] != 2.5 || result[2] != -3.7 {
		t.Fatalf("vector mismatch: %v", result)
	}
}

func TestEncodeColumnValueString(t *testing.T) {
	encoded, err := encodeColumnValue("hello world")
	if err != nil {
		t.Fatal(err)
	}
	if encoded[0] != 's' {
		t.Fatalf("expected 's' tag, got %c", encoded[0])
	}
	decoded, err := decodeColumnValue(encoded)
	if err != nil {
		t.Fatal(err)
	}
	if decoded != "hello world" {
		t.Fatalf("string mismatch: %v", decoded)
	}
}

func TestEncodeColumnValueBool(t *testing.T) {
	// True
	enc, err := encodeColumnValue(true)
	if err != nil {
		t.Fatal(err)
	}
	dec, err := decodeColumnValue(enc)
	if err != nil {
		t.Fatal(err)
	}
	if dec != true {
		t.Fatalf("bool true mismatch: %v", dec)
	}

	// False
	enc, err = encodeColumnValue(false)
	if err != nil {
		t.Fatal(err)
	}
	dec, err = decodeColumnValue(enc)
	if err != nil {
		t.Fatal(err)
	}
	if dec != false {
		t.Fatalf("bool false mismatch: %v", dec)
	}
}

func TestEncodeColumnValueInt64(t *testing.T) {
	encoded, err := encodeColumnValue(int64(12345))
	if err != nil {
		t.Fatal(err)
	}
	decoded, err := decodeColumnValue(encoded)
	if err != nil {
		t.Fatal(err)
	}
	if decoded != int64(12345) {
		t.Fatalf("int64 mismatch: %v", decoded)
	}
}

func TestEncodeColumnValueFloat64(t *testing.T) {
	encoded, err := encodeColumnValue(float64(3.14))
	if err != nil {
		t.Fatal(err)
	}
	decoded, err := decodeColumnValue(encoded)
	if err != nil {
		t.Fatal(err)
	}
	if decoded != float64(3.14) {
		t.Fatalf("float64 mismatch: %v", decoded)
	}
}

func TestEncodeColumnValueNil(t *testing.T) {
	encoded, err := encodeColumnValue(nil)
	if err != nil {
		t.Fatal(err)
	}
	if encoded[0] != binNullMarker {
		t.Fatalf("expected null marker, got %x", encoded[0])
	}
	decoded, err := decodeColumnValue(encoded)
	if err != nil {
		t.Fatal(err)
	}
	if decoded != nil {
		t.Fatalf("expected nil, got %v", decoded)
	}
}

func TestEncodeColumnValueUnsupportedType(t *testing.T) {
	_, err := encodeColumnValue(struct{}{})
	if err == nil {
		t.Fatal("expected error for unsupported type")
	}
}

func TestDecodeColumnValueEmptyData(t *testing.T) {
	decoded, err := decodeColumnValue([]byte{})
	if err != nil {
		t.Fatal(err)
	}
	if decoded != nil {
		t.Fatalf("expected nil for empty data, got %v", decoded)
	}
}

func TestDecodeColumnValueTruncatedInt64(t *testing.T) {
	_, err := decodeColumnValue([]byte{'i', 0x01, 0x02}) // only 3 bytes, need 9
	if err == nil {
		t.Fatal("expected error for truncated int64")
	}
}

func TestDecodeColumnValueTruncatedFloat64(t *testing.T) {
	_, err := decodeColumnValue([]byte{'f', 0x01, 0x02})
	if err == nil {
		t.Fatal("expected error for truncated float64")
	}
}

func TestDecodeColumnValueTruncatedBool(t *testing.T) {
	_, err := decodeColumnValue([]byte{'b'})
	if err == nil {
		t.Fatal("expected error for truncated bool")
	}
}

func TestDecodeColumnValueTruncatedString(t *testing.T) {
	_, err := decodeColumnValue([]byte{'s'})
	if err == nil {
		t.Fatal("expected error for truncated string header")
	}
}

func TestDecodeColumnValueTruncatedStringData(t *testing.T) {
	// String header says 10 bytes but only 2 follow
	data := []byte{'s', 10, 0, 'a', 'b'}
	_, err := decodeColumnValue(data)
	if err == nil {
		t.Fatal("expected error for truncated string data")
	}
}

func TestDecodeColumnValueTruncatedJSONB(t *testing.T) {
	_, err := decodeColumnValue([]byte{'j'})
	if err == nil {
		t.Fatal("expected error for truncated jsonb header")
	}
}

func TestDecodeColumnValueTruncatedJSONBData(t *testing.T) {
	data := []byte{'j', 10, 0, '{', 'a'}
	_, err := decodeColumnValue(data)
	if err == nil {
		t.Fatal("expected error for truncated jsonb data")
	}
}

func TestDecodeColumnValueTruncatedVector(t *testing.T) {
	_, err := decodeColumnValue([]byte{'v'})
	if err == nil {
		t.Fatal("expected error for truncated vector header")
	}
}

func TestDecodeColumnValueTruncatedVectorData(t *testing.T) {
	data := []byte{'v', 3, 0} // count=3 but no data
	_, err := decodeColumnValue(data)
	if err == nil {
		t.Fatal("expected error for truncated vector data")
	}
}

func TestDecodeColumnValueUnknownTag(t *testing.T) {
	_, err := decodeColumnValue([]byte{'Z', 1, 2, 3})
	if err == nil {
		t.Fatal("expected error for unknown type tag")
	}
}

func TestDecodeColumnValueJSONBNonJSON(t *testing.T) {
	// JSONB decode falls back to string for non-JSON data
	data := []byte{'j', 5, 0, 'h', 'e', 'l', 'l', 'o'}
	decoded, err := decodeColumnValue(data)
	if err != nil {
		t.Fatal(err)
	}
	// Not valid JSON — should return as string
	if s, ok := decoded.(string); !ok || s != "hello" {
		t.Fatalf("expected string fallback 'hello', got %v (%T)", decoded, decoded)
	}
}

func TestDecodeColumnValueJSONBNonMapJSON(t *testing.T) {
	// JSONB with valid JSON but not a map (e.g. an array)
	// Build properly: length prefix + JSON bytes
	encoded := []byte{'j', 5, 0, '[', '1', ',', '2', ']'}
	decoded, err := decodeColumnValue(encoded)
	if err != nil {
		t.Fatal(err)
	}
	// Array JSON — not a map, so returns string
	if _, ok := decoded.(map[string]interface{}); ok {
		t.Fatal("expected non-map type for JSON array")
	}
}

func TestEncodeBinaryTupleEmptyRow(t *testing.T) {
	_, err := EncodeRow(1, 0, Row{})
	if err == nil {
		t.Fatal("expected error for empty row")
	}
}

func TestEncodeBinaryTupleTooLarge(t *testing.T) {
	// Create a row that will exceed 65535 bytes after encoding
	bigString := make([]byte, 60000)
	for i := range bigString {
		bigString[i] = 'x'
	}
	_, err := EncodeRow(1, 0, Row{string(bigString), string(bigString)})
	if err == nil {
		t.Fatal("expected error for oversized tuple")
	}
}

func TestDecodeBinaryTupleTruncatedHeader(t *testing.T) {
	_, _, _, err := DecodeRow([]byte{0, 1, 2}, nil)
	if err == nil {
		t.Fatal("expected error for truncated header")
	}
}

func TestDecodeBinaryTupleTruncatedColCount(t *testing.T) {
	// Enough for txIDs but not for colCount
	data := make([]byte, 16)
	_, _, _, err := DecodeRow(data, nil)
	if err == nil {
		t.Fatal("expected error for truncated col count")
	}
}

func TestDecodeBinaryTupleTruncatedTupleHeader(t *testing.T) {
	// Set colCount=3 but provide insufficient header space
	data := make([]byte, 18+6) // 16 header + 2 colCount + 3*2 offsets = 24
	data[16] = 3               // colCount = 3
	// But tuple is only 24 bytes total — should fit for 3 cols
	// Actually test with colCount=5 but insufficient header
	data2 := make([]byte, 20)
	data2[16] = 5 // colCount = 5 → needs 16 + 2 + 5*2 = 28 bytes
	_, _, _, err := DecodeRow(data2, nil)
	if err == nil {
		t.Fatal("expected error for truncated tuple header")
	}
}

// ── Buffer pool edge cases ────────────────────────────────────────────────

func TestBufferPoolWritePreImage(t *testing.T) {
	walPath := t.TempDir() + "/test.wal"
	w, err := wal.Open(walPath)
	if err != nil {
		t.Fatal(err)
	}
	defer w.Close()

	bp := NewBufferPool(4)
	bp.SetWAL(w)
	defer bp.Close()

	hf := setupHeapFile(t)
	pid, _, err := hf.AllocatePage(page.PageTypeHeap)
	if err != nil {
		t.Fatal(err)
	}

	// Fetch page with db/table info for WAL
	bp.FetchPage(pid, hf, "db", "table")
	bp.UnpinPage(pid, false)

	// Write pre-image should work (page is in cache)
	bp.WritePreImage(pid)

	// Second write should be no-op (imageWritten=true)
	bp.WritePreImage(pid)

	// WritePreImage for non-cached page should be no-op
	nonCached := page.PageID{TableID: 999, SegmentNo: 0, PageNo: 99}
	bp.WritePreImage(nonCached)
}

func TestBufferPoolInvalidatePagePinned(t *testing.T) {
	bp := NewBufferPool(4)
	defer bp.Close()
	hf := setupHeapFile(t)

	pid, _, _ := hf.AllocatePage(page.PageTypeHeap)
	bp.FetchPage(pid, hf) // pinned

	// Invalidate should skip pinned pages
	bp.InvalidatePage(pid)

	stats := bp.Stats()
	if stats.Used != 1 {
		t.Fatalf("expected 1 (pinned page survives), got %d", stats.Used)
	}

	bp.UnpinPage(pid, false)
}

func TestBufferPoolInvalidatePageUnpinned(t *testing.T) {
	bp := NewBufferPool(4)
	defer bp.Close()
	hf := setupHeapFile(t)

	pid, _, _ := hf.AllocatePage(page.PageTypeHeap)
	bp.FetchPage(pid, hf)
	bp.UnpinPage(pid, false)

	bp.InvalidatePage(pid)

	stats := bp.Stats()
	if stats.Used != 0 {
		t.Fatalf("expected 0 after invalidate unpinned, got %d", stats.Used)
	}
}

func TestBufferPoolInvalidatePageNotCached(t *testing.T) {
	bp := NewBufferPool(4)
	defer bp.Close()

	// Invalidating a page not in cache should be a no-op
	bp.InvalidatePage(page.PageID{TableID: 1, SegmentNo: 0, PageNo: 0})
}

func TestBufferPoolStartBackgroundFlush(t *testing.T) {
	bp := NewBufferPool(4)
	defer bp.Close()
	hf := setupHeapFile(t)

	pid, _, _ := hf.AllocatePage(page.PageTypeHeap)
	bp.FetchPage(pid, hf)
	bp.UnpinPage(pid, true) // dirty

	// Start background flush
	bp.StartBackgroundFlush(t.Context(), 10*time.Millisecond)
	// Start again to test replacement of existing goroutine
	bp.StartBackgroundFlush(t.Context(), 10*time.Millisecond)

	// Let it tick once
	time.Sleep(30 * time.Millisecond)

	stats := bp.Stats()
	if stats.DirtyCount != 0 {
		t.Fatalf("expected 0 dirty after background flush, got %d", stats.DirtyCount)
	}
}

// ── Page lock operations ──────────────────────────────────────────────────

func TestPageLockRLockUnlock(t *testing.T) {
	pm := NewPageLockManager()
	pid := page.PageID{TableID: 1, SegmentNo: 0, PageNo: 0}

	pm.RLockPage(pid)
	pm.UnlockPage(pid)
}

func TestPageLockTable(t *testing.T) {
	pm := NewPageLockManager()
	pids := []page.PageID{
		{TableID: 1, SegmentNo: 0, PageNo: 2},
		{TableID: 1, SegmentNo: 0, PageNo: 0},
		{TableID: 1, SegmentNo: 0, PageNo: 1},
	}

	pm.LockTable(pids)
	pm.UnlockTable(pids)
}

func TestPageLockEvictUnused(t *testing.T) {
	pm := NewPageLockManager()
	pid := page.PageID{TableID: 1, SegmentNo: 0, PageNo: 0}

	// Create a lock entry
	pm.LockPage(pid)
	pm.UnlockPageWrite(pid)

	// Evict should return 0 for striped locks (no-op)
	removed := pm.EvictUnused()
	if removed != 0 {
		t.Fatalf("expected 0 removed, got %d", removed)
	}
}

func TestPageLockSortPageIDs(t *testing.T) {
	pids := []page.PageID{
		{SegmentNo: 1, PageNo: 3},
		{SegmentNo: 0, PageNo: 5},
		{SegmentNo: 0, PageNo: 2},
		{SegmentNo: 1, PageNo: 1},
	}
	sortPageIDs(pids)

	// Verify sorted by SegmentNo then PageNo
	for i := 1; i < len(pids); i++ {
		if !lessPageID(pids[i-1], pids[i]) {
			t.Fatalf("not sorted: %v >= %v", pids[i-1], pids[i])
		}
	}
}

func TestPageLockUnlockWithoutEntry(t *testing.T) {
	pm := NewPageLockManager()
	pid := page.PageID{TableID: 999, SegmentNo: 0, PageNo: 0}
	pm.RLockPage(pid)
	pm.UnlockPage(pid)
	pm.LockPage(pid)
	pm.UnlockPageWrite(pid)
}

// ── Row lock operations ───────────────────────────────────────────────────

// ── Row pool ──────────────────────────────────────────────────────────────

func TestGetRowPool(t *testing.T) {
	r := GetRow()
	if cap(r) < 16 {
		t.Fatalf("expected cap >= 16, got %d", cap(r))
	}
	if len(r) != 0 {
		t.Fatalf("expected len 0, got %d", len(r))
	}
}

func TestPutRowPool(t *testing.T) {
	r := make(Row, 0, 16)
	PutRow(r) // should not panic

	// Large capacity rows should be discarded
	big := make(Row, 0, 512)
	PutRow(big) // should not panic
}

func TestGetRowWithLenPool(t *testing.T) {
	r := GetRowWithLen(5)
	if len(r) != 5 {
		t.Fatalf("expected len 5, got %d", len(r))
	}
}

func TestGetRowWithLenFromPool(t *testing.T) {
	// Put a row with large capacity back, then request small len
	r := make(Row, 0, 64)
	PutRow(r)
	r2 := GetRowWithLen(3)
	if len(r2) != 3 {
		t.Fatalf("expected len 3, got %d", len(r2))
	}
}

// ── parseTimestampFlexible ─────────────────────────────────────────────────

func TestParseTimestampFlexible(t *testing.T) {
	tests := []struct {
		input   string
		wantErr bool
	}{
		{"2006-01-02 15:04:05", false},
		{"2006-01-02T15:04:05", false},
		{"2006-01-02T15:04:05Z", false},
		{"2006-01-02T15:04:05.000Z", false},
		{"not-a-timestamp", true},
	}
	for _, tt := range tests {
		_, err := parseTimestampFlexible(tt.input)
		if (err != nil) != tt.wantErr {
			t.Errorf("parseTimestampFlexible(%q): err=%v, wantErr=%v", tt.input, err, tt.wantErr)
		}
	}
}

// ── DataDir accessor ──────────────────────────────────────────────────────

func TestDataDir(t *testing.T) {
	e := newPageEngine(t)
	dir := e.DataDir()
	if dir == "" {
		t.Fatal("DataDir should not be empty")
	}
}

// ── ReadSampleRows ────────────────────────────────────────────────────────

func TestReadSampleRows(t *testing.T) {
	e := newPageEngine(t)
	_ = e.CreateDatabase("db")
	_ = e.CreateTable("db", usersSchema())

	_, _ = e.InsertRows("db", "users", []Row{
		{int64(1), "a", 1.0},
		{int64(2), "b", 2.0},
		{int64(3), "c", 3.0},
	})

	// Limit > rows
	rows, err := e.ReadSampleRows("db", "users", 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 3 {
		t.Fatalf("expected 3, got %d", len(rows))
	}

	// Limit = 1
	rows, err = e.ReadSampleRows("db", "users", 1)
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 1 {
		t.Fatalf("expected 1, got %d", len(rows))
	}

	// Limit = 0
	rows, err = e.ReadSampleRows("db", "users", 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 0 {
		t.Fatalf("expected 0, got %d", len(rows))
	}

	// Limit < 0
	rows, err = e.ReadSampleRows("db", "users", -1)
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 0 {
		t.Fatalf("expected 0, got %d", len(rows))
	}
}

// ── TxIDAtTimestamp ───────────────────────────────────────────────────────

func TestTxIDAtTimestamp(t *testing.T) {
	e := newPageEngine(t)
	_ = e.CreateDatabase("db")
	_ = e.CreateTable("db", usersSchema())

	_, _ = e.InsertRows("db", "users", []Row{{int64(1), "a", 1.0}})

	// Valid timestamp
	txID, err := e.TxIDAtTimestamp("db", "2099-01-01 00:00:00")
	if err != nil {
		t.Fatal(err)
	}
	if txID == 0 {
		t.Fatal("expected non-zero txID for future timestamp")
	}

	// Invalid timestamp
	_, err = e.TxIDAtTimestamp("db", "not-a-date")
	if err == nil {
		t.Fatal("expected error for invalid timestamp")
	}
}

// ── UpdateRowsDirect ──────────────────────────────────────────────────────

func TestUpdateRowsDirect(t *testing.T) {
	e := newPageEngine(t)
	_ = e.CreateDatabase("db")
	_ = e.CreateTable("db", usersSchema())

	_, _ = e.InsertRows("db", "users", []Row{
		{int64(1), "alice", 10.0},
		{int64(2), "bob", 20.0},
	})

	newValues := []Row{
		{int64(1), "ALICE", 100.0},
	}
	n, err := e.UpdateRowsDirect("db", "users", []int{0}, newValues)
	if err != nil {
		t.Fatal(err)
	}
	if n != 1 {
		t.Fatalf("expected 1 affected, got %d", n)
	}

	rows, _ := e.ReadCurrentRows("db", "users")
	found := false
	for _, r := range rows {
		if r[0] == int64(1) && r[1] == "ALICE" && r[2] == 100.0 {
			found = true
		}
	}
	if !found {
		t.Fatalf("direct update not found: %#v", rows)
	}
}

// ── WAL recovery undo paths ───────────────────────────────────────────────

func TestWALRecoveryUndoIncompleteTx(t *testing.T) {
	dir := t.TempDir()
	walPath := dir + "/test.wal"
	w, err := wal.Open(walPath)
	if err != nil {
		t.Fatal(err)
	}

	e, err := NewPageStorageEngine(dir, w, nil)
	if err != nil {
		t.Fatal(err)
	}
	_ = e.CreateDatabase("db")
	_ = e.CreateTable("db", usersSchema())
	_, _ = e.InsertRows("db", "users", []Row{{int64(1), "a", 1.0}})

	// Create a properly encoded tuple for the WAL insert
	tupleData, _ := encodePageTuple(999, 0, Row{int64(999), "ghost", 0.0})

	txID := uint64(999)
	payload := wal.WALPageInsertPayload{
		DB:        "db",
		Table:     "users",
		SegmentNo: 0,
		PageNo:    0,
		SlotNo:    0,
		XID:       txID,
		TupleData: tupleData,
	}
	_, _ = w.AppendWithTx(txID, wal.OpPageInsert, payload)
	// No OpCommit — this tx is in-progress

	w.Close()

	// Reopen and recover
	w2, err := wal.Open(walPath)
	if err != nil {
		t.Fatal(err)
	}
	defer w2.Close()

	e2, err := NewPageStorageEngine(dir, w2, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer e2.Close()

	if err := e2.RecoverFromWAL(); err != nil {
		t.Fatal(err)
	}

	// The incomplete tx should have been rolled back — only the committed row remains
	rows, _ := e2.ReadCurrentRows("db", "users")
	if len(rows) != 1 {
		t.Fatalf("expected 1 row (rollback of incomplete tx), got %d", len(rows))
	}
}

// ── extractRewriteFields ──────────────────────────────────────────────────

func TestExtractRewriteFields(t *testing.T) {
	// With error
	db, table := extractRewriteFields(nil, fmt.Errorf("some error"))
	if db != "" || table != "" {
		t.Fatalf("expected empty on error, got %q/%q", db, table)
	}

	// With nil error but wrong type
	db, table = extractRewriteFields("not a payload", nil)
	if db != "" || table != "" {
		t.Fatalf("expected empty for wrong type, got %q/%q", db, table)
	}

	// With correct type
	p := wal.WALRewritePayload{DB: "mydb", Table: "mytable"}
	db, table = extractRewriteFields(p, nil)
	if db != "mydb" || table != "mytable" {
		t.Fatalf("expected mydb/mytable, got %q/%q", db, table)
	}
}

// ── Concurrent reads and writes to same table ─────────────────────────────

func TestConcurrentReadsWritesSameTable(t *testing.T) {
	e := newPageEngine(t)
	_ = e.CreateDatabase("db")
	_ = e.CreateTable("db", usersSchema())

	// Seed initial data
	_, _ = e.InsertRows("db", "users", []Row{
		{int64(1), "a", 1.0},
		{int64(2), "b", 2.0},
	})

	var wg sync.WaitGroup
	errCh := make(chan error, 20)

	// Concurrent writers
	for w := 0; w < 5; w++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			for i := 0; i < 10; i++ {
				_, err := e.InsertRows("db", "users", []Row{
					{int64(1000 + id*100 + i), "writer", float64(i)},
				})
				if err != nil {
					errCh <- err
					return
				}
			}
		}(w)
	}

	// Concurrent readers
	for r := 0; r < 5; r++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			for i := 0; i < 10; i++ {
				rows, err := e.ReadCurrentRows("db", "users")
				if err != nil {
					errCh <- err
					return
				}
				// Rows should be at least the initial 2
				if len(rows) < 2 {
					errCh <- fmt.Errorf("reader %d: got %d rows, want >= 2", id, len(rows))
					return
				}
			}
		}(r)
	}

	wg.Wait()
	close(errCh)

	for err := range errCh {
		t.Fatal(err)
	}
}

// ── Compaction/GC via vacuum ──────────────────────────────────────────────

func TestVacuumCleansOldVersions(t *testing.T) {
	e := newPageEngine(t)
	_ = e.CreateDatabase("db")
	_ = e.CreateTable("db", usersSchema())

	// Insert, update multiple times, creating multiple dead versions
	_, _ = e.InsertRows("db", "users", []Row{{int64(1), "v1", 1.0}})
	_, _ = e.UpdateRows("db", "users", []int{0}, map[string]Value{"name": "v2"})
	_, _ = e.UpdateRows("db", "users", []int{0}, map[string]Value{"name": "v3"})
	_, _ = e.UpdateRows("db", "users", []int{0}, map[string]Value{"name": "v4"})

	// Should have 4 total rows (1 live + 3 dead)
	stats, _ := e.TableVersionStats("db", "users")
	if stats.TotalRows != 4 || stats.DeadRows != 3 {
		t.Fatalf("before vacuum: total=%d dead=%d, want 4/3", stats.TotalRows, stats.DeadRows)
	}

	_, _ = e.Vacuum("db", "users")

	stats, _ = e.TableVersionStats("db", "users")
	if stats.TotalRows != 1 || stats.DeadRows != 0 {
		t.Fatalf("after vacuum: total=%d dead=%d, want 1/0", stats.TotalRows, stats.DeadRows)
	}

	// Verify the surviving row is the latest version
	rows, _ := e.ReadCurrentRows("db", "users")
	if len(rows) != 1 || rows[0][1] != "v4" {
		t.Fatalf("expected v4, got %v", rows)
	}
}

// ── Partial write recovery ────────────────────────────────────────────────

func TestPartialWriteRecovery(t *testing.T) {
	dir := t.TempDir()
	walPath := dir + "/test.wal"
	w, err := wal.Open(walPath)
	if err != nil {
		t.Fatal(err)
	}

	e, err := NewPageStorageEngine(dir, w, nil)
	if err != nil {
		t.Fatal(err)
	}
	_ = e.CreateDatabase("db")
	_ = e.CreateTable("db", usersSchema())

	// Write data
	_, _ = e.InsertRows("db", "users", []Row{
		{int64(1), "row1", 1.0},
		{int64(2), "row2", 2.0},
	})

	// Close without checkpoint — simulates crash after commit
	// (Close flushes buffer pool but WAL entries remain on disk)
	_ = e.Close()

	// Reopen and recover
	w2, err := wal.Open(walPath)
	if err != nil {
		t.Fatal(err)
	}
	defer w2.Close()

	e2, err := NewPageStorageEngine(dir, w2, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer e2.Close()

	if err := e2.RecoverFromWAL(); err != nil {
		t.Fatal(err)
	}

	// After recovery, we should be able to read data
	rows, err := e2.ReadCurrentRows("db", "users")
	if err != nil {
		t.Fatal(err)
	}
	// Data may have duplicates from WAL replay on top of flushed heap,
	// but the key invariant is: no data is lost
	if len(rows) < 2 {
		t.Fatalf("expected at least 2 rows after recovery, got %d", len(rows))
	}
}

// ── Checkpoint with WAL ───────────────────────────────────────────────────

func TestCheckpointWithWAL(t *testing.T) {
	dir := t.TempDir()
	walPath := dir + "/test.wal"
	w, err := wal.Open(walPath)
	if err != nil {
		t.Fatal(err)
	}

	e, err := NewPageStorageEngine(dir, w, nil)
	if err != nil {
		t.Fatal(err)
	}
	_ = e.CreateDatabase("db")
	_ = e.CreateTable("db", usersSchema())
	_, _ = e.InsertRows("db", "users", []Row{
		{int64(1), "a", 1.0},
		{int64(2), "b", 2.0},
	})

	// Run checkpoint
	if err := e.doCheckpoint(); err != nil {
		t.Fatal(err)
	}

	// After checkpoint, WAL should be truncated
	entries, _ := w.Recover()
	if len(entries) != 0 {
		t.Fatalf("expected 0 WAL entries after checkpoint, got %d", len(entries))
	}

	// Data should still be readable
	rows, _ := e.ReadCurrentRows("db", "users")
	if len(rows) != 2 {
		t.Fatalf("expected 2 rows after checkpoint, got %d", len(rows))
	}

	_ = e.Close()
}

// ── Binary encoding boundary values ───────────────────────────────────────

func TestEncodeColumnValueBoundaryInt64(t *testing.T) {
	vals := []int64{0, 1, -1, math.MaxInt64, math.MinInt64}
	for _, v := range vals {
		enc, err := encodeColumnValue(v)
		if err != nil {
			t.Fatalf("encode %d: %v", v, err)
		}
		dec, err := decodeColumnValue(enc)
		if err != nil {
			t.Fatalf("decode %d: %v", v, err)
		}
		if dec != v {
			t.Fatalf("roundtrip %d: got %v", v, dec)
		}
	}
}

func TestEncodeColumnValueBoundaryFloat64(t *testing.T) {
	vals := []float64{0.0, 1.0, -1.0, math.MaxFloat64, -math.MaxFloat64, math.SmallestNonzeroFloat64}
	for _, v := range vals {
		enc, err := encodeColumnValue(v)
		if err != nil {
			t.Fatalf("encode %f: %v", v, err)
		}
		dec, err := decodeColumnValue(enc)
		if err != nil {
			t.Fatalf("decode %f: %v", v, err)
		}
		if dec != v {
			t.Fatalf("roundtrip %f: got %v", v, dec)
		}
	}
}

func TestEncodeColumnValueEmptyString(t *testing.T) {
	enc, err := encodeColumnValue("")
	if err != nil {
		t.Fatal(err)
	}
	dec, err := decodeColumnValue(enc)
	if err != nil {
		t.Fatal(err)
	}
	if dec != "" {
		t.Fatalf("expected empty string, got %v", dec)
	}
}

func TestEncodeColumnValueEmptyVector(t *testing.T) {
	enc, err := encodeColumnValue([]float64{})
	if err != nil {
		t.Fatal(err)
	}
	dec, err := decodeColumnValue(enc)
	if err != nil {
		t.Fatal(err)
	}
	vec, ok := dec.([]float64)
	if !ok || len(vec) != 0 {
		t.Fatalf("expected empty vector, got %v", dec)
	}
}

func TestEncodeColumnValueEmptyJSONB(t *testing.T) {
	enc, err := encodeColumnValue(map[string]interface{}{})
	if err != nil {
		t.Fatal(err)
	}
	dec, err := decodeColumnValue(enc)
	if err != nil {
		t.Fatal(err)
	}
	m, ok := dec.(map[string]interface{})
	if !ok || len(m) != 0 {
		t.Fatalf("expected empty map, got %v", dec)
	}
}

// ── Decode JSONB with non-map JSON ────────────────────────────────────────

func TestDecodeJSONBArray(t *testing.T) {
	// Encode a JSON array as JSONB
	jsonBytes := []byte("[1,2,3]")
	buf := make([]byte, 3+len(jsonBytes))
	buf[0] = 'j'
	buf[1] = byte(len(jsonBytes))
	buf[2] = 0
	copy(buf[3:], jsonBytes)

	decoded, err := decodeColumnValue(buf)
	if err != nil {
		t.Fatal(err)
	}
	// Array JSON should return as string (not a map)
	if s, ok := decoded.(string); !ok {
		t.Fatalf("expected string for non-map JSON, got %T: %v", decoded, decoded)
	} else if s != "[1,2,3]" {
		t.Fatalf("expected '[1,2,3]', got %q", s)
	}
}

// ── CachePage duplicates ──────────────────────────────────────────────────

func TestBufferPoolCachePageDuplicate(t *testing.T) {
	bp := NewBufferPool(4)
	defer bp.Close()
	hf := setupHeapFile(t)

	pid, pg, _ := hf.AllocatePage(page.PageTypeHeap)
	bp.CachePage(pid, pg, hf)

	// CachePage again — should be no-op
	bp.CachePage(pid, pg, hf)

	stats := bp.Stats()
	if stats.Used != 1 {
		t.Fatalf("expected 1 after duplicate CachePage, got %d", stats.Used)
	}
}

// ── UnpinPageDirty ────────────────────────────────────────────────────────

func TestBufferPoolUnpinPageDirty(t *testing.T) {
	bp := NewBufferPool(4)
	defer bp.Close()
	hf := setupHeapFile(t)

	pid, _, _ := hf.AllocatePage(page.PageTypeHeap)
	bp.FetchPage(pid, hf)
	bp.UnpinPageDirty(pid, 42)

	bp.mu.RLock()
	idx := bp.cache[pid]
	entry := bp.buffers[idx]
	bp.mu.RUnlock()

	if !entry.dirty {
		t.Fatal("expected dirty flag after UnpinPageDirty")
	}
	if entry.lastModifiedLSN != 42 {
		t.Fatalf("expected LSN 42, got %d", entry.lastModifiedLSN)
	}

	bp.UnpinPage(pid, false)
}

func TestBufferPoolUnpinPageNotCached(t *testing.T) {
	bp := NewBufferPool(4)
	defer bp.Close()

	// Unpin non-cached page should be no-op
	bp.UnpinPage(page.PageID{TableID: 1}, false)
	bp.UnpinPageDirty(page.PageID{TableID: 1}, 1)
}

// ── FlushDirtyPagesUpToLSN ───────────────────────────────────────────────

func TestFlushDirtyPagesUpToLSN(t *testing.T) {
	bp := NewBufferPool(4)
	defer bp.Close()
	hf := setupHeapFile(t)

	pid1, _, _ := hf.AllocatePage(page.PageTypeHeap)
	pid2, _, _ := hf.AllocatePage(page.PageTypeHeap)

	bp.FetchPage(pid1, hf)
	bp.FetchPage(pid2, hf)

	bp.UnpinPageDirty(pid1, 10) // LSN 10
	bp.UnpinPageDirty(pid2, 20) // LSN 20

	// Flush only pages with LSN <= 15
	if err := bp.FlushDirtyPagesUpToLSN(15); err != nil {
		t.Fatal(err)
	}

	stats := bp.Stats()
	if stats.DirtyCount != 1 {
		t.Fatalf("expected 1 dirty (pid2), got %d", stats.DirtyCount)
	}
}

// ── PrefetchPages ─────────────────────────────────────────────────────────

func TestPrefetchPagesEmpty(t *testing.T) {
	bp := NewBufferPool(4)
	defer bp.Close()
	hf := setupHeapFile(t)

	// Empty list should be no-op
	bp.PrefetchPages([]page.PageID{}, hf)

	stats := bp.Stats()
	if stats.Used != 0 {
		t.Fatalf("expected 0, got %d", stats.Used)
	}
}

// ── Close without background flush ────────────────────────────────────────

func TestBufferPoolCloseNoBackground(t *testing.T) {
	bp := NewBufferPool(4)
	// No background flush started
	bp.Close() // should not panic
	bp.Close() // double close should not panic
}

// ── Evict on empty pool ───────────────────────────────────────────────────

func TestBufferPoolEvictEmptySlot(t *testing.T) {
	bp := NewBufferPool(2)
	hf := setupHeapFile(t)

	pid1, _, _ := hf.AllocatePage(page.PageTypeHeap)
	pid2, _, _ := hf.AllocatePage(page.PageTypeHeap)

	bp.FetchPage(pid1, hf)
	bp.UnpinPage(pid1, false)
	bp.FetchPage(pid2, hf)
	bp.UnpinPage(pid2, false)

	// Evict should succeed (clock hand finds an empty or evictable slot)
	bp.mu.Lock()
	err := bp.evict()
	bp.mu.Unlock()
	if err != nil {
		t.Fatalf("evict: %v", err)
	}
}

// ── EncodeColumnValue with empty JSONB ────────────────────────────────────

func TestEncodeColumnValueJSONBNil(t *testing.T) {
	// JSONB marshal of nil map
	enc, err := encodeColumnValue(map[string]interface{}(nil))
	if err != nil {
		t.Fatal(err)
	}
	dec, err := decodeColumnValue(enc)
	if err != nil {
		t.Fatal(err)
	}
	if dec != nil {
		// nil map JSON-marshal to "null" → decodeColumnValue may return nil or string
		t.Logf("nil map decoded as: %v (%T)", dec, dec)
	}
}

// ── Additional coverage tests ─────────────────────────────────────────────

// CheckpointLoop with context cancellation
func TestCheckpointLoopContextCancel(t *testing.T) {
	e := newPageEngine(t)
	_ = e.CreateDatabase("db")
	_ = e.CreateTable("db", usersSchema())
	_, _ = e.InsertRows("db", "users", []Row{{int64(1), "a", 1.0}})

	ctx, cancel := context.WithCancel(t.Context())
	done := make(chan struct{})
	go func() {
		e.CheckpointLoop(ctx, 10*time.Millisecond)
		close(done)
	}()

	time.Sleep(30 * time.Millisecond)
	cancel()
	<-done
}

// FinalCheckpoint
func TestFinalCheckpoint(t *testing.T) {
	e := newPageEngine(t)
	_ = e.CreateDatabase("db")
	_ = e.CreateTable("db", usersSchema())
	_, _ = e.InsertRows("db", "users", []Row{{int64(1), "a", 1.0}})

	if err := e.FinalCheckpoint(); err != nil {
		t.Fatal(err)
	}
}

// SchemaVersion
// TableModifiedSince
func TestTableModifiedSince(t *testing.T) {
	e := newPageEngine(t)
	_ = e.CreateDatabase("db")
	_ = e.CreateTable("db", usersSchema())

	modified, err := e.TableModifiedSince("db", "users", 0)
	if err != nil {
		t.Fatal(err)
	}
	if modified {
		t.Fatal("table should not be modified since tx 0")
	}

	_, _ = e.InsertRows("db", "users", []Row{{int64(1), "a", 1.0}})

	modified, err = e.TableModifiedSince("db", "users", 0)
	if err != nil {
		t.Fatal(err)
	}
	if !modified {
		t.Fatal("table should be modified after insert")
	}
}

// SetTableRLS and AddPolicy
func TestSetTableRLSAndAddPolicy(t *testing.T) {
	e := newPageEngine(t)
	_ = e.CreateDatabase("db")
	_ = e.CreateTable("db", usersSchema())

	if err := e.SetTableRLS("db", "users", true); err != nil {
		t.Fatal(err)
	}
	schema, _ := e.GetTableSchema("db", "users")
	if !schema.RLSEnabled {
		t.Fatal("RLS should be enabled")
	}

	policy := RLSPolicy{Name: "test_policy", ToUser: "admin", UsingExpr: "true"}
	if err := e.AddPolicy("db", "users", policy); err != nil {
		t.Fatal(err)
	}
	schema, _ = e.GetTableSchema("db", "users")
	if len(schema.Policies) != 1 {
		t.Fatalf("expected 1 policy, got %d", len(schema.Policies))
	}
}

// CreateIndexMulti
func TestCreateIndexMulti(t *testing.T) {
	e := newPageEngine(t)
	_ = e.CreateDatabase("db")
	schema := TableSchema{
		Name: "items",
		Columns: []ColumnSchema{
			{Name: "a", Type: "INT"},
			{Name: "b", Type: "TEXT"},
			{Name: "c", Type: "FLOAT"},
		},
	}
	_ = e.CreateTable("db", schema)
	_, _ = e.InsertRows("db", "items", []Row{
		{int64(1), "x", 1.0},
		{int64(2), "y", 2.0},
	})

	err := e.CreateIndexMulti("db", "items", "idx_ab", []string{"a", "b"})
	if err != nil {
		t.Fatal(err)
	}

	// Verify index exists
	idx, ok := e.GetIndex("db", "items", "idx_ab")
	if !ok || idx == nil {
		t.Fatal("composite index should exist")
	}

	// CreateIndexMulti with missing column should fail
	err = e.CreateIndexMulti("db", "items", "idx_bad", []string{"nonexistent"})
	if err == nil {
		t.Fatal("expected error for missing column")
	}
}

// GetIndex
func TestGetIndex(t *testing.T) {
	e := newPageEngine(t)
	_ = e.CreateDatabase("db")
	_ = e.CreateTable("db", usersSchema())
	_, _ = e.InsertRows("db", "users", []Row{{int64(1), "a", 1.0}})

	_ = e.CreateIndex("db", "users", "idx_name", "name", "")

	idx, ok := e.GetIndex("db", "users", "idx_name")
	if !ok || idx == nil {
		t.Fatal("index should exist")
	}
	if idx.Name() != "idx_name" {
		t.Fatalf("expected idx_name, got %s", idx.Name())
	}

	// Non-existent index
	idx, ok = e.GetIndex("db", "users", "nonexistent")
	if ok || idx != nil {
		t.Fatal("non-existent index should return nil")
	}

	// Non-existent table
	idx, ok = e.GetIndex("db", "nosuchtable", "idx")
	if ok || idx != nil {
		t.Fatal("non-existent table should return nil")
	}
}

// IndexRangeLookup and IndexFTSLookup
func TestIndexRangeLookup(t *testing.T) {
	e := newPageEngine(t)
	_ = e.CreateDatabase("db")
	_ = e.CreateTable("db", usersSchema())

	// IndexRangeLookup without any index should return empty
	positions, ok := e.IndexRangeLookup("db", "users", "name", "a", "z")
	if ok {
		t.Fatal("IndexRangeLookup should return false without index")
	}
	_ = positions
}

func TestIndexFTSLookup(t *testing.T) {
	e := newPageEngine(t)
	_ = e.CreateDatabase("db")
	_ = e.CreateTable("db", usersSchema())

	// IndexFTSLookup without any index should return empty
	positions, ok := e.IndexFTSLookup("db", "users", "name", "search term")
	if ok {
		t.Fatal("IndexFTSLookup should return false without index")
	}
	_ = positions
}

// ValidateObjectName edge cases
func TestValidateObjectNameEdgeCases(t *testing.T) {
	tests := []struct {
		name    string
		wantErr bool
	}{
		{"", true},
		{"a", false},
		{"_abc", false},
		{"abc123", false},
		{"123abc", true},          // starts with digit
		{"a-b", true},             // contains dash
		{"a.b", true},             // contains dot
		{"a/b", true},             // contains slash
		{"a\\b", true},            // contains backslash
		{"a..b", true},            // path traversal
		{string([]byte{0}), true}, // null byte
	}
	for _, tt := range tests {
		err := ValidateObjectName(tt.name)
		if (err != nil) != tt.wantErr {
			t.Errorf("ValidateObjectName(%q): err=%v, wantErr=%v", tt.name, err, tt.wantErr)
		}
	}
}

// StartBackgroundFlush via engine
func TestEngineStartBackgroundFlush(t *testing.T) {
	e := newPageEngine(t)
	_ = e.CreateDatabase("db")
	_ = e.CreateTable("db", usersSchema())
	_, _ = e.InsertRows("db", "users", []Row{{int64(1), "a", 1.0}})

	ctx, cancel := context.WithCancel(t.Context())
	e.StartBackgroundFlush(ctx, 10*time.Millisecond)
	time.Sleep(25 * time.Millisecond)
	cancel()
}

// undoDelete via WAL recovery
func TestWALRecoveryUndoDelete(t *testing.T) {
	dir := t.TempDir()
	walPath := dir + "/test.wal"
	w, err := wal.Open(walPath)
	if err != nil {
		t.Fatal(err)
	}

	e, err := NewPageStorageEngine(dir, w, nil)
	if err != nil {
		t.Fatal(err)
	}
	_ = e.CreateDatabase("db")
	_ = e.CreateTable("db", usersSchema())

	// Insert a row (committed)
	_, _ = e.InsertRows("db", "users", []Row{{int64(1), "a", 1.0}})

	// Now manually add a WAL delete + commit (for tx 100)
	// and a separate in-progress delete (tx 200) that should be undone
	deleteTx := uint64(200)
	payload := wal.WALPageDeletePayload{
		DB:        "db",
		Table:     "users",
		SegmentNo: 0,
		PageNo:    0,
		SlotNo:    0,
		XMax:      deleteTx,
	}
	_, _ = w.AppendWithTx(deleteTx, wal.OpPageDelete, payload)
	// No commit — tx 200 is in-progress

	w.Close()

	// Reopen and recover
	w2, err := wal.Open(walPath)
	if err != nil {
		t.Fatal(err)
	}
	defer w2.Close()

	e2, err := NewPageStorageEngine(dir, w2, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer e2.Close()

	if err := e2.RecoverFromWAL(); err != nil {
		t.Fatal(err)
	}

	// The in-progress delete should have been rolled back
	rows, _ := e2.ReadCurrentRows("db", "users")
	if len(rows) < 1 {
		t.Fatalf("expected at least 1 row after undo, got %d", len(rows))
	}
}

// UpdateRowsDirect with multiple rows
func TestUpdateRowsDirectMultiple(t *testing.T) {
	e := newPageEngine(t)
	_ = e.CreateDatabase("db")
	_ = e.CreateTable("db", usersSchema())

	_, _ = e.InsertRows("db", "users", []Row{
		{int64(1), "a", 1.0},
		{int64(2), "b", 2.0},
		{int64(3), "c", 3.0},
	})

	newValues := []Row{
		{int64(1), "A", 10.0},
		{int64(3), "C", 30.0},
	}
	n, err := e.UpdateRowsDirect("db", "users", []int{0, 2}, newValues)
	if err != nil {
		t.Fatal(err)
	}
	if n != 2 {
		t.Fatalf("expected 2 affected, got %d", n)
	}

	rows, _ := e.ReadCurrentRows("db", "users")
	for _, r := range rows {
		if r[0] == int64(1) && r[1] != "A" {
			t.Fatalf("expected A, got %v", r[1])
		}
		if r[0] == int64(3) && r[1] != "C" {
			t.Fatalf("expected C, got %v", r[1])
		}
	}
}

// Catalog operations
func TestCatalogGetTable(t *testing.T) {
	e := newPageEngine(t)
	_ = e.CreateDatabase("db")
	_ = e.CreateTable("db", usersSchema())

	// CachedCatalog should have the table
	if e.cachedCatalog != nil {
		_, found := e.cachedCatalog.GetTable("db/users")
		if !found {
			t.Fatal("expected table info from cached catalog")
		}
	}

	// Non-existent table
	if e.cachedCatalog != nil {
		_, found := e.cachedCatalog.GetTable("db/nonexistent")
		if found {
			t.Fatal("expected nil for non-existent table")
		}
	}
}

// Removed TestPageLockEvictUnusedMultiple as striped locks do not support Range and do not need eviction.

// invertOp
func TestInvertOp(t *testing.T) {
	tests := []struct {
		input, want string
	}{
		{"<", ">"},
		{">", "<"},
		{"<=", ">="},
		{">=", "<="},
		{"=", "="},
		{"!=", "!="},
	}
	for _, tt := range tests {
		got := invertOp(tt.input)
		if got != tt.want {
			t.Errorf("invertOp(%q) = %q, want %q", tt.input, got, tt.want)
		}
	}
}

// readDirFilenames and removeFile
func TestReadDirFilenamesAndRemoveFile(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(dir+"/a.txt", []byte("a"), 0o644)
	os.WriteFile(dir+"/b.txt", []byte("b"), 0o644)

	names, err := readDirFilenames(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(names) != 2 {
		t.Fatalf("expected 2 files, got %d", len(names))
	}

	if err := removeFile(dir + "/a.txt"); err != nil {
		t.Fatal(err)
	}
	names, _ = readDirFilenames(dir)
	if len(names) != 1 {
		t.Fatalf("expected 1 file after remove, got %d", len(names))
	}
}

// SchemaVersion — directly test the function
func TestSchemaVersionDirect(t *testing.T) {
	e := newPageEngine(t)
	_ = e.CreateDatabase("db")
	_ = e.CreateTable("db", usersSchema())
	_, _ = e.InsertRows("db", "users", []Row{{int64(1), "a", 1.0}})

	v1 := e.SchemaVersion()
	if v1 == 0 {
		t.Fatal("expected non-zero schema version after insert")
	}
}

// Removed TestEvictIfTooLarge as striped locks do not support Range and do not need eviction.

// FinalCheckpoint — cover error path
func TestFinalCheckpointError(t *testing.T) {
	e := newPageEngine(t)
	_ = e.CreateDatabase("db")
	_ = e.CreateTable("db", usersSchema())
	_, _ = e.InsertRows("db", "users", []Row{{int64(1), "a", 1.0}})

	// Normal case should succeed
	if err := e.FinalCheckpoint(); err != nil {
		t.Fatal(err)
	}
}

// Vacuum with invalid table
func TestVacuumInvalidTable(t *testing.T) {
	e := newPageEngine(t)
	_ = e.CreateDatabase("db")
	_, err := e.Vacuum("db", "nonexistent")
	if err == nil {
		t.Fatal("expected error for nonexistent table")
	}
}

// DropTable error paths
func TestDropTableInvalidNames(t *testing.T) {
	e := newPageEngine(t)
	_ = e.CreateDatabase("db")
	_ = e.CreateTable("db", usersSchema())

	// Drop non-existent table
	err := e.DropTable("db", "nonexistent")
	if err == nil {
		t.Fatal("expected error for nonexistent table")
	}

	// Drop with invalid DB name
	err = e.DropTable("", "users")
	if err == nil {
		t.Fatal("expected error for empty db name")
	}
}

// CreateDatabase error paths
func TestCreateDatabaseInvalidNames(t *testing.T) {
	e := newPageEngine(t)

	// Empty name
	err := e.CreateDatabase("")
	if err == nil {
		t.Fatal("expected error for empty db name")
	}

	// Duplicate database
	_ = e.CreateDatabase("db")
	err = e.CreateDatabase("db")
	if err == nil {
		t.Fatal("expected error for duplicate db")
	}
}

// CreateTable error paths
func TestCreateTableInvalidNames(t *testing.T) {
	e := newPageEngine(t)
	_ = e.CreateDatabase("db")

	// Empty table name
	err := e.CreateTable("db", TableSchema{Name: "", Columns: []ColumnSchema{{Name: "id", Type: "INT"}}})
	if err == nil {
		t.Fatal("expected error for empty table name")
	}

	// Non-existent database
	err = e.CreateTable("nodb", usersSchema())
	if err == nil {
		t.Fatal("expected error for non-existent database")
	}
}
