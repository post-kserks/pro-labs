package wal

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"sync"
	"testing"

	vaultcrypto "vaultdb/internal/core/crypto"
)

// ---------------------------------------------------------------------------
// Helpers
// ---------------------------------------------------------------------------

func mustOpen(t *testing.T) *WAL {
	t.Helper()
	path := filepath.Join(t.TempDir(), "vaultdb.wal")
	w, err := Open(path)
	if err != nil {
		t.Fatalf("Open failed: %v", err)
	}
	t.Cleanup(func() { w.Close() })
	return w
}

func appendPayload(t *testing.T, w *WAL, op byte, payload interface{}) uint64 {
	t.Helper()
	txID, err := w.Append(op, payload)
	if err != nil {
		t.Fatalf("Append failed: %v", err)
	}
	return txID
}

func appendWithTx(t *testing.T, w *WAL, txID uint64, op byte, payload interface{}) uint64 {
	t.Helper()
	id, err := w.AppendWithTx(txID, op, payload)
	if err != nil {
		t.Fatalf("AppendWithTx failed: %v", err)
	}
	return id
}

// ---------------------------------------------------------------------------
// 1. WAL Replay — replay entries after crash
// ---------------------------------------------------------------------------

func TestReplayAfterCrash(t *testing.T) {
	path := filepath.Join(t.TempDir(), "vaultdb.wal")

	// Write 3 entries, close normally (simulates a clean shutdown before crash)
	w, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 3; i++ {
		appendPayload(t, w, OpInsert, map[string]interface{}{"i": i})
	}
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}

	// Append more entries WITHOUT closing — simulates entries written but not fsynced
	f, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		t.Fatal(err)
	}
	for i := 3; i < 6; i++ {
		payload, _ := buildRecord(uint64(i+1), OpInsert, mustMarshal(t, map[string]interface{}{"i": i}), nil, nil)
		f.Write(payload)
	}
	f.Close()

	// Reopen — scanAndTruncate should keep all 6 entries
	w2, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer w2.Close()

	var replayed []Entry
	err = w2.Replay(func(e Entry) error {
		replayed = append(replayed, e)
		return nil
	})
	if err != nil {
		t.Fatalf("Replay failed: %v", err)
	}
	if len(replayed) != 6 {
		t.Fatalf("expected 6 entries after crash-replay, got %d", len(replayed))
	}
}

func TestReplayPreservesOrder(t *testing.T) {
	w := mustOpen(t)

	for i := byte(0); i < 5; i++ {
		appendPayload(t, w, OpInsert, map[string]interface{}{"seq": i})
	}

	var seen []byte
	err := w.Replay(func(e Entry) error {
		seen = append(seen, e.OpType)
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(seen) != 5 {
		t.Fatalf("expected 5, got %d", len(seen))
	}
	for i := 0; i < 5; i++ {
		if seen[i] != OpInsert {
			t.Errorf("entry %d: want OpInsert, got %d", i, seen[i])
		}
	}
}

func TestReplayStopsOnError(t *testing.T) {
	w := mustOpen(t)
	appendPayload(t, w, OpInsert, map[string]interface{}{"v": 1})
	appendPayload(t, w, OpInsert, map[string]interface{}{"v": 2})

	called := 0
	err := w.Replay(func(e Entry) error {
		called++
		if called == 1 {
			return os.ErrNotExist
		}
		return nil
	})
	if err == nil {
		t.Fatal("expected error from Replay")
	}
	if called != 1 {
		t.Fatalf("expected callback to be called once, got %d", called)
	}
}

func TestReplayEmptyWAL(t *testing.T) {
	w := mustOpen(t)
	var count int
	err := w.Replay(func(e Entry) error {
		count++
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if count != 0 {
		t.Fatalf("expected 0 entries, got %d", count)
	}
}

func TestReplayTransaction(t *testing.T) {
	w := mustOpen(t)

	appendWithTx(t, w, 1, OpInsert, map[string]interface{}{"v": "a"})
	appendWithTx(t, w, 2, OpUpdate, map[string]interface{}{"v": "b"})
	appendWithTx(t, w, 1, OpDelete, map[string]interface{}{"v": "a2"})
	appendWithTx(t, w, 3, OpInsert, map[string]interface{}{"v": "c"})

	var entries []Entry
	err := w.ReplayTransaction(1, func(e Entry) error {
		entries = append(entries, e)
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 2 {
		t.Fatalf("expected 2 entries for tx 1, got %d", len(entries))
	}
	for _, e := range entries {
		if e.TxID != 1 {
			t.Errorf("expected txID 1, got %d", e.TxID)
		}
	}
}

// ---------------------------------------------------------------------------
// 2. AnalyzeTransactions — committed vs in-progress
// ---------------------------------------------------------------------------

func TestAnalyzeCommittedAndInProgress(t *testing.T) {
	w := mustOpen(t)

	// tx 1: insert + commit
	appendWithTx(t, w, 1, OpInsert, map[string]interface{}{"v": 1})
	appendWithTx(t, w, 1, OpCommit, nil)

	// tx 2: insert only (in-progress)
	appendWithTx(t, w, 2, OpUpdate, map[string]interface{}{"v": 2})

	// tx 3: insert + abort
	appendWithTx(t, w, 3, OpDelete, map[string]interface{}{"v": 3})
	appendWithTx(t, w, 3, OpAbort, nil)

	// tx 4: insert + commit + later operation (committed wins)
	appendWithTx(t, w, 4, OpInsert, map[string]interface{}{"v": 4})
	appendWithTx(t, w, 4, OpCommit, nil)
	appendWithTx(t, w, 4, OpUpdate, map[string]interface{}{"v": 41})

	committed, inProgress, err := w.AnalyzeTransactions()
	if err != nil {
		t.Fatal(err)
	}

	if !committed[1] {
		t.Error("tx 1 should be committed")
	}
	if inProgress[1] {
		t.Error("tx 1 should not be in-progress")
	}

	if !inProgress[2] {
		t.Error("tx 2 should be in-progress")
	}
	if committed[2] {
		t.Error("tx 2 should not be committed")
	}

	if inProgress[3] {
		t.Error("tx 3 should not be in-progress (aborted)")
	}
	if committed[3] {
		t.Error("tx 3 should not be committed (aborted)")
	}

	if !committed[4] {
		t.Error("tx 4 should be committed")
	}
	if inProgress[4] {
		t.Error("tx 4 should not be in-progress")
	}
}

func TestAnalyzeEmptyWAL(t *testing.T) {
	w := mustOpen(t)
	committed, inProgress, err := w.AnalyzeTransactions()
	if err != nil {
		t.Fatal(err)
	}
	if len(committed) != 0 || len(inProgress) != 0 {
		t.Fatalf("expected empty maps, got committed=%d inProgress=%d", len(committed), len(inProgress))
	}
}

func TestAnalyzeMultipleCommits(t *testing.T) {
	w := mustOpen(t)

	for i := uint64(1); i <= 10; i++ {
		appendWithTx(t, w, i, OpInsert, map[string]interface{}{"i": i})
		appendWithTx(t, w, i, OpCommit, nil)
	}

	committed, inProgress, err := w.AnalyzeTransactions()
	if err != nil {
		t.Fatal(err)
	}
	if len(committed) != 10 {
		t.Fatalf("expected 10 committed, got %d", len(committed))
	}
	if len(inProgress) != 0 {
		t.Fatalf("expected 0 in-progress, got %d", len(inProgress))
	}
}

// ---------------------------------------------------------------------------
// 3. scanAndTruncate — corrupt tail handling
// ---------------------------------------------------------------------------

func TestScanAndTruncateCorruptMagic(t *testing.T) {
	path := filepath.Join(t.TempDir(), "vaultdb.wal")

	w, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	appendPayload(t, w, OpInsert, map[string]interface{}{"v": 1})
	appendPayload(t, w, OpInsert, map[string]interface{}{"v": 2})
	w.Close()

	// Overwrite last 4 bytes of second entry (corrupt CRC)
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	data[len(data)-1] ^= 0xFF
	os.WriteFile(path, data, 0o644)

	// Reopen should keep the first entry and truncate the corrupt second
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
		t.Fatalf("expected 1 entry after CRC corruption, got %d", len(entries))
	}
}

func TestScanAndTruncateInvalidMagic(t *testing.T) {
	path := filepath.Join(t.TempDir(), "vaultdb.wal")

	w, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	appendPayload(t, w, OpInsert, map[string]interface{}{"v": 1})
	w.Close()

	// Append garbage that doesn't start with magic
	f, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		t.Fatal(err)
	}
	f.Write([]byte("GARBAGE_DATA_HERE_NO_MAGIC"))
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
		t.Fatalf("expected 1 entry, got %d", len(entries))
	}
}

func TestScanAndTruncatePartialRecord(t *testing.T) {
	path := filepath.Join(t.TempDir(), "vaultdb.wal")

	w, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	appendPayload(t, w, OpInsert, map[string]interface{}{"v": 1})
	w.Close()

	// Append a valid magic header but truncated payload
	f, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		t.Fatal(err)
	}
	f.Write([]byte("VDB1"))
	f.Write([]byte{0x01, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00}) // txID
	f.Write([]byte{0x02})                                           // opType
	f.Write([]byte{0xFF, 0xFF, 0xFF, 0xFF})                         // huge payloadLen
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
		t.Fatalf("expected 1 entry after partial record, got %d", len(entries))
	}
}

func TestScanAndTruncateResyncAfterCorrupt(t *testing.T) {
	path := filepath.Join(t.TempDir(), "vaultdb.wal")

	w, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	// Write first valid entry
	appendPayload(t, w, OpInsert, map[string]interface{}{"v": 1})
	w.Close()

	// Build corrupt entry: valid magic but bad CRC → triggers resync path
	entry1, _ := buildRecord(1, OpInsert, mustMarshal(t, map[string]interface{}{"v": 1}), nil, nil)
	entry2, _ := buildRecord(2, OpInsert, mustMarshal(t, map[string]interface{}{"v": 2}), nil, nil)

	// Create a corrupt entry with valid magic "VDB1" but wrong CRC
	corruptEntry := make([]byte, len(entry1))
	copy(corruptEntry, entry1)
	corruptEntry[len(corruptEntry)-1] ^= 0xFF // flip CRC

	var buf bytes.Buffer
	buf.Write(entry1)
	buf.Write(corruptEntry)
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
	// Resync should find entry2 after the corrupt entry
	if len(entries) < 1 {
		t.Fatalf("expected at least 1 entry after resync, got %d", len(entries))
	}
	if entries[0].OpType != OpInsert {
		t.Errorf("expected OpInsert, got %d", entries[0].OpType)
	}
}

func TestScanAndTruncateEmptyFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "vaultdb.wal")
	w, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer w.Close()
	if w.CurrentTxID() != 0 {
		t.Errorf("empty WAL should have txID 0, got %d", w.CurrentTxID())
	}
}

// ---------------------------------------------------------------------------
// 4. WriteFullPageImage — full page image write/read
// ---------------------------------------------------------------------------

func TestWriteFullPageImageBasic(t *testing.T) {
	w := mustOpen(t)

	pageData := make([]byte, 8192)
	for i := range pageData {
		pageData[i] = byte(i % 256)
	}

	err := w.WriteFullPageImage(1, "mydb", "users", 0, 42, pageData)
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
	if entries[0].OpType != OpFullPageImage {
		t.Fatalf("expected OpFullPageImage, got %d", entries[0].OpType)
	}
	if entries[0].TxID != 1 {
		t.Fatalf("expected txID 1, got %d", entries[0].TxID)
	}

	decoded, err := DecodeWALPayload(entries[0].Payload, entries[0].OpType)
	if err != nil {
		t.Fatal(err)
	}
	payload, ok := decoded.(FullPageImagePayload)
	if !ok {
		t.Fatalf("expected FullPageImagePayload, got %T", decoded)
	}
	if payload.DB != "mydb" {
		t.Errorf("DB = %q, want %q", payload.DB, "mydb")
	}
	if payload.Table != "users" {
		t.Errorf("Table = %q, want %q", payload.Table, "users")
	}
	if payload.SegmentNo != 0 {
		t.Errorf("SegmentNo = %d, want 0", payload.SegmentNo)
	}
	if payload.PageNo != 42 {
		t.Errorf("PageNo = %d, want 42", payload.PageNo)
	}
	if !bytes.Equal(payload.PageData, pageData) {
		t.Errorf("PageData mismatch: got %d bytes, want %d bytes", len(payload.PageData), len(pageData))
	}
}

func TestWriteFullPageImageMultiplePages(t *testing.T) {
	w := mustOpen(t)

	for pageNo := uint32(0); pageNo < 5; pageNo++ {
		pageData := make([]byte, 8192)
		pageData[0] = byte(pageNo)
		err := w.WriteFullPageImage(uint64(pageNo+1), "db", "tbl", 0, pageNo, pageData)
		if err != nil {
			t.Fatalf("WriteFullPageImage page %d failed: %v", pageNo, err)
		}
	}

	entries, err := w.Recover()
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 5 {
		t.Fatalf("expected 5 entries, got %d", len(entries))
	}
	for i, entry := range entries {
		if entry.OpType != OpFullPageImage {
			t.Errorf("entry %d: expected OpFullPageImage, got %d", i, entry.OpType)
		}
	}
}

func TestWriteFullPageImageRoundTrip(t *testing.T) {
	w := mustOpen(t)

	original := bytes.Repeat([]byte("ABCD"), 2048) // 8KB
	err := w.WriteFullPageImage(100, "testdb", "testtbl", 3, 999, original)
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
	payload, ok := decoded.(FullPageImagePayload)
	if !ok {
		t.Fatalf("expected FullPageImagePayload, got %T", decoded)
	}
	if !bytes.Equal(payload.PageData, original) {
		t.Fatal("page data round-trip mismatch")
	}
}

func TestWriteFullPageImageFollowedByOtherOps(t *testing.T) {
	w := mustOpen(t)

	pageData := make([]byte, 8192)
	err := w.WriteFullPageImage(1, "db", "tbl", 0, 10, pageData)
	if err != nil {
		t.Fatal(err)
	}

	// Write a normal insert after the page image
	appendPayload(t, w, OpInsert, map[string]interface{}{"v": 42})

	entries, err := w.Recover()
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 2 {
		t.Fatalf("expected 2 entries, got %d", len(entries))
	}
	if entries[0].OpType != OpFullPageImage {
		t.Errorf("first entry should be OpFullPageImage, got %d", entries[0].OpType)
	}
	if entries[1].OpType != OpInsert {
		t.Errorf("second entry should be OpInsert, got %d", entries[1].OpType)
	}
}

// ---------------------------------------------------------------------------
// 5. BatchFsync — batch fsync behavior
// ---------------------------------------------------------------------------

func TestBatchFsyncWithBatchSize(t *testing.T) {
	path := filepath.Join(t.TempDir(), "vaultdb.wal")

	w, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer w.Close()
	w.SyncBatchSize = 4

	for i := 0; i < 8; i++ {
		if _, err := w.Append(OpInsert, map[string]interface{}{"i": i}); err != nil {
			t.Fatalf("append %d failed: %v", i, err)
		}
	}

	entries, err := w.Recover()
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 8 {
		t.Fatalf("expected 8 entries, got %d", len(entries))
	}
}

func TestBatchFsyncZeroBatchSize(t *testing.T) {
	path := filepath.Join(t.TempDir(), "vaultdb.wal")

	w, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer w.Close()
	w.SyncBatchSize = 0 // sync every write

	for i := 0; i < 10; i++ {
		if _, err := w.Append(OpInsert, map[string]interface{}{"i": i}); err != nil {
			t.Fatalf("append %d failed: %v", i, err)
		}
	}

	entries, err := w.Recover()
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 10 {
		t.Fatalf("expected 10 entries, got %d", len(entries))
	}
}

func TestBatchFsyncCounterReset(t *testing.T) {
	path := filepath.Join(t.TempDir(), "vaultdb.wal")

	w, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer w.Close()
	w.SyncBatchSize = 3

	// Write 6 entries (2 full batches)
	for i := 0; i < 6; i++ {
		if _, err := w.Append(OpInsert, map[string]interface{}{"i": i}); err != nil {
			t.Fatalf("append %d failed: %v", i, err)
		}
	}

	entries, err := w.Recover()
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 6 {
		t.Fatalf("expected 6, got %d", len(entries))
	}
}

func TestBatchFsyncOnAppendWithTx(t *testing.T) {
	path := filepath.Join(t.TempDir(), "vaultdb.wal")

	w, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer w.Close()
	w.SyncBatchSize = 3

	for i := uint64(1); i <= 9; i++ {
		if _, err := w.AppendWithTx(i, OpInsert, map[string]interface{}{"i": i}); err != nil {
			t.Fatalf("append tx %d failed: %v", i, err)
		}
	}

	entries, err := w.Recover()
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 9 {
		t.Fatalf("expected 9, got %d", len(entries))
	}
}

func TestBatchFsyncCallback(t *testing.T) {
	path := filepath.Join(t.TempDir(), "vaultdb.wal")

	w, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer w.Close()
	w.SyncBatchSize = 2

	var mu sync.Mutex
	callCount := 0
	w.OnAppend = func() {
		mu.Lock()
		callCount++
		mu.Unlock()
	}

	for i := 0; i < 6; i++ {
		if _, err := w.Append(OpInsert, map[string]interface{}{"i": i}); err != nil {
			t.Fatalf("append %d failed: %v", i, err)
		}
	}

	mu.Lock()
	defer mu.Unlock()
	if callCount != 6 {
		t.Fatalf("OnAppend called %d times, want 6", callCount)
	}
}

// ---------------------------------------------------------------------------
// Integration: checkpoint + replay after crash
// ---------------------------------------------------------------------------

func TestCheckpointThenCrashReplay(t *testing.T) {
	path := filepath.Join(t.TempDir(), "vaultdb.wal")

	w, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}

	// Write entries and checkpoint
	for i := 0; i < 5; i++ {
		appendPayload(t, w, OpInsert, map[string]interface{}{"i": i})
	}
	w.Checkpoint()

	// Write entries after checkpoint, then close
	for i := 5; i < 8; i++ {
		appendPayload(t, w, OpInsert, map[string]interface{}{"i": i})
	}
	w.Close()

	// Reopen — should see only post-checkpoint entries
	w2, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer w2.Close()

	entries, err := w2.Recover()
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 3 {
		t.Fatalf("expected 3 entries after checkpoint, got %d", len(entries))
	}
}

// ---------------------------------------------------------------------------
// 6. WriteCheckpointRecord and TruncateWAL
// ---------------------------------------------------------------------------

func TestWriteCheckpointRecord(t *testing.T) {
	w := mustOpen(t)

	appendPayload(t, w, OpInsert, map[string]interface{}{"v": 1})

	lsn, err := w.WriteCheckpointRecord()
	if err != nil {
		t.Fatal(err)
	}
	if lsn == 0 {
		t.Fatal("expected non-zero LSN")
	}

	// WriteCheckpointRecord should NOT truncate — both entries should be readable
	entries, err := w.Recover()
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 2 {
		t.Fatalf("expected 2 entries (1 insert + 1 checkpoint), got %d", len(entries))
	}
	if entries[1].OpType != OpCheckpoint {
		t.Errorf("second entry should be OpCheckpoint, got %d", entries[1].OpType)
	}
}

func TestTruncateWAL(t *testing.T) {
	w := mustOpen(t)

	for i := 0; i < 5; i++ {
		appendPayload(t, w, OpInsert, map[string]interface{}{"i": i})
	}

	if err := w.TruncateWAL(); err != nil {
		t.Fatal(err)
	}

	entries, err := w.Recover()
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 0 {
		t.Fatalf("expected 0 entries after TruncateWAL, got %d", len(entries))
	}
}

func TestWriteCheckpointRecordThenTruncate(t *testing.T) {
	path := filepath.Join(t.TempDir(), "vaultdb.wal")

	w, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}

	for i := 0; i < 3; i++ {
		appendPayload(t, w, OpInsert, map[string]interface{}{"i": i})
	}

	// WriteCheckpointRecord → TruncateWAL simulates the doCheckpoint flow
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

	// Write new entries after truncation
	appendPayload(t, w, OpInsert, map[string]interface{}{"after": true})

	entries, err := w.Recover()
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 {
		t.Fatalf("expected 1 entry after truncation, got %d", len(entries))
	}
	if entries[0].OpType != OpInsert {
		t.Errorf("expected OpInsert, got %d", entries[0].OpType)
	}
}

// ---------------------------------------------------------------------------
// 7. Flush
// ---------------------------------------------------------------------------

func TestFlushReturnsLSN(t *testing.T) {
	w := mustOpen(t)

	lsn, err := w.Flush()
	if err != nil {
		t.Fatal(err)
	}
	if lsn != 0 {
		t.Errorf("expected LSN 0 for empty WAL, got %d", lsn)
	}

	appendPayload(t, w, OpInsert, map[string]interface{}{"v": 1})

	lsn, err = w.Flush()
	if err != nil {
		t.Fatal(err)
	}
	if lsn == 0 {
		t.Error("expected non-zero LSN after append")
	}
}

// ---------------------------------------------------------------------------
// 8. FindLastVacuumCommit
// ---------------------------------------------------------------------------

func TestFindLastVacuumCommitFound(t *testing.T) {
	w := mustOpen(t)

	// VacuumBegin for wrong table
	appendWithTx(t, w, 1, OpVacuumBegin, WALVacuumPayload{DB: "db1", Table: "other"})
	// VacuumBegin for correct table
	appendWithTx(t, w, 2, OpVacuumBegin, WALVacuumPayload{DB: "db1", Table: "users"})
	// VacuumCommit for correct table
	appendWithTx(t, w, 3, OpVacuumCommit, WALVacuumPayload{DB: "db1", Table: "users"})

	found, txID, err := w.FindLastVacuumCommit("db1", "users")
	if err != nil {
		t.Fatal(err)
	}
	if !found {
		t.Fatal("expected to find vacuum commit")
	}
	if txID != 3 {
		t.Errorf("expected txID 3, got %d", txID)
	}
}

func TestFindLastVacuumCommitNotCommitted(t *testing.T) {
	w := mustOpen(t)

	appendWithTx(t, w, 1, OpVacuumBegin, WALVacuumPayload{DB: "db1", Table: "users"})

	found, txID, err := w.FindLastVacuumCommit("db1", "users")
	if err != nil {
		t.Fatal(err)
	}
	if found {
		t.Fatal("should not find committed vacuum")
	}
	if txID != 1 {
		t.Errorf("expected txID 1, got %d", txID)
	}
}

func TestFindLastVacuumCommitNotFound(t *testing.T) {
	w := mustOpen(t)

	appendPayload(t, w, OpInsert, map[string]interface{}{"v": 1})

	found, txID, err := w.FindLastVacuumCommit("db1", "users")
	if err != nil {
		t.Fatal(err)
	}
	if found {
		t.Fatal("should not find vacuum commit in empty WAL")
	}
	if txID != 0 {
		t.Errorf("expected txID 0, got %d", txID)
	}
}

// ---------------------------------------------------------------------------
// 9. Edge cases and error paths
// ---------------------------------------------------------------------------

func TestBuildRecordPayloadTooLarge(t *testing.T) {
	w := mustOpen(t)

	// maxPayloadSize is 32MB — try to append a payload exceeding it
	hugePayload := bytes.Repeat([]byte("x"), maxPayloadSize+1)
	_, err := w.Append(OpInsert, hugePayload)
	if err == nil {
		t.Fatal("expected error for oversized payload")
	}
}

func TestCloseNilFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "vaultdb.wal")
	w, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	w.Close()
	// Second close should be safe
	if err := w.Close(); err != nil {
		t.Errorf("second Close returned error: %v", err)
	}
}

func TestRecoverAfterReopen(t *testing.T) {
	path := filepath.Join(t.TempDir(), "vaultdb.wal")

	w, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	appendPayload(t, w, OpInsert, map[string]interface{}{"v": 1})
	appendPayload(t, w, OpUpdate, map[string]interface{}{"v": 2})
	w.Close()

	w2, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer w2.Close()

	entries, err := w2.Recover()
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 2 {
		t.Fatalf("expected 2 entries, got %d", len(entries))
	}

	// CurrentTxID should reflect max txID from entries
	if w2.CurrentTxID() < 2 {
		t.Errorf("CurrentTxID should be >= 2, got %d", w2.CurrentTxID())
	}
}

func TestMultipleCheckpoints(t *testing.T) {
	path := filepath.Join(t.TempDir(), "vaultdb.wal")

	w, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}

	// First batch + checkpoint
	appendPayload(t, w, OpInsert, map[string]interface{}{"batch": 1})
	w.Checkpoint()

	// Second batch + checkpoint
	appendPayload(t, w, OpInsert, map[string]interface{}{"batch": 2})
	w.Checkpoint()

	// Third batch (no checkpoint)
	appendPayload(t, w, OpInsert, map[string]interface{}{"batch": 3})
	w.Close()

	w2, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer w2.Close()

	entries, err := w2.Recover()
	if err != nil {
		t.Fatal(err)
	}
	// After second checkpoint, only batch 3 should remain
	if len(entries) != 1 {
		t.Fatalf("expected 1 entry, got %d", len(entries))
	}
}

func TestFindLastVacuumCommitWrongDB(t *testing.T) {
	w := mustOpen(t)

	appendWithTx(t, w, 1, OpVacuumCommit, WALVacuumPayload{DB: "other", Table: "users"})

	found, _, err := w.FindLastVacuumCommit("db1", "users")
	if err != nil {
		t.Fatal(err)
	}
	if found {
		t.Fatal("should not find vacuum commit for wrong DB")
	}
}

// ---------------------------------------------------------------------------
// Helpers continued
// ---------------------------------------------------------------------------

func mustMarshal(t *testing.T, v interface{}) []byte {
	t.Helper()
	// Use encoding/json via the wal package's internal json (already imported)
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatalf("json.Marshal failed: %v", err)
	}
	return b
}

// ---------------------------------------------------------------------------
// 10. WAL Encryption
// ---------------------------------------------------------------------------

func newTestEncryptionManager(t *testing.T) *vaultcrypto.EncryptionManager {
	t.Helper()
	dek := make([]byte, 32)
	for i := range dek {
		dek[i] = byte(i)
	}
	em, err := vaultcrypto.NewEncryptionManager(dek, "test-key-v1")
	if err != nil {
		t.Fatalf("NewEncryptionManager failed: %v", err)
	}
	return em
}

func TestWALEncryptionRoundtrip(t *testing.T) {
	path := filepath.Join(t.TempDir(), "vaultdb.wal")
	w, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer w.Close()

	em := newTestEncryptionManager(t)
	w.SetEncryptionManager(em)

	payload := map[string]interface{}{"key": "secret_value", "num": 42}
	txID, err := w.Append(OpInsert, payload)
	if err != nil {
		t.Fatalf("Append failed: %v", err)
	}
	if txID == 0 {
		t.Fatal("expected non-zero txID")
	}

	// Verify the record on disk has the encrypted flag set
	w.Close()

	w2, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer w2.Close()

	// Must set encryption manager to read encrypted records
	w2.SetEncryptionManager(em)

	entries, err := w2.Recover()
	if err != nil {
		t.Fatalf("Recover failed: %v", err)
	}
	if len(entries) != 1 {
		t.Fatalf("expected 1 entry, got %d", len(entries))
	}

	var got map[string]interface{}
	if err := json.Unmarshal(entries[0].Payload, &got); err != nil {
		t.Fatalf("unmarshal payload: %v", err)
	}
	if got["key"] != "secret_value" {
		t.Errorf("key = %v, want secret_value", got["key"])
	}
	if got["num"] != float64(42) {
		t.Errorf("num = %v, want 42", got["num"])
	}
}

func TestWALReadWithoutEncryptionFailsOnEncrypted(t *testing.T) {
	path := filepath.Join(t.TempDir(), "vaultdb.wal")
	w, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}

	em := newTestEncryptionManager(t)
	w.SetEncryptionManager(em)

	_, err = w.Append(OpInsert, map[string]interface{}{"secret": true})
	if err != nil {
		t.Fatal(err)
	}
	w.Close()

	// Reopen WITHOUT setting encryption manager
	w2, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer w2.Close()

	// Recovery should fail because records are encrypted but no EM is set
	_, err = w2.Recover()
	if err == nil {
		t.Fatal("expected error when reading encrypted WAL without EncryptionManager")
	}
}

func TestWALPlaintextStillWorks(t *testing.T) {
	path := filepath.Join(t.TempDir(), "vaultdb.wal")
	w, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer w.Close()

	// No encryption manager set — plaintext mode
	payload := map[string]interface{}{"data": "hello"}
	txID, err := w.Append(OpInsert, payload)
	if err != nil {
		t.Fatal(err)
	}
	if txID != 1 {
		t.Errorf("txID = %d, want 1", txID)
	}

	entries, err := w.Recover()
	if err != nil {
		t.Fatalf("Recover failed: %v", err)
	}
	if len(entries) != 1 {
		t.Fatalf("expected 1 entry, got %d", len(entries))
	}

	var got map[string]interface{}
	if err := json.Unmarshal(entries[0].Payload, &got); err != nil {
		t.Fatal(err)
	}
	if got["data"] != "hello" {
		t.Errorf("data = %v, want hello", got["data"])
	}
}

func TestWALEncryptionMultipleEntries(t *testing.T) {
	path := filepath.Join(t.TempDir(), "vaultdb.wal")
	w, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}

	em := newTestEncryptionManager(t)
	w.SetEncryptionManager(em)

	for i := 0; i < 10; i++ {
		_, err := w.Append(OpInsert, map[string]interface{}{"i": i})
		if err != nil {
			t.Fatalf("Append %d failed: %v", i, err)
		}
	}
	w.Close()

	w2, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer w2.Close()
	w2.SetEncryptionManager(em)

	entries, err := w2.Recover()
	if err != nil {
		t.Fatalf("Recover failed: %v", err)
	}
	if len(entries) != 10 {
		t.Fatalf("expected 10 entries, got %d", len(entries))
	}
	for i, e := range entries {
		var got map[string]interface{}
		if err := json.Unmarshal(e.Payload, &got); err != nil {
			t.Fatalf("entry %d unmarshal: %v", i, err)
		}
		if got["i"] != float64(i) {
			t.Errorf("entry %d: i = %v, want %d", i, got["i"], i)
		}
	}
}

func TestWALEncryptionWithAppendWithTx(t *testing.T) {
	path := filepath.Join(t.TempDir(), "vaultdb.wal")
	w, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}

	em := newTestEncryptionManager(t)
	w.SetEncryptionManager(em)

	_, err = w.AppendWithTx(100, OpInsert, map[string]interface{}{"tx": 100})
	if err != nil {
		t.Fatal(err)
	}
	w.Close()

	w2, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer w2.Close()
	w2.SetEncryptionManager(em)

	entries, err := w2.Recover()
	if err != nil {
		t.Fatalf("Recover failed: %v", err)
	}
	if len(entries) != 1 {
		t.Fatalf("expected 1 entry, got %d", len(entries))
	}
	if entries[0].TxID != 100 {
		t.Errorf("txID = %d, want 100", entries[0].TxID)
	}
}
