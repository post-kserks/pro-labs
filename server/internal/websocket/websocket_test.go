package websocket

import (
	"bufio"
	"bytes"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
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

func TestUpgradeSuccess(t *testing.T) {
	// Start a real TCP listener so we have a real conn to hijack.
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()

	// Server goroutine: accept one connection, upgrade it.
	type result struct {
		conn  net.Conn
		bufrw *bufio.ReadWriter
		err   error
	}
	ch := make(chan result, 1)

	go func() {
		clientConn, err := net.Dial("tcp", listener.Addr().String())
		if err != nil {
			ch <- result{err: err}
			return
		}

		req, _ := http.ReadRequest(bufio.NewReader(clientConn))
		// Build a minimal hijacker-backed ResponseWriter
		hj := &fakeHijacker{conn: clientConn}
		conn, bufrw, err := Upgrade(hj, req)
		ch <- result{conn: conn, bufrw: bufrw, err: err}
	}()

	// Accept the connection and send a valid HTTP upgrade request.
	conn, err := listener.Accept()
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()

	// Send a valid websocket upgrade request.
	reqStr := "GET / HTTP/1.1\r\n" +
		"Host: localhost\r\n" +
		"Upgrade: websocket\r\n" +
		"Connection: Upgrade\r\n" +
		"Sec-WebSocket-Key: dGhlIHNhbXBsZSBub25jZQ==\r\n" +
		"Sec-WebSocket-Version: 13\r\n" +
		"\r\n"
	_, err = conn.Write([]byte(reqStr))
	if err != nil {
		t.Fatal(err)
	}

	r := <-ch
	if r.err != nil {
		t.Fatalf("Upgrade failed: %v", r.err)
	}
	if r.conn == nil {
		t.Fatal("expected non-nil conn")
	}
	if r.bufrw == nil {
		t.Fatal("expected non-nil bufrw")
	}
}

// fakeHijacker wraps a net.Conn to provide http.Hijacker interface.
type fakeHijacker struct {
	conn net.Conn
	code int
}

func (f *fakeHijacker) Header() http.Header {
	return http.Header{}
}

func (f *fakeHijacker) Write(b []byte) (int, error) {
	return f.conn.Write(b)
}

func (f *fakeHijacker) WriteHeader(code int) {
	f.code = code
}

func (f *fakeHijacker) Hijack() (net.Conn, *bufio.ReadWriter, error) {
	return f.conn, bufio.NewReadWriter(bufio.NewReader(f.conn), bufio.NewWriter(f.conn)), nil
}

// nonHijacker implements http.ResponseWriter but NOT http.Hijacker.
type nonHijacker struct{}

func (n *nonHijacker) Header() http.Header         { return http.Header{} }
func (n *nonHijacker) Write(b []byte) (int, error) { return len(b), nil }
func (n *nonHijacker) WriteHeader(code int)        {}

func TestUpgradeNonHijacker(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.Header.Set("Upgrade", "websocket")

	var w http.ResponseWriter = &nonHijacker{}
	_, _, err := Upgrade(w, req)
	if err == nil {
		t.Fatal("expected error for non-hijacker")
	}
	if !strings.Contains(err.Error(), "doesn't support hijacking") {
		t.Errorf("unexpected error message: %v", err)
	}
}

// errConn is a net.Conn that returns errors on Write/Close.
type errConn struct {
	writeErr error
	closeErr error
}

func (e *errConn) Read(b []byte) (int, error)         { return 0, io.EOF }
func (e *errConn) Write(b []byte) (int, error)        { return 0, e.writeErr }
func (e *errConn) Close() error                       { return e.closeErr }
func (e *errConn) LocalAddr() net.Addr                { return nil }
func (e *errConn) RemoteAddr() net.Addr               { return nil }
func (e *errConn) SetDeadline(_ time.Time) error      { return nil }
func (e *errConn) SetReadDeadline(_ time.Time) error  { return nil }
func (e *errConn) SetWriteDeadline(_ time.Time) error { return nil }

func TestUpgradeWriteStringError(t *testing.T) {
	// Create a hijacker that returns a conn whose write fails during handshake.
	errConn := &errConn{writeErr: &net.OpError{Op: "write", Err: &net.OpError{}}}

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.Header.Set("Upgrade", "websocket")
	req.Header.Set("Sec-WebSocket-Key", "dGhlIHNhbXBsZSBub25jZQ==")

	hj := &failingHijacker{conn: errConn}
	_, _, err := Upgrade(hj, req)
	if err == nil {
		t.Fatal("expected error when WriteString fails")
	}
}

type failingHijacker struct {
	conn net.Conn
}

func (f *failingHijacker) Header() http.Header {
	return http.Header{}
}

func (f *failingHijacker) Write(b []byte) (int, error) {
	return 0, &net.OpError{Op: "write", Err: &net.OpError{}}
}

func (f *failingHijacker) WriteHeader(code int) {}

func (f *failingHijacker) Hijack() (net.Conn, *bufio.ReadWriter, error) {
	return f.conn, bufio.NewReadWriter(bufio.NewReader(f.conn), bufio.NewWriter(f.conn)), nil
}

func TestUpgradeFlushError(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.Header.Set("Upgrade", "websocket")
	req.Header.Set("Sec-WebSocket-Key", "dGhlIHNhbXBsZSBub25jZQ==")

	hj := &flushErrorHijacker{}
	_, _, err := Upgrade(hj, req)
	if err == nil {
		t.Fatal("expected error when Flush fails")
	}
}

type flushErrorHijacker struct{}

func (f *flushErrorHijacker) Header() http.Header         { return http.Header{} }
func (f *flushErrorHijacker) Write(b []byte) (int, error) { return len(b), nil }
func (f *flushErrorHijacker) WriteHeader(code int)        {}

func (f *flushErrorHijacker) Hijack() (net.Conn, *bufio.ReadWriter, error) {
	// Return a pipe-based conn (for Read/Close) and a bufrw backed by a writer
	// that succeeds on individual writes but fails on Flush (buffered writes).
	pr, pw := io.Pipe()
	_ = pw // not used directly
	conn := &pipeConn{reader: pr, writer: pw}
	bufrw := bufio.NewReadWriter(bufio.NewReader(conn), bufio.NewWriter(&flushFailWriter{}))
	return conn, bufrw, nil
}

// flushFailWriter accepts writes into the bufio buffer but fails when Flush
// tries to actually write the buffered data.
type flushFailWriter struct{}

func (f *flushFailWriter) Write(p []byte) (int, error) {
	return 0, &net.OpError{Op: "write", Err: io.ErrClosedPipe}
}

// pipeConn implements net.Conn backed by io.Pipe.
type pipeConn struct {
	reader *io.PipeReader
	writer *io.PipeWriter
}

func (c *pipeConn) Read(b []byte) (int, error)         { return c.reader.Read(b) }
func (c *pipeConn) Write(b []byte) (int, error)        { return c.writer.Write(b) }
func (c *pipeConn) Close() error                       { c.reader.Close(); return c.writer.Close() }
func (c *pipeConn) LocalAddr() net.Addr                { return nil }
func (c *pipeConn) RemoteAddr() net.Addr               { return nil }
func (c *pipeConn) SetDeadline(_ time.Time) error      { return nil }
func (c *pipeConn) SetReadDeadline(_ time.Time) error  { return nil }
func (c *pipeConn) SetWriteDeadline(_ time.Time) error { return nil }

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

func TestWriteJSONWriteError(t *testing.T) {
	// Use a closed pipe to trigger a write error.
	server, client := net.Pipe()
	server.Close()
	client.Close()

	bufrw := bufio.NewReadWriter(bufio.NewReader(client), bufio.NewWriter(client))

	payload := []byte("test")
	err := WriteJSON(bufrw, payload)
	if err == nil {
		t.Error("expected error when write fails")
	}
}
