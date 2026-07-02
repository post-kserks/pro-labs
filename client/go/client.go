// Package vaultdb provides a Go client for the VaultDB HTTP API.
package vaultdb

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

// Client is a VaultDB HTTP API client.
type Client struct {
	baseURL    string
	token      string
	httpClient *http.Client
}

// QueryRequest represents a query to send to VaultDB.
type QueryRequest struct {
	Database string   `json:"database,omitempty"`
	Query    string   `json:"query"`
	Params   []string `json:"params,omitempty"`
}

// QueryResponse represents the response from a VaultDB query.
type QueryResponse struct {
	Status     string           `json:"status"`
	Type       string           `json:"type"`
	Columns    []string         `json:"columns"`
	Rows       [][]interface{} `json:"rows"`
	Affected   int              `json:"affected"`
	DurationMs int64            `json:"duration_ms"`
	Message    string           `json:"message,omitempty"`
	Error      string           `json:"error,omitempty"`
}

// BatchQueryItem represents a single query in a batch request.
type BatchQueryItem struct {
	Query  string   `json:"query"`
	Params []string `json:"params,omitempty"`
}

// BatchRequest represents a batch of queries to send to VaultDB.
type BatchRequest struct {
	Queries  []BatchQueryItem `json:"queries"`
	Database string           `json:"database,omitempty"`
}

// BatchResponseResult represents the result of a single query in a batch response.
type BatchResponseResult struct {
	Status     string           `json:"status"`
	Type       string           `json:"type"`
	Columns    []string         `json:"columns"`
	Rows       [][]interface{} `json:"rows"`
	Affected   int              `json:"affected"`
	DurationMs int64            `json:"duration_ms"`
	Message    string           `json:"message,omitempty"`
	Error      string           `json:"error,omitempty"`
}

// BatchResponse represents the response from a batch query.
type BatchResponse struct {
	Results []BatchResponseResult `json:"results"`
}

// Option configures a Client.
type Option func(*Client)

// WithHTTPClient sets a custom HTTP client.
func WithHTTPClient(c *http.Client) Option {
	return func(cl *Client) {
		cl.httpClient = c
	}
}

// NewClient creates a new VaultDB client.
// baseURL is the server address (e.g., "http://localhost:8080").
// token is the authentication token (empty string disables auth).
func NewClient(baseURL, token string, opts ...Option) *Client {
	baseURL = strings.TrimRight(baseURL, "/")
	c := &Client{
		baseURL: baseURL,
		token:   token,
		httpClient: &http.Client{
			Timeout: 30 * time.Second,
		},
	}
	for _, opt := range opts {
		opt(c)
	}
	return c
}

// Query executes a SQL query against the specified database.
// Pass an empty string for database to use the default database.
func (c *Client) Query(database, query string, params ...string) (*QueryResponse, error) {
	req := QueryRequest{
		Database: database,
		Query:    query,
		Params:   params,
	}
	return c.doQuery(req)
}

// doQuery sends a query request and returns the response.
func (c *Client) doQuery(req QueryRequest) (*QueryResponse, error) {
	body, err := json.Marshal(req)
	if err != nil {
		return nil, fmt.Errorf("marshal request: %w", err)
	}

	httpReq, err := http.NewRequest(http.MethodPost, c.baseURL+"/api/query", bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("create request: %w", err)
	}
	httpReq.Header.Set("Content-Type", "application/json")
	if c.token != "" {
		httpReq.Header.Set("Authorization", "Bearer "+c.token)
	}

	resp, err := c.httpClient.Do(httpReq)
	if err != nil {
		return nil, fmt.Errorf("execute query: %w", err)
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("read response: %w", err)
	}

	if resp.StatusCode >= 400 {
		var errResp QueryResponse
		if err := json.Unmarshal(respBody, &errResp); err == nil && errResp.Error != "" {
			return nil, fmt.Errorf("query error (%d): %s", resp.StatusCode, errResp.Error)
		}
		return nil, fmt.Errorf("query failed with status %d: %s", resp.StatusCode, string(respBody))
	}

	var queryResp QueryResponse
	if err := json.Unmarshal(respBody, &queryResp); err != nil {
		return nil, fmt.Errorf("decode response: %w", err)
	}
	return &queryResp, nil
}

// Batch executes multiple queries in a single request.
func (c *Client) Batch(database string, queries ...BatchQueryItem) (*BatchResponse, error) {
	req := BatchRequest{
		Queries:  queries,
		Database: database,
	}
	body, err := json.Marshal(req)
	if err != nil {
		return nil, fmt.Errorf("marshal request: %w", err)
	}

	httpReq, err := http.NewRequest(http.MethodPost, c.baseURL+"/api/batch", bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("create request: %w", err)
	}
	httpReq.Header.Set("Content-Type", "application/json")
	if c.token != "" {
		httpReq.Header.Set("Authorization", "Bearer "+c.token)
	}

	resp, err := c.httpClient.Do(httpReq)
	if err != nil {
		return nil, fmt.Errorf("execute batch: %w", err)
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("read response: %w", err)
	}

	if resp.StatusCode >= 400 {
		return nil, fmt.Errorf("batch failed with status %d: %s", resp.StatusCode, string(respBody))
	}

	var batchResp BatchResponse
	if err := json.Unmarshal(respBody, &batchResp); err != nil {
		return nil, fmt.Errorf("decode response: %w", err)
	}
	return &batchResp, nil
}
