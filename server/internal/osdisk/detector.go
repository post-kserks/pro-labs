package osdisk

import (
	"os/exec"
	"runtime"
	"strings"
)

type EncryptionStatus struct {
	Encrypted  bool
	Mechanism  string // "LUKS" | "FileVault" | "BitLocker" | "none"
	DevicePath string
}

func DetectDiskEncryption(path string) (*EncryptionStatus, error) {
	switch runtime.GOOS {
	case "linux":
		return detectLUKS(path)
	case "darwin":
		return detectFileVault()
	case "windows":
		return detectBitLocker(path)
	default:
		return &EncryptionStatus{Encrypted: false, Mechanism: "none"}, nil
	}
}

func detectLUKS(path string) (*EncryptionStatus, error) {
	cmd := exec.Command("lsblk", "-no", "FSTYPE", "-J")
	out, err := cmd.Output()
	if err != nil {
		return &EncryptionStatus{Encrypted: false, Mechanism: "none"}, nil
	}
	output := string(out)
	if strings.Contains(output, "crypto_LUKS") {
		return &EncryptionStatus{Encrypted: true, Mechanism: "LUKS"}, nil
	}
	return &EncryptionStatus{Encrypted: false, Mechanism: "none"}, nil
}

func detectFileVault() (*EncryptionStatus, error) {
	cmd := exec.Command("fdesetup", "status")
	out, err := cmd.Output()
	if err != nil {
		return &EncryptionStatus{Encrypted: false, Mechanism: "none"}, nil
	}
	if strings.Contains(string(out), "FileVault is On") {
		return &EncryptionStatus{Encrypted: true, Mechanism: "FileVault"}, nil
	}
	return &EncryptionStatus{Encrypted: false, Mechanism: "none"}, nil
}

func detectBitLocker(path string) (*EncryptionStatus, error) {
	cmd := exec.Command("manage-bde", "-status")
	out, err := cmd.Output()
	if err != nil {
		return &EncryptionStatus{Encrypted: false, Mechanism: "none"}, nil
	}
	if strings.Contains(string(out), "Protection On") {
		return &EncryptionStatus{Encrypted: true, Mechanism: "BitLocker"}, nil
	}
	return &EncryptionStatus{Encrypted: false, Mechanism: "none"}, nil
}
