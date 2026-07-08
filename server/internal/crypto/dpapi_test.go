package crypto

import (
	"context"
	"runtime"
	"testing"
)

func TestDPAPISource_Name(t *testing.T) {
	s := NewDPAPISource(t.TempDir() + "/dpapi.blob")
	if got := s.Name(); got != "dpapi" {
		t.Errorf("Name() = %q, want %q", got, "dpapi")
	}
}

func TestDPAPISource_GetKEK_UnsupportedPlatform(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("DPAPI is available on Windows; skip stub test")
	}
	s := NewDPAPISource(t.TempDir() + "/dpapi.blob")
	_, err := s.GetKEK(context.Background())
	if err == nil {
		t.Fatal("expected error on non-Windows platform")
	}
	if got := err.Error(); got != "DPAPI not supported on this platform" {
		t.Errorf("error = %q, want %q", got, "DPAPI not supported on this platform")
	}
}
