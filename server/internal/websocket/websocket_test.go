package websocket

import (
	"bufio"
	"bytes"
	"net"
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

func TestComputeAcceptKeyEmpty(t *testing.T) {
	got := computeAcceptKey("")
	if got == "" {
		t.Error("computeAcceptKey('') returned empty string")
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

func TestWriteJSONSmallPayload(t *testing.T) {
	server, client := net.Pipe()
	defer server.Close()
	defer client.Close()

	bufrw := bufio.NewReadWriter(bufio.NewReader(client), bufio.NewWriter(client))

	payload := []byte(`{"type":"ping"}`)
	go func() {
		err := WriteJSON(bufrw, payload)
		if err != nil {
			t.Errorf("WriteJSON error: %v", err)
		}
	}()

	buf := make([]byte, 1024)
	n, _ := server.Read(buf)
	if n < 2 {
		t.Fatalf("expected at least 2 bytes (frame header), got %d", n)
	}
	if buf[0] != 0x81 {
		t.Errorf("first byte = 0x%02x, want 0x81 (FIN + TEXT)", buf[0])
	}
	expectedLen := byte(len(payload))
	if buf[1] != expectedLen {
		t.Errorf("length byte = %d, want %d", buf[1], expectedLen)
	}
	if !bytes.Equal(buf[2:2+len(payload)], payload) {
		t.Errorf("payload mismatch")
	}
}

func TestWriteJSONMediumPayload(t *testing.T) {
	server, client := net.Pipe()
	defer server.Close()
	defer client.Close()

	bufrw := bufio.NewReadWriter(bufio.NewReader(client), bufio.NewWriter(client))

	payload := []byte(strings.Repeat("x", 200))
	go func() {
		err := WriteJSON(bufrw, payload)
		if err != nil {
			t.Errorf("WriteJSON error: %v", err)
		}
	}()

	buf := make([]byte, 1024)
	n, _ := server.Read(buf)
	if n < 4 {
		t.Fatalf("expected at least 4 bytes for medium payload, got %d", n)
	}
	if buf[0] != 0x81 {
		t.Errorf("first byte = 0x%02x, want 0x81", buf[0])
	}
	if buf[1] != 126 {
		t.Errorf("length indicator = %d, want 126 for medium payload", buf[1])
	}
}

func TestWriteJSONLargePayload(t *testing.T) {
	server, client := net.Pipe()
	defer server.Close()
	defer client.Close()

	bufrw := bufio.NewReadWriter(bufio.NewReader(client), bufio.NewWriter(client))

	payload := []byte(strings.Repeat("x", 70000))
	go func() {
		err := WriteJSON(bufrw, payload)
		if err != nil {
			t.Errorf("WriteJSON error: %v", err)
		}
	}()

	buf := make([]byte, 80000)
	n, _ := server.Read(buf)
	if n < 10 {
		t.Fatalf("expected at least 10 bytes for large payload, got %d", n)
	}
	if buf[0] != 0x81 {
		t.Errorf("first byte = 0x%02x, want 0x81", buf[0])
	}
	if buf[1] != 127 {
		t.Errorf("length indicator = %d, want 127 for large payload", buf[1])
	}
}

func TestWriteJSONNonBytePayload(t *testing.T) {
	server, client := net.Pipe()
	defer server.Close()
	defer client.Close()

	bufrw := bufio.NewReadWriter(bufio.NewReader(client), bufio.NewWriter(client))

	err := WriteJSON(bufrw, "not bytes")
	if err == nil {
		t.Error("expected error for non-byte payload")
	}
	if !strings.Contains(err.Error(), "requires []byte") {
		t.Errorf("unexpected error: %v", err)
	}
}
