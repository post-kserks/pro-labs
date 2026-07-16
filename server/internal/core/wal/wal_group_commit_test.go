package wal

import (
	"encoding/json"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestGroupCommitBatching(t *testing.T) {
	path := filepath.Join(t.TempDir(), "gc_test.wal")
	w, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer w.Close()

	gc := NewGroupCommit(w, 10, 1*time.Second)
	defer gc.Close()

	// Append 10 records — should trigger a single flush
	for i := 0; i < 10; i++ {
		payload, _ := json.Marshal(map[string]interface{}{"i": i})
		txID := w.nextTxID.Add(1)
		rec := &WALRecord{TxID: txID}
		rec.Data, _ = buildRecord(txID, OpInsert, payload, nil, nil)
		gc.AppendBatch(rec)
	}

	// Give the worker time to flush
	time.Sleep(50 * time.Millisecond)

	entries, err := w.Recover()
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 10 {
		t.Fatalf("expected 10 entries, got %d", len(entries))
	}
}

func TestGroupCommitTimeout(t *testing.T) {
	path := filepath.Join(t.TempDir(), "gc_timeout.wal")
	w, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer w.Close()

	gc := NewGroupCommit(w, 100, 50*time.Millisecond) // batch=100, timeout=50ms
	defer gc.Close()

	// Append only 3 records — won't hit batch size
	for i := 0; i < 3; i++ {
		payload, _ := json.Marshal(map[string]interface{}{"i": i})
		txID := w.nextTxID.Add(1)
		rec := &WALRecord{TxID: txID}
		rec.Data, _ = buildRecord(txID, OpInsert, payload, nil, nil)
		gc.AppendBatch(rec)
	}

	// Wait for timeout flush
	time.Sleep(200 * time.Millisecond)

	entries, err := w.Recover()
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 3 {
		t.Fatalf("expected 3 entries, got %d", len(entries))
	}
}

func TestGroupCommitDataLoss(t *testing.T) {
	path := filepath.Join(t.TempDir(), "gc_concurrent.wal")
	w, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer w.Close()

	gc := NewGroupCommit(w, 50, 10*time.Millisecond)
	defer gc.Close()

	var wg sync.WaitGroup
	var count atomic.Int64
	const goroutines = 8
	const perGoroutine = 100

	for g := 0; g < goroutines; g++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := 0; i < perGoroutine; i++ {
				payload, _ := json.Marshal(map[string]interface{}{"g": g, "i": i})
				txID := w.nextTxID.Add(1)
				rec := &WALRecord{TxID: txID}
				rec.Data, _ = buildRecord(txID, OpInsert, payload, nil, nil)
				gc.AppendBatch(rec)
				count.Add(1)
			}
		}()
	}

	wg.Wait()

	// Let final flush complete
	time.Sleep(200 * time.Millisecond)

	entries, err := w.Recover()
	if err != nil {
		t.Fatal(err)
	}
	expected := int(count.Load())
	if len(entries) != expected {
		t.Fatalf("expected %d entries, got %d", expected, len(entries))
	}
}

func TestGroupCommitCloseFlushesPending(t *testing.T) {
	path := filepath.Join(t.TempDir(), "gc_close.wal")
	w, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer w.Close()

	gc := NewGroupCommit(w, 100, 10*time.Second) // large batch and timeout

	// Append 5 records — won't trigger any flush
	for i := 0; i < 5; i++ {
		payload, _ := json.Marshal(map[string]interface{}{"i": i})
		txID := w.nextTxID.Add(1)
		rec := &WALRecord{TxID: txID}
		rec.Data, _ = buildRecord(txID, OpInsert, payload, nil, nil)
		gc.AppendBatch(rec)
	}

	// Close should flush pending records
	gc.Close()

	entries, err := w.Recover()
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 5 {
		t.Fatalf("expected 5 entries, got %d", len(entries))
	}
}

func TestGroupCommitEnableDisable(t *testing.T) {
	path := filepath.Join(t.TempDir(), "gc_enable.wal")
	w, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer w.Close()

	// Enable, append via group commit, disable
	w.EnableGroupCommit(5, 50*time.Millisecond)
	defer w.DisableGroupCommit()

	payload, _ := json.Marshal(map[string]interface{}{"key": "val"})
	txID := w.nextTxID.Add(1)
	rec := &WALRecord{TxID: txID}
	rec.Data, _ = buildRecord(txID, OpInsert, payload, nil, nil)
	w.groupCommit.AppendBatch(rec)

	time.Sleep(100 * time.Millisecond)

	entries, err := w.Recover()
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 {
		t.Fatalf("expected 1 entry, got %d", len(entries))
	}
}

func TestGroupCommitZeroBatchSizeDisabled(t *testing.T) {
	path := filepath.Join(t.TempDir(), "gc_zero.wal")
	w, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer w.Close()

	// batchSize=0 should be a no-op
	w.EnableGroupCommit(0, 50*time.Millisecond)
	if w.groupCommit != nil {
		t.Fatal("expected groupCommit to be nil for batchSize=0")
	}
}

// ---------------------------------------------------------------------------
// Benchmarks
// ---------------------------------------------------------------------------

func BenchmarkGroupCommitAppend_100(b *testing.B) {
	dir := b.TempDir()
	path := filepath.Join(dir, "gc_bench.wal")

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		w, _ := Open(path)
		gc := NewGroupCommit(w, 64, 10*time.Millisecond)
		for j := 0; j < 100; j++ {
			payload, _ := json.Marshal(map[string]interface{}{"i": j})
			txID := w.nextTxID.Add(1)
			rec := &WALRecord{TxID: txID}
			rec.Data, _ = buildRecord(txID, OpInsert, payload, nil, nil)
			gc.AppendBatch(rec)
		}
		gc.Close()
		w.Close()
	}
}

func BenchmarkWALAppend_100(b *testing.B) {
	dir := b.TempDir()
	path := filepath.Join(dir, "plain_bench.wal")

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		w, _ := Open(path)
		for j := 0; j < 100; j++ {
			w.Append(OpInsert, map[string]interface{}{"i": j})
		}
		w.Close()
	}
}

func BenchmarkGroupCommitAppend_1K(b *testing.B) {
	dir := b.TempDir()
	path := filepath.Join(dir, "gc_bench_1k.wal")

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		w, _ := Open(path)
		gc := NewGroupCommit(w, 64, 10*time.Millisecond)
		for j := 0; j < 1000; j++ {
			payload, _ := json.Marshal(map[string]interface{}{"i": j})
			txID := w.nextTxID.Add(1)
			rec := &WALRecord{TxID: txID}
			rec.Data, _ = buildRecord(txID, OpInsert, payload, nil, nil)
			gc.AppendBatch(rec)
		}
		gc.Close()
		w.Close()
	}
}

func BenchmarkWALAppend_1K(b *testing.B) {
	dir := b.TempDir()
	path := filepath.Join(dir, "plain_bench_1k.wal")

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		w, _ := Open(path)
		for j := 0; j < 1000; j++ {
			w.Append(OpInsert, map[string]interface{}{"i": j})
		}
		w.Close()
	}
}
