package main

import (
	"bytes"
	"encoding/json"
	"log/slog"
	"net"
	"strings"
	"testing"

	"vaultdb/internal/executor"
	"vaultdb/internal/protocol"
)

// bufConn wraps a bytes.Buffer as a net.Conn for non-blocking write tests.
type bufConn struct {
	net.Conn
	buf bytes.Buffer
}

func (c *bufConn) Write(p []byte) (int, error) { return c.buf.Write(p) }
func (c *bufConn) Close() error                { return nil }
func (c *bufConn) RemoteAddr() net.Addr        { return &net.UnixAddr{} }

func TestSanitizeErrorMessage(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		expected string
	}{
		{"safe: no active database", "no active database", "no active database"},
		{"safe: does not exist", "table does not exist", "table does not exist"},
		{"safe: already exists", "table already exists", "table already exists"},
		{"safe: duplicate primary key", "duplicate primary key 42", "duplicate primary key 42"},
		{"safe: column", "column not found", "column not found"},
		{"safe: unauthorized", "unauthorized access", "unauthorized access"},
		{"safe: invalid", "invalid syntax", "invalid syntax"},
		{"safe: transaction", "transaction aborted", "transaction aborted"},
		{"safe: permission", "permission denied", "permission denied"},
		{"safe: empty", "empty input", "empty input"},
		{"safe: mismatch", "type mismatch", "type mismatch"},
		{"safe: out of range", "value out of range", "value out of range"},
		{"safe: cannot", "cannot drop table", "cannot drop table"},
		{"safe: query timeout", "query timeout exceeded", "query timeout exceeded"},
		{"safe: unknown statement", "unknown statement type", "unknown statement type"},
		{"safe: aggregate", "aggregate function error", "aggregate function error"},
		{"safe: savepoint", "savepoint not found", "savepoint not found"},
		{"safe: not supported", "feature not supported", "feature not supported"},
		{"safe: must not", "must not be null", "must not be null"},
		{"safe: missing", "missing column definition", "missing column definition"},
		{"safe: unsupported", "unsupported type", "unsupported type"},
		{"safe: expected", "value expected", "value expected"},
		{"safe: rate limit", "rate limit exceeded", "rate limit exceeded"},
		{"safe: too many", "too many connections", "too many connections"},
		{"safe: overflow", "integer overflow", "integer overflow"},
		{"case insensitive", "NO ACTIVE DATABASE", "NO ACTIVE DATABASE"},
		{"safe with extra text", "UNAUTHORIZED user login", "UNAUTHORIZED user login"},
		{"unsafe: file path", "error at /home/user/file.go:42", "internal error"},
		{"unsafe: goroutine", "panic in goroutine 42", "internal error"},
		{"unsafe: random text", "something broke badly", "internal error"},
		{"unsafe: segfault", "segfault at address 0xff", "internal error"},
		{"unsafe: EOF", "EOF", "internal error"},
		{"empty message", "", "internal error"},
		{"whitespace only", "   ", "internal error"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := sanitizeErrorMessage(tt.input)
			if got != tt.expected {
				t.Errorf("sanitizeErrorMessage(%q) = %q, want %q", tt.input, got, tt.expected)
			}
		})
	}
}

func TestSanitizeErrorMessageTruncation(t *testing.T) {
	// 180 a's + " invalid" (8) + 50 b's = 238 chars total.
	// Contains "invalid" (safe) and len > 200, so msg[:200] + "..."
	// msg[:200] = 180 a's + " invalid" + 12 b's
	input := strings.Repeat("a", 180) + " invalid" + strings.Repeat("b", 50)
	got := sanitizeErrorMessage(input)
	if len(got) != 203 {
		t.Errorf("length = %d, want 203", len(got))
	}
	if !strings.HasSuffix(got, "...") {
		t.Error("missing '...' suffix")
	}
	if got != strings.Repeat("a", 180)+" invalid"+strings.Repeat("b", 12)+"..." {
		t.Errorf("truncated content mismatch")
	}
}

func TestWriteResponse(t *testing.T) {
	tests := []struct {
		name     string
		response protocol.Response
	}{
		{
			name: "error response",
			response: protocol.Response{
				ID: "1", Status: "error", Type: "error",
				Columns: []string{}, Rows: [][]string{}, Message: "something failed",
			},
		},
		{
			name: "ok with data",
			response: protocol.Response{
				ID: "2", Status: "ok", Type: "select",
				Columns: []string{"id", "name"},
				Rows:    [][]string{{"1", "alice"}, {"2", "bob"}},
			},
		},
		{
			name: "ok with affected",
			response: protocol.Response{
				ID: "3", Status: "ok", Type: "insert",
				Columns: []string{}, Rows: [][]string{}, Affected: 5,
			},
		},
		{
			name: "with as_of_note",
			response: protocol.Response{
				ID: "4", Status: "ok", Type: "select",
				Columns: []string{"val"}, Rows: [][]string{{"42"}},
				AsOfNote: "snapshot at 100",
			},
		},
		{
			name: "empty response",
			response: protocol.Response{
				ID: "5", Status: "ok", Type: "ok",
				Columns: []string{}, Rows: [][]string{},
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			c := &bufConn{}
			if err := writeResponse(c, tt.response); err != nil {
				t.Fatalf("writeResponse error: %v", err)
			}
			raw := c.buf.Bytes()
			if len(raw) == 0 || raw[len(raw)-1] != '\n' {
				t.Fatal("response must end with newline")
			}
			var got protocol.Response
			if err := json.Unmarshal(raw[:len(raw)-1], &got); err != nil {
				t.Fatalf("JSON parse error: %v", err)
			}
			if got.ID != tt.response.ID {
				t.Errorf("ID = %q, want %q", got.ID, tt.response.ID)
			}
			if got.Status != tt.response.Status {
				t.Errorf("Status = %q, want %q", got.Status, tt.response.Status)
			}
			if got.Type != tt.response.Type {
				t.Errorf("Type = %q, want %q", got.Type, tt.response.Type)
			}
			if got.Affected != tt.response.Affected {
				t.Errorf("Affected = %d, want %d", got.Affected, tt.response.Affected)
			}
			if got.Message != tt.response.Message {
				t.Errorf("Message = %q, want %q", got.Message, tt.response.Message)
			}
			if got.AsOfNote != tt.response.AsOfNote {
				t.Errorf("AsOfNote = %q, want %q", got.AsOfNote, tt.response.AsOfNote)
			}
		})
	}
}

func TestWriteResponseClosedConn(t *testing.T) {
	server, client := net.Pipe()
	server.Close()
	client.Close()
	err := writeResponse(server, protocol.Response{ID: "1", Status: "ok"})
	if err == nil {
		t.Error("expected error writing to closed pipe")
	}
}

func TestWriteResponseMultipleWrites(t *testing.T) {
	c := &bufConn{}
	for i := 0; i < 3; i++ {
		resp := protocol.Response{
			ID: string(rune('A' + i)), Status: "ok", Type: "ok",
		}
		if err := writeResponse(c, resp); err != nil {
			t.Fatalf("write %d failed: %v", i, err)
		}
	}
	dec := json.NewDecoder(&c.buf)
	for i := 0; i < 3; i++ {
		var resp protocol.Response
		if err := dec.Decode(&resp); err != nil {
			t.Fatalf("decode %d failed: %v", i, err)
		}
		want := string(rune('A' + i))
		if resp.ID != want {
			t.Errorf("response %d ID = %q, want %q", i, resp.ID, want)
		}
	}
}

func TestSendError(t *testing.T) {
	logger := slog.Default()

	t.Run("success", func(t *testing.T) {
		c := &bufConn{}
		ok := sendError(c, "1", "something invalid happened", logger)
		if !ok {
			t.Fatal("sendError returned false")
		}
		raw := c.buf.Bytes()
		var resp protocol.Response
		json.Unmarshal(raw[:len(raw)-1], &resp)
		if resp.Status != "error" || resp.Type != "error" || resp.ID != "1" {
			t.Errorf("unexpected response: %+v", resp)
		}
		if resp.Message != "something invalid happened" {
			t.Errorf("Message = %q", resp.Message)
		}
		if len(resp.Columns) != 0 || len(resp.Rows) != 0 {
			t.Error("error response should have empty columns/rows")
		}
	})

	t.Run("sanitized internal details", func(t *testing.T) {
		c := &bufConn{}
		sendError(c, "1", "error at /internal/path.go:42", logger)
		var resp protocol.Response
		json.Unmarshal(c.buf.Bytes()[:c.buf.Len()-1], &resp)
		if resp.Message != "internal error" {
			t.Errorf("expected sanitized, got %q", resp.Message)
		}
	})

	t.Run("pipe disconnected", func(t *testing.T) {
		server, _ := net.Pipe()
		defer server.Close()
		server.Close()
		ok := sendError(server, "1", "error", logger)
		if ok {
			t.Error("expected false for closed pipe")
		}
	})
}

func TestSendResult(t *testing.T) {
	t.Run("nil result", func(t *testing.T) {
		c := &bufConn{}
		if err := sendResult(c, "1", nil); err != nil {
			t.Fatal(err)
		}
		var resp protocol.Response
		json.Unmarshal(c.buf.Bytes()[:c.buf.Len()-1], &resp)
		if resp.Status != "ok" {
			t.Errorf("Status = %q", resp.Status)
		}
		if len(resp.Columns) != 0 || len(resp.Rows) != 0 {
			t.Error("nil result should produce empty columns/rows")
		}
	})

	t.Run("with data", func(t *testing.T) {
		c := &bufConn{}
		result := &executor.Result{
			Type: "select", Columns: []string{"id", "name"},
			Rows: [][]string{{"1", "alice"}, {"2", "bob"}},
		}
		if err := sendResult(c, "42", result); err != nil {
			t.Fatal(err)
		}
		var resp protocol.Response
		json.Unmarshal(c.buf.Bytes()[:c.buf.Len()-1], &resp)
		if resp.ID != "42" || resp.Type != "select" {
			t.Errorf("unexpected: ID=%q Type=%q", resp.ID, resp.Type)
		}
		if len(resp.Columns) != 2 || len(resp.Rows) != 2 {
			t.Errorf("expected 2 cols 2 rows, got %d/%d", len(resp.Columns), len(resp.Rows))
		}
	})

	t.Run("nil columns become empty", func(t *testing.T) {
		c := &bufConn{}
		result := &executor.Result{Type: "ok"}
		sendResult(c, "1", result)
		var resp protocol.Response
		json.Unmarshal(c.buf.Bytes()[:c.buf.Len()-1], &resp)
		if len(resp.Columns) != 0 || len(resp.Rows) != 0 {
			t.Error("nil slices should become empty")
		}
	})

	t.Run("affected and message", func(t *testing.T) {
		c := &bufConn{}
		result := &executor.Result{
			Type: "insert", Affected: 3, Message: "3 rows inserted", AsOfNote: "at tx 10",
		}
		sendResult(c, "5", result)
		var resp protocol.Response
		json.Unmarshal(c.buf.Bytes()[:c.buf.Len()-1], &resp)
		if resp.Affected != 3 || resp.Message != "3 rows inserted" || resp.AsOfNote != "at tx 10" {
			t.Errorf("unexpected: %+v", resp)
		}
	})

	t.Run("pipe disconnected", func(t *testing.T) {
		server, _ := net.Pipe()
		server.Close()
		if err := sendResult(server, "1", nil); err == nil {
			t.Error("expected error")
		}
	})
}

func TestSendResultJSONFormat(t *testing.T) {
	c := &bufConn{}
	result := &executor.Result{
		Type: "select", Columns: []string{"count"}, Rows: [][]string{{"42"}},
	}
	sendResult(c, "100", result)

	raw := c.buf.String()
	for _, snippet := range []string{
		`"id":"100"`, `"status":"ok"`, `"type":"select"`,
		`"columns":["count"]`, `"rows":[["42"]]`,
	} {
		if !strings.Contains(raw, snippet) {
			t.Errorf("JSON missing %s", snippet)
		}
	}
}

func TestSanitizeSafePatternsComplete(t *testing.T) {
	patterns := []string{
		"no active database", "does not exist", "already exists",
		"duplicate primary key", "column", "unknown column",
		"unknown statement", "unauthorized", "rate limit",
		"too many", "overflow", "query timeout", "mismatch",
		"invalid", "expected", "unsupported", "empty", "savepoint",
		"transaction", "not supported", "missing", "must not",
		"out of range", "cannot", "permission", "aggregate",
	}
	for _, p := range patterns {
		msg := "test " + p + " here"
		if sanitizeErrorMessage(msg) != msg {
			t.Errorf("pattern %q not recognized", p)
		}
	}
}

func TestSanitizeUnsafePatterns(t *testing.T) {
	msgs := []string{
		"error at /home/user/file.go:42",
		"panic: runtime error",
		"segmentation fault",
		"goroutine 1 running",
		"fatal: database locked",
		"connection refused to 127.0.0.1:5432",
		"segfault at address 0xff",
	}
	for _, m := range msgs {
		if sanitizeErrorMessage(m) != "internal error" {
			t.Errorf("message %q not sanitized", m)
		}
	}
}
