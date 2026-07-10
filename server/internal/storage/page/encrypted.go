package page

import (
	"encoding/binary"
	"fmt"
)

const (
	// EncryptedPageHeaderSize is the size of the unencrypted header
	// that precedes the encrypted page data.
	// Magic (4) + KeyVersion (4) + Nonce (12) = 20 bytes
	EncryptedPageHeaderSize = 20

	// GCMTagSize is the size of the GCM authentication tag
	// appended to the encrypted data.
	GCMTagSize = 16

	// EncryptedPageMagic marks an encrypted page
	EncryptedPageMagic = "VDBE"
)

// EncryptedPageHeader is the unencrypted prefix of an encrypted page.
// It contains metadata needed to decrypt the page.
type EncryptedPageHeader struct {
	Magic      [4]byte  // "VDBE" marker
	KeyVersion uint32   // DEK version for key rotation
	Nonce      [12]byte // GCM nonce, unique per page write
}

// ParseEncryptedHeader parses the unencrypted header from raw page bytes.
func ParseEncryptedHeader(raw []byte) (*EncryptedPageHeader, error) {
	if len(raw) < EncryptedPageHeaderSize {
		return nil, fmt.Errorf("page too small for encrypted header")
	}
	if string(raw[0:4]) != EncryptedPageMagic {
		return nil, fmt.Errorf("not an encrypted page: expected magic %s", EncryptedPageMagic)
	}
	return &EncryptedPageHeader{
		Magic:      [4]byte(raw[0:4]),
		KeyVersion: binary.LittleEndian.Uint32(raw[4:8]),
		Nonce:      [12]byte(raw[8:20]),
	}, nil
}

// WriteEncryptedHeader writes the header to a buffer.
func WriteEncryptedHeader(buf []byte, hdr *EncryptedPageHeader) {
	copy(buf[0:4], hdr.Magic[:])
	binary.LittleEndian.PutUint32(buf[4:8], hdr.KeyVersion)
	copy(buf[8:20], hdr.Nonce[:])
}

// IsEncryptedPage checks if raw page bytes start with the encrypted magic.
func IsEncryptedPage(raw []byte) bool {
	return len(raw) >= 4 && string(raw[0:4]) == EncryptedPageMagic
}
