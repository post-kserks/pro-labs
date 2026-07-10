package main

import (
	"encoding/json"
	"net"
	"testing"
	"time"

	"vaultdb/internal/protocol"
)

func TestWriteResponse_Handshake(t *testing.T) {
	server, client := net.Pipe()
	defer server.Close()
	defer client.Close()

	go func() {
		resp := protocol.HandshakeResponse{
			Type:              "handshake",
			ProtocolVersion:   "2.0",
			Server:            "VaultDB",
			ServerVersion:     "2.0.0",
			SupportedFeatures: []string{"time_travel", "transactions"},
		}
		writeResponse(server, resp)
	}()

	client.SetReadDeadline(time.Now().Add(time.Second))
	buf := make([]byte, 4096)
	n, err := client.Read(buf)
	if err != nil {
		t.Fatalf("read failed: %v", err)
	}

	var hs protocol.HandshakeResponse
	if err := json.Unmarshal(buf[:n-1], &hs); err != nil { // strip trailing \n
		t.Fatalf("unmarshal failed: %v", err)
	}

	if hs.Type != "handshake" {
		t.Errorf("expected type 'handshake', got %q", hs.Type)
	}
	if hs.ProtocolVersion != "2.0" {
		t.Errorf("expected protocol_version '2.0', got %q", hs.ProtocolVersion)
	}
	if hs.Server != "VaultDB" {
		t.Errorf("expected server 'VaultDB', got %q", hs.Server)
	}
	if len(hs.SupportedFeatures) != 2 {
		t.Errorf("expected 2 supported features, got %d", len(hs.SupportedFeatures))
	}
}

func TestWriteResponse_Query(t *testing.T) {
	server, client := net.Pipe()
	defer server.Close()
	defer client.Close()

	go func() {
		resp := protocol.Response{
			ID:      "1",
			Status:  "ok",
			Type:    "query_result",
			Columns: []string{"id", "name"},
			Rows:    [][]string{{"1", "alice"}},
		}
		writeResponse(server, resp)
	}()

	client.SetReadDeadline(time.Now().Add(time.Second))
	buf := make([]byte, 4096)
	n, err := client.Read(buf)
	if err != nil {
		t.Fatalf("read failed: %v", err)
	}

	var resp protocol.Response
	if err := json.Unmarshal(buf[:n-1], &resp); err != nil {
		t.Fatalf("unmarshal failed: %v", err)
	}

	if resp.ID != "1" {
		t.Errorf("expected id '1', got %q", resp.ID)
	}
	if resp.Status != "ok" {
		t.Errorf("expected status 'ok', got %q", resp.Status)
	}
	if len(resp.Columns) != 2 {
		t.Errorf("expected 2 columns, got %d", len(resp.Columns))
	}
}

func TestHandshakeRequest_Unmarshal(t *testing.T) {
	data := `{"type":"handshake","client_version":"2.0","client_name":"test-client","supported_features":["time_travel"]}`

	var hs protocol.HandshakeRequest
	if err := json.Unmarshal([]byte(data), &hs); err != nil {
		t.Fatalf("unmarshal failed: %v", err)
	}

	if hs.Type != "handshake" {
		t.Errorf("expected type 'handshake', got %q", hs.Type)
	}
	if hs.ClientVersion != "2.0" {
		t.Errorf("expected client_version '2.0', got %q", hs.ClientVersion)
	}
	if hs.ClientName != "test-client" {
		t.Errorf("expected client_name 'test-client', got %q", hs.ClientName)
	}
	if len(hs.SupportedFeatures) != 1 {
		t.Errorf("expected 1 feature, got %d", len(hs.SupportedFeatures))
	}
}

func TestRequest_V2Fields(t *testing.T) {
	data := `{"id":"1","query":"SELECT 1","version":"2.0","params":[1,"hello"],"database":"testdb","as_of":"2024-01-01","isolation":"serializable"}`

	var req protocol.Request
	if err := json.Unmarshal([]byte(data), &req); err != nil {
		t.Fatalf("unmarshal failed: %v", err)
	}

	if req.Version != "2.0" {
		t.Errorf("expected version '2.0', got %q", req.Version)
	}
	if req.Database != "testdb" {
		t.Errorf("expected database 'testdb', got %q", req.Database)
	}
	if len(req.Params) != 2 {
		t.Errorf("expected 2 params, got %d", len(req.Params))
	}
	if req.Isolation != "serializable" {
		t.Errorf("expected isolation 'serializable', got %q", req.Isolation)
	}
}

func TestRequest_V1BackwardCompatible(t *testing.T) {
	data := `{"id":"1","token":"abc","query":"SELECT 1"}`

	var req protocol.Request
	if err := json.Unmarshal([]byte(data), &req); err != nil {
		t.Fatalf("unmarshal failed: %v", err)
	}

	if req.ID != "1" {
		t.Errorf("expected id '1', got %q", req.ID)
	}
	if req.Token != "abc" {
		t.Errorf("expected token 'abc', got %q", req.Token)
	}
	if req.Query != "SELECT 1" {
		t.Errorf("expected query 'SELECT 1', got %q", req.Query)
	}
	if req.Version != "" {
		t.Errorf("expected empty version for v1, got %q", req.Version)
	}
	if req.Database != "" {
		t.Errorf("expected empty database for v1, got %q", req.Database)
	}
}

func TestSanitizeErrorMessage(t *testing.T) {
	tests := []struct {
		input string
		want  string
	}{
		{"no active database", "no active database"},
		{"does not exist", "does not exist"},
		{"some internal details", "internal error"},
		{"unauthorized access", "unauthorized access"},
		{"rate limit exceeded", "rate limit exceeded"},
	}

	for _, tt := range tests {
		got := sanitizeErrorMessage(tt.input)
		if got != tt.want {
			t.Errorf("sanitizeErrorMessage(%q) = %q, want %q", tt.input, got, tt.want)
		}
	}
}
