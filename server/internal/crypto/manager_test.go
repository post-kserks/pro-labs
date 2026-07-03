package crypto

import (
	"bytes"
	"crypto/rand"
	"testing"
)

func TestEncryptDecryptRoundtrip(t *testing.T) {
	dek := make([]byte, 32)
	if _, err := rand.Read(dek); err != nil {
		t.Fatal(err)
	}

	em, err := NewEncryptionManager(dek, "test-key-1")
	if err != nil {
		t.Fatal(err)
	}

	plaintext := []byte("Hello, World! This is a test page.")
	pageID := []byte("page-123")

	nonce, ciphertext, err := em.EncryptPage(plaintext, pageID)
	if err != nil {
		t.Fatal(err)
	}

	decrypted, err := em.DecryptPage(nonce, ciphertext, pageID, 1)
	if err != nil {
		t.Fatal(err)
	}

	if !bytes.Equal(plaintext, decrypted) {
		t.Errorf("decrypted text does not match original")
	}
}

func TestDecryptWithWrongKeyFails(t *testing.T) {
	dek1 := make([]byte, 32)
	dek2 := make([]byte, 32)
	if _, err := rand.Read(dek1); err != nil {
		t.Fatal(err)
	}
	if _, err := rand.Read(dek2); err != nil {
		t.Fatal(err)
	}

	em1, err := NewEncryptionManager(dek1, "key-1")
	if err != nil {
		t.Fatal(err)
	}

	em2, err := NewEncryptionManager(dek2, "key-2")
	if err != nil {
		t.Fatal(err)
	}

	plaintext := []byte("Secret data")
	pageID := []byte("page-456")

	nonce, ciphertext, err := em1.EncryptPage(plaintext, pageID)
	if err != nil {
		t.Fatal(err)
	}

	_, err = em2.DecryptPage(nonce, ciphertext, pageID, 1)
	if err == nil {
		t.Error("expected decryption with wrong key to fail")
	}
}

func TestDecryptWithTamperedDataFails(t *testing.T) {
	dek := make([]byte, 32)
	if _, err := rand.Read(dek); err != nil {
		t.Fatal(err)
	}

	em, err := NewEncryptionManager(dek, "test-key")
	if err != nil {
		t.Fatal(err)
	}

	plaintext := []byte("Important data")
	pageID := []byte("page-789")

	nonce, ciphertext, err := em.EncryptPage(plaintext, pageID)
	if err != nil {
		t.Fatal(err)
	}

	// Tamper with ciphertext
	tampered := make([]byte, len(ciphertext))
	copy(tampered, ciphertext)
	tampered[0] ^= 0xFF

	_, err = em.DecryptPage(nonce, tampered, pageID, 1)
	if err == nil {
		t.Error("expected decryption with tampered data to fail")
	}
}

func TestZeroize(t *testing.T) {
	dek := make([]byte, 32)
	for i := range dek {
		dek[i] = byte(i)
	}

	em, err := NewEncryptionManager(dek, "test-key")
	if err != nil {
		t.Fatal(err)
	}

	em.Zeroize()

	for i, b := range em.activeDEK {
		if b != 0 {
			t.Errorf("dek[%d] = %d, expected 0", i, b)
		}
	}
}

func TestZeroizeBlocksUsage(t *testing.T) {
	dek := make([]byte, 32)
	if _, err := rand.Read(dek); err != nil {
		t.Fatal(err)
	}

	em, err := NewEncryptionManager(dek, "test-key")
	if err != nil {
		t.Fatal(err)
	}

	em.Zeroize()

	_, _, err = em.EncryptPage([]byte("data"), []byte("page"))
	if err == nil {
		t.Error("expected EncryptPage to fail after Zeroize")
	}

	_, err = em.DecryptPage([]byte("nonce"), []byte("ciphertext"), []byte("page"), 1)
	if err == nil {
		t.Error("expected DecryptPage to fail after Zeroize")
	}
}

func TestMultiVersionEncryptDecrypt(t *testing.T) {
	dek := make([]byte, 32)
	if _, err := rand.Read(dek); err != nil {
		t.Fatal(err)
	}

	em, err := NewEncryptionManager(dek, "test-key")
	if err != nil {
		t.Fatal(err)
	}

	if em.KeyVersion() != 1 {
		t.Fatalf("initial version = %d, want 1", em.KeyVersion())
	}

	// Encrypt with version 1
	plaintext := []byte("page data before rotation")
	pageID := []byte("page-v1")
	nonce1, ciphertext1, err := em.EncryptPage(plaintext, pageID)
	if err != nil {
		t.Fatal(err)
	}

	// Rotate to a new DEK
	newDEK := make([]byte, 32)
	if _, err := rand.Read(newDEK); err != nil {
		t.Fatal(err)
	}
	if err := em.RotateDEK(newDEK); err != nil {
		t.Fatal(err)
	}

	if em.KeyVersion() != 2 {
		t.Fatalf("version after rotation = %d, want 2", em.KeyVersion())
	}

	// Encrypt with version 2
	plaintext2 := []byte("page data after rotation")
	pageID2 := []byte("page-v2")
	nonce2, ciphertext2, err := em.EncryptPage(plaintext2, pageID2)
	if err != nil {
		t.Fatal(err)
	}

	// Decrypt version 1 data with version 1
	decrypted1, err := em.DecryptPage(nonce1, ciphertext1, pageID, 1)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(plaintext, decrypted1) {
		t.Error("v1 decrypt mismatch")
	}

	// Decrypt version 2 data with version 2
	decrypted2, err := em.DecryptPage(nonce2, ciphertext2, pageID2, 2)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(plaintext2, decrypted2) {
		t.Error("v2 decrypt mismatch")
	}
}

func TestForKeyVersion(t *testing.T) {
	dek := make([]byte, 32)
	if _, err := rand.Read(dek); err != nil {
		t.Fatal(err)
	}

	em, err := NewEncryptionManager(dek, "test-key")
	if err != nil {
		t.Fatal(err)
	}

	// ForKeyVersion(1) should return the same manager
	v1 := em.ForKeyVersion(1)
	if v1 != em {
		t.Error("ForKeyVersion(1) should return self")
	}

	// Unknown version returns nil
	if em.ForKeyVersion(99) != nil {
		t.Error("ForKeyVersion(99) should return nil")
	}

	// After rotation, ForKeyVersion(1) still works
	newDEK := make([]byte, 32)
	if _, err := rand.Read(newDEK); err != nil {
		t.Fatal(err)
	}
	if err := em.RotateDEK(newDEK); err != nil {
		t.Fatal(err)
	}

	v1again := em.ForKeyVersion(1)
	if v1again == nil {
		t.Fatal("ForKeyVersion(1) returned nil after rotation")
	}
	if v1again.KeyVersion() != 1 {
		t.Errorf("ForKeyVersion(1).KeyVersion() = %d, want 1", v1again.KeyVersion())
	}
}

func TestRotateDEK(t *testing.T) {
	dek := make([]byte, 32)
	if _, err := rand.Read(dek); err != nil {
		t.Fatal(err)
	}

	em, err := NewEncryptionManager(dek, "test-key")
	if err != nil {
		t.Fatal(err)
	}

	// Rotate multiple times
	for i := 0; i < 3; i++ {
		newDEK := make([]byte, 32)
		if _, err := rand.Read(newDEK); err != nil {
			t.Fatal(err)
		}
		if err := em.RotateDEK(newDEK); err != nil {
			t.Fatalf("rotation %d failed: %v", i+1, err)
		}
		if em.KeyVersion() != uint32(i+2) {
			t.Errorf("version = %d after rotation %d, want %d", em.KeyVersion(), i+1, i+2)
		}
	}

	if em.KeyVersion() != 4 {
		t.Fatalf("final version = %d, want 4", em.KeyVersion())
	}
}

func TestDecryptWithOldVersion(t *testing.T) {
	dek := make([]byte, 32)
	if _, err := rand.Read(dek); err != nil {
		t.Fatal(err)
	}

	em, err := NewEncryptionManager(dek, "test-key")
	if err != nil {
		t.Fatal(err)
	}

	// Encrypt with version 1
	plaintext := []byte("old version data")
	pageID := []byte("page-old")
	nonce, ciphertext, err := em.EncryptPage(plaintext, pageID)
	if err != nil {
		t.Fatal(err)
	}

	// Rotate twice
	for i := 0; i < 2; i++ {
		newDEK := make([]byte, 32)
		if _, err := rand.Read(newDEK); err != nil {
			t.Fatal(err)
		}
		if err := em.RotateDEK(newDEK); err != nil {
			t.Fatal(err)
		}
	}

	// Current version is 3, but we can still decrypt version 1 data
	if em.KeyVersion() != 3 {
		t.Fatalf("version = %d, want 3", em.KeyVersion())
	}

	decrypted, err := em.DecryptPage(nonce, ciphertext, pageID, 1)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(plaintext, decrypted) {
		t.Error("old version decrypt mismatch")
	}

	// Version 99 doesn't exist
	_, err = em.DecryptPage(nonce, ciphertext, pageID, 99)
	if err == nil {
		t.Error("expected error for unknown version")
	}
}

func TestZeroizeClearsAllVersions(t *testing.T) {
	dek := make([]byte, 32)
	if _, err := rand.Read(dek); err != nil {
		t.Fatal(err)
	}

	em, err := NewEncryptionManager(dek, "test-key")
	if err != nil {
		t.Fatal(err)
	}

	// Rotate to add old versions
	for i := 0; i < 3; i++ {
		newDEK := make([]byte, 32)
		if _, err := rand.Read(newDEK); err != nil {
			t.Fatal(err)
		}
		if err := em.RotateDEK(newDEK); err != nil {
			t.Fatal(err)
		}
	}

	em.Zeroize()

	if em.oldDEKs != nil {
		t.Error("oldDEKs not cleared")
	}
	if em.aeads != nil {
		t.Error("aeads not cleared")
	}
}
