package websocket

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestComputeAcceptKey(t *testing.T) {
	key := "dGhlIHNhbXBsZSBub25jZQ=="
	expected := "s3pPLMBiTxaQ9kYGzzhZRbK+xOo="

	got := computeAcceptKey(key)
	if got != expected {
		t.Errorf("computeAcceptKey(%q) = %q, want %q", key, got, expected)
	}
}

func TestUpgradeNotWebsocket(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	w := httptest.NewRecorder()

	_, _, err := Upgrade(w, req)
	if err == nil {
		t.Fatal("expected error for non-websocket request")
	}
	if !strings.Contains(err.Error(), "not a websocket upgrade request") {
		t.Errorf("unexpected error message: %v", err)
	}
}
