package page

import (
	"bytes"
	"testing"
)

func TestParseEncryptedHeader(t *testing.T) {
	raw := make([]byte, EncryptedPageHeaderSize+64)
	copy(raw[0:4], EncryptedPageMagic)
	raw[4] = 0x01
	raw[5] = 0x02
	raw[6] = 0x03
	raw[7] = 0x04
	for i := 8; i < 20; i++ {
		raw[i] = byte(i)
	}

	hdr, err := ParseEncryptedHeader(raw)
	if err != nil {
		t.Fatalf("ParseEncryptedHeader: %v", err)
	}
	if string(hdr.Magic[:]) != EncryptedPageMagic {
		t.Errorf("Magic = %q, want %q", hdr.Magic[:], EncryptedPageMagic)
	}
	if hdr.KeyVersion != 0x04030201 {
		t.Errorf("KeyVersion = %d, want %d", hdr.KeyVersion, 0x04030201)
	}
	for i := 0; i < 12; i++ {
		if hdr.Nonce[i] != byte(i+8) {
			t.Errorf("Nonce[%d] = %d, want %d", i, hdr.Nonce[i], i+8)
		}
	}
}

func TestWriteEncryptedHeader(t *testing.T) {
	hdr := &EncryptedPageHeader{
		Magic:      [4]byte{'V', 'D', 'B', 'E'},
		KeyVersion: 42,
		Nonce:      [12]byte{1, 2, 3, 4, 5, 6, 7, 8, 9, 10, 11, 12},
	}

	buf := make([]byte, EncryptedPageHeaderSize)
	WriteEncryptedHeader(buf, hdr)

	parsed, err := ParseEncryptedHeader(buf)
	if err != nil {
		t.Fatalf("ParseEncryptedHeader: %v", err)
	}
	if *parsed != *hdr {
		t.Errorf("roundtrip mismatch: got %+v, want %+v", parsed, hdr)
	}
}

func TestWriteEncryptedHeaderRoundtrip(t *testing.T) {
	hdr := &EncryptedPageHeader{
		Magic:      [4]byte{'V', 'D', 'B', 'E'},
		KeyVersion: 100,
		Nonce:      [12]byte{0xAA, 0xBB, 0xCC, 0xDD, 0xEE, 0xFF, 0x11, 0x22, 0x33, 0x44, 0x55, 0x66},
	}

	buf := make([]byte, EncryptedPageHeaderSize)
	WriteEncryptedHeader(buf, hdr)

	got, err := ParseEncryptedHeader(buf)
	if err != nil {
		t.Fatalf("ParseEncryptedHeader: %v", err)
	}
	if got.KeyVersion != hdr.KeyVersion {
		t.Errorf("KeyVersion = %d, want %d", got.KeyVersion, hdr.KeyVersion)
	}
	if !bytes.Equal(got.Nonce[:], hdr.Nonce[:]) {
		t.Errorf("Nonce mismatch")
	}
}

func TestIsEncryptedPage(t *testing.T) {
	tests := []struct {
		name string
		raw  []byte
		want bool
	}{
		{"valid", []byte("VDBExxxx"), true},
		{"too short", []byte("VD"), false},
		{"empty", nil, false},
		{"wrong magic", []byte("NOTEXXXX"), false},
		{"plain page", make([]byte, PageSize), false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := IsEncryptedPage(tt.raw); got != tt.want {
				t.Errorf("IsEncryptedPage(%v) = %v, want %v", tt.raw, got, tt.want)
			}
		})
	}
}

func TestParseEncryptedHeaderTooSmall(t *testing.T) {
	raw := make([]byte, EncryptedPageHeaderSize-1)
	_, err := ParseEncryptedHeader(raw)
	if err == nil {
		t.Fatal("expected error for too-small buffer")
	}
}

func TestParseEncryptedHeaderWrongMagic(t *testing.T) {
	raw := make([]byte, EncryptedPageHeaderSize+64)
	copy(raw[0:4], "XXXX")
	_, err := ParseEncryptedHeader(raw)
	if err == nil {
		t.Fatal("expected error for wrong magic")
	}
}
