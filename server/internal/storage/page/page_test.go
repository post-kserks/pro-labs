package page

import (
	"bytes"
	"fmt"
	"math/rand"
	"sort"
	"testing"
)

func TestPageInit(t *testing.T) {
	p := &Page{}
	p.Init(PageTypeHeap)

	h := p.Header()
	if h.Lower != PageHeaderSize {
		t.Errorf("Lower = %d, want %d", h.Lower, PageHeaderSize)
	}
	if h.Upper != PageSize {
		t.Errorf("Upper = %d, want %d", h.Upper, PageSize)
	}
	if h.Special != PageSize {
		t.Errorf("Special = %d, want %d", h.Special, PageSize)
	}
	if h.PageType != PageTypeHeap {
		t.Errorf("PageType = %d, want %d", h.PageType, PageTypeHeap)
	}
	if h.NItems != 0 {
		t.Errorf("NItems = %d, want 0", h.NItems)
	}
	if got := p.FreeSpace(); got != PageSize-PageHeaderSize {
		t.Errorf("FreeSpace = %d, want %d", got, PageSize-PageHeaderSize)
	}
}

func TestInsertAndGetTuple(t *testing.T) {
	p := &Page{}
	p.Init(PageTypeHeap)

	tuples := [][]byte{
		[]byte("hello"),
		[]byte("a much longer tuple with some content"),
		{0x00, 0xFF, 0x42},
	}

	var slots []uint16
	for _, data := range tuples {
		slot, err := p.InsertTuple(data)
		if err != nil {
			t.Fatalf("InsertTuple: %v", err)
		}
		slots = append(slots, slot)
	}

	for i, slot := range slots {
		got := p.GetTuple(slot)
		if !bytes.Equal(got, tuples[i]) {
			t.Errorf("slot %d: got %q, want %q", slot, got, tuples[i])
		}
	}

	if got := p.GetTuple(99); got != nil {
		t.Errorf("GetTuple(out of range) = %v, want nil", got)
	}
}

func TestInsertUntilFull(t *testing.T) {
	p := &Page{}
	p.Init(PageTypeHeap)

	data := make([]byte, 100)
	count := 0
	for {
		_, err := p.InsertTuple(data)
		if err == ErrPageFull {
			break
		}
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		count++
		if count > PageSize {
			t.Fatal("never returned ErrPageFull")
		}
	}

	// Each tuple costs 100 bytes + 4-byte item pointer.
	want := (PageSize - PageHeaderSize) / (100 + ItemPointerSize)
	if count != want {
		t.Errorf("inserted %d tuples, want %d", count, want)
	}
	if p.FreeSpace() >= 100+ItemPointerSize {
		t.Errorf("page reported full but FreeSpace = %d", p.FreeSpace())
	}
}

func TestMarkDead(t *testing.T) {
	p := &Page{}
	p.Init(PageTypeHeap)

	slot, _ := p.InsertTuple([]byte("doomed"))
	keep, _ := p.InsertTuple([]byte("kept"))

	p.MarkDead(slot)

	if got := p.GetTuple(slot); got != nil {
		t.Errorf("GetTuple(dead slot) = %q, want nil", got)
	}
	if got := p.GetTuple(keep); !bytes.Equal(got, []byte("kept")) {
		t.Errorf("live tuple damaged: got %q", got)
	}
	// MarkDead on an out-of-range slot must be a no-op, not a panic.
	p.MarkDead(1000)
}

func TestCompact(t *testing.T) {
	p := &Page{}
	p.Init(PageTypeHeap)
	p.SetLSN(777)

	var live [][]byte
	for i := 0; i < 20; i++ {
		data := []byte(fmt.Sprintf("tuple-%02d", i))
		slot, err := p.InsertTuple(data)
		if err != nil {
			t.Fatal(err)
		}
		if i%2 == 0 {
			p.MarkDead(slot)
		} else {
			live = append(live, data)
		}
	}

	freeBefore := p.FreeSpace()
	p.Compact()

	if p.FreeSpace() <= freeBefore {
		t.Errorf("FreeSpace after Compact = %d, want > %d", p.FreeSpace(), freeBefore)
	}
	h := p.Header()
	if int(h.NItems) != len(live) {
		t.Errorf("NItems = %d, want %d", h.NItems, len(live))
	}
	if p.LSN() != 777 {
		t.Errorf("LSN not preserved: got %d", p.LSN())
	}
	for i, want := range live {
		if got := p.GetTuple(uint16(i)); !bytes.Equal(got, want) {
			t.Errorf("slot %d after Compact: got %q, want %q", i, got, want)
		}
	}
}

func TestChecksum(t *testing.T) {
	p := &Page{}
	p.Init(PageTypeHeap)
	p.InsertTuple([]byte("some data"))

	p.SetChecksum()
	if !p.VerifyChecksum() {
		t.Fatal("fresh checksum does not verify")
	}

	p[4000] ^= 0x01 // flip a bit in the body
	if p.VerifyChecksum() {
		t.Fatal("corruption not detected")
	}
}

func TestItemPointerPacking(t *testing.T) {
	cases := []struct {
		offset, length uint16
		flags          uint8
	}{
		{0, 0, ItemFlagNormal},
		{PageSize - 1, 100, ItemFlagDead},
		{28, MaxTupleLength, ItemFlagRedirect},
		{12345, 7777, ItemFlagNormal},
	}
	for _, c := range cases {
		ip := NewItemPointer(c.offset, c.length, c.flags)
		if ip.Offset() != c.offset || ip.Length() != c.length || ip.Flags() != c.flags {
			t.Errorf("roundtrip(%d,%d,%d) = (%d,%d,%d)",
				c.offset, c.length, c.flags, ip.Offset(), ip.Length(), ip.Flags())
		}
	}
}

// TestRandomOperations runs 10 000 random insert/delete/compact operations
// against a model and checks the page never loses or corrupts live tuples.
func TestRandomOperations(t *testing.T) {
	rng := rand.New(rand.NewSource(42))
	p := &Page{}
	p.Init(PageTypeHeap)

	live := map[uint16][]byte{} // slot -> expected payload

	verify := func(step int) {
		t.Helper()
		for slot, want := range live {
			got := p.GetTuple(slot)
			if !bytes.Equal(got, want) {
				t.Fatalf("step %d: slot %d corrupted: got %q, want %q", step, slot, got, want)
			}
		}
	}

	compact := func() {
		slots := make([]int, 0, len(live))
		for s := range live {
			slots = append(slots, int(s))
		}
		sort.Ints(slots)

		p.Compact()

		newLive := map[uint16][]byte{}
		for newSlot, oldSlot := range slots {
			newLive[uint16(newSlot)] = live[uint16(oldSlot)]
		}
		live = newLive
	}

	for step := 0; step < 10000; step++ {
		switch op := rng.Intn(10); {
		case op < 6: // insert
			data := make([]byte, 1+rng.Intn(300))
			rng.Read(data)
			slot, err := p.InsertTuple(data)
			if err == ErrPageFull {
				compact()
				slot, err = p.InsertTuple(data)
				if err == ErrPageFull {
					// genuinely full of live tuples: delete one instead
					for s := range live {
						p.MarkDead(s)
						delete(live, s)
						break
					}
					continue
				}
			}
			if err != nil {
				t.Fatalf("step %d: %v", step, err)
			}
			live[slot] = append([]byte(nil), data...)

		case op < 9: // delete a random live tuple
			for s := range live {
				p.MarkDead(s)
				delete(live, s)
				break
			}

		default: // compact
			compact()
		}

		if step%500 == 0 {
			verify(step)
		}
	}
	verify(10000)
}

func TestTupleHeaderRoundtrip(t *testing.T) {
	h := TupleHeader{
		XMin:        12345678901234,
		XMax:        99,
		CMin:        7,
		InfoMask:    InfoXMinCommitted | InfoHasNulls,
		NAttributes: 12,
	}
	buf := make([]byte, TupleHeaderSize)
	h.Serialize(buf)
	got := ParseTupleHeader(buf)
	if got != h {
		t.Errorf("roundtrip = %+v, want %+v", got, h)
	}
}

func TestNullBitmap(t *testing.T) {
	if NullBitmapSize(0) != 0 || NullBitmapSize(8) != 1 || NullBitmapSize(9) != 2 {
		t.Errorf("NullBitmapSize wrong: %d %d %d",
			NullBitmapSize(0), NullBitmapSize(8), NullBitmapSize(9))
	}

	bm := make([]byte, NullBitmapSize(10))
	SetNotNull(bm, 0)
	SetNotNull(bm, 9)
	for i := 0; i < 10; i++ {
		wantNull := i != 0 && i != 9
		if IsNull(bm, i) != wantNull {
			t.Errorf("attr %d: IsNull = %v, want %v", i, IsNull(bm, i), wantNull)
		}
	}
}
