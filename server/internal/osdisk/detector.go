package osdisk

import (
	"os/exec"
	"runtime"
	"strings"
)

// CommandRunner executes shell commands. Default uses real exec.Command.
type CommandRunner interface {
	Output(name string, args ...string) ([]byte, error)
}

type realCommandRunner struct{}

func (r *realCommandRunner) Output(name string, args ...string) ([]byte, error) {
	return exec.Command(name, args...).Output()
}

var defaultRunner CommandRunner = &realCommandRunner{}

// SetCommandRunner overrides the command runner for testing.
func SetCommandRunner(r CommandRunner) {
	defaultRunner = r
}

// ResetCommandRunner restores the default command runner.
func ResetCommandRunner() {
	defaultRunner = &realCommandRunner{}
}

type EncryptionStatus struct {
	Encrypted  bool
	Mechanism  string // "LUKS" | "FileVault" | "BitLocker" | "none"
	DevicePath string
}

func DetectDiskEncryption(path string) (*EncryptionStatus, error) {
	return detectDiskEncryptionForOS(path, runtime.GOOS, defaultRunner)
}

func detectDiskEncryptionForOS(path, goos string, runner CommandRunner) (*EncryptionStatus, error) {
	switch goos {
	case "linux":
		return detectLUKS(runner)
	case "darwin":
		return detectFileVault(runner)
	case "windows":
		return detectBitLocker(runner)
	default:
		return &EncryptionStatus{Encrypted: false, Mechanism: "none"}, nil
	}
}

func detectLUKS(runner CommandRunner) (*EncryptionStatus, error) {
	out, err := runner.Output("lsblk", "-no", "FSTYPE", "-J")
	if err != nil {
		return &EncryptionStatus{Encrypted: false, Mechanism: "none"}, nil
	}
	if strings.Contains(string(out), "crypto_LUKS") {
		return &EncryptionStatus{Encrypted: true, Mechanism: "LUKS"}, nil
	}
	return &EncryptionStatus{Encrypted: false, Mechanism: "none"}, nil
}

func detectFileVault(runner CommandRunner) (*EncryptionStatus, error) {
	out, err := runner.Output("fdesetup", "status")
	if err != nil {
		return &EncryptionStatus{Encrypted: false, Mechanism: "none"}, nil
	}
	if strings.Contains(string(out), "FileVault is On") {
		return &EncryptionStatus{Encrypted: true, Mechanism: "FileVault"}, nil
	}
	return &EncryptionStatus{Encrypted: false, Mechanism: "none"}, nil
}

func detectBitLocker(runner CommandRunner) (*EncryptionStatus, error) {
	out, err := runner.Output("manage-bde", "-status")
	if err != nil {
		return &EncryptionStatus{Encrypted: false, Mechanism: "none"}, nil
	}
	if strings.Contains(string(out), "Protection On") {
		return &EncryptionStatus{Encrypted: true, Mechanism: "BitLocker"}, nil
	}
	return &EncryptionStatus{Encrypted: false, Mechanism: "none"}, nil
}
