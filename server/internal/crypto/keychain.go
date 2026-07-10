package crypto

import (
	"context"
	"encoding/base64"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
)

// OSKeychainSource stores KEK in the operating system's secure storage.
type OSKeychainSource struct {
	serviceName string
	account     string
}

func NewOSKeychainSource(serviceName, account string) *OSKeychainSource {
	return &OSKeychainSource{serviceName: serviceName, account: account}
}

func (s *OSKeychainSource) GetKEK(ctx context.Context) ([]byte, error) {
	switch runtime.GOOS {
	case "darwin":
		return s.getFromMacKeychain()
	case "linux":
		return s.getFromLinuxKeyring()
	case "windows":
		return s.getFromWindowsDPAPI()
	default:
		return nil, fmt.Errorf("OS keychain not supported on %s", runtime.GOOS)
	}
}

func (s *OSKeychainSource) Name() string {
	return "os_keychain"
}

// StoreKEK saves the KEK to OS keychain.
func (s *OSKeychainSource) StoreKEK(ctx context.Context, kek []byte) error {
	switch runtime.GOOS {
	case "darwin":
		return s.storeToMacKeychain(kek)
	case "linux":
		return s.storeToLinuxKeyring(kek)
	case "windows":
		return s.storeToWindowsDPAPI(kek)
	default:
		return fmt.Errorf("OS keychain not supported on %s", runtime.GOOS)
	}
}

// macOS Keychain
func (s *OSKeychainSource) getFromMacKeychain() ([]byte, error) {
	cmd := exec.Command("security", "find-generic-password",
		"-s", s.serviceName, "-a", s.account, "-w")
	out, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("keychain lookup failed: %w", err)
	}
	return decodeBase64(strings.TrimSpace(string(out)))
}

func (s *OSKeychainSource) storeToMacKeychain(kek []byte) error {
	encoded := encodeBase64(kek)
	// Try to delete existing first
	exec.Command("security", "delete-generic-password",
		"-s", s.serviceName, "-a", s.account).Run()
	cmd := exec.Command("security", "add-generic-password",
		"-s", s.serviceName, "-a", s.account,
		"-w", encoded, "-U")
	return cmd.Run()
}

// Linux keyring (via secret-tool)
func (s *OSKeychainSource) getFromLinuxKeyring() ([]byte, error) {
	cmd := exec.Command("secret-tool", "lookup",
		"service", s.serviceName, "account", s.account)
	out, err := cmd.Output()
	if err == nil {
		return out, nil
	}
	return nil, fmt.Errorf("linux keyring not available: install libsecret-tools")
}

func (s *OSKeychainSource) storeToLinuxKeyring(kek []byte) error {
	cmd := exec.Command("secret-tool", "store",
		"--label="+s.serviceName,
		"service", s.serviceName,
		"account", s.account)
	cmd.Stdin = strings.NewReader(string(kek))
	return cmd.Run()
}

// Windows DPAPI — uses build-tag-gated protect()/unprotect() primitives.
// Blob stored at {UserConfigDir}/vaultdb/{serviceName}_{account}.dpapi
func (s *OSKeychainSource) dpapiBlobPath() string {
	configDir, err := os.UserConfigDir()
	if err != nil {
		configDir = os.TempDir()
	}
	dir := filepath.Join(configDir, "vaultdb")
	_ = os.MkdirAll(dir, 0700)
	return filepath.Join(dir, fmt.Sprintf("%s_%s.dpapi", s.serviceName, s.account))
}

func (s *OSKeychainSource) getFromWindowsDPAPI() ([]byte, error) {
	data, err := os.ReadFile(s.dpapiBlobPath())
	if err != nil {
		return nil, fmt.Errorf("DPAPI blob not found: %w", err)
	}
	return unprotect(data)
}

func (s *OSKeychainSource) storeToWindowsDPAPI(kek []byte) error {
	protected, err := protect(kek)
	if err != nil {
		return fmt.Errorf("DPAPI protect failed: %w", err)
	}
	return os.WriteFile(s.dpapiBlobPath(), protected, 0600)
}

func encodeBase64(data []byte) string {
	return base64.StdEncoding.EncodeToString(data)
}

func decodeBase64(s string) ([]byte, error) {
	return base64.StdEncoding.DecodeString(s)
}
