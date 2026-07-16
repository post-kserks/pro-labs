package pgwire

import (
	"bytes"
	"encoding/binary"
	"io"
	"net"
	"testing"
)

func TestHandleHandshake_DirectStartup(t *testing.T) {
	clientConn, serverConn := net.Pipe()
	defer clientConn.Close()
	defer serverConn.Close()

	// Prepare StartupMessage payload: user=postgres, database=testdb
	payload := []byte("user\x00postgres\x00database\x00testdb\x00\x00")
	length := uint32(8 + len(payload))

	// Write StartupMessage from client side in a separate goroutine
	go func() {
		buf := make([]byte, 8)
		binary.BigEndian.PutUint32(buf[0:4], length)
		binary.BigEndian.PutUint32(buf[4:8], CodeProtocolV3)
		_, _ = clientConn.Write(buf)
		_, _ = clientConn.Write(payload)
	}()

	type result struct {
		params map[string]string
		err    error
	}
	resChan := make(chan result, 1)
	go func() {
		params, err := HandleHandshake(serverConn)
		resChan <- result{params, err}
	}()

	// Read and verify server responses on client side
	// 1. AuthenticationOk ('R')
	typ, respPayload, err := ReadMessage(clientConn)
	if err != nil {
		t.Fatalf("failed to read AuthenticationOk: %v", err)
	}
	if typ != 'R' {
		t.Errorf("expected type 'R', got %q", typ)
	}
	if !bytes.Equal(respPayload, []byte{0, 0, 0, 0}) {
		t.Errorf("expected AuthOk code 0, got %v", respPayload)
	}

	// 2-4. ParameterStatus ('S') - server_version, client_encoding, DateStyle
	expectedParams := map[string]string{
		"server_version":  "16.0",
		"client_encoding": "UTF8",
		"DateStyle":       "ISO",
	}

	for i := 0; i < 3; i++ {
		typ, respPayload, err = ReadMessage(clientConn)
		if err != nil {
			t.Fatalf("failed to read ParameterStatus %d: %v", i, err)
		}
		if typ != 'S' {
			t.Fatalf("expected type 'S', got %q", typ)
		}

		// Parse key/value from payload
		idx := 0
		for idx < len(respPayload) && respPayload[idx] != 0 {
			idx++
		}
		if idx >= len(respPayload) {
			t.Fatal("parameter key not null-terminated")
		}
		key := string(respPayload[0:idx])
		idx++ // skip null

		valStart := idx
		for idx < len(respPayload) && respPayload[idx] != 0 {
			idx++
		}
		if idx >= len(respPayload) {
			t.Fatal("parameter value not null-terminated")
		}
		val := string(respPayload[valStart:idx])

		expectedVal, exists := expectedParams[key]
		if !exists {
			t.Errorf("unexpected ParameterStatus key: %q", key)
		} else if expectedVal != val {
			t.Errorf("expected value %q for key %q, got %q", expectedVal, key, val)
		}
	}

	// 5. BackendKeyData ('K')
	typ, respPayload, err = ReadMessage(clientConn)
	if err != nil {
		t.Fatalf("failed to read BackendKeyData: %v", err)
	}
	if typ != 'K' {
		t.Errorf("expected type 'K', got %q", typ)
	}
	if len(respPayload) != 8 {
		t.Errorf("expected BackendKeyData payload of 8 bytes, got %d", len(respPayload))
	}

	// 6. ReadyForQuery ('Z')
	typ, respPayload, err = ReadMessage(clientConn)
	if err != nil {
		t.Fatalf("failed to read ReadyForQuery: %v", err)
	}
	if typ != 'Z' {
		t.Errorf("expected type 'Z', got %q", typ)
	}
	if !bytes.Equal(respPayload, []byte{'I'}) {
		t.Errorf("expected ReadyForQuery status 'I', got %v", respPayload)
	}

	res := <-resChan
	if res.err != nil {
		t.Fatalf("HandleHandshake failed: %v", res.err)
	}
	params := res.params

	// Verify returned params
	if params["user"] != "postgres" {
		t.Errorf("expected user postgres, got %q", params["user"])
	}
	if params["database"] != "testdb" {
		t.Errorf("expected database testdb, got %q", params["database"])
	}
}

func TestHandleHandshake_SSLRequestFallback(t *testing.T) {
	clientConn, serverConn := net.Pipe()
	defer clientConn.Close()
	defer serverConn.Close()

	startupDone := make(chan struct{})
	// Write SSLRequest first, then StartupMessage from client side in a separate goroutine
	go func() {
		// SSLRequest
		sslBuf := make([]byte, 8)
		binary.BigEndian.PutUint32(sslBuf[0:4], 8)
		binary.BigEndian.PutUint32(sslBuf[4:8], CodeSSLRequest)
		_, _ = clientConn.Write(sslBuf)

		// Read SSL response ('N')
		resp := make([]byte, 1)
		_, _ = io.ReadFull(clientConn, resp)
		if resp[0] != 'N' {
			close(startupDone)
			return
		}

		// Write actual StartupMessage
		payload := []byte("user\x00postgres\x00\x00")
		length := uint32(8 + len(payload))
		startupBuf := make([]byte, 8)
		binary.BigEndian.PutUint32(startupBuf[0:4], length)
		binary.BigEndian.PutUint32(startupBuf[4:8], CodeProtocolV3)
		_, _ = clientConn.Write(startupBuf)
		_, _ = clientConn.Write(payload)
		close(startupDone)
	}()

	type result struct {
		params map[string]string
		err    error
	}
	resChan := make(chan result, 1)
	go func() {
		params, err := HandleHandshake(serverConn)
		resChan <- result{params, err}
	}()

	<-startupDone

	// Read messages to clean up/verify from clientConn
	// Consume until ReadyForQuery ('Z') is received
	for {
		typ, _, err := ReadMessage(clientConn)
		if err != nil {
			t.Errorf("failed to read handshake response: %v", err)
			break
		}
		if typ == 'Z' {
			break
		}
	}

	res := <-resChan
	if res.err != nil {
		t.Fatalf("HandleHandshake failed: %v", res.err)
	}
	params := res.params

	if params["user"] != "postgres" {
		t.Errorf("expected user postgres, got %q", params["user"])
	}
}

func TestHandleHandshake_UnsupportedProtocol(t *testing.T) {
	clientConn, serverConn := net.Pipe()
	defer clientConn.Close()
	defer serverConn.Close()

	go func() {
		buf := make([]byte, 8)
		binary.BigEndian.PutUint32(buf[0:4], 8)
		binary.BigEndian.PutUint32(buf[4:8], 12345) // invalid code
		_, _ = clientConn.Write(buf)
	}()

	_, err := HandleHandshake(serverConn)
	if err == nil {
		t.Error("expected error for unsupported protocol version, got nil")
	}
}
