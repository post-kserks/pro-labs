package page

// ItemPointerSize is the on-page size of one ItemPointer.
const ItemPointerSize = 4

// MaxTupleLength is the largest tuple length representable in the 14-bit
// length field of an ItemPointer. Tuples this large never fit on a page
// anyway (PageSize - PageHeaderSize - ItemPointerSize is the real limit).
const MaxTupleLength = 1<<14 - 1

// ItemPointer locates a tuple inside a page.
// Packed into one uint32: 15-bit offset | 14-bit length | 3-bit flags.
type ItemPointer uint32

// Item pointer flags.
const (
	ItemFlagNormal   uint8 = 0 // ordinary tuple
	ItemFlagDead     uint8 = 1 // deleted, space reusable after Compact
	ItemFlagRedirect uint8 = 2 // moved (after HOT update)
)

// NewItemPointer packs offset, length and flags into an ItemPointer.
func NewItemPointer(offset, length uint16, flags uint8) ItemPointer {
	return ItemPointer(uint32(offset)<<17 | uint32(length&0x3FFF)<<3 | uint32(flags&0x7))
}

// Offset returns the byte offset of the tuple inside the page.
func (ip ItemPointer) Offset() uint16 { return uint16(ip >> 17) }

// Length returns the tuple length in bytes.
func (ip ItemPointer) Length() uint16 { return uint16((ip >> 3) & 0x3FFF) }

// Flags returns the item flags.
func (ip ItemPointer) Flags() uint8 { return uint8(ip & 0x7) }
