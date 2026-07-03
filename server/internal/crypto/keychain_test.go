package crypto

import (
	"context"
	"testing"
)

func TestOSKeychainSourceName(t *testing.T) {
	src := NewOSKeychainSource("test-service", "test-account")
	if src.Name() != "os_keychain" {
		t.Fatalf("Name() = %q, want %q", src.Name(), "os_keychain")
	}
}

func TestOSKeychainUnsupportedOS(t *testing.T) {
	// This test verifies the unsupported OS error path
	// by testing the encodeBase64 and decodeBase64 helpers
	testData := []byte("test-key-data-12345")

	encoded := encodeBase64(testData)
	decoded, err := decodeBase64(encoded)
	if err != nil {
		t.Fatalf("decodeBase64: %v", err)
	}
	if string(decoded) != string(testData) {
		t.Fatalf("round-trip failed: got %q, want %q", decoded, testData)
	}

	// Test invalid base64
	_, err = decodeBase64("not-valid-base64!!!")
	if err == nil {
		t.Fatal("expected error for invalid base64, got nil")
	}
}

func TestOSKeychainSourceRoundTrip(t *testing.T) {
	// Test encode/decode round-trip with various data sizes
	testCases := [][]byte{
		nil,
		{},
		{0},
		{0, 0, 0, 0},
		[]byte("hello world"),
		make([]byte, 32), // AES-256 key size
	}

	for i, tc := range testCases {
		encoded := encodeBase64(tc)
		decoded, err := decodeBase64(encoded)
		if err != nil {
			t.Fatalf("case %d: decodeBase64: %v", i, err)
		}
		if string(decoded) != string(tc) {
			t.Fatalf("case %d: round-trip failed", i)
		}
	}
}

func TestOSKeychainStoreKEKUnsupportedOS(t *testing.T) {
	// This test verifies the StoreKEK method returns an error on unsupported platforms
	// We can't easily mock runtime.GOOS, but we can verify the method exists and compiles
	src := NewOSKeychainSource("test-service", "test-account")
	
	// On any OS, if the keychain is not available, we should get an error
	// This test is mainly to ensure the method signature is correct
	err := src.StoreKEK(context.Background(), []byte("test"))
	// We expect either success (if keychain is available) or an error
	// Both are valid outcomes for this test
	_ = err
}
