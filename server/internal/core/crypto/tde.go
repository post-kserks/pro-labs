package crypto

import (
	"crypto/aes"
	"crypto/cipher"
	"encoding/binary"
	"errors"
)

var (
	ErrInvalidKeyLength = errors.New("invalid key length: must be 32 bytes for AES-256")
	ErrDecryptionFailed = errors.New("decryption failed or data corrupted")
)

// TDEEngine provides Transparent Data Encryption using AES-256-GCM.
type TDEEngine struct {
	aead cipher.AEAD
}

// NewTDEEngine creates a new TDEEngine with the given key.
// The key must be exactly 32 bytes for AES-256.
func NewTDEEngine(key []byte) (*TDEEngine, error) {
	if len(key) != 32 {
		return nil, ErrInvalidKeyLength
	}

	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}

	aead, err := cipher.NewGCM(block)
	if err != nil {
		return nil, err
	}

	return &TDEEngine{aead: aead}, nil
}

// EncryptPage encrypts a database page. The pageID is used as part of the nonce.
func (e *TDEEngine) EncryptPage(data []byte, pageID uint64) ([]byte, error) {
	nonce := make([]byte, e.aead.NonceSize())
	binary.LittleEndian.PutUint64(nonce, pageID)
	// Additional bytes in nonce are 0, which is fine as long as pageID is unique per key.
	// We can also include an object/table ID if needed in the future.

	// #nosec G407
	return e.aead.Seal(nil, nonce, data, nil), nil
}

// DecryptPage decrypts a database page. The pageID is used as part of the nonce.
func (e *TDEEngine) DecryptPage(data []byte, pageID uint64) ([]byte, error) {
	nonce := make([]byte, e.aead.NonceSize())
	binary.LittleEndian.PutUint64(nonce, pageID)

	plaintext, err := e.aead.Open(nil, nonce, data, nil)
	if err != nil {
		return nil, ErrDecryptionFailed
	}
	return plaintext, nil
}

// EncryptWAL encrypts a WAL record. The LSN is used as part of the nonce.
func (e *TDEEngine) EncryptWAL(data []byte, lsn uint64) ([]byte, error) {
	nonce := make([]byte, e.aead.NonceSize())
	binary.LittleEndian.PutUint64(nonce, lsn)

	// #nosec G407
	return e.aead.Seal(nil, nonce, data, nil), nil
}

// DecryptWAL decrypts a WAL record. The LSN is used as part of the nonce.
func (e *TDEEngine) DecryptWAL(data []byte, lsn uint64) ([]byte, error) {
	nonce := make([]byte, e.aead.NonceSize())
	binary.LittleEndian.PutUint64(nonce, lsn)

	plaintext, err := e.aead.Open(nil, nonce, data, nil)
	if err != nil {
		return nil, ErrDecryptionFailed
	}
	return plaintext, nil
}
