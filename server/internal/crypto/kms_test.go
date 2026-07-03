package crypto

import (
	"context"
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
