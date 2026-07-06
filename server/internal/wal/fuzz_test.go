package wal

import (
	"os"
	"path/filepath"
	"testing"
)

func FuzzWALRecovery(f *testing.F) {
	// Create a valid WAL file
	tmpDir := f.TempDir()
	walPath := filepath.Join(tmpDir, "test.wal")

	w, err := Open(walPath)
	if err != nil {
		return
	}

	// Write some valid records
	for i := 0; i < 5; i++ {
		payload, _ := EncodeWALPayloadBinary(WALPageInsertPayload{
			DB:        "testdb",
			Table:     "testtbl",
			PageNo:    uint32(i + 1),
			TupleData: []byte("test tuple"),
		})
		_, _ = w.Append(OpPageInsert, payload)
	}
	w.Flush()
	w.Close()

	// Read valid WAL bytes
	validWAL, _ := os.ReadFile(walPath)
	f.Add(validWAL)

	// Corrupted variants
	if len(validWAL) > 50 {
		corrupted := make([]byte, len(validWAL))
		copy(corrupted, validWAL)
		corrupted[50] ^= 0xFF
		f.Add(corrupted)
	}

	// Truncated
	if len(validWAL) > 10 {
		f.Add(validWAL[:len(validWAL)/2])
	}

	// Empty
	f.Add([]byte{})

	f.Fuzz(func(t *testing.T, walBytes []byte) {
		defer func() {
			if r := recover(); r != nil {
				t.Fatalf("WAL recovery panicked: %v", r)
			}
		}()

		tmpDir := t.TempDir()
		fuzzPath := filepath.Join(tmpDir, "fuzz.wal")
		os.WriteFile(fuzzPath, walBytes, 0644)

		// Try to open and recover — should not panic
		w, err := Open(fuzzPath)
		if err != nil {
			return // expected for malformed WAL
		}
		defer w.Close()

		// Replay should handle corruption gracefully
		_ = w.Replay(func(e Entry) error {
			return nil
		})
	})
}
