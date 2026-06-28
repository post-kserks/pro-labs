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
	h := sha1.New() //nolint:gosec // SHA1 required by WebSocket protocol RFC 6455
	h.Write([]byte(key))
	h.Write([]byte(magic))
	return base64.StdEncoding.EncodeToString(h.Sum(nil))
}

// WriteJSON sends a JSON message over WebSocket.
func WriteJSON(bufrw *bufio.ReadWriter, v interface{}) error {
	data, ok := v.([]byte)
	if !ok {
		return fmt.Errorf("WriteJSON requires []byte payload")
	}
	// WebSocket frame: FIN + text opcode, masked
	frame := []byte{0x81} // FIN + TEXT
	length := len(data)
	if length < 126 {
		frame = append(frame, byte(length))
	} else if length < 65536 {
		frame = append(frame, 126)
		frame = append(frame, byte(length>>8), byte(length))
	} else {
		frame = append(frame, 127)
		for i := 7; i >= 0; i-- {
			frame = append(frame, byte(length>>(8*i)))
		}
	}
	frame = append(frame, data...)
	_, err := bufrw.Write(frame)
	if err != nil {
		return err
	}
	return bufrw.Flush()
}
