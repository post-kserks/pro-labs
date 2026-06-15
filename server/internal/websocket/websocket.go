package websocket

import (
	"bufio"
	"crypto/sha1"
	"encoding/base64"
	"fmt"
	"net"
	"net/http"
)

// Upgrade upgrades the HTTP connection to a WebSocket connection.
func Upgrade(w http.ResponseWriter, r *http.Request) (net.Conn, *bufio.ReadWriter, error) {
	if r.Header.Get("Upgrade") != "websocket" {
		return nil, nil, fmt.Errorf("not a websocket upgrade request")
	}

	hj, ok := w.(http.Hijacker)
	if !ok {
		return nil, nil, fmt.Errorf("webserver doesn't support hijacking")
	}

	conn, bufrw, err := hj.Hijack()
	if err != nil {
		return nil, nil, err
	}

	// Handshake
	key := r.Header.Get("Sec-WebSocket-Key")
	accept := computeAcceptKey(key)

	header := "HTTP/1.1 101 Switching Protocols\r\n" +
		"Upgrade: websocket\r\n" +
		"Connection: Upgrade\r\n" +
		"Sec-WebSocket-Accept: " + accept + "\r\n\r\n"

	if _, err := bufrw.WriteString(header); err != nil {
		conn.Close()
		return nil, nil, err
	}
	if err := bufrw.Flush(); err != nil {
		conn.Close()
		return nil, nil, err
	}

	return conn, bufrw, nil
}

func computeAcceptKey(key string) string {
	const magic = "258EAFA5-E914-47DA-95CA-C5AB0DC85B11"
	h := sha1.New()
	h.Write([]byte(key))
	h.Write([]byte(magic))
	return base64.StdEncoding.EncodeToString(h.Sum(nil))
}

// WriteJSON sends a JSON message over WebSocket.
// This is a VERY simplified implementation for broadcasting.
func WriteJSON(bufrw *bufio.ReadWriter, v interface{}) error {
	return fmt.Errorf("websocket not implemented; use SSE for live queries")
}
