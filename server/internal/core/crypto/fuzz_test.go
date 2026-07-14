package crypto

import (
	"testing"
)

func FuzzDecryptPage(f *testing.F) {
	testKey := make([]byte, 32)
	for i := range testKey {
		testKey[i] = byte(i)
	}

	em, err := NewEncryptionManager(testKey, "test-kek")
	if err != nil {
		return
	}

	// Seed with valid encrypted data
	plaintext := []byte("test page data for fuzzing")
	pageID := []byte{0, 0, 0, 1}
	nonce, ciphertext, err := em.EncryptPage(plaintext, pageID)
	if err == nil {
		f.Add(nonce, ciphertext)
	}

	// Edge cases
	f.Add(make([]byte, 12), make([]byte, 100))
	f.Add([]byte{}, []byte{})
	f.Add(make([]byte, 12), plaintext)

	f.Fuzz(func(t *testing.T, nonce, ciphertext []byte) {
		defer func() {
			if r := recover(); r != nil {
				t.Fatalf("DecryptPage panicked: %v", r)
			}
		}()

		if len(nonce) != 12 {
			return // AES-GCM nonce must be 12 bytes
		}

		_, _ = em.DecryptPage(nonce, ciphertext, pageID, 1)
	})
}
