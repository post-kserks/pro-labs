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

	decrypted, err := em.DecryptPage(nonce, ciphertext, pageID)
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

	_, err = em2.DecryptPage(nonce, ciphertext, pageID)
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

	_, err = em.DecryptPage(nonce, tampered, pageID)
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

	for i, b := range em.dek {
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

	_, err = em.DecryptPage([]byte("nonce"), []byte("ciphertext"), []byte("page"))
	if err == nil {
		t.Error("expected DecryptPage to fail after Zeroize")
	}
}