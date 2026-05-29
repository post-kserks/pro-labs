// Package vaultdb is a thin client for the VaultDB SQL engine.
//
// It speaks the VaultDB TCP protocol (newline-delimited JSON) instead of the
// HTTP API on purpose: the TCP server keeps a *persistent session per
// connection*, which is what makes BEGIN/COMMIT/ROLLBACK transactions work
// across multiple statements. The HTTP API creates a fresh session for every
// request, so multi-statement transactions are impossible there.
package vaultdb

import (
	"bufio"
	"encoding/json"
	"fmt"
	"net"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

// request is one line sent to the VaultDB TCP server.
type request struct {
	ID    string `json:"id"`
	Query string `json:"query"`
}

// response is one line returned by the VaultDB TCP server.
type response struct {
	ID       string     `json:"id"`
	Status   string     `json:"status"`
	Type     string     `json:"type"`
	Columns  []string   `json:"columns"`
	Rows     [][]string `json:"rows"`
	Affected int        `json:"affected"`
	Message  string     `json:"message"`
	AsOfNote string     `json:"as_of_note"`
}

// Result is the decoded outcome of a query.
type Result struct {
	Type     string
	Columns  []string
	Rows     [][]string
	Affected int
	Message  string
	AsOfNote string
}

// Maps turns the column/row matrix into a slice of column->value maps so
// handlers can address fields by name. All VaultDB values are strings.
func (r *Result) Maps() []map[string]string {
	out := make([]map[string]string, 0, len(r.Rows))
	for _, row := range r.Rows {
		m := make(map[string]string, len(r.Columns))
		for i, col := range r.Columns {
			if i < len(row) {
				m[col] = row[i]
			}
		}
		out = append(out, m)
	}
	return out
}

// PlanText reconstructs the textual EXPLAIN plan from the result rows.
func (r *Result) PlanText() string {
	var b strings.Builder
	for _, row := range r.Rows {
		b.WriteString(strings.Join(row, " "))
		b.WriteByte('\n')
	}
	if b.Len() == 0 {
		return r.Message
	}
	return b.String()
}

// conn is a single live TCP session to VaultDB.
type conn struct {
	netConn net.Conn
	reader  *bufio.Reader
}

// Client is a small connection pool over the VaultDB TCP protocol.
type Client struct {
	addr      string
	database  string
	dialTO    time.Duration
	idCounter atomic.Uint64

	mu   sync.Mutex
	pool []*conn
}

// New creates a client. addr is host:port (e.g. "vaultdb:5432"); database is
// the database every connection switches to via USE on dial.
func New(addr, database string) *Client {
	return &Client{
		addr:     addr,
		database: database,
		dialTO:   5 * time.Second,
	}
}

func (c *Client) nextID() string {
	return strconv.FormatUint(c.idCounter.Add(1), 10)
}

// dial opens a new connection and selects the working database.
func (c *Client) dial() (*conn, error) {
	netConn, err := net.DialTimeout("tcp", c.addr, c.dialTO)
	if err != nil {
		return nil, fmt.Errorf("vaultdb dial %s: %w", c.addr, err)
	}
	cn := &conn{netConn: netConn, reader: bufio.NewReaderSize(netConn, 1<<20)}
	if c.database != "" {
		if _, err := c.exec(cn, "USE "+c.database+";"); err != nil {
			_ = netConn.Close()
			return nil, fmt.Errorf("vaultdb use %s: %w", c.database, err)
		}
	}
	return cn, nil
}

// exec sends a single statement on cn and reads exactly one response line.
func (c *Client) exec(cn *conn, sql string) (*Result, error) {
	req := request{ID: c.nextID(), Query: sql}
	payload, err := json.Marshal(req)
	if err != nil {
		return nil, err
	}
	payload = append(payload, '\n')

	_ = cn.netConn.SetDeadline(time.Now().Add(30 * time.Second))
	if _, err := cn.netConn.Write(payload); err != nil {
		return nil, fmt.Errorf("vaultdb write: %w", err)
	}

	line, err := cn.reader.ReadBytes('\n')
	if err != nil {
		return nil, fmt.Errorf("vaultdb read: %w", err)
	}

	var resp response
	if err := json.Unmarshal(line, &resp); err != nil {
		return nil, fmt.Errorf("vaultdb decode: %w", err)
	}
	if resp.Status != "ok" {
		msg := resp.Message
		if msg == "" {
			msg = "query failed"
		}
		return nil, &QueryError{Message: msg}
	}
	return &Result{
		Type:     resp.Type,
		Columns:  resp.Columns,
		Rows:     resp.Rows,
		Affected: resp.Affected,
		Message:  resp.Message,
		AsOfNote: resp.AsOfNote,
	}, nil
}

func (c *Client) acquire() (*conn, error) {
	c.mu.Lock()
	if n := len(c.pool); n > 0 {
		cn := c.pool[n-1]
		c.pool = c.pool[:n-1]
		c.mu.Unlock()
		return cn, nil
	}
	c.mu.Unlock()
	return c.dial()
}

func (c *Client) release(cn *conn, healthy bool) {
	if !healthy {
		_ = cn.netConn.Close()
		return
	}
	c.mu.Lock()
	if len(c.pool) < 16 {
		c.pool = append(c.pool, cn)
		c.mu.Unlock()
		return
	}
	c.mu.Unlock()
	_ = cn.netConn.Close()
}

// Query runs a statement and returns its result.
func (c *Client) Query(sql string) (*Result, error) {
	cn, err := c.acquire()
	if err != nil {
		return nil, err
	}
	res, err := c.exec(cn, sql)
	// Connection-level (transport) errors poison the connection; logical query
	// errors do not.
	_, transport := err.(*QueryError)
	c.release(cn, err == nil || transport)
	return res, err
}

// Exec runs a statement and discards the result rows.
func (c *Client) Exec(sql string) error {
	_, err := c.Query(sql)
	return err
}

// Ping verifies VaultDB is reachable and the database is selectable.
func (c *Client) Ping() error {
	cn, err := c.dial()
	if err != nil {
		return err
	}
	c.release(cn, true)
	return nil
}

// Tx is a transaction bound to a dedicated connection.
type Tx struct {
	client *Client
	cn     *conn
	done   bool
}

// Begin opens a transaction on a dedicated connection.
func (c *Client) Begin() (*Tx, error) {
	cn, err := c.acquire()
	if err != nil {
		return nil, err
	}
	if _, err := c.exec(cn, "BEGIN;"); err != nil {
		c.release(cn, isLogical(err))
		return nil, err
	}
	return &Tx{client: c, cn: cn}, nil
}

// Exec runs a statement inside the transaction.
func (t *Tx) Exec(sql string) error {
	if t.done {
		return fmt.Errorf("transaction already finished")
	}
	_, err := t.client.exec(t.cn, sql)
	return err
}

// Query runs a statement inside the transaction and returns rows.
func (t *Tx) Query(sql string) (*Result, error) {
	if t.done {
		return nil, fmt.Errorf("transaction already finished")
	}
	return t.client.exec(t.cn, sql)
}

// Commit commits the transaction and returns its connection to the pool.
func (t *Tx) Commit() error {
	if t.done {
		return nil
	}
	t.done = true
	_, err := t.client.exec(t.cn, "COMMIT;")
	t.client.release(t.cn, err == nil || isLogical(err))
	return err
}

// Rollback aborts the transaction. Safe to call after Commit (no-op).
func (t *Tx) Rollback() {
	if t.done {
		return
	}
	t.done = true
	_, err := t.client.exec(t.cn, "ROLLBACK;")
	t.client.release(t.cn, err == nil || isLogical(err))
}

// QueryError is a logical error reported by VaultDB (bad SQL, missing table…),
// as opposed to a transport failure.
type QueryError struct{ Message string }

func (e *QueryError) Error() string { return e.Message }

func isLogical(err error) bool {
	_, ok := err.(*QueryError)
	return ok
}
