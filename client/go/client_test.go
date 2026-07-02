package vaultdb

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestQuery(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/query" {
			t.Errorf("unexpected path: %s", r.URL.Path)
			http.NotFound(w, r)
			return
		}
		if r.Method != http.MethodPost {
			t.Errorf("unexpected method: %s", r.Method)
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		if r.Header.Get("Content-Type") != "application/json" {
			t.Errorf("unexpected Content-Type: %s", r.Header.Get("Content-Type"))
		}

		var req QueryRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			t.Errorf("failed to decode request: %v", err)
			http.Error(w, "bad request", http.StatusBadRequest)
			return
		}
		if req.Query != "SELECT 1" {
			t.Errorf("unexpected query: %s", req.Query)
		}
		if req.Database != "testdb" {
			t.Errorf("unexpected database: %s", req.Database)
		}

		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(QueryResponse{
			Status:  "ok",
			Type:    "select",
			Columns: []string{"id"},
			Rows:    [][]interface{}{{1}},
		})
	}))
	defer server.Close()

	client := NewClient(server.URL, "")
	resp, err := client.Query("testdb", "SELECT 1")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if resp.Status != "ok" {
		t.Errorf("unexpected status: %s", resp.Status)
	}
	if len(resp.Columns) != 1 || resp.Columns[0] != "id" {
		t.Errorf("unexpected columns: %v", resp.Columns)
	}
	if len(resp.Rows) != 1 || len(resp.Rows[0]) != 1 || resp.Rows[0][0] != float64(1) {
		t.Errorf("unexpected rows: %v", resp.Rows)
	}
}

func TestQueryWithParams(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req QueryRequest
		json.NewDecoder(r.Body).Decode(&req)

		if req.Query != "SELECT * FROM users WHERE id = $1" {
			t.Errorf("unexpected query: %s", req.Query)
		}
		if len(req.Params) != 1 || req.Params[0] != "42" {
			t.Errorf("unexpected params: %v", req.Params)
		}

		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(QueryResponse{
			Status:  "ok",
			Type:    "select",
			Columns: []string{"id", "name"},
			Rows:    [][]interface{}{{42, "Alice"}},
		})
	}))
	defer server.Close()

	client := NewClient(server.URL, "")
	resp, err := client.Query("testdb", "SELECT * FROM users WHERE id = $1", "42")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if resp.Status != "ok" {
		t.Errorf("unexpected status: %s", resp.Status)
	}
}

func TestQueryWithToken(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		auth := r.Header.Get("Authorization")
		if auth != "Bearer test-token-123" {
			t.Errorf("unexpected Authorization header: %s", auth)
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}

		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(QueryResponse{
			Status: "ok",
			Type:   "create",
		})
	}))
	defer server.Close()

	client := NewClient(server.URL, "test-token-123")
	resp, err := client.Query("testdb", "CREATE TABLE t1 (id INT)")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if resp.Status != "ok" {
		t.Errorf("unexpected status: %s", resp.Status)
	}
}

func TestQueryError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadRequest)
		json.NewEncoder(w).Encode(QueryResponse{
			Status: "error",
			Error:  "parse error: syntax error",
		})
	}))
	defer server.Close()

	client := NewClient(server.URL, "")
	_, err := client.Query("", "INVALID SQL")
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if err.Error() != "query error (400): parse error: syntax error" {
		t.Errorf("unexpected error message: %s", err.Error())
	}
}

func TestBatch(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/batch" {
			t.Errorf("unexpected path: %s", r.URL.Path)
			http.NotFound(w, r)
			return
		}
		if r.Method != http.MethodPost {
			t.Errorf("unexpected method: %s", r.Method)
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}

		var req BatchRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			t.Errorf("failed to decode request: %v", err)
			http.Error(w, "bad request", http.StatusBadRequest)
			return
		}
		if len(req.Queries) != 2 {
			t.Errorf("unexpected query count: %d", len(req.Queries))
		}

		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(BatchResponse{
			Results: []BatchResponseResult{
				{Status: "ok", Type: "select", Columns: []string{"id"}, Rows: [][]interface{}{{1}}},
				{Status: "ok", Type: "select", Columns: []string{"name"}, Rows: [][]interface{}{{"test"}}},
			},
		})
	}))
	defer server.Close()

	client := NewClient(server.URL, "")
	resp, err := client.Batch("testdb",
		BatchQueryItem{Query: "SELECT id FROM t1"},
		BatchQueryItem{Query: "SELECT name FROM t2"},
	)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(resp.Results) != 2 {
		t.Fatalf("unexpected result count: %d", len(resp.Results))
	}
	if resp.Results[0].Status != "ok" {
		t.Errorf("unexpected status: %s", resp.Results[0].Status)
	}
	if resp.Results[1].Status != "ok" {
		t.Errorf("unexpected status: %s", resp.Results[1].Status)
	}
}

func TestQueryConnectionError(t *testing.T) {
	client := NewClient("http://localhost:1", "")
	_, err := client.Query("", "SELECT 1")
	if err == nil {
		t.Fatal("expected error, got nil")
	}
}

func TestQueryEmptyResponse(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(QueryResponse{
			Status:  "ok",
			Type:    "select",
			Columns: []string{"id"},
			Rows:    [][]interface{}{},
		})
	}))
	defer server.Close()

	client := NewClient(server.URL, "")
	resp, err := client.Query("testdb", "SELECT id FROM t1 WHERE false")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if resp.Status != "ok" {
		t.Errorf("unexpected status: %s", resp.Status)
	}
	if len(resp.Rows) != 0 {
		t.Errorf("expected empty rows, got: %v", resp.Rows)
	}
}

func TestQueryAffectedRows(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req QueryRequest
		json.NewDecoder(r.Body).Decode(&req)
		if req.Query != "INSERT INTO t1 (name) VALUES ('test')" {
			t.Errorf("unexpected query: %s", req.Query)
		}

		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(QueryResponse{
			Status:   "ok",
			Type:     "insert",
			Affected: 1,
			Message:  "inserted 1 row",
		})
	}))
	defer server.Close()

	client := NewClient(server.URL, "")
	resp, err := client.Query("testdb", "INSERT INTO t1 (name) VALUES ('test')")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if resp.Affected != 1 {
		t.Errorf("unexpected affected count: %d", resp.Affected)
	}
	if resp.Message != "inserted 1 row" {
		t.Errorf("unexpected message: %s", resp.Message)
	}
}
