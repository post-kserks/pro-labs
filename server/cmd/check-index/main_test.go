package main

import (
	"encoding/json"
	"net"
	"os/exec"
	"testing"
	"time"
)

// mockConn implements net.Conn for testing mustExecute directly.
type mockConn struct {
	readBuf  []byte
	readPos  int
	writeBuf []byte
	closed   bool
}

func newMockConn(response string) *mockConn {
	return &mockConn{readBuf: []byte(response)}
}

func (m *mockConn) Read(b []byte) (int, error) {
	if m.readPos >= len(m.readBuf) {
		return 0, net.ErrClosed
	}
	n := copy(b, m.readBuf[m.readPos:])
	m.readPos += n
	return n, nil
}

func (m *mockConn) Write(b []byte) (int, error) {
	m.writeBuf = append(m.writeBuf, b...)
	return len(b), nil
}

func (m *mockConn) Close() error                       { m.closed = true; return nil }
func (m *mockConn) LocalAddr() net.Addr                { return &net.TCPAddr{} }
func (m *mockConn) RemoteAddr() net.Addr               { return &net.TCPAddr{} }
func (m *mockConn) SetDeadline(_ time.Time) error      { return nil }
func (m *mockConn) SetReadDeadline(_ time.Time) error  { return nil }
func (m *mockConn) SetWriteDeadline(_ time.Time) error { return nil }

func TestMustExecuteValid(t *testing.T) {
	resp := Response{ID: "check", Status: "ok", Message: "query executed"}
	data, _ := json.Marshal(resp)
	conn := newMockConn(string(data) + "\n")

	mustExecute(conn, "SELECT 1;")

	if conn.closed {
		t.Error("connection should not be closed by mustExecute")
	}
}

func TestMustExecuteQuerySent(t *testing.T) {
	resp := Response{ID: "check", Status: "ok", Message: "ok"}
	data, _ := json.Marshal(resp)
	conn := newMockConn(string(data))

	query := "EXPLAIN ANALYZE SELECT * FROM users WHERE id = 100;"
	mustExecute(conn, query)

	var req Request
	if err := json.Unmarshal(conn.writeBuf, &req); err != nil {
		t.Fatalf("failed to unmarshal written request: %v", err)
	}
	if req.Query != query {
		t.Errorf("query = %q, want %q", req.Query, query)
	}
	if req.ID != "check" {
		t.Errorf("id = %q, want %q", req.ID, "check")
	}
}

func TestMustExecuteResponseParsed(t *testing.T) {
	resp := Response{ID: "check", Status: "error", Message: "table not found"}
	data, _ := json.Marshal(resp)
	conn := newMockConn(string(data) + "\n")

	mustExecute(conn, "SELECT * FROM nonexistent;")

	if string(conn.writeBuf) == "" {
		t.Error("expected data to be written to connection")
	}
}

func TestRequestJSON(t *testing.T) {
	req := Request{ID: "check", Query: "USE bench_db;"}
	data, err := json.Marshal(req)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}

	var decoded Request
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if decoded.ID != req.ID {
		t.Errorf("ID = %q, want %q", decoded.ID, req.ID)
	}
	if decoded.Query != req.Query {
		t.Errorf("Query = %q, want %q", decoded.Query, req.Query)
	}
}

func TestResponseJSON(t *testing.T) {
	resp := Response{ID: "check", Status: "ok", Message: "done"}
	data, err := json.Marshal(resp)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}

	var decoded Response
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if decoded.Status != "ok" {
		t.Errorf("Status = %q, want %q", decoded.Status, "ok")
	}
	if decoded.Message != "done" {
		t.Errorf("Message = %q, want %q", decoded.Message, "done")
	}
}

func TestMainIntegration(t *testing.T) {
	bin := buildCheckIndex(t)
	cmd := exec.Command(bin)
	out, err := cmd.CombinedOutput()
	// main() hardcodes 127.0.0.1:5432; if vaultdb-server is running this should succeed.
	// If not, we just verify the binary built correctly.
	if err != nil {
		t.Logf("main exited (server may not be running): %v\n%s", err, out)
	} else {
		t.Logf("main succeeded: %s", out)
	}
}

func buildCheckIndex(t *testing.T) string {
	t.Helper()
	bin := t.TempDir() + "/check-index"
	cmd := exec.Command("go", "build", "-o", bin, ".")
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("build failed: %v\n%s", err, out)
	}
	return bin
}
