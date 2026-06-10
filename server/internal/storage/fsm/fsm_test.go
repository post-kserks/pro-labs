package fsm

import (
	"os"
	"path/filepath"
	"sync"
	"testing"
)

func TestSearchEmpty(t *testing.T) {
	f := New("", 0)
	if _, ok := f.Search(1); ok {
		t.Error("Search on empty FSM found a page")
	}

	f = New("", 10)
	if _, ok := f.Search(100); ok {
		t.Error("Search found a page when all pages have zero free space")
	}
}

func TestUpdateAndSearch(t *testing.T) {
	f := New("", 100)
	f.Update(7, 4096)
	f.Update(42, 256)

	pageNo, ok := f.Search(2000)
	if !ok || pageNo != 7 {
		t.Errorf("Search(2000) = (%d, %v), want (7, true)", pageNo, ok)
	}

	pageNo, ok = f.Search(200)
	if !ok {
		t.Fatal("Search(200) found nothing")
	}
	if pageNo != 7 && pageNo != 42 {
		t.Errorf("Search(200) = %d, want 7 or 42", pageNo)
	}

	if _, ok := f.Search(8000); ok {
		t.Error("Search(8000) found a page, none qualifies")
	}
}

func TestSearchGuaranteesSpace(t *testing.T) {
	// 100 free bytes are recorded as category 3 (>= 96 guaranteed).
	f := New("", 4)
	f.Update(2, 100)

	if pageNo, ok := f.Search(96); !ok || pageNo != 2 {
		t.Errorf("Search(96) = (%d, %v), want (2, true)", pageNo, ok)
	}
	// A 97-byte request rounds up to category 4 and conservatively misses.
	if _, ok := f.Search(97); ok {
		t.Error("Search(97) returned a page whose guarantee is only 96 bytes")
	}
}

func TestUpdateShrinksAvailability(t *testing.T) {
	f := New("", 8)
	f.Update(3, 8000)
	f.Update(3, 0) // page filled up

	if _, ok := f.Search(64); ok {
		t.Error("Search found page 3 after its free space dropped to 0")
	}
}

func TestGrowAndAutoGrow(t *testing.T) {
	f := New("", 2)
	f.Grow(50)
	if f.PageCount() != 50 {
		t.Errorf("PageCount = %d, want 50", f.PageCount())
	}

	// Update beyond the tracked range grows the map automatically.
	f.Update(200, 4096)
	if f.PageCount() != 201 {
		t.Errorf("PageCount = %d, want 201", f.PageCount())
	}
	if pageNo, ok := f.Search(4000); !ok || pageNo != 200 {
		t.Errorf("Search(4000) = (%d, %v), want (200, true)", pageNo, ok)
	}
}

func TestSaveLoad(t *testing.T) {
	path := filepath.Join(t.TempDir(), "fsm.map")
	f := New(path, 30)
	f.Update(5, 1024)
	f.Update(29, 7168)
	if err := f.Save(); err != nil {
		t.Fatal(err)
	}

	loaded, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if loaded.PageCount() != 30 {
		t.Errorf("PageCount = %d, want 30", loaded.PageCount())
	}
	if pageNo, ok := loaded.Search(7000); !ok || pageNo != 29 {
		t.Errorf("Search(7000) = (%d, %v), want (29, true)", pageNo, ok)
	}
	if pageNo, ok := loaded.Search(1000); !ok || (pageNo != 5 && pageNo != 29) {
		t.Errorf("Search(1000) = (%d, %v), want page 5 or 29", pageNo, ok)
	}
}

func TestLoadRejectsGarbage(t *testing.T) {
	path := filepath.Join(t.TempDir(), "bad.map")
	if err := os.WriteFile(path, []byte("not an fsm file"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := Load(path); err == nil {
		t.Error("Load accepted a garbage file")
	}
}

func TestConcurrentAccess(t *testing.T) {
	f := New("", 1024)
	var wg sync.WaitGroup
	for g := 0; g < 16; g++ {
		wg.Add(1)
		go func(g int) {
			defer wg.Done()
			for i := 0; i < 1000; i++ {
				f.Update(uint32((g*1000+i)%1024), uint16(i%8192))
				f.Search(uint16(i % 4096))
			}
		}(g)
	}
	wg.Wait()
}
