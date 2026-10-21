package pgwire

import (
	"bytes"
	"crypto/rand"
	"crypto/tls"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"net"
)

const (
	CodeSSLRequest = uint32(80877103)
	CodeProtocolV3 = uint32(196608)
)

// HandshakeOptions configures optional behaviour of the startup handshake.
type HandshakeOptions struct {
	// TLSConfig, when non-nil, makes the server answer an SSLRequest with 'S'
	// and upgrade the connection to TLS before reading the StartupMessage.
	TLSConfig *tls.Config
	// Authenticate, when non-nil, makes the server request a cleartext
	// password (AuthenticationCleartextPassword) after the StartupMessage and
	// pass it to this callback. A non-nil error rejects the connection with a
	// FATAL 28P01 ErrorResponse.
	Authenticate func(params map[string]string, password string) error
}

// HandshakeResult is the outcome of a successful handshake.
type HandshakeResult struct {
	// Conn is the connection to use for the rest of the session. It differs
	// from the original connection when TLS was negotiated.
	Conn net.Conn
	// Params holds the StartupMessage parameters.
	Params map[string]string
	// Password is the password supplied by the client (empty if no
	// authentication was requested).
	Password string
}

// ErrAuthenticationFailed is returned when the Authenticate callback rejects
// the client's password.
var ErrAuthenticationFailed = errors.New("password authentication failed")

// maxPasswordMessageLen bounds the PasswordMessage payload during auth.
const maxPasswordMessageLen = 8 * 1024

// HandleHandshake performs the initial PG wire protocol handshake without TLS
// or authentication. It returns a map of startup parameters or an error.
func HandleHandshake(conn net.Conn) (map[string]string, error) {
	res, err := HandleHandshakeWithOptions(conn, HandshakeOptions{})
	if err != nil {
		return nil, err
	}
	return res.Params, nil
}

// HandleHandshakeWithOptions performs the initial PG wire protocol handshake,
// optionally negotiating TLS and authenticating the client.
func HandleHandshakeWithOptions(conn net.Conn, opts HandshakeOptions) (*HandshakeResult, error) {
	buf := make([]byte, 8)
	if _, err := io.ReadFull(conn, buf); err != nil {
		return nil, fmt.Errorf("failed to read startup packet header: %w", err)
	}

	length := binary.BigEndian.Uint32(buf[0:4])
	code := binary.BigEndian.Uint32(buf[4:8])

	if code == CodeSSLRequest {
		if opts.TLSConfig != nil {
			if _, err := conn.Write([]byte{'S'}); err != nil {
				return nil, fmt.Errorf("failed to write SSL response: %w", err)
			}
			tlsConn := tls.Server(conn, opts.TLSConfig)
			if err := tlsConn.Handshake(); err != nil {
				return nil, fmt.Errorf("TLS handshake failed: %w", err)
			}
			conn = tlsConn
		} else {
			// Write 'N' (SSL not supported)
			if _, err := conn.Write([]byte{'N'}); err != nil {
				return nil, fmt.Errorf("failed to write SSL response: %w", err)
			}
		}

		// Read next startup message
		if _, err := io.ReadFull(conn, buf); err != nil {
			return nil, fmt.Errorf("failed to read post-SSL startup packet header: %w", err)
		}
		length = binary.BigEndian.Uint32(buf[0:4])
		code = binary.BigEndian.Uint32(buf[4:8])
	}

	if code != CodeProtocolV3 {
		return nil, fmt.Errorf("unsupported protocol version or code: %d", code)
	}

	if length < 8 {
		return nil, fmt.Errorf("invalid startup message length: %d", length)
	}

	// StartupMessage length includes length (4) and code (4), so we read length - 8 bytes.
	payloadLen := length - 8
	if payloadLen > 1024*1024 {
		return nil, fmt.Errorf("startup message payload too large: %d", payloadLen)
	}

	payload := make([]byte, payloadLen)
	if _, err := io.ReadFull(conn, payload); err != nil {
		return nil, fmt.Errorf("failed to read startup message payload: %w", err)
	}

	params, err := parseStartupParameters(payload)
	if err != nil {
		return nil, fmt.Errorf("failed to parse startup parameters: %w", err)
	}

	var password string
	if opts.Authenticate != nil {
		// AuthenticationCleartextPassword ('R', code 3)
		if err := WriteMessage(conn, 'R', []byte{0, 0, 0, 3}); err != nil {
			return nil, fmt.Errorf("failed to write AuthenticationCleartextPassword: %w", err)
		}
		typ, msg, err := readMessageLimited(conn, maxPasswordMessageLen)
		if err != nil {
			return nil, fmt.Errorf("failed to read PasswordMessage: %w", err)
		}
		if typ != 'p' {
			_ = writeFatalError(conn, "08P01", fmt.Sprintf("expected password response, got message type %q", typ))
			return nil, fmt.Errorf("expected PasswordMessage, got %q", typ)
		}
		if i := bytes.IndexByte(msg, 0); i >= 0 {
			msg = msg[:i]
		}
		password = string(msg)
		if err := opts.Authenticate(params, password); err != nil {
			user := params["user"]
			_ = writeFatalError(conn, "28P01", fmt.Sprintf("password authentication failed for user %q", user))
			return nil, fmt.Errorf("%w: %v", ErrAuthenticationFailed, err)
		}
	}

	// Send responses:
	// 1. AuthenticationOk ('R')
	// Type: 'R', Length: 8, Code: 0 (AuthOk)
	authOk := []byte{0, 0, 0, 0}
	if err := WriteMessage(conn, 'R', authOk); err != nil {
		return nil, fmt.Errorf("failed to write AuthenticationOk: %w", err)
	}

	// 2. ParameterStatus ('S')
	// e.g., server_version -> 16.0, client_encoding -> UTF8, DateStyle -> ISO
	if err := writeParameterStatus(conn, "server_version", "16.0"); err != nil {
		return nil, fmt.Errorf("failed to write server_version: %w", err)
	}
	if err := writeParameterStatus(conn, "client_encoding", "UTF8"); err != nil {
		return nil, fmt.Errorf("failed to write client_encoding: %w", err)
	}
	if err := writeParameterStatus(conn, "DateStyle", "ISO"); err != nil {
		return nil, fmt.Errorf("failed to write DateStyle: %w", err)
	}

	// 3. BackendKeyData ('K')
	// random process ID & secret
	pid, secret, err := generateBackendKey()
	if err != nil {
		return nil, fmt.Errorf("failed to generate backend key: %w", err)
	}
	backendKey := make([]byte, 8)
	binary.BigEndian.PutUint32(backendKey[0:4], pid)
	binary.BigEndian.PutUint32(backendKey[4:8], secret)
	if err := WriteMessage(conn, 'K', backendKey); err != nil {
		return nil, fmt.Errorf("failed to write BackendKeyData: %w", err)
	}

	// 4. ReadyForQuery ('Z')
	// 'I' (idle status)
	if err := WriteMessage(conn, 'Z', []byte{'I'}); err != nil {
		return nil, fmt.Errorf("failed to write ReadyForQuery: %w", err)
	}

	return &HandshakeResult{Conn: conn, Params: params, Password: password}, nil
}

// parseStartupParameters extracts key-value pairs from the null-terminated payload.
func parseStartupParameters(payload []byte) (map[string]string, error) {
	params := make(map[string]string)
	idx := 0
	for idx < len(payload) {
		if payload[idx] == 0 {
			// Final null byte indicating end of startup message parameters
			break
		}

		// Read key
		keyStart := idx
		for idx < len(payload) && payload[idx] != 0 {
			idx++
		}
		if idx >= len(payload) {
			return nil, fmt.Errorf("key not null-terminated")
		}
		key := string(payload[keyStart:idx])
		idx++ // skip null byte

		// Read value
		valStart := idx
		for idx < len(payload) && payload[idx] != 0 {
			idx++
		}
		if idx >= len(payload) {
			return nil, fmt.Errorf("value not null-terminated")
		}
		val := string(payload[valStart:idx])
		idx++ // skip null byte

		params[key] = val
	}
	return params, nil
}

// generateBackendKey generates a random process ID and secret for BackendKeyData.
func generateBackendKey() (uint32, uint32, error) {
	b := make([]byte, 8)
	if _, err := rand.Read(b); err != nil {
		return 0, 0, err
	}
	pid := binary.BigEndian.Uint32(b[0:4])
	secret := binary.BigEndian.Uint32(b[4:8])
	if pid == 0 {
		pid = 1
	}
	return pid, secret, nil
}

// writeFatalError sends a FATAL ErrorResponse with the given SQLSTATE code.
func writeFatalError(w io.Writer, code, msg string) error {
	buf := &pgBuffer{}
	buf.writeByte('S')
	buf.writeString("FATAL")
	buf.writeByte('V')
	buf.writeString("FATAL")
	buf.writeByte('C')
	buf.writeString(code)
	buf.writeByte('M')
	buf.writeString(msg)
	buf.writeByte(0)
	return WriteMessage(w, 'E', buf.buf)
}

// writeParameterStatus sends a ParameterStatus message to the client.
func writeParameterStatus(w io.Writer, key, val string) error {
	payload := make([]byte, len(key)+1+len(val)+1)
	copy(payload, key)
	payload[len(key)] = 0
	copy(payload[len(key)+1:], val)
	payload[len(payload)-1] = 0
	return WriteMessage(w, 'S', payload)
}

// ReadMessage reads a single message from the client.
// It returns the message type, the payload, and any error.
func ReadMessage(r io.Reader) (byte, []byte, error) {
	return readMessageLimited(r, maxMessagePayload)
}

// maxMessagePayload limits a single message payload to prevent OOM (10MB).
const maxMessagePayload = 10 * 1024 * 1024

// readMessageLimited reads a single message whose payload may not exceed maxPayload bytes.
func readMessageLimited(r io.Reader, maxPayload uint32) (byte, []byte, error) {
	hdr := make([]byte, 5)
	if _, err := io.ReadFull(r, hdr); err != nil {
		return 0, nil, err
	}
	typ := hdr[0]
	length := binary.BigEndian.Uint32(hdr[1:5])
	if length < 4 {
		return 0, nil, fmt.Errorf("invalid message length: %d", length)
	}
	payloadLen := length - 4
	if payloadLen > maxPayload {
		return 0, nil, fmt.Errorf("payload length too large: %d", payloadLen)
	}
	payload := make([]byte, payloadLen)
	if _, err := io.ReadFull(r, payload); err != nil {
		return 0, nil, err
	}
	return typ, payload, nil
}

// WriteMessage writes a message to the writer with the given type and payload.
func WriteMessage(w io.Writer, typ byte, payload []byte) error {
	length := uint32(len(payload) + 4)
	hdr := make([]byte, 5)
	hdr[0] = typ
	binary.BigEndian.PutUint32(hdr[1:5], length)
	if _, err := w.Write(hdr); err != nil {
		return err
	}
	if _, err := w.Write(payload); err != nil {
		return err
	}
	return nil
}
