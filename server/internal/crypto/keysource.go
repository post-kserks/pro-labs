package crypto

import (
	"context"
	"crypto/rand"
	"fmt"
	"os"
	"strings"

	"golang.org/x/crypto/argon2"
)

// KeySource abstracts the origin of a Key Encryption Key (KEK).
type KeySource interface {
	GetKEK(ctx context.Context) ([]byte, error)
	Name() string
}

// PassphraseKeySource derives a KEK from a passphrase using Argon2id.
type PassphraseKeySource struct {
	passphrase string
	salt       []byte
}

func NewPassphraseKeySource(passphrase string, salt []byte) *PassphraseKeySource {
	return &PassphraseKeySource{passphrase: passphrase, salt: salt}
}

func (s *PassphraseKeySource) GetKEK(ctx context.Context) ([]byte, error) {
	return argon2.IDKey(
		[]byte(s.passphrase),
		s.salt,
		3,       // iterations
		64*1024, // memory: 64 MB
		4,       // parallelism
		32,      // output: 32 bytes for AES-256
	), nil
}

func (s *PassphraseKeySource) Name() string {
	return "passphrase"
}

// GenerateSalt creates a cryptographically random 16-byte salt.
func GenerateSalt() ([]byte, error) {
	salt := make([]byte, 16)
	_, err := rand.Read(salt)
	return salt, err
}

// FileKeySource reads a passphrase from a file and derives a KEK.
type FileKeySource struct {
	filePath string
	salt     []byte
}

func NewFileKeySource(filePath string, salt []byte) *FileKeySource {
	return &FileKeySource{filePath: filePath, salt: salt}
}

func (s *FileKeySource) GetKEK(ctx context.Context) ([]byte, error) {
	data, err := os.ReadFile(s.filePath)
	if err != nil {
		return nil, fmt.Errorf("read key file: %w", err)
	}
	passphrase := strings.TrimSpace(string(data))
	return argon2.IDKey([]byte(passphrase), s.salt, 3, 64*1024, 4, 32), nil
}

func (s *FileKeySource) Name() string {
	return "file"
}
