package crypto

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"fmt"
	"io"
)

type EncryptionManager struct {
	activeDEK []byte                // 32 bytes for AES-256
	activeVer uint32                // current version
	oldDEKs   map[uint32][]byte     // old DEKs for reading existing pages
	aeads     map[uint32]cipher.AEAD // AEAD for each version
	keyID     string
	closed    bool
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
	return &EncryptionManager{
		activeDEK: dek,
		activeVer: 1,
		oldDEKs:   make(map[uint32][]byte),
		aeads:     map[uint32]cipher.AEAD{1: aead},
		keyID:     keyID,
	}, nil
}

// ForKeyVersion returns a view of the manager scoped to a specific DEK version.
// Returns nil if the version is unknown or the manager is closed.
func (em *EncryptionManager) ForKeyVersion(version uint32) *EncryptionManager {
	if em.closed {
		return nil
	}
	if version == em.activeVer {
		return em
	}
	if aead, ok := em.aeads[version]; ok {
		return &EncryptionManager{
			activeDEK: em.oldDEKs[version],
			activeVer: version,
			aeads:     map[uint32]cipher.AEAD{version: aead},
			closed:    false,
		}
	}
	return nil
}

func (em *EncryptionManager) EncryptPage(plaintext []byte, pageID []byte) (nonce, ciphertext []byte, err error) {
	if em.closed {
		return nil, nil, fmt.Errorf("encryption manager is closed")
	}
	nonce = make([]byte, 12)
	if _, err := io.ReadFull(rand.Reader, nonce); err != nil {
		return nil, nil, err
	}
	ciphertext = em.aeads[em.activeVer].Seal(nil, nonce, plaintext, pageID)
	return nonce, ciphertext, nil
}

func (em *EncryptionManager) DecryptPage(nonce, ciphertext []byte, pageID []byte, version uint32) ([]byte, error) {
	if em.closed {
		return nil, fmt.Errorf("encryption manager is closed")
	}
	aead, ok := em.aeads[version]
	if !ok {
		return nil, fmt.Errorf("unknown key version %d", version)
	}
	return aead.Open(nil, nonce, ciphertext, pageID)
}

func (em *EncryptionManager) KeyVersion() uint32 {
	return em.activeVer
}

// RotateDEK rotates to a new DEK, moving the current one to oldDEKs.
func (em *EncryptionManager) RotateDEK(newDEK []byte) error {
	if len(newDEK) != 32 {
		return fmt.Errorf("DEK must be 32 bytes")
	}
	block, err := aes.NewCipher(newDEK)
	if err != nil {
		return err
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		return err
	}
	newVer := em.activeVer + 1
	em.oldDEKs[em.activeVer] = em.activeDEK
	em.aeads[newVer] = aead
	em.activeDEK = newDEK
	em.activeVer = newVer
	return nil
}

func (em *EncryptionManager) Zeroize() {
	zeroizeSlice(em.activeDEK)
	for _, dek := range em.oldDEKs {
		zeroizeSlice(dek)
	}
	em.aeads = nil
	em.oldDEKs = nil
	em.closed = true
}
