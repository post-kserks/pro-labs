//go:build windows && cgo

package crypto

import (
	"bytes"
	"context"
	"os"
	"testing"
)

func TestDPAPISource_GetKEK_GenerateAndLoad(t *testing.T) {
	dir := t.TempDir()
	blobPath := dir + "/test.dpapi"

	// First call: generates key, protects it, stores blob
	src := NewDPAPISource(blobPath)
	kek1, err := src.GetKEK(context.Background())
	if err != nil {
		t.Fatalf("GetKEK (first call): %v", err)
	}
	if len(kek1) != 32 {
		t.Fatalf("expected 32-byte key, got %d", len(kek1))
	}

	// Verify blob was written
	if _, err := os.Stat(blobPath); os.IsNotExist(err) {
		t.Fatal("expected blob file to be created")
	}

	// Second call: loads blob, unprotects, returns same key
	src2 := NewDPAPISource(blobPath)
	kek2, err := src2.GetKEK(context.Background())
	if err != nil {
		t.Fatalf("GetKEK (second call): %v", err)
	}
	if !bytes.Equal(kek1, kek2) {
		t.Fatal("expected same key from second GetKEK call")
	}
}

func TestDPAPISource_GetKEK_BlobExists(t *testing.T) {
	dir := t.TempDir()
	blobPath := dir + "/test.dpapi"

	// Create key via first source
	src1 := NewDPAPISource(blobPath)
	kek1, err := src1.GetKEK(context.Background())
	if err != nil {
		t.Fatalf("GetKEK: %v", err)
	}

	// New source instance reads the same blob
	src2 := NewDPAPISource(blobPath)
	kek2, err := src2.GetKEK(context.Background())
	if err != nil {
		t.Fatalf("GetKEK from existing blob: %v", err)
	}
	if !bytes.Equal(kek1, kek2) {
		t.Fatal("expected consistent key across instances")
	}
}

func TestDPAPISource_Name(t *testing.T) {
	s := NewDPAPISource(t.TempDir() + "/dpapi.blob")
	if got := s.Name(); got != "dpapi" {
		t.Errorf("Name() = %q, want %q", got, "dpapi")
	}
}

func TestDPAPI_ProtectUnprotect(t *testing.T) {
	data := []byte("hello, dpapi!")

	protected, err := protect(data)
	if err != nil {
		t.Fatalf("protect() error: %v", err)
	}
	if len(protected) == 0 {
		t.Fatal("protect() returned empty data")
	}
	if bytes.Equal(protected, data) {
		t.Fatal("protect() returned same data (should be encrypted)")
	}

	unprotected, err := unprotect(protected)
	if err != nil {
		t.Fatalf("unprotect() error: %v", err)
	}
	if !bytes.Equal(unprotected, data) {
		t.Errorf("unprotect() = %x, want %x", unprotected, data)
	}
}

func TestDPAPI_ProtectNil(t *testing.T) {
	_, err := protect(nil)
	if err == nil {
		t.Fatal("protect(nil) should return error")
	}
}

func TestDPAPI_UnprotectNil(t *testing.T) {
	_, err := unprotect(nil)
	if err == nil {
		t.Fatal("unprotect(nil) should return error")
	}
}

func TestDPAPI_ProtectEmpty(t *testing.T) {
	_, err := protect([]byte{})
	if err == nil {
		t.Fatal("protect([]byte{}) should return error")
	}
}

func TestDPAPI_UnprotectEmpty(t *testing.T) {
	_, err := unprotect([]byte{})
	if err == nil {
		t.Fatal("unprotect([]byte{}) should return error")
	}
}
