package wal

import (
	"errors"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// syncCountingFile wraps a File, counts Sync calls and can inject faults.
type syncCountingFile struct {
	File
	syncs     atomic.Int64
	syncDelay time.Duration
	syncErr   error

	mu sync.Mutex
	// failWriteAt: when >= 0, the write call with this index (0-based)
	// writes only the first half of the buffer and then fails/short-writes.
	failWriteAt   int
	writes        int
	shortNoErr    bool  // report a short write with a nil error
	truncateErr   error // error returned by Truncate
	truncateCalls int
}

func (f *syncCountingFile) Sync() error {
	f.syncs.Add(1)
	if f.syncDelay > 0 {
		time.Sleep(f.syncDelay)
	}
	if f.syncErr != nil {
		return f.syncErr
	}
	return f.File.Sync()
}

func (f *syncCountingFile) Write(b []byte) (int, error) {
	f.mu.Lock()
	idx := f.writes
	f.writes++
	fail := f.failWriteAt >= 0 && idx == f.failWriteAt
	f.mu.Unlock()
	if fail {
		n, err := f.File.Write(b[:len(b)/2])
		if err != nil {
			return n, err
		}
		if f.shortNoErr {
			return n, nil
		}
		return n, errors.New("injected: disk full")
	}
	return f.File.Write(b)
}

func (f *syncCountingFile) Truncate(size int64) error {
	f.mu.Lock()
	f.truncateCalls++
	terr := f.truncateErr
	f.mu.Unlock()
	if terr != nil {
		return terr
	}
	return f.File.Truncate(size)
}

func openWithCountingFile(t *testing.T) (*WAL, *syncCountingFile, string) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "durability.wal")
	w, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	cf := &syncCountingFile{File: w.file, failWriteAt: -1}
	w.file = cf
	return w, cf, path
}

func replayAll(t *testing.T, path string) []Entry {
	t.Helper()
	w, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer w.Close()
	var entries []Entry
	if err := w.Replay(func(e Entry) error {
		entries = append(entries, e)
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	return entries
}

// Data records keep the batched (no per-record fsync) behaviour, while a
// synchronous commit record is fsynced before AppendCommitSync returns.
func TestAppendCommitSync_FsyncsCommitOnly(t *testing.T) {
	w, cf, _ := openWithCountingFile(t)
	defer w.Close()

	for i := 0; i < 10; i++ {
		if _, err := w.AppendWithTx(42, OpPageInsert, WALPageInsertPayload{DB: "d", Table: "t", XID: 42}); err != nil {
			t.Fatal(err)
		}
	}
	if got := cf.syncs.Load(); got != 0 {
		t.Fatalf("data records must not be fsynced individually, got %d syncs", got)
	}
	if w.UnsyncedRecords() != 10 {
		t.Fatalf("expected 10 unsynced records, got %d", w.UnsyncedRecords())
	}

	if _, err := w.AppendCommitSync(42, nil); err != nil {
		t.Fatal(err)
	}
	if got := cf.syncs.Load(); got != 1 {
		t.Fatalf("expected exactly 1 fsync for the commit, got %d", got)
	}
	if n := w.UnsyncedRecords(); n != 0 {
		t.Fatalf("commit returned with %d unsynced records", n)
	}

	// A second commit with nothing new written still needs its own record synced.
	if _, err := w.AppendCommitSync(43, nil); err != nil {
		t.Fatal(err)
	}
	if got := cf.syncs.Load(); got != 2 {
		t.Fatalf("expected 2 fsyncs, got %d", got)
	}
}

// AppendWithTx(OpCommit) (used by asynchronous/autocommit storage paths) keeps
// the batched behaviour.
func TestAppendWithTxCommit_StaysBatched(t *testing.T) {
	w, cf, _ := openWithCountingFile(t)
	defer w.Close()
	if _, err := w.AppendWithTx(7, OpCommit, nil); err != nil {
		t.Fatal(err)
	}
	if got := cf.syncs.Load(); got != 0 {
		t.Fatalf("expected no fsync, got %d", got)
	}
}

func TestAppendCommitSync_SyncErrorIsReturned(t *testing.T) {
	w, cf, _ := openWithCountingFile(t)
	cf.syncErr = errors.New("injected EIO")
	if _, err := w.AppendCommitSync(1, nil); err == nil {
		t.Fatal("expected commit to fail when fsync fails")
	}
	cf.syncErr = nil
	_ = w.Close()
}

// Concurrent committers share fsyncs (group commit) and all are durable.
func TestAppendCommitSync_GroupCommitCoalesces(t *testing.T) {
	w, cf, path := openWithCountingFile(t)
	cf.syncDelay = 5 * time.Millisecond

	const n = 32
	var wg sync.WaitGroup
	start := make(chan struct{})
	errs := make(chan error, n)
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(id uint64) {
			defer wg.Done()
			<-start
			if _, err := w.AppendWithTx(id, OpPageInsert, WALPageInsertPayload{DB: "d", Table: "t", XID: id}); err != nil {
				errs <- err
				return
			}
			if _, err := w.AppendCommitSync(id, nil); err != nil {
				errs <- err
			}
		}(uint64(1000 + i))
	}
	close(start)
	wg.Wait()
	close(errs)
	for err := range errs {
		t.Fatal(err)
	}
	if got := cf.syncs.Load(); got >= n {
		t.Fatalf("expected concurrent commits to share fsyncs, got %d fsyncs for %d commits", got, n)
	}
	if u := w.UnsyncedRecords(); u != 0 {
		t.Fatalf("expected all records synced, %d unsynced", u)
	}
	t.Logf("%d commits -> %d fsyncs", n, cf.syncs.Load())
	cf.syncDelay = 0
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}

	commits := 0
	for _, e := range replayAll(t, path) {
		if e.OpType == OpCommit {
			commits++
		}
	}
	if commits != n {
		t.Fatalf("expected %d commit records, got %d", n, commits)
	}
}

func testTornWriteRolledBack(t *testing.T, shortNoErr bool) {
	w, cf, path := openWithCountingFile(t)
	cf.shortNoErr = shortNoErr

	if _, err := w.AppendWithTx(1, OpPageInsert, WALPageInsertPayload{DB: "d", Table: "t", XID: 1}); err != nil {
		t.Fatal(err)
	}
	cf.mu.Lock()
	cf.failWriteAt = cf.writes // next write is torn
	cf.mu.Unlock()
	if _, err := w.AppendWithTx(2, OpPageInsert, WALPageInsertPayload{DB: "d", Table: "t", XID: 2}); err == nil {
		t.Fatal("expected torn write to return an error")
	}
	// Later records must land right after record 1, not after garbage.
	if _, err := w.AppendWithTx(3, OpPageInsert, WALPageInsertPayload{DB: "d", Table: "t", XID: 3}); err != nil {
		t.Fatal(err)
	}
	if _, err := w.AppendCommitSync(3, nil); err != nil {
		t.Fatal(err)
	}
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}

	var got []uint64
	for _, e := range replayAll(t, path) {
		got = append(got, e.TxID)
	}
	want := []uint64{1, 3, 3}
	if len(got) != len(want) {
		t.Fatalf("replayed txIDs %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("replayed txIDs %v, want %v", got, want)
		}
	}
}

func TestTornWrite_ErrorIsTruncated(t *testing.T)      { testTornWriteRolledBack(t, false) }
func TestTornWrite_ShortWriteIsTruncated(t *testing.T) { testTornWriteRolledBack(t, true) }

// Torn write in the batched (write-behind / group commit) path.
func TestTornWrite_WriteRecordRaw(t *testing.T) {
	w, cf, path := openWithCountingFile(t)
	rec1, _ := buildRecord(1, OpInsert, []byte("a"), nil, nil)
	rec2, _ := buildRecord(2, OpInsert, []byte("b"), nil, nil)
	rec3, _ := buildRecord(3, OpInsert, []byte("c"), nil, nil)
	cf.failWriteAt = 1

	w.mu.Lock()
	if err := w.writeRecordRaw(WALRecord{TxID: 1, Data: rec1}); err != nil {
		t.Fatal(err)
	}
	if err := w.writeRecordRaw(WALRecord{TxID: 2, Data: rec2}); err == nil {
		t.Fatal("expected error")
	}
	if err := w.writeRecordRaw(WALRecord{TxID: 3, Data: rec3}); err != nil {
		t.Fatal(err)
	}
	w.mu.Unlock()
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	entries := replayAll(t, path)
	if len(entries) != 2 || entries[0].TxID != 1 || entries[1].TxID != 3 {
		t.Fatalf("unexpected entries after torn raw write: %+v", entries)
	}
}

// If the torn bytes cannot be removed, the WAL must refuse further writes
// instead of appending after garbage.
func TestTornWrite_UndoFailurePoisonsWAL(t *testing.T) {
	w, cf, _ := openWithCountingFile(t)
	cf.failWriteAt = 0
	cf.truncateErr = errors.New("injected truncate failure")
	if _, err := w.AppendWithTx(1, OpInsert, nil); err == nil {
		t.Fatal("expected error")
	}
	if _, err := w.AppendWithTx(2, OpInsert, nil); err == nil {
		t.Fatal("expected WAL to refuse writes after unrecoverable torn write")
	}
	if _, err := w.AppendCommitSync(2, nil); err == nil {
		t.Fatal("expected commit to be refused after unrecoverable torn write")
	}
	cf.truncateErr = nil
	// TruncateWAL empties the file and clears the poisoned state.
	if err := w.TruncateWAL(); err != nil {
		t.Fatal(err)
	}
	if _, err := w.AppendWithTx(3, OpInsert, nil); err != nil {
		t.Fatalf("expected writes to resume after TruncateWAL: %v", err)
	}
	_ = w.Close()
}

// A successful write must not touch Truncate (hot path stays syscall-free).
func TestWrite_NoTruncateOnSuccess(t *testing.T) {
	w, cf, _ := openWithCountingFile(t)
	defer w.Close()
	for i := 0; i < 5; i++ {
		if _, err := w.Append(OpInsert, nil); err != nil {
			t.Fatal(err)
		}
	}
	if cf.truncateCalls != 0 {
		t.Fatalf("unexpected Truncate calls: %d", cf.truncateCalls)
	}
}

// Commit-path benchmarks: 10 data records + 1 commit record per transaction.
// "Batched" is the pre-fix behaviour (commit record not forced to disk),
// "Sync" is synchronous_commit = on via AppendCommitSync.
func benchCommit(b *testing.B, durable bool, parallel bool) {
	w, err := Open(filepath.Join(b.TempDir(), "commit_bench.wal"))
	if err != nil {
		b.Fatal(err)
	}
	defer w.Close()
	payload := WALPageInsertPayload{DB: "d", Table: "t", TupleData: make([]byte, 64)}
	var next atomic.Uint64
	one := func() {
		id := next.Add(1)
		for j := 0; j < 10; j++ {
			if _, err := w.AppendWithTx(id, OpPageInsert, payload); err != nil {
				b.Error(err)
				return
			}
		}
		if durable {
			_, err = w.AppendCommitSync(id, nil)
		} else {
			_, err = w.AppendWithTx(id, OpCommit, nil)
		}
		if err != nil {
			b.Error(err)
		}
	}
	b.ResetTimer()
	if parallel {
		b.SetParallelism(4) // 4*GOMAXPROCS concurrent sessions
		b.RunParallel(func(pb *testing.PB) {
			for pb.Next() {
				one()
			}
		})
		return
	}
	for i := 0; i < b.N; i++ {
		one()
	}
}

func BenchmarkCommitTx10_Batched(b *testing.B)         { benchCommit(b, false, false) }
func BenchmarkCommitTx10_Sync(b *testing.B)            { benchCommit(b, true, false) }
func BenchmarkCommitTx10_BatchedParallel(b *testing.B) { benchCommit(b, false, true) }
func BenchmarkCommitTx10_SyncParallel(b *testing.B)    { benchCommit(b, true, true) }
