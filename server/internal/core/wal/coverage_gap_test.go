package wal

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"sync"
	"testing"
	"time"
)

// ── Binary payload round-trips ────────────────────────────────────────────

func TestEncodeDecodeRewriteBinary(t *testing.T) {
	original := WALRewritePayload{DB: "mydb", Table: "mytable"}

	encoded, err := EncodeWALPayloadBinary(original)
	if err != nil {
		t.Fatal(err)
	}
	if encoded[0] != binaryPayloadMarker {
		t.Fatalf("expected binary marker, got %x", encoded[0])
	}

	decoded, err := DecodeWALPayload(encoded, OpRewriteBegin)
	if err != nil {
		t.Fatal(err)
	}
	p, ok := decoded.(WALRewritePayload)
	if !ok {
		t.Fatalf("expected WALRewritePayload, got %T", decoded)
	}
	if p.DB != "mydb" || p.Table != "mytable" {
		t.Fatalf("mismatch: %+v", p)
	}

	// Also test decode for OpRewriteCommit and OpRewriteData
	for _, op := range []byte{OpRewriteCommit, OpRewriteData} {
		decoded, err = DecodeWALPayload(encoded, op)
		if err != nil {
			t.Fatalf("decode op 0x%02x: %v", op, err)
		}
		if _, ok := decoded.(WALRewritePayload); !ok {
			t.Fatalf("op 0x%02x: expected WALRewritePayload, got %T", op, decoded)
		}
	}
}

func TestEncodeDecodeTruncateTableBinary(t *testing.T) {
	original := WALTruncateTablePayload{DB: "db1", Table: "tbl1"}

	encoded, err := EncodeWALPayloadBinary(original)
	if err != nil {
		t.Fatal(err)
	}
	if encoded[0] != binaryPayloadMarker {
		t.Fatalf("expected binary marker, got %x", encoded[0])
	}

	decoded, err := DecodeWALPayload(encoded, OpTruncateTable)
	if err != nil {
		t.Fatal(err)
	}
	p, ok := decoded.(WALTruncateTablePayload)
	if !ok {
		t.Fatalf("expected WALTruncateTablePayload, got %T", decoded)
	}
	if p.DB != "db1" || p.Table != "tbl1" {
		t.Fatalf("mismatch: %+v", p)
	}
}

func TestEncodeDecodeVacuumBinary(t *testing.T) {
	original := WALVacuumPayload{DB: "db1", Table: "tbl1", ShadowPath: "/tmp/shadow"}

	encoded, err := EncodeWALPayloadBinary(original)
	if err != nil {
		t.Fatal(err)
	}

	decoded, err := DecodeWALPayload(encoded, OpVacuumBegin)
	if err != nil {
		t.Fatal(err)
	}
	p, ok := decoded.(WALVacuumPayload)
	if !ok {
		t.Fatalf("expected WALVacuumPayload, got %T", decoded)
	}
	if p.DB != "db1" || p.Table != "tbl1" || p.ShadowPath != "/tmp/shadow" {
		t.Fatalf("mismatch: %+v", p)
	}
}

func TestEncodeDecodeCheckpointBinary(t *testing.T) {
	original := CheckpointPayload{LSN: 42}

	encoded, err := EncodeWALPayloadBinary(original)
	if err != nil {
		t.Fatal(err)
	}

	decoded, err := DecodeWALPayload(encoded, OpCheckpoint)
	if err != nil {
		t.Fatal(err)
	}
	p, ok := decoded.(CheckpointPayload)
	if !ok {
		t.Fatalf("expected CheckpointPayload, got %T", decoded)
	}
	if p.LSN != 42 {
		t.Fatalf("LSN mismatch: %d", p.LSN)
	}
}

func TestEncodeDecodeFullPageImageBinary(t *testing.T) {
	pageData := make([]byte, 8192)
	for i := range pageData {
		pageData[i] = byte(i % 256)
	}
	original := FullPageImagePayload{
		DB:        "db",
		Table:     "tbl",
		SegmentNo: 3,
		PageNo:    99,
		PageData:  pageData,
	}

	encoded, err := EncodeWALPayloadBinary(original)
	if err != nil {
		t.Fatal(err)
	}

	decoded, err := DecodeWALPayload(encoded, OpFullPageImage)
	if err != nil {
		t.Fatal(err)
	}
	p, ok := decoded.(FullPageImagePayload)
	if !ok {
		t.Fatalf("expected FullPageImagePayload, got %T", decoded)
	}
	if p.DB != "db" || p.Table != "tbl" || p.SegmentNo != 3 || p.PageNo != 99 {
		t.Fatalf("field mismatch: %+v", p)
	}
	if !bytes.Equal(p.PageData, pageData) {
		t.Fatal("page data mismatch")
	}
}

func TestEncodeDecodePageInsertBinary(t *testing.T) {
	original := WALPageInsertPayload{
		DB:        "db",
		Table:     "tbl",
		SegmentNo: 1,
		PageNo:    10,
		SlotNo:    5,
		XID:       100,
		TupleData: []byte{1, 2, 3, 4, 5},
	}

	encoded, err := EncodeWALPayloadBinary(original)
	if err != nil {
		t.Fatal(err)
	}

	decoded, err := DecodeWALPayload(encoded, OpPageInsert)
	if err != nil {
		t.Fatal(err)
	}
	p, ok := decoded.(WALPageInsertPayload)
	if !ok {
		t.Fatalf("expected WALPageInsertPayload, got %T", decoded)
	}
	if p.DB != "db" || p.Table != "tbl" || p.SegmentNo != 1 || p.PageNo != 10 || p.SlotNo != 5 || p.XID != 100 {
		t.Fatalf("field mismatch: %+v", p)
	}
	if !bytes.Equal(p.TupleData, []byte{1, 2, 3, 4, 5}) {
		t.Fatal("tuple data mismatch")
	}
}

func TestEncodeDecodePageDeleteBinary(t *testing.T) {
	original := WALPageDeletePayload{
		DB:        "db",
		Table:     "tbl",
		SegmentNo: 2,
		PageNo:    20,
		SlotNo:    8,
		XMax:      55,
	}

	encoded, err := EncodeWALPayloadBinary(original)
	if err != nil {
		t.Fatal(err)
	}

	// Test OpPageDelete
	decoded, err := DecodeWALPayload(encoded, OpPageDelete)
	if err != nil {
		t.Fatal(err)
	}
	p, ok := decoded.(WALPageDeletePayload)
	if !ok {
		t.Fatalf("expected WALPageDeletePayload, got %T", decoded)
	}
	if p.DB != "db" || p.Table != "tbl" || p.SegmentNo != 2 || p.PageNo != 20 || p.SlotNo != 8 || p.XMax != 55 {
		t.Fatalf("field mismatch: %+v", p)
	}

	// Test OpPageUpdateXMax
	decoded, err = DecodeWALPayload(encoded, OpPageUpdateXMax)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := decoded.(WALPageDeletePayload); !ok {
		t.Fatalf("OpPageUpdateXMax: expected WALPageDeletePayload, got %T", decoded)
	}
}

func TestEncodeDecodeSchemaWriteBinary(t *testing.T) {
	original := WALSchemaWritePayload{
		DB:     "db",
		Table:  "tbl",
		Schema: `{"name":"tbl","columns":[{"name":"id","type":"INT"}]}`,
	}

	encoded, err := EncodeWALPayloadBinary(original)
	if err != nil {
		t.Fatal(err)
	}

	decoded, err := DecodeWALPayload(encoded, OpSchemaWrite)
	if err != nil {
		t.Fatal(err)
	}
	p, ok := decoded.(WALSchemaWritePayload)
	if !ok {
		t.Fatalf("expected WALSchemaWritePayload, got %T", decoded)
	}
	if p.DB != "db" || p.Table != "tbl" || p.Schema != original.Schema {
		t.Fatalf("mismatch: %+v", p)
	}
}

// ── Legacy JSON payload decode ────────────────────────────────────────────

func TestDecodeLegacyJSONPageInsert(t *testing.T) {
	jsonPayload := `{"DB":"db","Table":"tbl","SegmentNo":1,"PageNo":2,"SlotNo":3,"XID":10,"TupleData":"AQID"}`
	decoded, err := decodeLegacyJSONPayload([]byte(jsonPayload), OpPageInsert)
	if err != nil {
		t.Fatal(err)
	}
	p, ok := decoded.(WALPageInsertPayload)
	if !ok {
		t.Fatalf("expected WALPageInsertPayload, got %T", decoded)
	}
	if p.DB != "db" || p.Table != "tbl" {
		t.Fatalf("mismatch: %+v", p)
	}
}

func TestDecodeLegacyJSONVacuumBegin(t *testing.T) {
	jsonPayload := `{"DB":"db","Table":"tbl","ShadowPath":"/tmp/shadow"}`
	decoded, err := decodeLegacyJSONPayload([]byte(jsonPayload), OpVacuumBegin)
	if err != nil {
		t.Fatal(err)
	}
	p, ok := decoded.(WALVacuumPayload)
	if !ok {
		t.Fatalf("expected WALVacuumPayload, got %T", decoded)
	}
	if p.ShadowPath != "/tmp/shadow" {
		t.Fatalf("mismatch: %+v", p)
	}
}

func TestDecodeLegacyJSONRewriteBegin(t *testing.T) {
	jsonPayload := `{"DB":"db","Table":"tbl"}`
	decoded, err := decodeLegacyJSONPayload([]byte(jsonPayload), OpRewriteBegin)
	if err != nil {
		t.Fatal(err)
	}
	p, ok := decoded.(WALRewritePayload)
	if !ok {
		t.Fatalf("expected WALRewritePayload, got %T", decoded)
	}
	if p.DB != "db" {
		t.Fatalf("mismatch: %+v", p)
	}
}

func TestDecodeLegacyJSONTruncateTable(t *testing.T) {
	jsonPayload := `{"DB":"db","Table":"tbl"}`
	decoded, err := decodeLegacyJSONPayload([]byte(jsonPayload), OpTruncateTable)
	if err != nil {
		t.Fatal(err)
	}
	p, ok := decoded.(WALTruncateTablePayload)
	if !ok {
		t.Fatalf("expected WALTruncateTablePayload, got %T", decoded)
	}
	if p.DB != "db" {
		t.Fatalf("mismatch: %+v", p)
	}
}

func TestDecodeLegacyJSONCheckpoint(t *testing.T) {
	jsonPayload := `{"LSN":42}`
	decoded, err := decodeLegacyJSONPayload([]byte(jsonPayload), OpCheckpoint)
	if err != nil {
		t.Fatal(err)
	}
	p, ok := decoded.(CheckpointPayload)
	if !ok {
		t.Fatalf("expected CheckpointPayload, got %T", decoded)
	}
	if p.LSN != 42 {
		t.Fatalf("mismatch: %+v", p)
	}
}

func TestDecodeLegacyJSONFullPageImage(t *testing.T) {
	jsonPayload := `{"DB":"db","Table":"tbl","SegmentNo":1,"PageNo":2,"PageData":"AA=="}`
	decoded, err := decodeLegacyJSONPayload([]byte(jsonPayload), OpFullPageImage)
	if err != nil {
		t.Fatal(err)
	}
	p, ok := decoded.(FullPageImagePayload)
	if !ok {
		t.Fatalf("expected FullPageImagePayload, got %T", decoded)
	}
	if p.DB != "db" || p.Table != "tbl" {
		t.Fatalf("mismatch: %+v", p)
	}
}

func TestDecodeLegacyJSONUnknownOp(t *testing.T) {
	jsonPayload := `{"custom":"data"}`
	decoded, err := decodeLegacyJSONPayload([]byte(jsonPayload), 0xFF)
	if err != nil {
		t.Fatal(err)
	}
	m, ok := decoded.(map[string]interface{})
	if !ok {
		t.Fatalf("expected map, got %T", decoded)
	}
	if m["custom"] != "data" {
		t.Fatalf("mismatch: %+v", m)
	}
}

// ── Binary payload decode with unknown op type ────────────────────────────

func TestDecodeBinaryPayloadUnknownOp(t *testing.T) {
	// Build a fake binary payload with unknown op type
	data := []byte{binaryPayloadMarker, 0x01, 0x02}
	_, err := decodeBinaryPayload(data, 0xFF)
	if err == nil {
		t.Fatal("expected error for unknown op type")
	}
}

// ── DecodeWALPayload edge cases ───────────────────────────────────────────

func TestDecodeWALPayloadEmpty(t *testing.T) {
	decoded, err := DecodeWALPayload([]byte{}, OpInsert)
	if err != nil {
		t.Fatal(err)
	}
	if decoded != nil {
		t.Fatalf("expected nil for empty payload, got %v", decoded)
	}
}

func TestDecodeWALPayloadJSON(t *testing.T) {
	jsonData := `{"key":"value"}`
	decoded, err := DecodeWALPayload([]byte(jsonData), OpInsert)
	if err != nil {
		t.Fatal(err)
	}
	m, ok := decoded.(map[string]interface{})
	if !ok {
		t.Fatalf("expected map, got %T", decoded)
	}
	if m["key"] != "value" {
		t.Fatalf("mismatch: %+v", m)
	}
}

// ── BuildRecord (public API) ──────────────────────────────────────────────

func TestBuildRecordPublic(t *testing.T) {
	payload := []byte("test payload")
	record, err := BuildRecord(1, OpInsert, payload, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(record) == 0 {
		t.Fatal("expected non-empty record")
	}
	// Verify magic
	if string(record[:4]) != recordMagic {
		t.Fatalf("expected magic %s, got %s", recordMagic, string(record[:4]))
	}
}

// ── NextTxID (public API) ────────────────────────────────────────────────

func TestNextTxID(t *testing.T) {
	w := mustOpen(t)
	next := w.NextTxID()
	if next != 1 {
		t.Fatalf("expected 1, got %d", next)
	}

	w.Append(OpInsert, map[string]interface{}{"v": 1})
	next = w.NextTxID()
	if next < 2 {
		t.Fatalf("expected >= 2, got %d", next)
	}
}

// ── GroupCommit Flush ─────────────────────────────────────────────────────

func TestGroupCommitFlush(t *testing.T) {
	w := mustOpen(t)
	w.EnableGroupCommit(10, 1*time.Second)
	defer w.DisableGroupCommit()

	// Append some records
	for i := 0; i < 3; i++ {
		w.AppendWithTx(uint64(i+1), OpInsert, map[string]interface{}{"i": i})
	}

	// Force flush
	w.groupCommit.Flush()

	// Records should be on disk now
	entries, err := w.Recover()
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 3 {
		t.Fatalf("expected 3 entries, got %d", len(entries))
	}
}

func TestGroupCommitTimerFlush(t *testing.T) {
	w := mustOpen(t)
	w.EnableGroupCommit(100, 20*time.Millisecond)
	defer w.DisableGroupCommit()

	// Append records — timer should flush them
	for i := 0; i < 5; i++ {
		w.AppendWithTx(uint64(i+1), OpInsert, map[string]interface{}{"i": i})
	}

	// Wait for timer-based flush
	time.Sleep(50 * time.Millisecond)

	entries, err := w.Recover()
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 5 {
		t.Fatalf("expected 5 entries after timer flush, got %d", len(entries))
	}
}

// ── WriteBehind buffer ────────────────────────────────────────────────────

func TestWriteBehindBuffer(t *testing.T) {
	w := mustOpen(t)
	w.EnableWriteBehind(10, 20*time.Millisecond)
	defer w.DisableWriteBehind()

	// Append records via write-behind
	for i := 0; i < 5; i++ {
		_, err := w.AppendWithWriteBehind(uint64(i+1), OpInsert, map[string]interface{}{"i": i})
		if err != nil {
			t.Fatal(err)
		}
	}

	// Wait for flush
	time.Sleep(50 * time.Millisecond)

	entries, err := w.Recover()
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 5 {
		t.Fatalf("expected 5 entries, got %d", len(entries))
	}
}

func TestWriteBehindFallbackToSync(t *testing.T) {
	w := mustOpen(t)
	// No write-behind enabled — should fall back to AppendWithTx
	_, err := w.AppendWithWriteBehind(1, OpInsert, map[string]interface{}{"v": 1})
	if err != nil {
		t.Fatal(err)
	}

	entries, err := w.Recover()
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 {
		t.Fatalf("expected 1 entry, got %d", len(entries))
	}
}

func TestWriteBehindFlushManual(t *testing.T) {
	w := mustOpen(t)
	w.EnableWriteBehind(100, 10*time.Second) // Long interval so we control flush
	defer w.DisableWriteBehind()

	w.AppendWithWriteBehind(1, OpInsert, map[string]interface{}{"v": 1})

	// Manual flush
	w.writeBehind.Flush()

	entries, err := w.Recover()
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 {
		t.Fatalf("expected 1 entry after manual flush, got %d", len(entries))
	}
}

// ── Concurrent WAL appends ────────────────────────────────────────────────

func TestConcurrentWALAppends(t *testing.T) {
	w := mustOpen(t)

	var wg sync.WaitGroup
	var mu sync.Mutex
	var errs []error

	numGoroutines := 10
	perGoroutine := 50

	for g := 0; g < numGoroutines; g++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			for i := 0; i < perGoroutine; i++ {
				txID := uint64(id*perGoroutine + i + 1)
				_, err := w.AppendWithTx(txID, OpInsert, map[string]interface{}{
					"goroutine": id,
					"seq":       i,
				})
				if err != nil {
					mu.Lock()
					errs = append(errs, fmt.Errorf("goroutine %d: %v", id, err))
					mu.Unlock()
					return
				}
			}
		}(g)
	}

	wg.Wait()

	if len(errs) > 0 {
		t.Fatalf("concurrent append errors: %v", errs[0])
	}

	entries, err := w.Recover()
	if err != nil {
		t.Fatal(err)
	}
	expected := numGoroutines * perGoroutine
	if len(entries) != expected {
		t.Fatalf("expected %d entries, got %d", expected, len(entries))
	}
}

// ── WAL corruption recovery ───────────────────────────────────────────────

func TestCorruptionRecoveryResync(t *testing.T) {
	path := filepath.Join(t.TempDir(), "vaultdb.wal")

	w, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	// Write first valid entry
	w.Append(OpInsert, map[string]interface{}{"v": 1})
	w.Close()

	// Build valid entries
	entry1, _ := buildRecord(1, OpInsert, mustMarshal(t, map[string]interface{}{"v": 1}), nil)
	entry2, _ := buildRecord(2, OpInsert, mustMarshal(t, map[string]interface{}{"v": 2}), nil)

	// Create corrupt data: valid entry + garbage + valid entry
	corruptData := make([]byte, 100) // 100 bytes of garbage
	var buf bytes.Buffer
	buf.Write(entry1)
	buf.Write(corruptData)
	buf.Write(entry2)
	os.WriteFile(path, buf.Bytes(), 0o644)

	w2, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer w2.Close()

	entries, err := w2.Recover()
	if err != nil {
		t.Fatal(err)
	}
	// Should recover at least the first entry, possibly the second after resync
	if len(entries) < 1 {
		t.Fatalf("expected at least 1 entry after corruption recovery, got %d", len(entries))
	}
}

func TestCorruptionRecoveryPartialEntry(t *testing.T) {
	path := filepath.Join(t.TempDir(), "vaultdb.wal")

	w, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	w.Append(OpInsert, map[string]interface{}{"v": 1})
	w.Close()

	// Append a partial entry (just magic + some bytes)
	f, _ := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0o644)
	f.Write([]byte("VDB1"))
	f.Write([]byte{0x01, 0x00}) // partial
	f.Close()

	w2, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer w2.Close()

	entries, err := w2.Recover()
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 {
		t.Fatalf("expected 1 entry after partial recovery, got %d", len(entries))
	}
}

// ── WriteCheckpointRecord then TruncateWAL ────────────────────────────────

func TestWriteCheckpointThenTruncate(t *testing.T) {
	w := mustOpen(t)

	for i := 0; i < 5; i++ {
		w.Append(OpInsert, map[string]interface{}{"i": i})
	}

	lsn, err := w.WriteCheckpointRecord()
	if err != nil {
		t.Fatal(err)
	}
	if lsn == 0 {
		t.Fatal("expected non-zero LSN")
	}

	if err := w.TruncateWAL(); err != nil {
		t.Fatal(err)
	}

	// Write new data after truncation
	w.Append(OpInsert, map[string]interface{}{"after": true})

	entries, err := w.Recover()
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 {
		t.Fatalf("expected 1 entry after truncation, got %d", len(entries))
	}
}

// ── AnalyzeTransactions with abort ────────────────────────────────────────

func TestAnalyzeTransactionsAbort(t *testing.T) {
	w := mustOpen(t)

	// tx 1: operations then abort
	w.AppendWithTx(1, OpInsert, map[string]interface{}{"v": 1})
	w.AppendWithTx(1, OpUpdate, map[string]interface{}{"v": 2})
	w.AppendWithTx(1, OpAbort, nil)

	committed, inProgress, err := w.AnalyzeTransactions()
	if err != nil {
		t.Fatal(err)
	}
	if committed[1] {
		t.Error("tx 1 should not be committed after abort")
	}
	if inProgress[1] {
		t.Error("tx 1 should not be in-progress after abort")
	}
}

func TestAnalyzeTransactionsMixed(t *testing.T) {
	w := mustOpen(t)

	// tx 1: committed
	w.AppendWithTx(1, OpInsert, map[string]interface{}{"v": 1})
	w.AppendWithTx(1, OpCommit, nil)

	// tx 2: in-progress (no commit/abort)
	w.AppendWithTx(2, OpInsert, map[string]interface{}{"v": 2})

	// tx 3: committed then has more ops (committed wins)
	w.AppendWithTx(3, OpInsert, map[string]interface{}{"v": 3})
	w.AppendWithTx(3, OpCommit, nil)
	w.AppendWithTx(3, OpUpdate, map[string]interface{}{"v": 31})

	// tx 0 operations should be ignored
	w.AppendWithTx(0, OpInsert, map[string]interface{}{"v": 0})

	committed, inProgress, err := w.AnalyzeTransactions()
	if err != nil {
		t.Fatal(err)
	}

	if !committed[1] {
		t.Error("tx 1 should be committed")
	}
	if !inProgress[2] {
		t.Error("tx 2 should be in-progress")
	}
	if !committed[3] {
		t.Error("tx 3 should be committed")
	}
}

// ── WAL with encryption and concurrent writes ─────────────────────────────

func TestConcurrentEncryptedWALAppends(t *testing.T) {
	path := filepath.Join(t.TempDir(), "vaultdb.wal")
	w, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer w.Close()

	em := newTestEncryptionManager(t)
	w.SetEncryptionManager(em)

	var wg sync.WaitGroup
	for g := 0; g < 5; g++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			for i := 0; i < 20; i++ {
				txID := uint64(id*20 + i + 1)
				_, err := w.AppendWithTx(txID, OpInsert, map[string]interface{}{
					"g": id, "i": i,
				})
				if err != nil {
					t.Errorf("goroutine %d: %v", id, err)
					return
				}
			}
		}(g)
	}
	wg.Wait()

	w.Close()

	// Reopen and verify
	w2, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer w2.Close()
	w2.SetEncryptionManager(em)

	entries, err := w2.Recover()
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 100 {
		t.Fatalf("expected 100 entries, got %d", len(entries))
	}
}

// ── Replay with errors ────────────────────────────────────────────────────

func TestReplayTransactionError(t *testing.T) {
	w := mustOpen(t)

	w.AppendWithTx(1, OpInsert, map[string]interface{}{"v": 1})
	w.AppendWithTx(1, OpInsert, map[string]interface{}{"v": 2})

	called := 0
	err := w.ReplayTransaction(1, func(e Entry) error {
		called++
		if called == 1 {
			return fmt.Errorf("simulated error")
		}
		return nil
	})
	if err == nil {
		t.Fatal("expected error from ReplayTransaction")
	}
	if called != 1 {
		t.Fatalf("expected callback called once, got %d", called)
	}
}

func TestReplayTransactionNotFound(t *testing.T) {
	w := mustOpen(t)

	w.AppendWithTx(1, OpInsert, map[string]interface{}{"v": 1})

	var count int
	err := w.ReplayTransaction(999, func(e Entry) error {
		count++
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if count != 0 {
		t.Fatalf("expected 0 entries for non-existent tx, got %d", count)
	}
}

// ── Flush ─────────────────────────────────────────────────────────────────

func TestFlushAfterAppend(t *testing.T) {
	w := mustOpen(t)

	w.Append(OpInsert, map[string]interface{}{"v": 1})

	lsn, err := w.Flush()
	if err != nil {
		t.Fatal(err)
	}
	if lsn == 0 {
		t.Fatal("expected non-zero LSN after flush")
	}
}

// ── WriteFullPageImage with various sizes ─────────────────────────────────

func TestWriteFullPageImageZeroLength(t *testing.T) {
	w := mustOpen(t)

	err := w.WriteFullPageImage(1, "db", "tbl", 0, 0, []byte{})
	if err != nil {
		t.Fatal(err)
	}

	entries, err := w.Recover()
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 {
		t.Fatalf("expected 1 entry, got %d", len(entries))
	}
}

func TestWriteFullPageImageLarge(t *testing.T) {
	w := mustOpen(t)

	// 64KB page data
	pageData := make([]byte, 65536)
	for i := range pageData {
		pageData[i] = byte(i % 256)
	}

	err := w.WriteFullPageImage(1, "db", "tbl", 0, 0, pageData)
	if err != nil {
		t.Fatal(err)
	}

	entries, err := w.Recover()
	if err != nil {
		t.Fatal(err)
	}

	decoded, err := DecodeWALPayload(entries[0].Payload, entries[0].OpType)
	if err != nil {
		t.Fatal(err)
	}
	p := decoded.(FullPageImagePayload)
	if !bytes.Equal(p.PageData, pageData) {
		t.Fatal("large page data mismatch")
	}
}

// ── Open error paths ──────────────────────────────────────────────────────

func TestOpenNonExistentDir(t *testing.T) {
	if os.Geteuid() == 0 || runtime.GOOS == "windows" {
		t.Skip("skipping non-existent root dir test when running as root or on Windows")
	}
	path := "/nonexistent/dir/that/does/not/exist/wal"
	_, err := Open(path)
	if err == nil {
		t.Fatal("expected error for non-existent directory")
	}
}

// ── Checkpoint ────────────────────────────────────────────────────────────

func TestCheckpointBasic(t *testing.T) {
	w := mustOpen(t)

	for i := 0; i < 5; i++ {
		w.Append(OpInsert, map[string]interface{}{"i": i})
	}

	if err := w.Checkpoint(); err != nil {
		t.Fatal(err)
	}

	// After checkpoint, WAL should be truncated
	entries, err := w.Recover()
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 0 {
		t.Fatalf("expected 0 entries after checkpoint, got %d", len(entries))
	}
}

// ── WriteBehind double enable/disable ─────────────────────────────────────

func TestWriteBehindDoubleDisable(t *testing.T) {
	w := mustOpen(t)
	w.EnableWriteBehind(10, 50*time.Millisecond)
	w.DisableWriteBehind()
	w.DisableWriteBehind() // second disable should be safe
}

// ── GroupCommit double enable/disable ─────────────────────────────────────

func TestGroupCommitDoubleDisable(t *testing.T) {
	w := mustOpen(t)
	w.EnableGroupCommit(10, 50*time.Millisecond)
	w.DisableGroupCommit()
	w.DisableGroupCommit() // second disable should be safe
}

// ── EnableWriteBehind invalid params ──────────────────────────────────────

func TestWriteBehindInvalidMaxBuffer(t *testing.T) {
	w := mustOpen(t)
	// maxBuffer <= 0 should be no-op
	w.EnableWriteBehind(0, 50*time.Millisecond)
	if w.writeBehind != nil {
		t.Fatal("writeBehind should not be enabled with maxBuffer=0")
	}
}

// ── EnableGroupCommit invalid params ──────────────────────────────────────

func TestGroupCommitInvalidBatchSize(t *testing.T) {
	w := mustOpen(t)
	// batchSize <= 0 should be no-op
	w.EnableGroupCommit(0, 50*time.Millisecond)
	if w.groupCommit != nil {
		t.Fatal("groupCommit should not be enabled with batchSize=0")
	}
}

// ── SetEncryptionManager nil ──────────────────────────────────────────────

func TestSetEncryptionManagerNil(t *testing.T) {
	w := mustOpen(t)
	em := newTestEncryptionManager(t)
	w.SetEncryptionManager(em)
	w.SetEncryptionManager(nil) // disable encryption
	if w.em != nil {
		t.Fatal("encryption manager should be nil after disabling")
	}
}

// ── ReplayTransaction with error path ─────────────────────────────────────

func TestReplayTransactionSeekError(t *testing.T) {
	w := mustOpen(t)
	w.Close() // Close the file

	err := w.ReplayTransaction(1, func(e Entry) error {
		return nil
	})
	if err == nil {
		t.Fatal("expected error from ReplayTransaction with closed file")
	}
}

func TestReplaySeekError(t *testing.T) {
	w := mustOpen(t)
	w.Close()

	err := w.Replay(func(e Entry) error {
		return nil
	})
	if err == nil {
		t.Fatal("expected error from Replay with closed file")
	}
}

// ── FindLastVacuumCommit with decode error ────────────────────────────────

func TestFindLastVacuumCommitDecodeError(t *testing.T) {
	w := mustOpen(t)

	// Write a vacuum commit with corrupt payload
	// Manually write a record with OpVacuumCommit but garbage payload
	payload := []byte("not valid payload")
	record, _ := BuildRecord(1, OpVacuumCommit, payload, nil)
	w.mu.Lock()
	w.file.Write(record)
	w.mu.Unlock()

	// Should not crash — just skip the bad entry
	found, _, err := w.FindLastVacuumCommit("db", "tbl")
	if err != nil {
		t.Fatal(err)
	}
	if found {
		t.Fatal("should not find vacuum commit with corrupt payload")
	}
}

// ── Recover after multiple operations ─────────────────────────────────────

func TestRecoverAfterMixedOps(t *testing.T) {
	w := mustOpen(t)

	w.AppendWithTx(1, OpInsert, map[string]interface{}{"v": 1})
	w.AppendWithTx(1, OpCommit, nil)
	w.AppendWithTx(2, OpUpdate, map[string]interface{}{"v": 2})
	// No commit for tx 2 — in-progress
	w.AppendWithTx(3, OpDelete, map[string]interface{}{"v": 3})
	w.AppendWithTx(3, OpAbort, nil)

	entries, err := w.Recover()
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 5 {
		t.Fatalf("expected 5 entries, got %d", len(entries))
	}

	committed, inProgress, err := w.AnalyzeTransactions()
	if err != nil {
		t.Fatal(err)
	}
	if !committed[1] {
		t.Error("tx 1 should be committed")
	}
	if !inProgress[2] {
		t.Error("tx 2 should be in-progress")
	}
	if committed[3] {
		t.Error("tx 3 should not be committed (aborted)")
	}
}

// ── ScanAndTruncate with rename fallback ──────────────────────────────────

func TestScanAndTruncateRenameFallback(t *testing.T) {
	path := filepath.Join(t.TempDir(), "vaultdb.wal")

	w, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	w.Append(OpInsert, map[string]interface{}{"v": 1})
	w.Close()

	// Create a corrupt WAL file (invalid magic at the start)
	os.WriteFile(path, []byte("CORRUPT_DATA_NO_MAGIC"), 0o644)

	w2, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer w2.Close()

	// Should have 0 entries (entire file was corrupt)
	entries, err := w2.Recover()
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 0 {
		t.Fatalf("expected 0 entries after corrupt file, got %d", len(entries))
	}
}

// ── NewWriteBehindBuffer and NewGroupCommit ───────────────────────────────

func TestWriteBehindBufferCreation(t *testing.T) {
	w := mustOpen(t)
	wb := NewWriteBehindBuffer(w, 10, 50*time.Millisecond)
	if wb == nil {
		t.Fatal("expected non-nil WriteBehindBuffer")
	}
	wb.Close()
}

func TestGroupCommitCreation(t *testing.T) {
	w := mustOpen(t)
	gc := NewGroupCommit(w, 10, 50*time.Millisecond)
	if gc == nil {
		t.Fatal("expected non-nil GroupCommit")
	}
	gc.Close()
}

// ── DisableGroupCommit and DisableWriteBehind with nil ────────────────────

func TestDisableGroupCommitNil(t *testing.T) {
	w := mustOpen(t)
	// groupCommit is nil — should be no-op
	w.DisableGroupCommit()
}

func TestDisableWriteBehindNil(t *testing.T) {
	w := mustOpen(t)
	// writeBehind is nil — should be no-op
	w.DisableWriteBehind()
}
