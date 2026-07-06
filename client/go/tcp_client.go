package vaultdb

import (
	"bufio"
	"encoding/json"
	"fmt"
	"net"
	"strconv"
	"sync"
	"time"
)

// TCPClient is a VaultDB TCP protocol client.
type TCPClient struct {
	conn            net.Conn
	scanner         *bufio.Scanner
	mu              sync.Mutex
	token           string
	protocolVersion string
	serverVersion   string
	features        []string
	connected       bool
}

// TCPResult represents a query result from TCP client.
type TCPResult struct {
	Status   string     `json:"status"`
	Type     string     `json:"type"`
	Columns  []string   `json:"columns"`
	Rows     [][]string `json:"rows"`
	Affected int        `json:"affected"`
	Message  string     `json:"message,omitempty"`
	AsOfNote string     `json:"as_of_note,omitempty"`
}

// TCPDial connects to VaultDB server and performs handshake.
func TCPDial(addr, token string) (*TCPClient, error) {
	conn, err := net.DialTimeout("tcp", addr, 10*time.Second)
	if err != nil {
		return nil, fmt.Errorf("connect: %w", err)
	}

	c := &TCPClient{
		conn:      conn,
		scanner:   bufio.NewScanner(conn),
		token:     token,
		connected: true,
	}
	c.scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)

	if err := c.handshake(); err != nil {
		conn.Close()
		return nil, fmt.Errorf("handshake: %w", err)
	}

	return c, nil
}

func (c *TCPClient) handshake() error {
	hsReq := map[string]interface{}{
		"type":               "handshake",
		"client_version":     "2.0",
		"client_name":        "vaultdb-go-client",
		"supported_features": []string{"time_travel", "transactions", "prepared_statements"},
	}

	if err := c.sendRaw(hsReq); err != nil {
		return fmt.Errorf("send handshake: %w", err)
	}

	var hsResp map[string]interface{}
	if err := c.readResponse(&hsResp); err != nil {
		return fmt.Errorf("read handshake response: %w", err)
	}

	if hsResp["type"] != "handshake" {
		return fmt.Errorf("expected handshake response, got %v", hsResp["type"])
	}

	if v, ok := hsResp["protocol_version"].(string); ok {
		c.protocolVersion = v
	}
	if v, ok := hsResp["server_version"].(string); ok {
		c.serverVersion = v
	}
	if f, ok := hsResp["features"].([]interface{}); ok {
		c.features = make([]string, len(f))
		for i, feat := range f {
			c.features[i] = fmt.Sprintf("%v", feat)
		}
	}

	return nil
}

// Query executes a SQL query and returns the result.
func (c *TCPClient) Query(database, sql string, params ...string) (*TCPResult, error) {
	c.mu.Lock()
	defer c.mu.Unlock()

	if !c.connected {
		return nil, fmt.Errorf("client is closed")
	}

	req := map[string]interface{}{
		"id":    strconv.FormatInt(time.Now().UnixNano(), 36),
		"token": c.token,
		"query": sql,
	}
	if database != "" {
		req["database"] = database
	}
	if len(params) > 0 {
		typedParams := make([]interface{}, len(params))
		for i, p := range params {
			if v, err := strconv.ParseInt(p, 10, 64); err == nil {
				typedParams[i] = v
			} else if v, err := strconv.ParseFloat(p, 64); err == nil {
				typedParams[i] = v
			} else if p == "true" {
				typedParams[i] = true
			} else if p == "false" {
				typedParams[i] = false
			} else {
				typedParams[i] = p
			}
		}
		req["params"] = typedParams
	}

	if err := c.sendRaw(req); err != nil {
		return nil, fmt.Errorf("send query: %w", err)
	}

	var rawResp map[string]interface{}
	if err := c.readResponse(&rawResp); err != nil {
		return nil, fmt.Errorf("read response: %w", err)
	}

	result := &TCPResult{
		Status:   getString(rawResp, "status"),
		Type:     getString(rawResp, "type"),
		Message:  getString(rawResp, "message"),
		AsOfNote: getString(rawResp, "as_of_note"),
	}

	if cols, ok := rawResp["columns"].([]interface{}); ok {
		result.Columns = make([]string, len(cols))
		for i, col := range cols {
			result.Columns[i] = fmt.Sprintf("%v", col)
		}
	}

	if rows, ok := rawResp["rows"].([]interface{}); ok {
		result.Rows = make([][]string, len(rows))
		for i, row := range rows {
			if r, ok := row.([]interface{}); ok {
				result.Rows[i] = make([]string, len(r))
				for j, cell := range r {
					result.Rows[i][j] = fmt.Sprintf("%v", cell)
				}
			}
		}
	}

	if aff, ok := rawResp["affected"].(float64); ok {
		result.Affected = int(aff)
	}

	if result.Status == "error" {
		return result, fmt.Errorf("query error: %s", result.Message)
	}

	return result, nil
}

// Begin starts a transaction.
func (c *TCPClient) Begin() error {
	_, err := c.Query("", "BEGIN;")
	return err
}

// Commit commits the current transaction.
func (c *TCPClient) Commit() error {
	_, err := c.Query("", "COMMIT;")
	return err
}

// Rollback rolls back the current transaction.
func (c *TCPClient) Rollback() error {
	_, err := c.Query("", "ROLLBACK;")
	return err
}

// Close closes the connection.
func (c *TCPClient) Close() error {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.connected = false
	return c.conn.Close()
}

// ServerVersion returns the server version from handshake.
func (c *TCPClient) ServerVersion() string {
	return c.serverVersion
}

// ProtocolVersion returns the negotiated protocol version.
func (c *TCPClient) ProtocolVersion() string {
	return c.protocolVersion
}

// Features returns the server's advertised features.
func (c *TCPClient) Features() []string {
	return c.features
}

func (c *TCPClient) sendRaw(msg interface{}) error {
	data, err := json.Marshal(msg)
	if err != nil {
		return err
	}
	data = append(data, '\n')
	_, err = c.conn.Write(data)
	return err
}

func (c *TCPClient) readResponse(target interface{}) error {
	if !c.scanner.Scan() {
		if err := c.scanner.Err(); err != nil {
			return fmt.Errorf("read: %w", err)
		}
		return fmt.Errorf("connection closed")
	}
	return json.Unmarshal(c.scanner.Bytes(), target)
}

func getString(m map[string]interface{}, key string) string {
	if v, ok := m[key].(string); ok {
		return v
	}
	return ""
}
