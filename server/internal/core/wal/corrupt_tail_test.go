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
