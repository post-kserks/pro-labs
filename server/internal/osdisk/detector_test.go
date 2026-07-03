package osdisk

import (
	"runtime"
	"testing"
)

func TestDetectDiskEncryption(t *testing.T) {
	status, err := DetectDiskEncryption("/tmp")
	if err != nil {
		t.Fatalf("DetectDiskEncryption returned error: %v", err)
	}
	if status == nil {
		t.Fatal("DetectDiskEncryption returned nil status")
	}

	switch runtime.GOOS {
	case "linux":
		if status.Mechanism != "LUKS" && status.Mechanism != "none" {
			t.Errorf("unexpected mechanism for linux: %s", status.Mechanism)
		}
	case "darwin":
		if status.Mechanism != "FileVault" && status.Mechanism != "none" {
			t.Errorf("unexpected mechanism for darwin: %s", status.Mechanism)
		}
	case "windows":
		if status.Mechanism != "BitLocker" && status.Mechanism != "none" {
			t.Errorf("unexpected mechanism for windows: %s", status.Mechanism)
		}
	default:
		if status.Mechanism != "none" {
			t.Errorf("unexpected mechanism for %s: %s", runtime.GOOS, status.Mechanism)
		}
	}
}

func TestEncryptionStatusStruct(t *testing.T) {
	s := &EncryptionStatus{
		Encrypted:  true,
		Mechanism:  "LUKS",
		DevicePath: "/dev/sda1",
	}
	if !s.Encrypted {
		t.Error("expected Encrypted to be true")
	}
	if s.Mechanism != "LUKS" {
		t.Errorf("expected Mechanism LUKS, got %s", s.Mechanism)
	}
	if s.DevicePath != "/dev/sda1" {
		t.Errorf("expected DevicePath /dev/sda1, got %s", s.DevicePath)
	}

	s2 := &EncryptionStatus{}
	if s2.Encrypted {
		t.Error("zero value Encrypted should be false")
	}
	if s2.Mechanism != "" {
		t.Errorf("zero value Mechanism should be empty, got %s", s2.Mechanism)
	}
}

func TestDetectDiskEncryptionPath(t *testing.T) {
	status, err := DetectDiskEncryption("/nonexistent")
	if err != nil {
		t.Fatalf("DetectDiskEncryption returned error: %v", err)
	}
	if status == nil {
		t.Fatal("status should not be nil")
	}
	if status.Mechanism == "" {
		t.Error("Mechanism should not be empty even for nonexistent path")
	}
}
