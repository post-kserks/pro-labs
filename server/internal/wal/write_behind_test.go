package wal

import (
	"encoding/json"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestWriteBehindBatching(t *testing.T) {
	path := filepath.Join(t.TempDir(), "wbb_batch.wal")
	w, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer w.Close()

	wbb := NewWriteBehindBuffer(w, 10, 1*time.Second)
	defer wbb.Close()

	for i := 0; i < 10; i++ {
		payload, _ := json.Marshal(map[string]interface{}{"i": i})
		txID := w.nextTxID.Add(1)
		rec := &WALRecord{TxID: txID}
		rec.Data, _ = buildRecord(txID, OpInsert, payload, nil)
		wbb.Append(rec)
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

func TestWriteBehindFlush(t *testing.T) {
	path := filepath.Join(t.TempDir(), "wbb_flush.wal")
	w, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer w.Close()

	wbb := NewWriteBehindBuffer(w, 100, 10*time.Second) // large batch and timeout
	defer wbb.Close()

	for i := 0; i < 5; i++ {
		payload, _ := json.Marshal(map[string]interface{}{"i": i})
		txID := w.nextTxID.Add(1)
		rec := &WALRecord{TxID: txID}
		rec.Data, _ = buildRecord(txID, OpInsert, payload, nil)
		wbb.Append(rec)
	}

	// Explicit flush should write all pending records
	wbb.Flush()

	entries, err := w.Recover()
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 5 {
		t.Fatalf("expected 5 entries, got %d", len(entries))
	}
}

func TestWriteBehindCloseDrainsPending(t *testing.T) {
	path := filepath.Join(t.TempDir(), "wbb_close.wal")
	w, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer w.Close()

	wbb := NewWriteBehindBuffer(w, 100, 10*time.Second) // large batch and timeout

	for i := 0; i < 5; i++ {
		payload, _ := json.Marshal(map[string]interface{}{"i": i})
		txID := w.nextTxID.Add(1)
		rec := &WALRecord{TxID: txID}
		rec.Data, _ = buildRecord(txID, OpInsert, payload, nil)
		wbb.Append(rec)
	}

	// Close should flush pending records
	wbb.Close()

	entries, err := w.Recover()
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 5 {
		t.Fatalf("expected 5 entries, got %d", len(entries))
	}
}

func TestWriteBehindTimeout(t *testing.T) {
	path := filepath.Join(t.TempDir(), "wbb_timeout.wal")
	w, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer w.Close()

	wbb := NewWriteBehindBuffer(w, 100, 50*time.Millisecond) // large batch, short timeout
	defer wbb.Close()

	// Append only 3 records — won't hit batch size
	for i := 0; i < 3; i++ {
		payload, _ := json.Marshal(map[string]interface{}{"i": i})
		txID := w.nextTxID.Add(1)
		rec := &WALRecord{TxID: txID}
		rec.Data, _ = buildRecord(txID, OpInsert, payload, nil)
		wbb.Append(rec)
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

func TestWriteBehindConcurrent(t *testing.T) {
	path := filepath.Join(t.TempDir(), "wbb_concurrent.wal")
	w, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer w.Close()

	wbb := NewWriteBehindBuffer(w, 50, 10*time.Millisecond)
	defer wbb.Close()

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
				rec.Data, _ = buildRecord(txID, OpInsert, payload, nil)
				wbb.Append(rec)
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

func TestWriteBehindEnableDisable(t *testing.T) {
	path := filepath.Join(t.TempDir(), "wbb_enable.wal")
	w, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer w.Close()

	w.EnableWriteBehind(5, 50*time.Millisecond)
	defer w.DisableWriteBehind()

	txID, err := w.AppendWithWriteBehind(1, OpInsert, map[string]interface{}{"key": "val"})
	if err != nil {
		t.Fatal(err)
	}
	if txID != 1 {
		t.Errorf("txID = %d, want 1", txID)
	}

	time.Sleep(100 * time.Millisecond)

	entries, err := w.Recover()
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 {
		t.Fatalf("expected 1 entry, got %d", len(entries))
	}
}

func TestWriteBehindFallbackWhenDisabled(t *testing.T) {
	path := filepath.Join(t.TempDir(), "wbb_fallback.wal")
	w, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer w.Close()

	// Without write-behind enabled, AppendWithWriteBehind falls back to AppendWithTx
	txID, err := w.AppendWithWriteBehind(1, OpInsert, map[string]interface{}{"v": 1})
	if err != nil {
		t.Fatal(err)
	}
	if txID != 1 {
		t.Errorf("txID = %d, want 1", txID)
	}

	entries, err := w.Recover()
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 {
		t.Fatalf("expected 1 entry, got %d", len(entries))
	}
}

func TestWriteBehindZeroMaxBufferDisabled(t *testing.T) {
	path := filepath.Join(t.TempDir(), "wbb_zero.wal")
	w, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer w.Close()

	w.EnableWriteBehind(0, 50*time.Millisecond)
	if w.writeBehind != nil {
		t.Fatal("expected writeBehind to be nil for maxBuffer=0")
	}
}

func TestWriteBehindPanicsOnDoubleEnable(t *testing.T) {
	path := filepath.Join(t.TempDir(), "wbb_panic.wal")
	w, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer w.Close()

	w.EnableWriteBehind(5, 50*time.Millisecond)
	defer w.DisableWriteBehind()

	defer func() {
		if r := recover(); r == nil {
			t.Fatal("expected panic on double EnableWriteBehind")
		}
	}()
	w.EnableWriteBehind(5, 50*time.Millisecond)
}

// ---------------------------------------------------------------------------
// Benchmarks: WriteBehindBuffer vs direct WAL writes
// ---------------------------------------------------------------------------

func BenchmarkWriteBehindAppend_100(b *testing.B) {
	dir := b.TempDir()
	path := filepath.Join(dir, "wbb_bench.wal")

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		w, _ := Open(path)
		wbb := NewWriteBehindBuffer(w, 64, 10*time.Millisecond)
		for j := 0; j < 100; j++ {
			payload, _ := json.Marshal(map[string]interface{}{"i": j})
			txID := w.nextTxID.Add(1)
			rec := &WALRecord{TxID: txID}
			rec.Data, _ = buildRecord(txID, OpInsert, payload, nil)
			wbb.Append(rec)
		}
		wbb.Close()
		w.Close()
	}
}

func BenchmarkWriteBehindAppend_1K(b *testing.B) {
	dir := b.TempDir()
	path := filepath.Join(dir, "wbb_bench_1k.wal")

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		w, _ := Open(path)
		wbb := NewWriteBehindBuffer(w, 64, 10*time.Millisecond)
		for j := 0; j < 1000; j++ {
			payload, _ := json.Marshal(map[string]interface{}{"i": j})
			txID := w.nextTxID.Add(1)
			rec := &WALRecord{TxID: txID}
			rec.Data, _ = buildRecord(txID, OpInsert, payload, nil)
			wbb.Append(rec)
		}
		wbb.Close()
		w.Close()
	}
}

func BenchmarkWriteBehindAppendWithWriteBehind_100(b *testing.B) {
	dir := b.TempDir()
	path := filepath.Join(dir, "wbb_awb_bench.wal")

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		w, _ := Open(path)
		w.EnableWriteBehind(64, 10*time.Millisecond)
		for j := 0; j < 100; j++ {
			w.AppendWithWriteBehind(uint64(j+1), OpInsert, map[string]interface{}{"i": j})
		}
		w.Close()
	}
}

func BenchmarkWriteBehindAppendWithWriteBehind_1K(b *testing.B) {
	dir := b.TempDir()
	path := filepath.Join(dir, "wbb_awb_bench_1k.wal")

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		w, _ := Open(path)
		w.EnableWriteBehind(64, 10*time.Millisecond)
		for j := 0; j < 1000; j++ {
			w.AppendWithWriteBehind(uint64(j+1), OpInsert, map[string]interface{}{"i": j})
		}
		w.Close()
	}
}
