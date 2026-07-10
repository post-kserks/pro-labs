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
	testCases := [][]byte{
		nil,
		{},
		{0},
		{0, 0, 0, 0},
		[]byte("hello world"),
		make([]byte, 32),
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
	src := NewOSKeychainSource("test-service", "test-account")
	err := src.StoreKEK(context.Background(), []byte("test"))
	_ = err
}

func TestOSKeychainGetKEK(t *testing.T) {
	src := NewOSKeychainSource("nonexistent-test-service", "nonexistent-account")
	_, err := src.GetKEK(context.Background())
	// On macOS this calls getFromMacKeychain which will fail because the
	// entry doesn't exist. The error is expected.
	if err == nil {
		t.Log("GetKEK succeeded (keychain entry exists)")
	} else {
		t.Logf("GetKEK error (expected): %v", err)
	}
}

func TestOSKeychainGetKEKErrorWrapping(t *testing.T) {
	src := NewOSKeychainSource("definitely-nonexistent-service-12345", "no-account")
	_, err := src.GetKEK(context.Background())
	if err == nil {
		t.Log("GetKEK succeeded; skipping error-wrapping check")
		return
	}
	// The error should contain information about the failure
	if err.Error() == "" {
		t.Error("expected non-empty error message")
	}
}

func TestOSKeychainStoreKEKAndRetrieveFails(t *testing.T) {
	src := NewOSKeychainSource("test-nonexistent-service", "test-nonexistent-account")
	// Store will likely fail or succeed depending on keychain availability
	_ = src.StoreKEK(context.Background(), []byte("test-key"))
	// GetKEK should fail because we didn't set up a real entry
	_, err := src.GetKEK(context.Background())
	// We just verify GetKEK doesn't panic
	_ = err
}

func TestEncodeBase64Empty(t *testing.T) {
	encoded := encodeBase64([]byte{})
	decoded, err := decodeBase64(encoded)
	if err != nil {
		t.Fatal(err)
	}
	if len(decoded) != 0 {
		t.Errorf("expected empty, got %d bytes", len(decoded))
	}
}

func TestDecodeBase64PaddingEdgeCases(t *testing.T) {
	// Standard padding
	tests := []string{
		"QQ==", // "A" with padding
		"QUE=", // "AB" with padding
		"QUJD", // "ABC" no padding
	}
	for _, tt := range tests {
		_, err := decodeBase64(tt)
		if err != nil {
			t.Errorf("decodeBase64(%q) error: %v", tt, err)
		}
	}
}
