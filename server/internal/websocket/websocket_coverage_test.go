package websocket

import (
	"bufio"
	"net"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestWriteJSONEmptyPayload(t *testing.T) {
	server, client := net.Pipe()
	defer server.Close()
	defer client.Close()

	bufrw := bufio.NewReadWriter(bufio.NewReader(client), bufio.NewWriter(client))

	payload := []byte{}
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
		t.Errorf("first byte = 0x%02x, want 0x81", buf[0])
	}
	// Empty payload length = 0
	if buf[1] != 0 {
		t.Errorf("length byte = %d, want 0", buf[1])
	}
}

func TestWriteJSONBoundaryPayload(t *testing.T) {
	// Test boundary between small and medium (125 → 126)
	server, client := net.Pipe()
	defer server.Close()
	defer client.Close()

	bufrw := bufio.NewReadWriter(bufio.NewReader(client), bufio.NewWriter(client))

	// 125 bytes — should use single-byte length
	payload := []byte(strings.Repeat("x", 125))
	go func() {
		err := WriteJSON(bufrw, payload)
		if err != nil {
			t.Errorf("WriteJSON error: %v", err)
		}
	}()

	buf := make([]byte, 200)
	n, _ := server.Read(buf)
	// 125 bytes payload: 1 (FIN) + 1 (length) + 125 = 127 bytes
	if n != 127 {
		t.Fatalf("expected 127 bytes for 125-byte payload, got %d", n)
	}
	if buf[1] != 125 {
		t.Errorf("length = %d, want 125", buf[1])
	}
}

func TestWriteJSONBoundaryMediumPayload(t *testing.T) {
	// Test boundary at 126 bytes → should use 2-byte length
	server, client := net.Pipe()
	defer server.Close()
	defer client.Close()

	bufrw := bufio.NewReadWriter(bufio.NewReader(client), bufio.NewWriter(client))

	payload := []byte(strings.Repeat("x", 126))
	go func() {
		err := WriteJSON(bufrw, payload)
		if err != nil {
			t.Errorf("WriteJSON error: %v", err)
		}
	}()

	buf := make([]byte, 200)
	n, _ := server.Read(buf)
	// 126 bytes payload: 1 (FIN) + 1 (126 indicator) + 2 (length) + 126 = 130 bytes
	if n != 130 {
		t.Fatalf("expected 130 bytes for 126-byte payload, got %d", n)
	}
	if buf[1] != 126 {
		t.Errorf("length indicator = %d, want 126", buf[1])
	}
	// Check 2-byte length encoding
	length := int(buf[2])<<8 | int(buf[3])
	if length != 126 {
		t.Errorf("encoded length = %d, want 126", length)
	}
}

func TestWriteJSONBoundaryLargePayload(t *testing.T) {
	// Test boundary at 65536 bytes → should use 8-byte length
	server, client := net.Pipe()
	defer server.Close()
	defer client.Close()

	bufrw := bufio.NewReadWriter(bufio.NewReader(client), bufio.NewWriter(client))

	payload := []byte(strings.Repeat("x", 65536))
	go func() {
		err := WriteJSON(bufrw, payload)
		if err != nil {
			t.Errorf("WriteJSON error: %v", err)
		}
	}()

	buf := make([]byte, 70000)
	n, _ := server.Read(buf)
	if n < 10 {
		t.Fatalf("expected at least 10 bytes for large payload, got %d", n)
	}
	if buf[0] != 0x81 {
		t.Errorf("first byte = 0x%02x, want 0x81", buf[0])
	}
	if buf[1] != 127 {
		t.Errorf("length indicator = %d, want 127", buf[1])
	}
	// Check 8-byte length encoding (big-endian)
	var length uint64
	for i := 0; i < 8; i++ {
		length = length<<8 | uint64(buf[2+i])
	}
	if int(length) != 65536 {
		t.Errorf("encoded length = %d, want 65536", length)
	}
}

func TestWriteJSONConcurrent(t *testing.T) {
	server, client := net.Pipe()
	defer server.Close()
	defer client.Close()

	bufrw := bufio.NewReadWriter(bufio.NewReader(client), bufio.NewWriter(client))

	var wg sync.WaitGroup
	errCount := make(chan error, 10)

	for i := 0; i < 10; i++ {
		wg.Add(1)
		go func(n int) {
			defer wg.Done()
			payload := []byte(`{"n":` + strings.Repeat("a", n*10) + `}`)
			if err := WriteJSON(bufrw, payload); err != nil {
				errCount <- err
			}
		}(i)
	}

	wg.Wait()
	close(errCount)

	for err := range errCount {
		t.Errorf("WriteJSON concurrent error: %v", err)
	}

	// Read all data from server side
	buf := make([]byte, 100000)
	total := 0
	for {
		server.SetReadDeadline(time.Now().Add(100 * time.Millisecond))
		n, err := server.Read(buf[total:])
		total += n
		if err != nil {
			break
		}
	}
	if total == 0 {
		t.Fatal("expected some data from concurrent writes")
	}
}

func TestWriteJSONIntPayload(t *testing.T) {
	server, client := net.Pipe()
	defer server.Close()
	defer client.Close()

	bufrw := bufio.NewReadWriter(bufio.NewReader(client), bufio.NewWriter(client))

	err := WriteJSON(bufrw, 42)
	if err == nil {
		t.Error("expected error for int payload")
	}
	if !strings.Contains(err.Error(), "requires []byte") {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestWriteJSONNilPayload(t *testing.T) {
	server, client := net.Pipe()
	defer server.Close()
	defer client.Close()

	bufrw := bufio.NewReadWriter(bufio.NewReader(client), bufio.NewWriter(client))

	err := WriteJSON(bufrw, nil)
	if err == nil {
		t.Error("expected error for nil payload")
	}
}

func TestUpgradeAcceptKeyVariants(t *testing.T) {
	// Test with different keys to ensure consistency
	keys := []string{
		"",
		"dGhlIHNhbXBsZSBub25jZQ==",
		"x3JJLb06O7UYj/6MzvZbAA==",
		strings.Repeat("a", 100),
	}

	for _, key := range keys {
		got := computeAcceptKey(key)
		got2 := computeAcceptKey(key)
		if got != got2 {
			t.Errorf("computeAcceptKey not deterministic for key %q", key)
		}
		if got == "" {
			t.Errorf("computeAcceptKey(%q) returned empty", key)
		}
	}
}

func TestUpgradeWithDifferentKeys(t *testing.T) {
	// Test that different keys produce different accept values
	key1 := "dGhlIHNhbXBsZSBub25jZQ=="
	key2 := "x3JJLb06O7UYj/6MzvZbAA=="

	accept1 := computeAcceptKey(key1)
	accept2 := computeAcceptKey(key2)

	if accept1 == accept2 {
		t.Fatal("different keys should produce different accept values")
	}
}

func TestWriteJSONFrameStructureSmall(t *testing.T) {
	server, client := net.Pipe()
	defer server.Close()
	defer client.Close()

	bufrw := bufio.NewReadWriter(bufio.NewReader(client), bufio.NewWriter(client))

	data := []byte(`{"msg":"test"}`)
	go WriteJSON(bufrw, data)

	buf := make([]byte, 100)
	n, _ := server.Read(buf)

	// Verify FIN bit (0x81 = 0x80 | 0x01 = FIN + TEXT)
	if buf[0]&0x80 == 0 {
		t.Error("FIN bit not set")
	}
	if buf[0]&0x0F != 0x01 {
		t.Errorf("opcode = %d, want 1 (TEXT)", buf[0]&0x0F)
	}
	// No mask bit for server-to-client
	if buf[1]&0x80 != 0 {
		t.Error("mask bit should not be set")
	}
	_ = n
}
