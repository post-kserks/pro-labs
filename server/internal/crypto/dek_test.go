package crypto

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func TestGenerateAndStoreDEK(t *testing.T) {
	dir := t.TempDir()

	salt, err := GenerateSalt()
	if err != nil {
		t.Fatal(err)
	}
	ks := NewPassphraseKeySource("test-passphrase", salt)

	mgr := NewDEKManager(dir)
	em, err := mgr.GenerateAndStoreDEK(context.Background(), ks)
	if err != nil {
		t.Fatalf("GenerateAndStoreDEK: %v", err)
	}

	if em == nil {
		t.Fatal("expected non-nil EncryptionManager")
	}

	// Verify files were created
	if _, err := os.Stat(filepath.Join(dir, ".dek.enc")); os.IsNotExist(err) {
		t.Error(".dek.enc not created")
	}
	if _, err := os.Stat(filepath.Join(dir, ".encryption_meta.json")); os.IsNotExist(err) {
		t.Error(".encryption_meta.json not created")
	}

	// Verify meta content
	metaRaw, err := os.ReadFile(filepath.Join(dir, ".encryption_meta.json"))
	if err != nil {
		t.Fatal(err)
	}
	var meta EncryptionMeta
	if err := json.Unmarshal(metaRaw, &meta); err != nil {
		t.Fatalf("unmarshal meta: %v", err)
	}
	if meta.Algorithm != "AES-256-GCM" {
		t.Errorf("algorithm = %q, want AES-256-GCM", meta.Algorithm)
	}
	if meta.KeySource != "passphrase" {
		t.Errorf("key_source = %q, want passphrase", meta.KeySource)
	}

	// Verify the DEK works for encrypt/decrypt
	plaintext := []byte("test page data")
	pageID := []byte("page-1")
	nonce, ciphertext, err := em.EncryptPage(plaintext, pageID)
	if err != nil {
		t.Fatal(err)
	}
	decrypted, err := em.DecryptPage(nonce, ciphertext, pageID, 1)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(plaintext, decrypted) {
		t.Error("roundtrip failed")
	}
}

func TestLoadDEK(t *testing.T) {
	dir := t.TempDir()

	salt, err := GenerateSalt()
	if err != nil {
		t.Fatal(err)
	}
	ks := NewPassphraseKeySource("my-secret", salt)

	mgr := NewDEKManager(dir)
	em1, err := mgr.GenerateAndStoreDEK(context.Background(), ks)
	if err != nil {
		t.Fatal(err)
	}

	// Encrypt a page with the original manager
	plaintext := []byte("persistent secret")
	pageID := []byte("page-99")
	nonce, ciphertext, err := em1.EncryptPage(plaintext, pageID)
	if err != nil {
		t.Fatal(err)
	}

	// Load from disk with a fresh manager and same key source
	mgr2 := NewDEKManager(dir)
	em2, err := mgr2.LoadDEK(context.Background(), ks)
	if err != nil {
		t.Fatalf("LoadDEK: %v", err)
	}

	decrypted, err := em2.DecryptPage(nonce, ciphertext, pageID, 1)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(plaintext, decrypted) {
		t.Error("LoadDEK roundtrip failed")
	}
}

func TestLoadDEKWrongKeyFails(t *testing.T) {
	dir := t.TempDir()

	salt, err := GenerateSalt()
	if err != nil {
		t.Fatal(err)
	}
	ks1 := NewPassphraseKeySource("correct-pass", salt)
	ks2 := NewPassphraseKeySource("wrong-pass", salt)

	mgr := NewDEKManager(dir)
	_, err = mgr.GenerateAndStoreDEK(context.Background(), ks1)
	if err != nil {
		t.Fatal(err)
	}

	// Loading with a different KEK must fail
	mgr2 := NewDEKManager(dir)
	_, err = mgr2.LoadDEK(context.Background(), ks2)
	if err == nil {
		t.Fatal("expected LoadDEK with wrong key to fail")
	}
}
