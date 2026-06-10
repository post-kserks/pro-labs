package page

import "encoding/binary"

// TupleHeaderSize is the serialized size of a TupleHeader.
// XMin(8) + XMax(8) + CMin(4) + InfoMask(2) + NAttributes(2) = 24 bytes.
const TupleHeaderSize = 24

// TupleHeader InfoMask bits.
const (
	InfoXMinCommitted uint16 = 1 << 0 // XMin transaction committed
	InfoXMinAborted   uint16 = 1 << 1 // XMin transaction aborted
	InfoXMaxCommitted uint16 = 1 << 2 // XMax transaction committed
	InfoXMaxAborted   uint16 = 1 << 3 // XMax transaction aborted
	InfoUpdated       uint16 = 1 << 4 // tuple has a newer version
	InfoHasNulls      uint16 = 1 << 5 // NULL bitmap follows the header
)

// TupleHeader precedes every tuple's attribute data. The MVCC fields are
// filled by INSERT/UPDATE/DELETE (TZ phase 4); phase 1 only defines the
// binary format.
type TupleHeader struct {
	XMin uint64 // TransactionID that created this tuple
	XMax uint64 // TransactionID that deleted this tuple (0 = live)

	// CMin orders operations inside one transaction, so a transaction can
	// see its own earlier changes but not later ones.
	CMin uint32

	InfoMask    uint16
	NAttributes uint16 // attribute count, needed to size the NULL bitmap
}

// Serialize writes the header into buf, which must hold TupleHeaderSize bytes.
func (h *TupleHeader) Serialize(buf []byte) {
	binary.LittleEndian.PutUint64(buf[0:], h.XMin)
	binary.LittleEndian.PutUint64(buf[8:], h.XMax)
	binary.LittleEndian.PutUint32(buf[16:], h.CMin)
	binary.LittleEndian.PutUint16(buf[20:], h.InfoMask)
	binary.LittleEndian.PutUint16(buf[22:], h.NAttributes)
}

// ParseTupleHeader reads a TupleHeader from the start of buf.
func ParseTupleHeader(buf []byte) TupleHeader {
	return TupleHeader{
		XMin:        binary.LittleEndian.Uint64(buf[0:]),
		XMax:        binary.LittleEndian.Uint64(buf[8:]),
		CMin:        binary.LittleEndian.Uint32(buf[16:]),
		InfoMask:    binary.LittleEndian.Uint16(buf[20:]),
		NAttributes: binary.LittleEndian.Uint16(buf[22:]),
	}
}

// NullBitmapSize returns the size in bytes of the NULL bitmap for nAttrs
// attributes: ceil(nAttrs / 8).
func NullBitmapSize(nAttrs uint16) int {
	return (int(nAttrs) + 7) / 8
}

// IsNull reports whether attribute i is NULL according to the bitmap that
// immediately follows the TupleHeader (bit i == 0 means NULL).
func IsNull(bitmap []byte, i int) bool {
	return bitmap[i/8]&(1<<(i%8)) == 0
}

// SetNotNull marks attribute i as not NULL in the bitmap.
func SetNotNull(bitmap []byte, i int) {
	bitmap[i/8] |= 1 << (i % 8)
}
