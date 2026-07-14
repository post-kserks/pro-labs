package heap

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"vaultdb/internal/core/crypto"
	"vaultdb/internal/core/storage/page"
)

func TestCreateAllocateReadWrite(t *testing.T) {
	dir := t.TempDir()
	hf, err := CreateHeapFile(filepath.Join(dir, "users"))
	if err != nil {
		t.Fatal(err)
	}
	defer hf.Close()

	pid, pg, err := hf.AllocatePage(page.PageTypeHeap)
	if err != nil {
		t.Fatal(err)
	}
	if pid.SegmentNo != 0 || pid.PageNo != 0 {
		t.Errorf("first page = %+v, want segment 0 page 0", pid)
	}

	if _, err := pg.InsertTuple([]byte("row one")); err != nil {
		t.Fatal(err)
	}
	if err := hf.WritePage(pid, pg); err != nil {
		t.Fatal(err)
	}

	var got page.Page
	if err := hf.ReadPage(pid, &got); err != nil {
		t.Fatal(err)
	}
	if data := got.GetTuple(0); !bytes.Equal(data, []byte("row one")) {
		t.Errorf("tuple after roundtrip = %q", data)
	}

	count, err := hf.PageCount()
	if err != nil {
		t.Fatal(err)
	}
	if count != 1 {
		t.Errorf("PageCount = %d, want 1", count)
	}
}

func TestAllocateSequentialPages(t *testing.T) {
	hf, err := CreateHeapFile(filepath.Join(t.TempDir(), "t"))
	if err != nil {
		t.Fatal(err)
	}
	defer hf.Close()

	for i := uint32(0); i < 5; i++ {
		pid, _, err := hf.AllocatePage(page.PageTypeHeap)
		if err != nil {
			t.Fatal(err)
		}
		if pid.PageNo != i {
			t.Errorf("allocation %d: PageNo = %d", i, pid.PageNo)
		}
	}
	if count, _ := hf.PageCount(); count != 5 {
		t.Errorf("PageCount = %d, want 5", count)
	}
}

func TestReopenHeapFile(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "orders")
	hf, err := CreateHeapFile(dir)
	if err != nil {
		t.Fatal(err)
	}

	pid, pg, err := hf.AllocatePage(page.PageTypeHeap)
	if err != nil {
		t.Fatal(err)
	}
	pg.InsertTuple([]byte("persisted"))
	if err := hf.WritePage(pid, pg); err != nil {
		t.Fatal(err)
	}
	if err := hf.Close(); err != nil {
		t.Fatal(err)
	}

	hf2, err := OpenHeapFile(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer hf2.Close()

	var got page.Page
	if err := hf2.ReadPage(pid, &got); err != nil {
		t.Fatal(err)
	}
	if data := got.GetTuple(0); !bytes.Equal(data, []byte("persisted")) {
		t.Errorf("tuple after reopen = %q", data)
	}
}

func TestChecksumMismatchDetected(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "corrupt")
	hf, err := CreateHeapFile(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer hf.Close()

	pid, pg, err := hf.AllocatePage(page.PageTypeHeap)
	if err != nil {
		t.Fatal(err)
	}
	pg.InsertTuple([]byte("important data"))
	if err := hf.WritePage(pid, pg); err != nil {
		t.Fatal(err)
	}

	// Corrupt one byte in the middle of the page directly on disk.
	f, err := os.OpenFile(filepath.Join(dir, "0000.heap"), os.O_RDWR, 0o644)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.WriteAt([]byte{0xFF}, pid.FileOffset()+4000); err != nil {
		t.Fatal(err)
	}
	f.Close()

	var got page.Page
	err = hf.ReadPage(pid, &got)
	if !errors.Is(err, ErrChecksumMismatch) {
		t.Errorf("ReadPage on corrupted page = %v, want ErrChecksumMismatch", err)
	}
}

func TestOpenMissingDir(t *testing.T) {
	if _, err := OpenHeapFile(filepath.Join(t.TempDir(), "nope")); err == nil {
		t.Error("OpenHeapFile on missing dir succeeded")
	}
}

func TestCreateExistingFails(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "dup")
	if _, err := CreateHeapFile(dir); err != nil {
		t.Fatal(err)
	}
	if _, err := CreateHeapFile(dir); err == nil {
		t.Error("CreateHeapFile over existing segment succeeded")
	}
}

func TestWriteReadEncryptedPage(t *testing.T) {
	dir := t.TempDir()
	hf, err := CreateHeapFile(filepath.Join(dir, "enc"))
	if err != nil {
		t.Fatal(err)
	}
	defer hf.Close()

	// 32-byte key for AES-256.
	key := make([]byte, 32)
	for i := range key {
		key[i] = byte(i)
	}
	em, err := crypto.NewEncryptionManager(key, "v1")
	if err != nil {
		t.Fatal(err)
	}

	pid, pg, err := hf.AllocatePage(page.PageTypeHeap)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := pg.InsertTuple([]byte("encrypted row")); err != nil {
		t.Fatal(err)
	}

	if err := hf.WritePageEncrypted(pid, pg, em); err != nil {
		t.Fatal(err)
	}

	var got page.Page
	if err := hf.ReadPageEncrypted(pid, &got, em); err != nil {
		t.Fatal(err)
	}
	if data := got.GetTuple(0); !bytes.Equal(data, []byte("encrypted row")) {
		t.Errorf("encrypted roundtrip data = %q, want %q", data, "encrypted row")
	}
}

func TestReadPlaintextWithNilEncryptionManager(t *testing.T) {
	dir := t.TempDir()
	hf, err := CreateHeapFile(filepath.Join(dir, "plain"))
	if err != nil {
		t.Fatal(err)
	}
	defer hf.Close()

	pid, pg, err := hf.AllocatePage(page.PageTypeHeap)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := pg.InsertTuple([]byte("plain row")); err != nil {
		t.Fatal(err)
	}

	// Write with nil em — should behave like plaintext WritePage.
	if err := hf.WritePageEncrypted(pid, pg, nil); err != nil {
		t.Fatal(err)
	}

	// Read with nil em — should behave like plaintext ReadPage.
	var got page.Page
	if err := hf.ReadPageEncrypted(pid, &got, nil); err != nil {
		t.Fatal(err)
	}
	if data := got.GetTuple(0); !bytes.Equal(data, []byte("plain row")) {
		t.Errorf("plaintext roundtrip data = %q, want %q", data, "plain row")
	}
}

func TestReadEncryptedWithoutKeyFails(t *testing.T) {
	dir := t.TempDir()
	hf, err := CreateHeapFile(filepath.Join(dir, "nokey"))
	if err != nil {
		t.Fatal(err)
	}
	defer hf.Close()

	key := make([]byte, 32)
	for i := range key {
		key[i] = byte(i)
	}
	em, err := crypto.NewEncryptionManager(key, "v1")
	if err != nil {
		t.Fatal(err)
	}

	pid, pg, err := hf.AllocatePage(page.PageTypeHeap)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := pg.InsertTuple([]byte("secret")); err != nil {
		t.Fatal(err)
	}
	if err := hf.WritePageEncrypted(pid, pg, em); err != nil {
		t.Fatal(err)
	}

	// Use a different key to read — decryption should fail.
	wrongKey := make([]byte, 32)
	for i := range wrongKey {
		wrongKey[i] = byte(i + 32)
	}
	wrongEM, err := crypto.NewEncryptionManager(wrongKey, "v2")
	if err != nil {
		t.Fatal(err)
	}

	var got page.Page
	err = hf.ReadPageEncrypted(pid, &got, wrongEM)
	if err == nil {
		t.Fatal("ReadPageEncrypted with wrong key succeeded, want error")
	}
}

func TestReadAheadSequential(t *testing.T) {
	dir := t.TempDir()
	hf, err := CreateHeapFile(filepath.Join(dir, "ra"))
	if err != nil {
		t.Fatal(err)
	}
	defer hf.Close()

	// Allocate 10 pages with distinct data.
	for i := uint32(0); i < 10; i++ {
		pid, pg, err := hf.AllocatePage(page.PageTypeHeap)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := pg.InsertTuple([]byte(fmt.Sprintf("row-%d", i))); err != nil {
			t.Fatal(err)
		}
		if err := hf.WritePage(pid, pg); err != nil {
			t.Fatal(err)
		}
	}

	// Read ahead 5 pages from page 2 → expect pages 2..6.
	start := page.PageID{TableID: 1, SegmentNo: 0, PageNo: 2}
	pages, err := hf.ReadPageAhead(start, 5)
	if err != nil {
		t.Fatal(err)
	}
	if len(pages) != 5 {
		t.Fatalf("got %d pages, want 5", len(pages))
	}
	for i, pg := range pages {
		data := pg.GetTuple(0)
		want := fmt.Sprintf("row-%d", uint32(i)+2)
		if !bytes.Equal(data, []byte(want)) {
			t.Errorf("page %d: got %q, want %q", i, data, want)
		}
	}
}

func TestReadAheadBounds(t *testing.T) {
	dir := t.TempDir()
	hf, err := CreateHeapFile(filepath.Join(dir, "ra"))
	if err != nil {
		t.Fatal(err)
	}
	defer hf.Close()

	// Allocate 3 pages.
	for i := 0; i < 3; i++ {
		pid, pg, err := hf.AllocatePage(page.PageTypeHeap)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := pg.InsertTuple([]byte(fmt.Sprintf("row-%d", i))); err != nil {
			t.Fatal(err)
		}
		if err := hf.WritePage(pid, pg); err != nil {
			t.Fatal(err)
		}
	}

	// Request 10 ahead from page 1 — should only return 2 pages (1 and 2).
	start := page.PageID{TableID: 1, SegmentNo: 0, PageNo: 1}
	pages, err := hf.ReadPageAhead(start, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(pages) != 2 {
		t.Fatalf("got %d pages, want 2 (clamped to boundary)", len(pages))
	}
	for i, pg := range pages {
		data := pg.GetTuple(0)
		want := fmt.Sprintf("row-%d", i+1)
		if !bytes.Equal(data, []byte(want)) {
			t.Errorf("page %d: got %q, want %q", i, data, want)
		}
	}
}

func TestReadAheadBeyondEnd(t *testing.T) {
	dir := t.TempDir()
	hf, err := CreateHeapFile(filepath.Join(dir, "ra"))
	if err != nil {
		t.Fatal(err)
	}
	defer hf.Close()

	// Allocate 2 pages.
	for i := 0; i < 2; i++ {
		pid, _, err := hf.AllocatePage(page.PageTypeHeap)
		if err != nil {
			t.Fatal(err)
		}
		_ = pid
	}

	// Request ahead starting at the last page — should return 1 page.
	start := page.PageID{TableID: 1, SegmentNo: 0, PageNo: 1}
	pages, err := hf.ReadPageAhead(start, 5)
	if err != nil {
		t.Fatal(err)
	}
	if len(pages) != 1 {
		t.Fatalf("got %d pages, want 1", len(pages))
	}
}

func TestReadAheadZeroOrNegative(t *testing.T) {
	dir := t.TempDir()
	hf, err := CreateHeapFile(filepath.Join(dir, "ra"))
	if err != nil {
		t.Fatal(err)
	}
	defer hf.Close()

	start := page.PageID{TableID: 1, SegmentNo: 0, PageNo: 0}
	pages, err := hf.ReadPageAhead(start, 0)
	if err != nil {
		t.Fatal(err)
	}
	if pages != nil {
		t.Fatalf("expected nil for ahead=0, got %d pages", len(pages))
	}
}

func TestReadAheadCorruptPage(t *testing.T) {
	dir := t.TempDir()
	hf, err := CreateHeapFile(filepath.Join(dir, "ra"))
	if err != nil {
		t.Fatal(err)
	}
	defer hf.Close()

	// Allocate 5 pages with data.
	for i := uint32(0); i < 5; i++ {
		pid, pg, err := hf.AllocatePage(page.PageTypeHeap)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := pg.InsertTuple([]byte(fmt.Sprintf("row-%d", i))); err != nil {
			t.Fatal(err)
		}
		if err := hf.WritePage(pid, pg); err != nil {
			t.Fatal(err)
		}
	}

	// Corrupt page 2 on disk so its checksum becomes invalid.
	f, err := os.OpenFile(filepath.Join(dir, "ra", "0000.heap"), os.O_RDWR, 0o644)
	if err != nil {
		t.Fatal(err)
	}
	corruptPid := page.PageID{TableID: 1, SegmentNo: 0, PageNo: 2}
	if _, err := f.WriteAt([]byte{0xFF}, corruptPid.FileOffset()+4000); err != nil {
		t.Fatal(err)
	}
	f.Close()

	// Read ahead 5 from page 0 — page 2 should fail checksum.
	start := page.PageID{TableID: 1, SegmentNo: 0, PageNo: 0}
	_, err = hf.ReadPageAhead(start, 5)
	if err == nil {
		t.Fatal("expected checksum error for corrupted page")
	}
}

func BenchmarkReadAhead(b *testing.B) {
	dir := b.TempDir()
	hf, err := CreateHeapFile(filepath.Join(dir, "bench"))
	if err != nil {
		b.Fatal(err)
	}
	defer hf.Close()

	// Allocate 256 pages.
	for i := uint32(0); i < 256; i++ {
		pid, pg, err := hf.AllocatePage(page.PageTypeHeap)
		if err != nil {
			b.Fatal(err)
		}
		if _, err := pg.InsertTuple([]byte("bench-row")); err != nil {
			b.Fatal(err)
		}
		if err := hf.WritePage(pid, pg); err != nil {
			b.Fatal(err)
		}
	}

	b.Run("sequential_no_ahead", func(b *testing.B) {
		for b.Loop() {
			for g := uint32(0); g < 256; g++ {
				pid := page.PageID{TableID: 1, SegmentNo: 0, PageNo: g}
				var pg page.Page
				if err := hf.ReadPage(pid, &pg); err != nil {
					b.Fatal(err)
				}
			}
		}
	})

	b.Run("sequential_readahead_16", func(b *testing.B) {
		for b.Loop() {
			for g := uint32(0); g < 256; g += 16 {
				start := page.PageID{TableID: 1, SegmentNo: 0, PageNo: g}
				_, err := hf.ReadPageAhead(start, 16)
				if err != nil {
					b.Fatal(err)
				}
			}
		}
	})

	b.Run("sequential_readahead_32", func(b *testing.B) {
		for b.Loop() {
			for g := uint32(0); g < 256; g += 32 {
				start := page.PageID{TableID: 1, SegmentNo: 0, PageNo: g}
				_, err := hf.ReadPageAhead(start, 32)
				if err != nil {
					b.Fatal(err)
				}
			}
		}
	})
}
