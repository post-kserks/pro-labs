package crypto

import (
	"context"
	"encoding/base64"
	"fmt"
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
type FileKMSClient struct {
	path string
}

func NewFileKMSClient(path string) *FileKMSClient {
	return &FileKMSClient{path: path}
}

func (c *FileKMSClient) Encrypt(ctx context.Context, keyID string, plaintext []byte) ([]byte, error) {
	if err := os.WriteFile(c.path, plaintext, 0600); err != nil {
		return nil, err
	}
	return plaintext, nil
}

func (c *FileKMSClient) Decrypt(ctx context.Context, keyID string, ciphertext []byte) ([]byte, error) {
	return os.ReadFile(c.path)
}
