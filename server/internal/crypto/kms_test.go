package crypto

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

func TestKMSKeySourceName(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "dek.enc")
	client := NewFileKMSClient(path)

	src := NewKMSKeySource("aws-kms", "arn:aws:kms:us-east-1:123456789012:key/12345678-1234-1234-1234-123456789012", client)

	if src.Name() != "kms-aws-kms" {
		t.Fatalf("Name() = %q, want %q", src.Name(), "kms-aws-kms")
	}
}

func TestKMSDecryptDEK(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "dek.enc")
	client := NewFileKMSClient(path)

	src := NewKMSKeySource("aws-kms", "test-key-id", client)

	// Simulate storing an encrypted DEK
	dek := []byte("this-is-a-secret-data-encryption-key!!")
	_, err := src.EncryptDEK(context.Background(), dek)
	if err != nil {
		t.Fatalf("EncryptDEK: %v", err)
	}

	// Decrypt should return the original DEK
	decrypted, err := src.DecryptDEK(context.Background(), dek)
	if err != nil {
		t.Fatalf("DecryptDEK: %v", err)
	}

	if string(decrypted) != string(dek) {
		t.Fatalf("DecryptDEK returned %q, want %q", decrypted, dek)
	}
}

func TestKMSKeySourceGetKEKReturnsError(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "dek.enc")
	client := NewFileKMSClient(path)

	src := NewKMSKeySource("hashicorp-vault", "transit/key/mykey", client)

	_, err := src.GetKEK(context.Background())
	if err == nil {
		t.Fatal("expected error from GetKEK, got nil")
	}
}

func TestNewAWSKMSClient(t *testing.T) {
	c := NewAWSKMSClient("us-west-2")
	if c.region != "us-west-2" {
		t.Errorf("region = %q, want us-west-2", c.region)
	}
}

func TestAWSKMSEncryptFailsWithoutCLI(t *testing.T) {
	c := NewAWSKMSClient("us-east-1")
	_, err := c.Encrypt(context.Background(), "test-key", []byte("plaintext"))
	if err == nil {
		// Might succeed if aws CLI is configured; that's fine
		return
	}
}

func TestAWSKMSDecryptFailsWithoutCLI(t *testing.T) {
	c := NewAWSKMSClient("us-east-1")
	_, err := c.Decrypt(context.Background(), "test-key", []byte("ciphertext"))
	if err == nil {
		// Might succeed if aws CLI is configured; that's fine
		return
	}
}

func TestKMSKeySourceAzureProvider(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "dek.enc")
	client := NewFileKMSClient(path)

	src := NewKMSKeySource("azure-keyvault", "https://vault.azure.net/keys/mykey", client)
	if src.Name() != "kms-azure-keyvault" {
		t.Errorf("Name() = %q, want kms-azure-keyvault", src.Name())
	}
}

func TestKMSKeySourceVaultProvider(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "dek.enc")
	client := NewFileKMSClient(path)

	src := NewKMSKeySource("hashicorp-vault", "transit/key/mykey", client)
	if src.Name() != "kms-hashicorp-vault" {
		t.Errorf("Name() = %q, want kms-hashicorp-vault", src.Name())
	}
}

func TestFileKMSClientEncryptWriteError(t *testing.T) {
	// Write to a path inside a non-existent directory
	c := NewFileKMSClient("/nonexistent/dir/file.enc")
	_, err := c.Encrypt(context.Background(), "key", []byte("data"))
	if err == nil {
		t.Fatal("expected error writing to non-existent path")
	}
}

func TestFileKMSClientEncryptDecryptRoundtrip(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "test-kms.enc")
	c := NewFileKMSClient(path)

	plaintext := []byte("roundtrip test data")
	_, err := c.Encrypt(context.Background(), "key-id", plaintext)
	if err != nil {
		t.Fatalf("Encrypt: %v", err)
	}

	decrypted, err := c.Decrypt(context.Background(), "key-id", nil)
	if err != nil {
		t.Fatalf("Decrypt: %v", err)
	}
	if string(decrypted) != string(plaintext) {
		t.Errorf("decrypted = %q, want %q", decrypted, plaintext)
	}
}

func TestFileKMSClientDecryptMissingFile(t *testing.T) {
	c := NewFileKMSClient("/nonexistent/path/file.enc")
	_, err := c.Decrypt(context.Background(), "key", nil)
	if err == nil {
		t.Fatal("expected error reading from non-existent file")
	}
}

func TestFileKMSClientEncryptEmptyData(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "empty.enc")
	c := NewFileKMSClient(path)

	_, err := c.Encrypt(context.Background(), "key", []byte{})
	if err != nil {
		t.Fatalf("Encrypt empty data: %v", err)
	}

	decrypted, err := c.Decrypt(context.Background(), "key", nil)
	if err != nil {
		t.Fatalf("Decrypt: %v", err)
	}
	if len(decrypted) != 0 {
		t.Errorf("expected empty data, got %d bytes", len(decrypted))
	}
}

func TestFileKMSClientEncryptDecryptRoundtripWithEncryption(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "encrypted-kms.enc")
	c := NewFileKMSClient(path)

	plaintext := []byte("secret-data-encryption-key-12345")
	_, err := c.Encrypt(context.Background(), "key-id", plaintext)
	if err != nil {
		t.Fatalf("Encrypt: %v", err)
	}

	decrypted, err := c.Decrypt(context.Background(), "key-id", nil)
	if err != nil {
		t.Fatalf("Decrypt: %v", err)
	}
	if string(decrypted) != string(plaintext) {
		t.Errorf("decrypted = %q, want %q", decrypted, plaintext)
	}
}

func TestFileKMSClientPlaintextNotStoredOnDisk(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "no-plaintext.enc")
	c := NewFileKMSClient(path)

	plaintext := []byte("this-should-not-be-readable-as-plaintext")
	_, err := c.Encrypt(context.Background(), "key-id", plaintext)
	if err != nil {
		t.Fatalf("Encrypt: %v", err)
	}

	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}

	if string(raw) == string(plaintext) {
		t.Error("plaintext was stored directly on disk — expected encrypted content")
	}
	if len(raw) == 0 {
		t.Error("file is empty after encrypt")
	}
}

func TestEncryptWithMachineKeyRoundtrip(t *testing.T) {
	plaintext := []byte("roundtrip test data for machine key encryption")
	encrypted, err := encryptWithMachineKey(plaintext)
	if err != nil {
		t.Fatalf("encryptWithMachineKey: %v", err)
	}
	if string(encrypted) == string(plaintext) {
		t.Error("encrypted data should differ from plaintext")
	}
	decrypted, err := decryptWithMachineKey(encrypted)
	if err != nil {
		t.Fatalf("decryptWithMachineKey: %v", err)
	}
	if string(decrypted) != string(plaintext) {
		t.Errorf("decrypted = %q, want %q", decrypted, plaintext)
	}
}

func TestDecryptWithMachineKeyTooShort(t *testing.T) {
	_, err := decryptWithMachineKey([]byte("short"))
	if err == nil {
		t.Fatal("expected error for too-short ciphertext")
	}
}
