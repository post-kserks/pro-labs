package wal

import (
	"os"
	"path/filepath"
	"testing"
)

// A crash can leave a partial record at the end of the WAL. Records appended
// after that garbage must still be recoverable on the next restart.
func TestRecoverAfterCorruptTail(t *testing.T) {
	path := filepath.Join(t.TempDir(), "test.wal")

	w, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := w.Append(OpInsert, map[string]string{"row": "one"}); err != nil {
		t.Fatal(err)
	}
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}

	// Simulate a torn write: valid magic followed by a truncated record.
	f, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.Write([]byte("VDB1\x01\x02")); err != nil {
		t.Fatal(err)
	}
	if err := f.Close(); err != nil {
		t.Fatal(err)
	}

	// Reopen (drops the corrupt tail) and write another record after it.
	w2, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer w2.Close()
	if _, err := w2.Append(OpInsert, map[string]string{"row": "two"}); err != nil {
		t.Fatal(err)
	}

	entries, err := w2.Recover()
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 2 {
		t.Fatalf("recovered %d entries, want 2 (record after corrupt tail lost)", len(entries))
	}
}

func TestRecoverAfterMidFileCorruption(t *testing.T) {
	path := filepath.Join(t.TempDir(), "mid_corrupt.wal")

	w, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := w.Append(OpInsert, map[string]string{"row": "first"}); err != nil {
		t.Fatal(err)
	}
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}

	// Read existing valid record
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}

	// Re-open and write second valid record to get its raw bytes
	w2, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := w2.Append(OpInsert, map[string]string{"row": "second"}); err != nil {
		t.Fatal(err)
	}
	if err := w2.Close(); err != nil {
		t.Fatal(err)
	}
	fullData, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	secondRecordData := fullData[len(data):]

	// Now reconstruct WAL file: [First Record] + [25 bytes of garbage/corruption] + [Second Record]
	corruptContent := append([]byte{}, data...)
	corruptContent = append(corruptContent, []byte("garbage_corrupt_bytes_12345")...)
	corruptContent = append(corruptContent, secondRecordData...)

	if err := os.WriteFile(path, corruptContent, 0o644); err != nil {
		t.Fatal(err)
	}

	// Reopen WAL — scanAndTruncate and Recover must resync over the garbage block and recover both records
	w3, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer w3.Close()

	entries, err := w3.Recover()
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 2 {
		t.Fatalf("recovered %d entries after mid-file corruption, want 2", len(entries))
	}
}
