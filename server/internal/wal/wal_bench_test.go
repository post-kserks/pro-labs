package wal

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"testing"
)

// oldReadEntriesLocked is the old implementation that loads ALL entries into memory.
// Kept here for benchmark comparison only.
func oldReadEntriesLocked(file *os.File, resetOnCheckpoint bool) ([]Entry, uint64, int64, error) {
	file.Seek(0, 0)

	entries := make([]Entry, 0, 16)
	var maxTxID uint64
	var validEnd int64

	for {
		magic := make([]byte, 4)
		if _, err := readFull(file, magic); err != nil {
			break
		}
		if string(magic) != recordMagic {
			break
		}

		txIDBytes := make([]byte, 8)
		if _, err := readFull(file, txIDBytes); err != nil {
			break
		}
		txID := leUint64(txIDBytes)
		if txID > maxTxID {
			maxTxID = txID
		}

		opBytes := make([]byte, 1)
		if _, err := readFull(file, opBytes); err != nil {
			break
		}
		opType := opBytes[0]

		lengthBytes := make([]byte, 4)
		if _, err := readFull(file, lengthBytes); err != nil {
			break
		}
		payloadLen := leUint32(lengthBytes)

		payload := make([]byte, payloadLen)
		if _, err := readFull(file, payload); err != nil {
			break
		}

		crcBytes := make([]byte, 4)
		if _, err := readFull(file, crcBytes); err != nil {
			break
		}

		validEnd += int64(4 + 8 + 1 + 4 + int(payloadLen) + 4)

		if opType == OpCheckpoint && resetOnCheckpoint {
			entries = entries[:0]
			continue
		}

		entries = append(entries, Entry{
			TxID:   txID,
			OpType: opType,
		})
	}

	file.Seek(0, 2)
	return entries, maxTxID, validEnd, nil
}

func readFull(f *os.File, buf []byte) (int, error) {
	n := 0
	for n < len(buf) {
		nn, err := f.Read(buf[n:])
		n += nn
		if err != nil {
			return n, err
		}
	}
	return n, nil
}

func leUint64(b []byte) uint64 {
	return uint64(b[0]) | uint64(b[1])<<8 | uint64(b[2])<<16 | uint64(b[3])<<24 |
		uint64(b[4])<<32 | uint64(b[5])<<40 | uint64(b[6])<<48 | uint64(b[7])<<56
}

func leUint32(b []byte) uint32 {
	return uint32(b[0]) | uint32(b[1])<<8 | uint32(b[2])<<16 | uint32(b[3])<<24
}

func populateWAL(tb testing.TB, path string, count int) {
	tb.Helper()
	w, err := Open(path)
	if err != nil {
		tb.Fatal(err)
	}
	for i := 0; i < count; i++ {
		_, err := w.Append(OpInsert, map[string]interface{}{
			"db":      "benchdb",
			"table":   "benchtable",
			"row_id":  i,
			"payload": fmt.Sprintf("row-%d-with-some-padding-data-to-make-it-realistic", i),
		})
		if err != nil {
			w.Close()
			tb.Fatal(err)
		}
	}
	w.Close()
}

func BenchmarkWALReadOld_1K(b *testing.B) {
	dir := b.TempDir()
	path := filepath.Join(dir, "bench.wal")
	populateWAL(b, path, 1000)

	f, _ := os.OpenFile(path, os.O_RDONLY, 0o644)
	defer f.Close()

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		f.Seek(0, 0)
		oldReadEntriesLocked(f, false)
	}
}

func BenchmarkWALReadOld_10K(b *testing.B) {
	dir := b.TempDir()
	path := filepath.Join(dir, "bench.wal")
	populateWAL(b, path, 10000)

	f, _ := os.OpenFile(path, os.O_RDONLY, 0o644)
	defer f.Close()

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		f.Seek(0, 0)
		oldReadEntriesLocked(f, false)
	}
}

func BenchmarkWALReadOld_100K(b *testing.B) {
	dir := b.TempDir()
	path := filepath.Join(dir, "bench.wal")
	populateWAL(b, path, 100000)

	f, _ := os.OpenFile(path, os.O_RDONLY, 0o644)
	defer f.Close()

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		f.Seek(0, 0)
		oldReadEntriesLocked(f, false)
	}
}

func BenchmarkWALStreamNew_1K(b *testing.B) {
	dir := b.TempDir()
	path := filepath.Join(dir, "bench.wal")
	populateWAL(b, path, 1000)

	f, _ := os.OpenFile(path, os.O_RDONLY, 0o644)
	defer f.Close()
	w := &WAL{file: f}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		f.Seek(0, 0)
		for {
			_, _, err := w.readEntryFrom(f)
			if err != nil {
				break
			}
		}
	}
}

func BenchmarkWALStreamNew_10K(b *testing.B) {
	dir := b.TempDir()
	path := filepath.Join(dir, "bench.wal")
	populateWAL(b, path, 10000)

	f, _ := os.OpenFile(path, os.O_RDONLY, 0o644)
	defer f.Close()
	w := &WAL{file: f}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		f.Seek(0, 0)
		for {
			_, _, err := w.readEntryFrom(f)
			if err != nil {
				break
			}
		}
	}
}

func BenchmarkWALStreamNew_100K(b *testing.B) {
	dir := b.TempDir()
	path := filepath.Join(dir, "bench.wal")
	populateWAL(b, path, 100000)

	f, _ := os.OpenFile(path, os.O_RDONLY, 0o644)
	defer f.Close()
	w := &WAL{file: f}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		f.Seek(0, 0)
		for {
			_, _, err := w.readEntryFrom(f)
			if err != nil {
				break
			}
		}
	}
}

func BenchmarkWALRecover_1K(b *testing.B) {
	dir := b.TempDir()
	path := filepath.Join(dir, "bench.wal")
	populateWAL(b, path, 1000)

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		w, _ := Open(path)
		w.Recover()
		w.Close()
	}
}

func BenchmarkWALRecover_10K(b *testing.B) {
	dir := b.TempDir()
	path := filepath.Join(dir, "bench.wal")
	populateWAL(b, path, 10000)

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		w, _ := Open(path)
		w.Recover()
		w.Close()
	}
}

func BenchmarkWALRecover_100K(b *testing.B) {
	dir := b.TempDir()
	path := filepath.Join(dir, "bench.wal")
	populateWAL(b, path, 100000)

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		w, _ := Open(path)
		w.Recover()
		w.Close()
	}
}

func BenchmarkWALScanAndTruncate_1K(b *testing.B) {
	dir := b.TempDir()
	path := filepath.Join(dir, "bench.wal")
	populateWAL(b, path, 1000)

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		f, _ := os.OpenFile(path, os.O_RDONLY, 0o644)
		w := &WAL{file: f}
		w.scanAndTruncate()
		f.Close()
	}
}

func BenchmarkWALScanAndTruncate_10K(b *testing.B) {
	dir := b.TempDir()
	path := filepath.Join(dir, "bench.wal")
	populateWAL(b, path, 10000)

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		f, _ := os.OpenFile(path, os.O_RDONLY, 0o644)
		w := &WAL{file: f}
		w.scanAndTruncate()
		f.Close()
	}
}

func BenchmarkWALScanAndTruncate_100K(b *testing.B) {
	dir := b.TempDir()
	path := filepath.Join(dir, "bench.wal")
	populateWAL(b, path, 100000)

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		f, _ := os.OpenFile(path, os.O_RDONLY, 0o644)
		w := &WAL{file: f}
		w.scanAndTruncate()
		f.Close()
	}
}

func BenchmarkWALReplay_1K(b *testing.B) {
	dir := b.TempDir()
	path := filepath.Join(dir, "bench.wal")
	populateWAL(b, path, 1000)

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		w, _ := Open(path)
		w.Replay(func(e Entry) error { return nil })
		w.Close()
	}
}

func BenchmarkWALReplay_10K(b *testing.B) {
	dir := b.TempDir()
	path := filepath.Join(dir, "bench.wal")
	populateWAL(b, path, 10000)

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		w, _ := Open(path)
		w.Replay(func(e Entry) error { return nil })
		w.Close()
	}
}

func BenchmarkWALReplay_100K(b *testing.B) {
	dir := b.TempDir()
	path := filepath.Join(dir, "bench.wal")
	populateWAL(b, path, 100000)

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		w, _ := Open(path)
		w.Replay(func(e Entry) error { return nil })
		w.Close()
	}
}

// ---------------------------------------------------------------------------
// Binary vs JSON encoding benchmarks
// ---------------------------------------------------------------------------

func BenchmarkBinaryEncodePageInsert(b *testing.B) {
	payload := WALPageInsertPayload{
		DB:        "benchdb",
		Table:     "benchtable",
		SegmentNo: 1,
		PageNo:    1234,
		SlotNo:    56,
		XID:       999999,
		TupleData: bytes.Repeat([]byte{0xAB}, 200),
	}
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		EncodeWALPayloadBinary(payload)
	}
}

func BenchmarkJSONEncodePageInsert(b *testing.B) {
	payload := WALPageInsertPayload{
		DB:        "benchdb",
		Table:     "benchtable",
		SegmentNo: 1,
		PageNo:    1234,
		SlotNo:    56,
		XID:       999999,
		TupleData: bytes.Repeat([]byte{0xAB}, 200),
	}
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		json.Marshal(payload)
	}
}

func BenchmarkBinaryDecodePageInsert(b *testing.B) {
	payload := WALPageInsertPayload{
		DB:        "benchdb",
		Table:     "benchtable",
		SegmentNo: 1,
		PageNo:    1234,
		SlotNo:    56,
		XID:       999999,
		TupleData: bytes.Repeat([]byte{0xAB}, 200),
	}
	data, _ := EncodeWALPayloadBinary(payload)
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		DecodeWALPayload(data, OpPageInsert)
	}
}

func BenchmarkJSONDecodePageInsert(b *testing.B) {
	payload := WALPageInsertPayload{
		DB:        "benchdb",
		Table:     "benchtable",
		SegmentNo: 1,
		PageNo:    1234,
		SlotNo:    56,
		XID:       999999,
		TupleData: bytes.Repeat([]byte{0xAB}, 200),
	}
	data, _ := json.Marshal(payload)
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		var p WALPageInsertPayload
		json.Unmarshal(data, &p)
	}
}

func BenchmarkBinaryEncodePageDelete(b *testing.B) {
	payload := WALPageDeletePayload{
		DB: "benchdb", Table: "benchtable",
		SegmentNo: 1, PageNo: 1234, SlotNo: 56, XMax: 999999,
	}
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		EncodeWALPayloadBinary(payload)
	}
}

func BenchmarkJSONEncodePageDelete(b *testing.B) {
	payload := WALPageDeletePayload{
		DB: "benchdb", Table: "benchtable",
		SegmentNo: 1, PageNo: 1234, SlotNo: 56, XMax: 999999,
	}
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		json.Marshal(payload)
	}
}
