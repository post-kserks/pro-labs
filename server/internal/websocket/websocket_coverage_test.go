package websocket

import (
	"bufio"
	"net"
	"strings"
	"testing"
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
	if buf[1] != 0 {
		t.Errorf("length byte = %d, want 0", buf[1])
	}
}

func TestWriteJSONBoundaryPayload(t *testing.T) {
	server, client := net.Pipe()
	defer server.Close()
	defer client.Close()

	bufrw := bufio.NewReadWriter(bufio.NewReader(client), bufio.NewWriter(client))

	payload := []byte(strings.Repeat("x", 125))
	go func() {
		err := WriteJSON(bufrw, payload)
		if err != nil {
			t.Errorf("WriteJSON error: %v", err)
		}
	}()

	buf := make([]byte, 200)
	n, _ := server.Read(buf)
	if n != 127 {
		t.Fatalf("expected 127 bytes for 125-byte payload, got %d", n)
	}
	if buf[1] != 125 {
		t.Errorf("length = %d, want 125", buf[1])
	}
}

func TestWriteJSONBoundaryMediumPayload(t *testing.T) {
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
	if n != 130 {
		t.Fatalf("expected 130 bytes for 126-byte payload, got %d", n)
	}
	if buf[1] != 126 {
		t.Errorf("length indicator = %d, want 126", buf[1])
	}
	length := int(buf[2])<<8 | int(buf[3])
	if length != 126 {
		t.Errorf("encoded length = %d, want 126", length)
	}
}

func TestWriteJSONBoundaryLargePayload(t *testing.T) {
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
	var length uint64
	for i := 0; i < 8; i++ {
		length = length<<8 | uint64(buf[2+i])
	}
	if int(length) != 65536 {
		t.Errorf("encoded length = %d, want 65536", length)
	}
}

func TestWriteJSONConcurrent(t *testing.T) {
	for i := 0; i < 10; i++ {
		s, c := net.Pipe()
		bufrw := bufio.NewReadWriter(bufio.NewReader(c), bufio.NewWriter(c))

		payload := []byte(`{"n":` + strings.Repeat("a", i*10) + `}`)
		go func() {
			buf := make([]byte, 1024)
			s.Read(buf)
			s.Close()
		}()

		err := WriteJSON(bufrw, payload)
		if err != nil {
			t.Errorf("iteration %d: WriteJSON error: %v", i, err)
		}
		c.Close()
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

	if buf[0]&0x80 == 0 {
		t.Error("FIN bit not set")
	}
	if buf[0]&0x0F != 0x01 {
		t.Errorf("opcode = %d, want 1 (TEXT)", buf[0]&0x0F)
	}
	if buf[1]&0x80 != 0 {
		t.Error("mask bit should not be set")
	}
	_ = n
}
