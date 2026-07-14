package crypto

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
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

func TestLoadDEKFileNotFound(t *testing.T) {
	dir := t.TempDir()
	salt, err := GenerateSalt()
	if err != nil {
		t.Fatal(err)
	}
	ks := NewPassphraseKeySource("pass", salt)

	mgr := NewDEKManager(dir)
	_, err = mgr.LoadDEK(context.Background(), ks)
	if err == nil {
		t.Fatal("expected error loading DEK from empty directory")
	}
}

func TestEncryptDEKDecryptDEK(t *testing.T) {
	dek := make([]byte, 32)
	for i := range dek {
		dek[i] = byte(i)
	}
	kek := make([]byte, 32)
	for i := range kek {
		kek[i] = byte(i + 32)
	}

	encDEK, err := EncryptDEK(dek, kek)
	if err != nil {
		t.Fatalf("EncryptDEK: %v", err)
	}
	if bytes.Equal(encDEK, dek) {
		t.Fatal("encrypted DEK should differ from plaintext")
	}

	decDEK, err := DecryptDEK(encDEK, kek)
	if err != nil {
		t.Fatalf("DecryptDEK: %v", err)
	}
	if !bytes.Equal(decDEK, dek) {
		t.Fatal("DecryptDEK did not recover original DEK")
	}
}

func TestDecryptDEKTooShort(t *testing.T) {
	kek := make([]byte, 32)
	short := make([]byte, 5)
	_, err := DecryptDEK(short, kek)
	if err == nil {
		t.Fatal("expected error for too-short encrypted DEK")
	}
}

func TestEncryptDEKInvalidKey(t *testing.T) {
	dek := make([]byte, 32)
	badKek := make([]byte, 7) // invalid for any AES variant
	_, err := EncryptDEK(dek, badKek)
	if err == nil {
		t.Fatal("expected error for invalid KEK size")
	}
}

func TestDecryptDEKInvalidKey(t *testing.T) {
	dek := make([]byte, 32)
	goodKek := make([]byte, 32)
	encDEK, err := EncryptDEK(dek, goodKek)
	if err != nil {
		t.Fatal(err)
	}

	badKek := make([]byte, 7) // invalid for any AES variant
	_, err = DecryptDEK(encDEK, badKek)
	if err == nil {
		t.Fatal("expected error for invalid KEK size")
	}
}

func TestZeroizeSlice(t *testing.T) {
	data := []byte("sensitive-key-material")
	ZeroizeSlice(data)
	for i, b := range data {
		if b != 0 {
			t.Errorf("data[%d] = %d, want 0", i, b)
		}
	}
}

func TestZeroizeSliceEmpty(t *testing.T) {
	data := []byte{}
	ZeroizeSlice(data) // should not panic
}

func TestZeroizeSliceNil(t *testing.T) {
	ZeroizeSlice(nil) // should not panic
}

func TestGenerateAndStoreDEKWriteFailure(t *testing.T) {
	if os.Geteuid() == 0 || runtime.GOOS == "windows" {
		t.Skip("skipping read-only directory test when running as root or on Windows")
	}
	dir := t.TempDir()
	// Make the directory read-only so writes fail
	if err := os.Chmod(dir, 0500); err != nil {
		t.Fatal(err)
	}
	defer os.Chmod(dir, 0700) // restore for cleanup

	salt, err := GenerateSalt()
	if err != nil {
		t.Fatal(err)
	}
	ks := NewPassphraseKeySource("pass", salt)

	mgr := NewDEKManager(dir)
	_, err = mgr.GenerateAndStoreDEK(context.Background(), ks)
	if err == nil {
		t.Fatal("expected error writing to read-only directory")
	}
}

func TestGenerateAndStoreDEKMetaWriteFailure(t *testing.T) {
	// Create a structure where DEK write succeeds but meta write fails.
	// We write .dek.enc first, then make the dir read-only before meta write.
	// This is hard to trigger reliably, so we test the general write-error path
	// by using a non-existent nested path.
	dir := t.TempDir()
	salt, err := GenerateSalt()
	if err != nil {
		t.Fatal(err)
	}
	ks := NewPassphraseKeySource("pass", salt)

	// Use a nested path that doesn't exist for meta
	mgr := &DEKManager{dbPath: filepath.Join(dir, "nonexistent")}
	_, err = mgr.GenerateAndStoreDEK(context.Background(), ks)
	if err == nil {
		t.Fatal("expected error writing to non-existent subdirectory")
	}
}

func TestEncryptionMetaJSON(t *testing.T) {
	dir := t.TempDir()
	salt, err := GenerateSalt()
	if err != nil {
		t.Fatal(err)
	}
	ks := NewPassphraseKeySource("pass", salt)
	mgr := NewDEKManager(dir)
	_, err = mgr.GenerateAndStoreDEK(context.Background(), ks)
	if err != nil {
		t.Fatal(err)
	}

	metaRaw, err := os.ReadFile(mgr.metaPath())
	if err != nil {
		t.Fatal(err)
	}
	var meta EncryptionMeta
	if err := json.Unmarshal(metaRaw, &meta); err != nil {
		t.Fatal(err)
	}
	if meta.Version != 1 {
		t.Errorf("version = %d, want 1", meta.Version)
	}
	if meta.DEKCreatedAt.IsZero() {
		t.Error("DEKCreatedAt should not be zero")
	}
}
