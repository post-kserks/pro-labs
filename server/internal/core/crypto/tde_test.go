package crypto

import (
	"bytes"
	"crypto/rand"
	"testing"
)

func TestTDEEngine_PageEncryption(t *testing.T) {
	key := make([]byte, 32)
	_, err := rand.Read(key)
	if err != nil {
		t.Fatalf("Failed to generate key: %v", err)
	}

	engine, err := NewTDEEngine(key)
	if err != nil {
		t.Fatalf("Failed to create TDE engine: %v", err)
	}

	originalData := []byte("hello, vaultdb page content here")
	pageID := uint64(42)

	// Encrypt
	ciphertext, err := engine.EncryptPage(originalData, pageID)
	if err != nil {
		t.Fatalf("Failed to encrypt page: %v", err)
	}

	if bytes.Equal(originalData, ciphertext) {
		t.Fatal("Ciphertext is identical to original data")
	}

	// Decrypt
	plaintext, err := engine.DecryptPage(ciphertext, pageID)
	if err != nil {
		t.Fatalf("Failed to decrypt page: %v", err)
	}

	if !bytes.Equal(originalData, plaintext) {
		t.Fatalf("Decrypted data does not match original data. Expected %s, got %s", originalData, plaintext)
	}

	// Decrypt with wrong pageID
	_, err = engine.DecryptPage(ciphertext, pageID+1)
	if err == nil {
		t.Fatal("Expected error when decrypting with wrong pageID, got nil")
	}
}

func TestTDEEngine_WALEncryption(t *testing.T) {
	key := make([]byte, 32)
	_, err := rand.Read(key)
	if err != nil {
		t.Fatalf("Failed to generate key: %v", err)
	}

	engine, err := NewTDEEngine(key)
	if err != nil {
		t.Fatalf("Failed to create TDE engine: %v", err)
	}

	originalData := []byte("WAL record payload")
	lsn := uint64(100500)

	// Encrypt
	ciphertext, err := engine.EncryptWAL(originalData, lsn)
	if err != nil {
		t.Fatalf("Failed to encrypt WAL: %v", err)
	}

	if bytes.Equal(originalData, ciphertext) {
		t.Fatal("Ciphertext is identical to original data")
	}

	// Decrypt
	plaintext, err := engine.DecryptWAL(ciphertext, lsn)
	if err != nil {
		t.Fatalf("Failed to decrypt WAL: %v", err)
	}

	if !bytes.Equal(originalData, plaintext) {
		t.Fatalf("Decrypted data does not match original data. Expected %s, got %s", originalData, plaintext)
	}
}

func TestTDEEngine_InvalidKey(t *testing.T) {
	key := make([]byte, 16) // AES-128 key, but we enforce AES-256 (32 bytes)
	_, err := NewTDEEngine(key)
	if err != ErrInvalidKeyLength {
		t.Fatalf("Expected ErrInvalidKeyLength, got %v", err)
	}
}
