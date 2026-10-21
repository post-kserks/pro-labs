//go:build chaos

package wal

import (
	"errors"
	"math/rand"
	"os"
	"os/exec"
	"path/filepath"
	"sync"
	"testing"
	"time"
)

// FaultyFile wraps an existing File and randomly injects I/O errors.
type FaultyFile struct {
	File
	WriteErrRate float64
	SyncErrRate  float64
}

func (f *FaultyFile) Write(b []byte) (int, error) {
	if f.WriteErrRate > 0 && rand.Float64() < f.WriteErrRate {
		return 0, errors.New("chaos: simulated write error (ENOSPC)")
	}
	return f.File.Write(b)
}

func (f *FaultyFile) Sync() error {
	if f.SyncErrRate > 0 && rand.Float64() < f.SyncErrRate {
		return errors.New("chaos: simulated sync error (EIO)")
	}
	return f.File.Sync()
}

func TestChaosFaultInjection(t *testing.T) {
	path := filepath.Join(t.TempDir(), "chaos_faulty.wal")

	// Create normal WAL
	w, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}

	// Swap the underlying file with our FaultyFile
	realFile := w.file
	faultyFile := &FaultyFile{
		File:         realFile,
		WriteErrRate: 0.1, // 10% chance of write failure
		SyncErrRate:  0.1, // 10% chance of sync failure
	}
	w.file = faultyFile

	var wg sync.WaitGroup
	// Concurrently try to append 1000 records
	for i := 0; i < 1000; i++ {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			payload := WALPageInsertPayload{
				DB:        "chaosdb",
				Table:     "chaostable",
				TupleData: []byte("chaos data chunk"),
			}
			// We ignore the error because we expect random failures
			_, _ = w.Append(OpPageInsert, payload)
		}(i)
	}
	wg.Wait()

	// Restore real file for clean close
	w.file = realFile
	if err := w.Close(); err != nil {
		t.Logf("Close returned error (expected due to potential dirty state): %v", err)
	}

	// Now try to recover the WAL
	w2, err := Open(path)
	if err != nil {
		t.Fatalf("Failed to recover WAL after fault injection: %v", err)
	}
	defer w2.Close()

	// Ensure we can replay over the valid entries without crashing
	count := 0
	err = w2.Replay(func(entry Entry) error {
		count++
		return nil
	})
	if err != nil {
		t.Fatalf("Iterate failed after recovery: %v", err)
	}
	t.Logf("Successfully recovered %d valid entries from chaos WAL", count)
}

func TestChaosCrashRecovery(t *testing.T) {
	// We use the test executable itself to run the child process.
	if os.Getenv("GO_WANT_HELPER_PROCESS") == "1" {
		runCrashHelperProcess()
		return
	}

	path := filepath.Join(t.TempDir(), "chaos_crash.wal")

	// Create the WAL so the helper has a file to work with.
	w, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	w.Close()

	cmd := exec.Command(os.Args[0], "-test.run=TestChaosCrashRecovery")
	cmd.Env = append(os.Environ(), "GO_WANT_HELPER_PROCESS=1", "CHAOS_WAL_PATH="+path)

	// Start the process
	if err := cmd.Start(); err != nil {
		t.Fatalf("Failed to start helper process: %v", err)
	}

	// Let it run for a short time to generate data, then kill it violently (SIGKILL)
	time.Sleep(150 * time.Millisecond)
	if err := cmd.Process.Kill(); err != nil {
		t.Fatalf("Failed to kill helper process: %v", err)
	}
	_ = cmd.Wait() // Wait for it to die (it will return an error, which is expected)

	// Now try to recover the WAL
	w2, err := Open(path)
	if err != nil {
		t.Fatalf("Failed to recover WAL after crash: %v", err)
	}
	defer w2.Close()

	count := 0
	err = w2.Replay(func(entry Entry) error {
		count++
		return nil
	})
	if err != nil {
		t.Fatalf("Replay failed after crash recovery: %v", err)
	}
	t.Logf("Successfully recovered %d entries from crashed WAL", count)
}

func runCrashHelperProcess() {
	path := os.Getenv("CHAOS_WAL_PATH")
	w, err := Open(path)
	if err != nil {
		os.Exit(1)
	}
	// Append as fast as possible until killed
	for {
		payload := WALPageInsertPayload{
			DB:        "crashdb",
			Table:     "crashtable",
			TupleData: []byte("crash data chunk"),
		}
		_, _ = w.Append(OpPageInsert, payload)
	}
}
