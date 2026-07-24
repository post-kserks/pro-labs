package toast

import (
	"bytes"
	"testing"
)

func TestTOASTLargeAttributes(t *testing.T) {
	// Generate a large payload > ToastThreshold
	payload := make([]byte, 5000)
	for i := range payload {
		payload[i] = byte(i % 256)
	}

	chunks, totalSize, err := ChunkToastedValue(payload)
	if err != nil {
		t.Fatalf("ChunkToastedValue failed: %v", err)
	}

	if len(chunks) == 0 {
		t.Fatalf("expected chunks, got 0")
	}

	assembled, err := AssembleToastedValue(chunks)
	if err != nil {
		t.Fatalf("AssembleToastedValue failed: %v", err)
	}

	if !bytes.Equal(payload, assembled) {
		t.Fatalf("assembled payload does not match original")
	}

	pointer := &ToastPointer{
		ChunkID:          123,
		TotalSize:        totalSize,
		ChunkCount:       uint32(len(chunks)),
		UncompressedSize: uint32(len(payload)),
	}

	encoded := pointer.Encode()
	decoded, err := DecodeToastPointer(encoded)
	if err != nil {
		t.Fatalf("DecodeToastPointer failed: %v", err)
	}

	if decoded.ChunkID != pointer.ChunkID || decoded.TotalSize != pointer.TotalSize || decoded.ChunkCount != pointer.ChunkCount || decoded.UncompressedSize != pointer.UncompressedSize {
		t.Fatalf("decoded pointer does not match original")
	}
}
