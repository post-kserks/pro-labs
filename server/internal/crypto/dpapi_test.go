//go:build windows && cgo

package crypto

import (
	"bytes"
	"testing"
)

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
