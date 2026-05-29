package main

import (
	"bufio"
	"encoding/json"
	"fmt"
	"net"
	"strconv"
	"strings"
	"time"
)

// vdbConn is a minimal VaultDB TCP client (newline-delimited JSON).
type vdbConn struct {
	conn net.Conn
	r    *bufio.Reader
	id   int
}

type vdbRequest struct {
	ID    string `json:"id"`
	Query string `json:"query"`
}

type vdbResponse struct {
	Status   string     `json:"status"`
	Type     string     `json:"type"`
	Columns  []string   `json:"columns"`
	Rows     [][]string `json:"rows"`
	Affected int        `json:"affected"`
	Message  string     `json:"message"`
}

func dial(addr string, timeout time.Duration) (*vdbConn, error) {
	deadline := time.Now().Add(timeout)
	var lastErr error
	for time.Now().Before(deadline) {
		c, err := net.DialTimeout("tcp", addr, 3*time.Second)
		if err == nil {
			return &vdbConn{conn: c, r: bufio.NewReaderSize(c, 1<<20)}, nil
		}
		lastErr = err
		time.Sleep(2 * time.Second)
	}
	return nil, fmt.Errorf("could not connect to %s: %w", addr, lastErr)
}

func (c *vdbConn) exec(query string) (*vdbResponse, error) {
	c.id++
	req := vdbRequest{ID: strconv.Itoa(c.id), Query: query}
	payload, _ := json.Marshal(req)
	payload = append(payload, '\n')

	_ = c.conn.SetDeadline(time.Now().Add(30 * time.Second))
	if _, err := c.conn.Write(payload); err != nil {
		return nil, err
	}
	line, err := c.r.ReadBytes('\n')
	if err != nil {
		return nil, err
	}
	var resp vdbResponse
	if err := json.Unmarshal(line, &resp); err != nil {
		return nil, err
	}
	if resp.Status != "ok" {
		return &resp, fmt.Errorf("%s", resp.Message)
	}
	return &resp, nil
}

func (c *vdbConn) close() { _ = c.conn.Close() }

// execIgnore runs a statement, tolerating errors that contain any substr.
func (c *vdbConn) execIgnore(query string, substrs ...string) error {
	_, err := c.exec(query)
	if err == nil {
		return nil
	}
	for _, s := range substrs {
		if strings.Contains(strings.ToLower(err.Error()), strings.ToLower(s)) {
			return nil
		}
	}
	return err
}
