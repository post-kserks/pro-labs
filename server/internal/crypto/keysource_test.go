package crypto

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

func TestPassphraseKeySource(t *testing.T) {
	salt, err := GenerateSalt()
	if err != nil {
		t.Fatalf("GenerateSalt: %v", err)
	}

	src := NewPassphraseKeySource("my-secret-pass", salt)
	if src.Name() != "passphrase" {
		t.Fatalf("Name() = %q, want %q", src.Name(), "passphrase")
	}

	kek, err := src.GetKEK(context.Background())
	if err != nil {
		t.Fatalf("GetKEK: %v", err)
	}
	if len(kek) != 32 {
		t.Fatalf("KEK length = %d, want 32", len(kek))
	}

	// Same passphrase + salt must produce the same KEK.
	kek2, err := NewPassphraseKeySource("my-secret-pass", salt).GetKEK(context.Background())
	if err != nil {
		t.Fatalf("GetKEK (second): %v", err)
	}
	if string(kek) != string(kek2) {
		t.Fatal("same inputs produced different KEKs")
	}

	// Different passphrase must produce a different KEK.
	kek3, err := NewPassphraseKeySource("other-pass", salt).GetKEK(context.Background())
	if err != nil {
		t.Fatalf("GetKEK (different): %v", err)
	}
	if string(kek) == string(kek3) {
		t.Fatal("different passphrases produced the same KEK")
	}
}

func TestFileKeySource(t *testing.T) {
	salt, err := GenerateSalt()
	if err != nil {
		t.Fatalf("GenerateSalt: %v", err)
	}

	dir := t.TempDir()
	keyFile := filepath.Join(dir, "key.txt")
	if err := os.WriteFile(keyFile, []byte("  file-secret\n"), 0600); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	src := NewFileKeySource(keyFile, salt)
	if src.Name() != "file" {
		t.Fatalf("Name() = %q, want %q", src.Name(), "file")
	}

	kek, err := src.GetKEK(context.Background())
	if err != nil {
		t.Fatalf("GetKEK: %v", err)
	}
	if len(kek) != 32 {
		t.Fatalf("KEK length = %d, want 32", len(kek))
	}

	// Must match a direct passphrase derivation of the trimmed value.
	expected, err := NewPassphraseKeySource("file-secret", salt).GetKEK(context.Background())
	if err != nil {
		t.Fatalf("GetKEK (expected): %v", err)
	}
	if string(kek) != string(expected) {
		t.Fatal("FileKeySource KEK does not match passphrase derivation")
	}
}

func TestFileKeySourceMissingFile(t *testing.T) {
	salt, err := GenerateSalt()
	if err != nil {
		t.Fatalf("GenerateSalt: %v", err)
	}

	src := NewFileKeySource("/nonexistent/path/key.txt", salt)
	_, err = src.GetKEK(context.Background())
	if err == nil {
		t.Fatal("expected error for missing file, got nil")
	}
}

func TestGenerateSalt(t *testing.T) {
	s1, err := GenerateSalt()
	if err != nil {
		t.Fatalf("GenerateSalt: %v", err)
	}
	if len(s1) != 16 {
		t.Fatalf("salt length = %d, want 16", len(s1))
	}

	s2, err := GenerateSalt()
	if err != nil {
		t.Fatalf("GenerateSalt (second): %v", err)
	}
	if string(s1) == string(s2) {
		t.Fatal("two consecutive salts are identical (extremely unlikely)")
	}
}
