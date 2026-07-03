package crypto

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"fmt"
	"io"
)

type EncryptionManager struct {
	dek    []byte      // 32 bytes for AES-256
	aead   cipher.AEAD
	keyID  string
	closed bool
}

func NewEncryptionManager(dek []byte, keyID string) (*EncryptionManager, error) {
	if len(dek) != 32 {
		return nil, fmt.Errorf("DEK must be 32 bytes, got %d", len(dek))
	}
	block, err := aes.NewCipher(dek)
	if err != nil {
		return nil, err
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		return nil, err
	}
	return &EncryptionManager{dek: dek, aead: aead, keyID: keyID}, nil
}

func (em *EncryptionManager) EncryptPage(plaintext []byte, pageID []byte) (nonce, ciphertext []byte, err error) {
	if em.closed {
		return nil, nil, fmt.Errorf("encryption manager is closed")
	}
	nonce = make([]byte, em.aead.NonceSize())
	if _, err := io.ReadFull(rand.Reader, nonce); err != nil {
		return nil, nil, err
	}
	ciphertext = em.aead.Seal(nil, nonce, plaintext, pageID)
	return nonce, ciphertext, nil
}

func (em *EncryptionManager) DecryptPage(nonce, ciphertext []byte, pageID []byte) ([]byte, error) {
	if em.closed {
		return nil, fmt.Errorf("encryption manager is closed")
	}
	return em.aead.Open(nil, nonce, ciphertext, pageID)
}

func (em *EncryptionManager) KeyVersion() uint32 {
	return 1 // simplified
}

func (em *EncryptionManager) Zeroize() {
	for i := range em.dek {
		em.dek[i] = 0
	}
	em.aead = nil
	em.closed = true
}