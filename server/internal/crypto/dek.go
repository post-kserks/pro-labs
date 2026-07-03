package crypto

import (
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"time"
)

type EncryptionMeta struct {
	Version      int        `json:"version"`
	Algorithm    string     `json:"algorithm"`
	KeySource    string     `json:"key_source"`
	DEKCreatedAt time.Time  `json:"dek_created_at"`
	DEKRotatedAt *time.Time `json:"dek_rotated_at,omitempty"`
}

type DEKManager struct {
	dbPath string
}

func NewDEKManager(dbPath string) *DEKManager {
	return &DEKManager{dbPath: dbPath}
}

func (m *DEKManager) dekPath() string {
	return filepath.Join(m.dbPath, ".dek.enc")
}

func (m *DEKManager) metaPath() string {
	return filepath.Join(m.dbPath, ".encryption_meta.json")
}

// GenerateAndStoreDEK creates a new DEK, encrypts it with KEK, and stores it.
func (m *DEKManager) GenerateAndStoreDEK(ctx context.Context, ks KeySource) (*EncryptionManager, error) {
	kek, err := ks.GetKEK(ctx)
	if err != nil {
		return nil, fmt.Errorf("get KEK: %w", err)
	}
	defer zeroizeSlice(kek)

	dek := make([]byte, 32)
	if _, err := rand.Read(dek); err != nil {
		return nil, fmt.Errorf("generate DEK: %w", err)
	}

	encDEK, err := encryptDEK(dek, kek)
	if err != nil {
		return nil, fmt.Errorf("encrypt DEK: %w", err)
	}

	if err := os.WriteFile(m.dekPath(), encDEK, 0600); err != nil {
		return nil, fmt.Errorf("write DEK: %w", err)
	}

	meta := EncryptionMeta{
		Version:      1,
		Algorithm:    "AES-256-GCM",
		KeySource:    ks.Name(),
		DEKCreatedAt: time.Now(),
	}
	metaBytes, _ := json.MarshalIndent(meta, "", "  ")
	if err := os.WriteFile(m.metaPath(), metaBytes, 0600); err != nil {
		return nil, fmt.Errorf("write meta: %w", err)
	}

	return NewEncryptionManager(dek, "v1")
}

// LoadDEK loads and decrypts the stored DEK.
func (m *DEKManager) LoadDEK(ctx context.Context, ks KeySource) (*EncryptionManager, error) {
	kek, err := ks.GetKEK(ctx)
	if err != nil {
		return nil, fmt.Errorf("get KEK: %w", err)
	}
	defer zeroizeSlice(kek)

	encDEK, err := os.ReadFile(m.dekPath())
	if err != nil {
		return nil, fmt.Errorf("read DEK: %w", err)
	}

	dek, err := decryptDEK(encDEK, kek)
	if err != nil {
		return nil, fmt.Errorf("decrypt DEK: %w", err)
	}

	return NewEncryptionManager(dek, "v1")
}

// EncryptDEK encrypts a DEK with a KEK.
func EncryptDEK(dek, kek []byte) ([]byte, error) {
	return encryptDEK(dek, kek)
}

// DecryptDEK decrypts an encrypted DEK with a KEK.
func DecryptDEK(encDEK, kek []byte) ([]byte, error) {
	return decryptDEK(encDEK, kek)
}

func encryptDEK(dek, kek []byte) ([]byte, error) {
	block, err := aes.NewCipher(kek)
	if err != nil {
		return nil, err
	}
	aesGCM, err := cipher.NewGCM(block)
	if err != nil {
		return nil, err
	}
	nonce := make([]byte, aesGCM.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		return nil, err
	}
	return aesGCM.Seal(nonce, nonce, dek, nil), nil
}

func decryptDEK(encDEK, kek []byte) ([]byte, error) {
	block, err := aes.NewCipher(kek)
	if err != nil {
		return nil, err
	}
	aesGCM, err := cipher.NewGCM(block)
	if err != nil {
		return nil, err
	}
	nonceSize := aesGCM.NonceSize()
	if len(encDEK) < nonceSize {
		return nil, fmt.Errorf("encrypted DEK too short")
	}
	nonce := encDEK[:nonceSize]
	ciphertext := encDEK[nonceSize:]
	return aesGCM.Open(nil, nonce, ciphertext, nil)
}

func zeroizeSlice(b []byte) {
	for i := range b {
		b[i] = 0
	}
}

// ZeroizeSlice zeros out a byte slice.
func ZeroizeSlice(b []byte) {
	zeroizeSlice(b)
}
