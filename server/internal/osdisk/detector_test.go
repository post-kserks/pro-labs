package osdisk

import (
	"fmt"
	"testing"
)

// mockRunner is a test command runner.
type mockRunner struct {
	output map[string][]byte
	errs   map[string]error
}

func newMockRunner() *mockRunner {
	return &mockRunner{
		output: make(map[string][]byte),
		errs:   make(map[string]error),
	}
}

func (m *mockRunner) Output(name string, args ...string) ([]byte, error) {
	if err, ok := m.errs[name]; ok {
		return nil, err
	}
	if out, ok := m.output[name]; ok {
		return out, nil
	}
	return nil, fmt.Errorf("command not mocked: %s", name)
}

func TestDetectDiskEncryption(t *testing.T) {
	status, err := DetectDiskEncryption("/tmp")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if status == nil {
		t.Fatal("status should not be nil")
	}
}

// --- LUKS tests (all platforms via mock) ---

func TestDetectLUKS_Encrypted(t *testing.T) {
	mock := newMockRunner()
	mock.output["lsblk"] = []byte(`{"filesystems":[{"fstype":"crypto_LUKS"}]}`)

	status, err := detectLUKS(mock)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !status.Encrypted {
		t.Error("expected Encrypted=true for LUKS")
	}
	if status.Mechanism != "LUKS" {
		t.Errorf("expected mechanism LUKS, got %s", status.Mechanism)
	}
}

func TestDetectLUKS_NotEncrypted(t *testing.T) {
	mock := newMockRunner()
	mock.output["lsblk"] = []byte(`{"filesystems":[{"fstype":"ext4"}]}`)

	status, err := detectLUKS(mock)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if status.Encrypted {
		t.Error("expected Encrypted=false for ext4")
	}
	if status.Mechanism != "none" {
		t.Errorf("expected mechanism none, got %s", status.Mechanism)
	}
}

func TestDetectLUKS_CommandError(t *testing.T) {
	mock := newMockRunner()
	mock.errs["lsblk"] = fmt.Errorf("command not found")

	status, err := detectLUKS(mock)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if status.Encrypted {
		t.Error("expected Encrypted=false on command error")
	}
}

func TestDetectLUKS_EmptyOutput(t *testing.T) {
	mock := newMockRunner()
	mock.output["lsblk"] = []byte(`{}`)

	status, err := detectLUKS(mock)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if status.Encrypted {
		t.Error("expected Encrypted=false for empty output")
	}
}

// --- FileVault tests ---

func TestDetectFileVault_On(t *testing.T) {
	mock := newMockRunner()
	mock.output["fdesetup"] = []byte("FileVault is On.\n")

	status, err := detectFileVault(mock)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !status.Encrypted {
		t.Error("expected Encrypted=true for FileVault On")
	}
	if status.Mechanism != "FileVault" {
		t.Errorf("expected mechanism FileVault, got %s", status.Mechanism)
	}
}

func TestDetectFileVault_Off(t *testing.T) {
	mock := newMockRunner()
	mock.output["fdesetup"] = []byte("FileVault is Off.\n")

	status, err := detectFileVault(mock)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if status.Encrypted {
		t.Error("expected Encrypted=false for FileVault Off")
	}
}

func TestDetectFileVault_CommandError(t *testing.T) {
	mock := newMockRunner()
	mock.errs["fdesetup"] = fmt.Errorf("command not found")

	status, err := detectFileVault(mock)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if status.Encrypted {
		t.Error("expected Encrypted=false on command error")
	}
}

func TestDetectFileVault_EmptyOutput(t *testing.T) {
	mock := newMockRunner()
	mock.output["fdesetup"] = []byte("")

	status, err := detectFileVault(mock)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if status.Encrypted {
		t.Error("expected Encrypted=false for empty output")
	}
}

// --- BitLocker tests ---

func TestDetectBitLocker_On(t *testing.T) {
	mock := newMockRunner()
	mock.output["manage-bde"] = []byte("Protection On")

	status, err := detectBitLocker(mock)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !status.Encrypted {
		t.Error("expected Encrypted=true for BitLocker On")
	}
	if status.Mechanism != "BitLocker" {
		t.Errorf("expected mechanism BitLocker, got %s", status.Mechanism)
	}
}

func TestDetectBitLocker_Off(t *testing.T) {
	mock := newMockRunner()
	mock.output["manage-bde"] = []byte("Protection Off")

	status, err := detectBitLocker(mock)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if status.Encrypted {
		t.Error("expected Encrypted=false for BitLocker Off")
	}
}

func TestDetectBitLocker_CommandError(t *testing.T) {
	mock := newMockRunner()
	mock.errs["manage-bde"] = fmt.Errorf("command not found")

	status, err := detectBitLocker(mock)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if status.Encrypted {
		t.Error("expected Encrypted=false on command error")
	}
}

func TestDetectBitLocker_EmptyOutput(t *testing.T) {
	mock := newMockRunner()
	mock.output["manage-bde"] = []byte("")

	status, err := detectBitLocker(mock)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if status.Encrypted {
		t.Error("expected Encrypted=false for empty output")
	}
}

// --- EncryptionStatus struct tests ---

func TestEncryptionStatusStruct(t *testing.T) {
	s := &EncryptionStatus{
		Encrypted:  true,
		Mechanism:  "LUKS",
		DevicePath: "/dev/sda1",
	}
	if !s.Encrypted {
		t.Error("expected Encrypted=true")
	}
	if s.Mechanism != "LUKS" {
		t.Errorf("expected LUKS, got %s", s.Mechanism)
	}
	if s.DevicePath != "/dev/sda1" {
		t.Errorf("expected /dev/sda1, got %s", s.DevicePath)
	}

	s2 := &EncryptionStatus{}
	if s2.Encrypted {
		t.Error("zero value Encrypted should be false")
	}
	if s2.Mechanism != "" {
		t.Errorf("zero value Mechanism should be empty, got %s", s2.Mechanism)
	}
}

// --- SetCommandRunner / ResetCommandRunner ---

func TestSetResetCommandRunner(t *testing.T) {
	mock := newMockRunner()
	SetCommandRunner(mock)
	if defaultRunner != mock {
		t.Error("defaultRunner should be mock after Set")
	}
	ResetCommandRunner()
	if defaultRunner == mock {
		t.Error("defaultRunner should be reset")
	}
}

// --- Integration test with mock via public API ---

func TestDetectDiskEncryption_WithMock(t *testing.T) {
	mock := newMockRunner()
	mock.output["fdesetup"] = []byte("FileVault is On.")
	SetCommandRunner(mock)
	defer ResetCommandRunner()

	status, err := DetectDiskEncryption("/tmp")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// On macOS, this should use the mock
	if status.Mechanism == "FileVault" && !status.Encrypted {
		t.Error("FileVault should report encrypted")
	}
}

// --- OS branch tests (cover all switch cases) ---

func TestDetectDiskEncryptionForOS_Linux(t *testing.T) {
	mock := newMockRunner()
	mock.output["lsblk"] = []byte(`{"filesystems":[{"fstype":"crypto_LUKS"}]}`)

	status, err := detectDiskEncryptionForOS("/dev/sda1", "linux", mock)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !status.Encrypted {
		t.Error("expected Encrypted=true for Linux LUKS")
	}
	if status.Mechanism != "LUKS" {
		t.Errorf("expected LUKS, got %s", status.Mechanism)
	}
}

func TestDetectDiskEncryptionForOS_Darwin(t *testing.T) {
	mock := newMockRunner()
	mock.output["fdesetup"] = []byte("FileVault is On.")

	status, err := detectDiskEncryptionForOS("/tmp", "darwin", mock)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !status.Encrypted {
		t.Error("expected Encrypted=true for macOS FileVault")
	}
	if status.Mechanism != "FileVault" {
		t.Errorf("expected FileVault, got %s", status.Mechanism)
	}
}

func TestDetectDiskEncryptionForOS_Windows(t *testing.T) {
	mock := newMockRunner()
	mock.output["manage-bde"] = []byte("Protection On")

	status, err := detectDiskEncryptionForOS("C:\\", "windows", mock)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !status.Encrypted {
		t.Error("expected Encrypted=true for Windows BitLocker")
	}
	if status.Mechanism != "BitLocker" {
		t.Errorf("expected BitLocker, got %s", status.Mechanism)
	}
}

func TestDetectDiskEncryptionForOS_Default(t *testing.T) {
	mock := newMockRunner()

	status, err := detectDiskEncryptionForOS("/tmp", "freebsd", mock)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if status.Encrypted {
		t.Error("expected Encrypted=false for unknown OS")
	}
	if status.Mechanism != "none" {
		t.Errorf("expected none, got %s", status.Mechanism)
	}
}

func TestDetectDiskEncryptionForOS_Linux_NotEncrypted(t *testing.T) {
	mock := newMockRunner()
	mock.output["lsblk"] = []byte(`{"filesystems":[{"fstype":"ext4"}]}`)

	status, err := detectDiskEncryptionForOS("/dev/sda1", "linux", mock)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if status.Encrypted {
		t.Error("expected Encrypted=false for ext4")
	}
}

func TestDetectDiskEncryptionForOS_Darwin_NotEncrypted(t *testing.T) {
	mock := newMockRunner()
	mock.output["fdesetup"] = []byte("FileVault is Off.")

	status, err := detectDiskEncryptionForOS("/tmp", "darwin", mock)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if status.Encrypted {
		t.Error("expected Encrypted=false for FileVault Off")
	}
}

func TestDetectDiskEncryptionForOS_Windows_NotEncrypted(t *testing.T) {
	mock := newMockRunner()
	mock.output["manage-bde"] = []byte("Protection Off")

	status, err := detectDiskEncryptionForOS("C:\\", "windows", mock)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if status.Encrypted {
		t.Error("expected Encrypted=false for BitLocker Off")
	}
}
