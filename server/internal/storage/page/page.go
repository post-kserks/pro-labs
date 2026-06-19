// Package page implements the fixed-size 8KB slotted page format that is
// the foundation of the new page-based storage engine (TZ phase 1).
//
// Page layout:
//
//	[PageHeader 28b][ItemPointer × NItems → grows down]
//	             ...free space...
//	[tuples ← grow up from Upper][Special area (indexes only)]
package page

import (
	"encoding/binary"
	"errors"
	"fmt"
	"hash/crc32"
	"math"
)

const (
	// PageSize is the fixed size of one page: 8 KB (PostgreSQL standard).
	PageSize = 8192
	// MaxSegmentSize is the maximum size of one segment file: 1 GB.
	MaxSegmentSize = 1 << 30
	// PagesPerSegment is the number of pages in a full segment (131072).
	PagesPerSegment = MaxSegmentSize / PageSize
)

// Page types stored in PageHeader.PageType.
const (
	PageTypeHeap    uint8 = 1 // heap (table data) page
	PageTypeBTreeIn uint8 = 2 // B-Tree internal node
	PageTypeBTreeLf uint8 = 3 // B-Tree leaf node
	PageTypeFSM     uint8 = 4 // Free Space Map page
	PageTypeWAL     uint8 = 5 // WAL page
)

// PageHeaderSize is the size of the serialized PageHeader.
const PageHeaderSize = 28

// PageHeader flag bits.
const (
	PageFlagHasMVCC  uint16 = 1 << 0 // page contains MVCC-versioned tuples
	PageFlagHasSpace uint16 = 1 << 1 // page has free space (FSM hint)
)

// ErrPageFull is returned by InsertTuple when the tuple does not fit.
var ErrPageFull = errors.New("page is full")

// PageID uniquely identifies a page in the database.
type PageID struct {
	TableID   uint32 // table ID
	SegmentNo uint16 // segment file number (0000.heap, 0001.heap, ...)
	PageNo    uint32 // page number inside the segment
}

// FileOffset returns the byte offset of the page inside its segment file.
func (p PageID) FileOffset() int64 {
	return int64(p.PageNo) * PageSize
}

// PageHeader is the fixed 28-byte header at the start of every page.
type PageHeader struct {
	LSN       uint64 // LSN of the last WAL record that modified this page
	Checksum  uint32 // CRC32 of the whole page except this field
	Flags     uint16
	Lower     uint16 // end of the item-pointer zone (start of free space)
	Upper     uint16 // start of the tuple zone (end of free space)
	Special   uint16 // start of the special area (index pages only)
	PageType  uint8
	NItems    uint16 // number of item pointers (not the number of live tuples!)
	FreeSpace uint16 // cached Upper - Lower
}

// Serialized header layout (little-endian):
//
//	[0:8]   LSN
//	[8:12]  Checksum
//	[12:14] Flags
//	[14:16] Lower
//	[16:18] Upper
//	[18:20] Special
//	[20]    PageType
//	[21:23] NItems
//	[23:25] FreeSpace
//	[25:28] reserved
const (
	offLSN       = 0
	offChecksum  = 8
	offFlags     = 12
	offLower     = 14
	offUpper     = 16
	offSpecial   = 18
	offPageType  = 20
	offNItems    = 21
	offFreeSpace = 23
)

// Page is an 8KB buffer with slotted-page accessors.
type Page [PageSize]byte

// Init initializes an empty page of the given type.
func (p *Page) Init(pageType uint8) {
	*p = Page{}
	p.writeHeader(PageHeader{
		Lower:     PageHeaderSize,
		Upper:     PageSize,
		Special:   PageSize,
		PageType:  pageType,
		FreeSpace: PageSize - PageHeaderSize,
	})
}

// Header reads the PageHeader from the start of the page.
func (p *Page) Header() PageHeader {
	return PageHeader{
		LSN:       binary.LittleEndian.Uint64(p[offLSN:]),
		Checksum:  binary.LittleEndian.Uint32(p[offChecksum:]),
		Flags:     binary.LittleEndian.Uint16(p[offFlags:]),
		Lower:     binary.LittleEndian.Uint16(p[offLower:]),
		Upper:     binary.LittleEndian.Uint16(p[offUpper:]),
		Special:   binary.LittleEndian.Uint16(p[offSpecial:]),
		PageType:  p[offPageType],
		NItems:    binary.LittleEndian.Uint16(p[offNItems:]),
		FreeSpace: binary.LittleEndian.Uint16(p[offFreeSpace:]),
	}
}

func (p *Page) writeHeader(h PageHeader) {
	binary.LittleEndian.PutUint64(p[offLSN:], h.LSN)
	binary.LittleEndian.PutUint32(p[offChecksum:], h.Checksum)
	binary.LittleEndian.PutUint16(p[offFlags:], h.Flags)
	binary.LittleEndian.PutUint16(p[offLower:], h.Lower)
	binary.LittleEndian.PutUint16(p[offUpper:], h.Upper)
	binary.LittleEndian.PutUint16(p[offSpecial:], h.Special)
	p[offPageType] = h.PageType
	binary.LittleEndian.PutUint16(p[offNItems:], h.NItems)
	binary.LittleEndian.PutUint16(p[offFreeSpace:], h.FreeSpace)
}

// LSN returns the page LSN.
func (p *Page) LSN() uint64 {
	return binary.LittleEndian.Uint64(p[offLSN:])
}

// SetLSN updates the page LSN in place.
func (p *Page) SetLSN(lsn uint64) {
	binary.LittleEndian.PutUint64(p[offLSN:], lsn)
}

// FreeSpace returns the number of free bytes between Lower and Upper.
func (p *Page) FreeSpace() uint16 {
	h := p.Header()
	if h.Upper <= h.Lower {
		return 0
	}
	return h.Upper - h.Lower
}

// InsertTuple places a tuple on the page and returns its slot number.
// Returns ErrPageFull if there is not enough room for the tuple plus its
// 4-byte item pointer.
func (p *Page) InsertTuple(data []byte) (uint16, error) {
	if len(data) > MaxTupleLength {
		return 0, ErrPageFull
	}
	needed := uint16(len(data)) + ItemPointerSize
	if p.FreeSpace() < needed {
		return 0, ErrPageFull
	}

	h := p.Header()

	// Tuples grow from the end of the page towards the header.
	tupleOffset := h.Upper - uint16(len(data))
	if tupleOffset > h.Upper {
		return 0, fmt.Errorf("page corruption: tuple offset underflow")
	}
	copy(p[tupleOffset:], data)

	ip := NewItemPointer(tupleOffset, uint16(len(data)), ItemFlagNormal)
	binary.LittleEndian.PutUint32(p[h.Lower:], uint32(ip))

	h.Upper = tupleOffset
	h.Lower += ItemPointerSize
	if h.NItems == math.MaxUint16 {
		return 0, fmt.Errorf("page full: too many items")
	}
	h.NItems++
	h.FreeSpace = h.Upper - h.Lower
	if h.Upper < h.Lower {
		return 0, fmt.Errorf("page corruption: upper < lower")
	}
	p.writeHeader(h)

	return h.NItems - 1, nil
}

// GetTuple returns the tuple stored in the given slot, or nil if the slot
// does not exist or is marked dead. The returned slice aliases the page.
func (p *Page) GetTuple(slot uint16) []byte {
	h := p.Header()
	if slot >= h.NItems {
		return nil
	}
	ip := p.itemPointer(slot)
	if ip.Flags() == ItemFlagDead {
		return nil
	}
	return p[ip.Offset() : ip.Offset()+ip.Length()]
}

// MarkDead marks the slot as deleted; its space is reclaimed by Compact.
func (p *Page) MarkDead(slot uint16) {
	h := p.Header()
	if slot >= h.NItems {
		return
	}
	ip := p.itemPointer(slot)
	newIP := NewItemPointer(ip.Offset(), ip.Length(), ItemFlagDead)
	binary.LittleEndian.PutUint32(p[itemPointerOffset(slot):], uint32(newIP))
}

// Compact defragments the page: live tuples are rewritten densely and dead
// item pointers are removed. Slot numbers of surviving tuples change (they
// are renumbered in ascending old-slot order). LSN and Flags are preserved.
func (p *Page) Compact() {
	h := p.Header()

	tmp := &Page{}
	tmp.Init(h.PageType)
	for slot := uint16(0); slot < h.NItems; slot++ {
		data := p.GetTuple(slot)
		if data == nil {
			continue
		}
		// Compact copies live tuples into a fresh page of the same size;
		// InsertTuple cannot fail here because all tuples already fit.
		_, _ = tmp.InsertTuple(data)
	}

	tmp.SetLSN(h.LSN)
	th := tmp.Header()
	th.Flags = h.Flags
	tmp.writeHeader(th)

	*p = *tmp
}

// ComputeChecksum returns the CRC32 of the page with the checksum field
// treated as zero. It does not modify the page.
func (p *Page) ComputeChecksum() uint32 {
	saved := binary.LittleEndian.Uint32(p[offChecksum:])
	binary.LittleEndian.PutUint32(p[offChecksum:], 0)
	crc := crc32.ChecksumIEEE(p[:])
	binary.LittleEndian.PutUint32(p[offChecksum:], saved)
	return crc
}

// SetChecksum computes and stores the checksum. Must be the last mutation
// before the page is written to disk.
func (p *Page) SetChecksum() {
	crc := p.ComputeChecksum()
	binary.LittleEndian.PutUint32(p[offChecksum:], crc)
}

// VerifyChecksum reports whether the stored checksum matches the content.
func (p *Page) VerifyChecksum() bool {
	stored := binary.LittleEndian.Uint32(p[offChecksum:])
	return stored == p.ComputeChecksum()
}

func itemPointerOffset(slot uint16) uint16 {
	return PageHeaderSize + slot*ItemPointerSize
}

func (p *Page) itemPointer(slot uint16) ItemPointer {
	return ItemPointer(binary.LittleEndian.Uint32(p[itemPointerOffset(slot):]))
}

func (p *Page) Clone() *Page {
	cp := *p
	return &cp
}
