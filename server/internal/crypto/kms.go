package crypto

import (
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"
)

// KMSClient abstracts KMS operations.
type KMSClient interface {
	Encrypt(ctx context.Context, keyID string, plaintext []byte) ([]byte, error)
	Decrypt(ctx context.Context, keyID string, ciphertext []byte) ([]byte, error)
}

// KMSKeySource uses an external KMS for key management.
// KMS never returns the master key — it only encrypts/decrypts DEKs.
type KMSKeySource struct {
	provider string // "aws-kms" | "hashicorp-vault" | "azure-keyvault"
	keyID    string
	client   KMSClient
}

func NewKMSKeySource(provider, keyID string, client KMSClient) *KMSKeySource {
	return &KMSKeySource{provider: provider, keyID: keyID, client: client}
}

func (s *KMSKeySource) GetKEK(ctx context.Context) ([]byte, error) {
	return nil, fmt.Errorf("KMS uses DecryptDEK, not GetKEK directly")
}

func (s *KMSKeySource) Name() string {
	return "kms-" + s.provider
}

// DecryptDEK decrypts the DEK using the KMS.
func (s *KMSKeySource) DecryptDEK(ctx context.Context, encryptedDEK []byte) ([]byte, error) {
	return s.client.Decrypt(ctx, s.keyID, encryptedDEK)
}

// EncryptDEK encrypts the DEK using the KMS.
func (s *KMSKeySource) EncryptDEK(ctx context.Context, dek []byte) ([]byte, error) {
	return s.client.Encrypt(ctx, s.keyID, dek)
}

// AWSKMSClient implements KMSClient for AWS KMS.
type AWSKMSClient struct {
	region string
}

func NewAWSKMSClient(region string) *AWSKMSClient {
	return &AWSKMSClient{region: region}
}

func (c *AWSKMSClient) Encrypt(ctx context.Context, keyID string, plaintext []byte) ([]byte, error) {
	encoded := base64.StdEncoding.EncodeToString(plaintext)
	cmd := exec.Command("aws", "kms", "encrypt",
		"--key-id", keyID,
		"--plaintext", encoded,
		"--region", c.region,
		"--output", "text",
		"--query", "CiphertextBlob")
	out, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("AWS KMS encrypt: %w", err)
	}
	return base64.StdEncoding.DecodeString(strings.TrimSpace(string(out)))
}

func (c *AWSKMSClient) Decrypt(ctx context.Context, keyID string, ciphertext []byte) ([]byte, error) {
	encoded := base64.StdEncoding.EncodeToString(ciphertext)
	cmd := exec.Command("aws", "kms", "decrypt",
		"--ciphertext-blob", encoded,
		"--region", c.region,
		"--output", "text",
		"--query", "Plaintext")
	out, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("AWS KMS decrypt: %w", err)
	}
	return base64.StdEncoding.DecodeString(strings.TrimSpace(string(out)))
}

// FileKMSClient reads encrypted DEK from a file (for testing).
// The stored key is encrypted with a machine-specific key derived from hostname.
type FileKMSClient struct {
	path string
}

func NewFileKMSClient(path string) *FileKMSClient {
	return &FileKMSClient{path: path}
}

// getMachineKey derives a 32-byte AES key from hostname + salt.
func getMachineKey() []byte {
	hostname, _ := os.Hostname()
	h := sha256.Sum256([]byte(hostname + "-vaultdb-kms-salt"))
	return h[:]
}

func (c *FileKMSClient) Encrypt(ctx context.Context, keyID string, plaintext []byte) ([]byte, error) {
	encrypted, err := encryptWithMachineKey(plaintext)
	if err != nil {
		return nil, fmt.Errorf("encrypt with machine key: %w", err)
	}
	if err := os.WriteFile(c.path, encrypted, 0600); err != nil {
		return nil, err
	}
	return plaintext, nil
}

func (c *FileKMSClient) Decrypt(ctx context.Context, keyID string, ciphertext []byte) ([]byte, error) {
	data, err := os.ReadFile(c.path)
	if err != nil {
		return nil, err
	}
	return decryptWithMachineKey(data)
}

// encryptWithMachineKey encrypts data using AES-GCM with a machine-specific key.
// Output format: nonce (12 bytes) || ciphertext || tag.
func encryptWithMachineKey(plaintext []byte) ([]byte, error) {
	block, err := aes.NewCipher(getMachineKey())
	if err != nil {
		return nil, err
	}
	aesGCM, err := cipher.NewGCM(block)
	if err != nil {
		return nil, err
	}
	nonce := make([]byte, aesGCM.NonceSize())
	if _, err := io.ReadFull(rand.Reader, nonce); err != nil {
		return nil, err
	}
	return aesGCM.Seal(nonce, nonce, plaintext, nil), nil
}

// decryptWithMachineKey decrypts data using AES-GCM with a machine-specific key.
func decryptWithMachineKey(data []byte) ([]byte, error) {
	block, err := aes.NewCipher(getMachineKey())
	if err != nil {
		return nil, err
	}
	aesGCM, err := cipher.NewGCM(block)
	if err != nil {
		return nil, err
	}
	nonceSize := aesGCM.NonceSize()
	if len(data) < nonceSize {
		return nil, fmt.Errorf("ciphertext too short: %d < %d", len(data), nonceSize)
	}
	nonce, ciphertext := data[:nonceSize], data[nonceSize:]
	return aesGCM.Open(nil, nonce, ciphertext, nil)
}
