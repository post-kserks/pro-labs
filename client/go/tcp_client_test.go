package vaultdb

import (
	"bufio"
	"encoding/json"
	"net"
	"strconv"
	"testing"
	"time"
)

func TestTCPClientStruct(t *testing.T) {
	// Verify the struct compiles and can be constructed
	c := &TCPClient{
		token:     "test",
		connected: true,
	}
	if c.token != "test" {
		t.Errorf("expected token 'test', got %q", c.token)
	}
	if !c.connected {
		t.Error("expected connected to be true")
	}
}

func TestTCPResultStruct(t *testing.T) {
	result := &TCPResult{
		Status:   "ok",
		Type:     "select",
		Columns:  []string{"id", "name"},
		Rows:     [][]string{{"1", "Alice"}, {"2", "Bob"}},
		Affected: 0,
	}
	if result.Status != "ok" {
		t.Errorf("expected status 'ok', got %q", result.Status)
	}
	if len(result.Columns) != 2 {
		t.Errorf("expected 2 columns, got %d", len(result.Columns))
	}
	if len(result.Rows) != 2 {
		t.Errorf("expected 2 rows, got %d", len(result.Rows))
	}
}

func TestParamTypeConversion(t *testing.T) {
	tests := []struct {
		input    string
		expected interface{}
	}{
		{"42", int64(42)},
		{"3.14", float64(3.14)},
		{"true", true},
		{"false", false},
		{"hello", "hello"},
	}

	for _, tt := range tests {
		var result interface{}
		if v, err := strconv.ParseInt(tt.input, 10, 64); err == nil {
			result = v
		} else if v, err := strconv.ParseFloat(tt.input, 64); err == nil {
			result = v
		} else if tt.input == "true" {
			result = true
		} else if tt.input == "false" {
			result = false
		} else {
			result = tt.input
		}

		if result != tt.expected {
			t.Errorf("input %q: expected %v (%T), got %v (%T)", tt.input, tt.expected, tt.expected, result, result)
		}
	}
}

func TestGetHelper(t *testing.T) {
	m := map[string]interface{}{
		"status":  "ok",
		"message": "done",
		"count":   42,
	}

	if got := getString(m, "status"); got != "ok" {
		t.Errorf("expected 'ok', got %q", got)
	}
	if got := getString(m, "message"); got != "done" {
		t.Errorf("expected 'done', got %q", got)
	}
	if got := getString(m, "missing"); got != "" {
		t.Errorf("expected empty string, got %q", got)
	}
	if got := getString(m, "count"); got != "" {
		t.Errorf("expected empty string for non-string, got %q", got)
	}
}

func TestTCPDialConnectionRefused(t *testing.T) {
	_, err := TCPDial("localhost:1", "")
	if err == nil {
		t.Fatal("expected error on connection refused")
	}
}

func TestTCPClientClose(t *testing.T) {
	// Start a mock TCP server
	ln, err := net.Listen("tcp", "localhost:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()

	addr := ln.Addr().String()

	// Accept connection and send handshake response in background
	go func() {
		conn, err := ln.Accept()
		if err != nil {
			return
		}
		defer conn.Close()
		scanner := bufio.NewScanner(conn)
		scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)

		// Read handshake request
		if scanner.Scan() {
			// Send handshake response
			resp := map[string]interface{}{
				"type":             "handshake",
				"protocol_version": "2.0",
				"server_version":   "2.0.0",
			}
			data, _ := json.Marshal(resp)
			data = append(data, '\n')
			conn.Write(data)
		}

		// Keep connection open until client closes
		scanner.Scan()
	}()

	c, err := TCPDial(addr, "")
	if err != nil {
		t.Fatalf("TCPDial failed: %v", err)
	}

	if c.ServerVersion() != "2.0.0" {
		t.Errorf("expected server version '2.0.0', got %q", c.ServerVersion())
	}
	if c.ProtocolVersion() != "2.0" {
		t.Errorf("expected protocol version '2.0', got %q", c.ProtocolVersion())
	}

	// Close should work without error
	if err := c.Close(); err != nil {
		t.Errorf("Close failed: %v", err)
	}

	// Query after close should fail
	_, err = c.Query("", "SELECT 1")
	if err == nil {
		t.Error("expected error after close")
	}
}

func TestTCPClientHandshakeFailure(t *testing.T) {
	ln, err := net.Listen("tcp", "localhost:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()

	addr := ln.Addr().String()

	// Server sends non-handshake response
	go func() {
		conn, err := ln.Accept()
		if err != nil {
			return
		}
		defer conn.Close()
		scanner := bufio.NewScanner(conn)
		scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)

		if scanner.Scan() {
			resp := map[string]interface{}{
				"type":    "error",
				"message": "not ready",
			}
			data, _ := json.Marshal(resp)
			data = append(data, '\n')
			conn.Write(data)
		}
	}()

	_, err = TCPDial(addr, "")
	if err == nil {
		t.Fatal("expected handshake error")
	}
}

func TestTCPClientQueryRoundTrip(t *testing.T) {
	ln, err := net.Listen("tcp", "localhost:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()

	addr := ln.Addr().String()

	go func() {
		conn, err := ln.Accept()
		if err != nil {
			return
		}
		defer conn.Close()
		scanner := bufio.NewScanner(conn)
		scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)

		// Handshake
		if scanner.Scan() {
			resp := map[string]interface{}{
				"type":             "handshake",
				"protocol_version": "2.0",
				"server_version":   "2.0.0",
			}
			data, _ := json.Marshal(resp)
			data = append(data, '\n')
			conn.Write(data)
		}

		// Query
		if scanner.Scan() {
			var req map[string]interface{}
			json.Unmarshal(scanner.Bytes(), &req)

			resp := map[string]interface{}{
				"id":      req["id"],
				"status":  "ok",
				"type":    "select",
				"columns": []interface{}{"id", "name"},
				"rows": []interface{}{
					[]interface{}{1, "Alice"},
					[]interface{}{2, "Bob"},
				},
			}
			data, _ := json.Marshal(resp)
			data = append(data, '\n')
			conn.Write(data)
		}
	}()

	c, err := TCPDial(addr, "test-token")
	if err != nil {
		t.Fatalf("TCPDial failed: %v", err)
	}
	defer c.Close()

	result, err := c.Query("testdb", "SELECT id, name FROM users")
	if err != nil {
		t.Fatalf("Query failed: %v", err)
	}
	if result.Status != "ok" {
		t.Errorf("expected status 'ok', got %q", result.Status)
	}
	if result.Type != "select" {
		t.Errorf("expected type 'select', got %q", result.Type)
	}
	if len(result.Columns) != 2 || result.Columns[0] != "id" || result.Columns[1] != "name" {
		t.Errorf("unexpected columns: %v", result.Columns)
	}
	if len(result.Rows) != 2 {
		t.Fatalf("expected 2 rows, got %d", len(result.Rows))
	}
	if result.Rows[0][0] != "1" || result.Rows[0][1] != "Alice" {
		t.Errorf("unexpected row 0: %v", result.Rows[0])
	}
	if result.Rows[1][0] != "2" || result.Rows[1][1] != "Bob" {
		t.Errorf("unexpected row 1: %v", result.Rows[1])
	}
}

func TestTCPClientQueryError(t *testing.T) {
	ln, err := net.Listen("tcp", "localhost:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()

	addr := ln.Addr().String()

	go func() {
		conn, err := ln.Accept()
		if err != nil {
			return
		}
		defer conn.Close()
		scanner := bufio.NewScanner(conn)
		scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)

		// Handshake
		if scanner.Scan() {
			resp := map[string]interface{}{
				"type":             "handshake",
				"protocol_version": "2.0",
				"server_version":   "2.0.0",
			}
			data, _ := json.Marshal(resp)
			data = append(data, '\n')
			conn.Write(data)
		}

		// Query error
		if scanner.Scan() {
			resp := map[string]interface{}{
				"id":      "err1",
				"status":  "error",
				"type":    "error",
				"message": "syntax error near 'INVALID'",
			}
			data, _ := json.Marshal(resp)
			data = append(data, '\n')
			conn.Write(data)
		}
	}()

	c, err := TCPDial(addr, "")
	if err != nil {
		t.Fatalf("TCPDial failed: %v", err)
	}
	defer c.Close()

	result, err := c.Query("", "INVALID SQL")
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if result.Status != "error" {
		t.Errorf("expected result status 'error', got %q", result.Status)
	}
}

func TestTCPClientBeginCommitRollback(t *testing.T) {
	ln, err := net.Listen("tcp", "localhost:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()

	addr := ln.Addr().String()
	callCount := 0

	go func() {
		conn, err := ln.Accept()
		if err != nil {
			return
		}
		defer conn.Close()
		scanner := bufio.NewScanner(conn)
		scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)

		// Handshake
		if scanner.Scan() {
			resp := map[string]interface{}{
				"type":             "handshake",
				"protocol_version": "2.0",
				"server_version":   "2.0.0",
			}
			data, _ := json.Marshal(resp)
			data = append(data, '\n')
			conn.Write(data)
		}

		// Handle 3 queries: BEGIN, COMMIT, ROLLBACK
		for scanner.Scan() {
			var req map[string]interface{}
			json.Unmarshal(scanner.Bytes(), &req)

			resp := map[string]interface{}{
				"id":     req["id"],
				"status": "ok",
				"type":   "command",
			}
			data, _ := json.Marshal(resp)
			data = append(data, '\n')
			conn.Write(data)
			callCount++
		}
	}()

	c, err := TCPDial(addr, "")
	if err != nil {
		t.Fatalf("TCPDial failed: %v", err)
	}
	defer c.Close()

	if err := c.Begin(); err != nil {
		t.Errorf("Begin failed: %v", err)
	}
	if err := c.Commit(); err != nil {
		t.Errorf("Commit failed: %v", err)
	}

	// Small delay to ensure server processes before next call
	time.Sleep(10 * time.Millisecond)

	if err := c.Rollback(); err != nil {
		t.Errorf("Rollback failed: %v", err)
	}
}

func TestPackageBuilds(t *testing.T) {
	// This test just verifies the package compiles successfully
	// by running the test suite itself (which requires compilation)
	t.Log("Package compiled successfully")
}
