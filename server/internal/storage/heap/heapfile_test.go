package heap

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"vaultdb/internal/storage/page"
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
