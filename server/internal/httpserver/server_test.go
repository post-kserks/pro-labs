package httpserver

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"vaultdb/internal/auth"
	"vaultdb/internal/executor"
	"vaultdb/internal/metrics"
	"vaultdb/internal/parser"
	"vaultdb/internal/storage"
	"vaultdb/internal/txmanager"
)

func newTestServer(t *testing.T, authMgr *auth.Manager) *Server {
	t.Helper()
	dir := t.TempDir()
	txm := txmanager.NewManager()
	store, err := storage.NewPageStorageEngine(dir, nil, txm)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })

	sess := executor.NewSession(store, metrics.New(), txmanager.NewManager(), executor.NewBroadcaster())
	for _, q := range []string{
		"CREATE DATABASE shop;",
		"USE shop;",
		"CREATE TABLE users (id INT, name TEXT, age INT);",
		"INSERT INTO users (id, name, age) VALUES (1, 'alice', 30), (2, 'bob', 17), (3, 'carol', 45);",
	} {
		stmt, err := parser.Parse(q)
		if err != nil {
			t.Fatalf("parse %q: %v", q, err)
		}
		if _, err := sess.Execute(stmt); err != nil {
			t.Fatalf("exec %q: %v", q, err)
		}
	}

	return New(Config{
		Storage:     store,
		Auth:        authMgr,
		Metrics:     metrics.New(),
		TxManager:   txmanager.NewManager(),
		Broadcaster: executor.NewBroadcaster(),
	})
}

func mustAuth(t *testing.T, enabled bool, tokens map[string]string) *auth.Manager {
	t.Helper()
	m, err := auth.New(enabled, tokens, nil)
	if err != nil {
		t.Fatalf("auth.New: %v", err)
	}
	return m
}

type tableDataResponse struct {
	Rows [][]string `json:"Rows"`
}

func getTableData(t *testing.T, srv *Server, query url.Values) *httptest.ResponseRecorder {
	t.Helper()
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/databases/shop/tables/users/data?"+query.Encode(), nil)
	srv.apiMux().ServeHTTP(rec, req)
	return rec
}

func TestTableDataFilter(t *testing.T) {
	srv := newTestServer(t, mustAuth(t, false, nil))

	rec := getTableData(t, srv, url.Values{"age": {"gt.20"}})
	if rec.Code != http.StatusOK {
		t.Fatalf("status %d: %s", rec.Code, rec.Body.String())
	}
	var res tableDataResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &res); err != nil {
		t.Fatal(err)
	}
	if len(res.Rows) != 2 {
		t.Fatalf("age>20 returned %d rows, want 2: %v", len(res.Rows), res.Rows)
	}
}

func TestTableDataInjectionAttempt(t *testing.T) {
	srv := newTestServer(t, mustAuth(t, false, nil))

	// Classic quote-breakout; must be treated as a literal string, matching nothing.
	rec := getTableData(t, srv, url.Values{"name": {"eq.x' OR '1'='1"}})
	if rec.Code != http.StatusOK {
		t.Fatalf("status %d: %s", rec.Code, rec.Body.String())
	}
	var res tableDataResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &res); err != nil {
		t.Fatal(err)
	}
	if len(res.Rows) != 0 {
		t.Fatalf("injection attempt returned %d rows, want 0: %v", len(res.Rows), res.Rows)
	}
}

func TestTableDataUnknownColumn(t *testing.T) {
	srv := newTestServer(t, mustAuth(t, false, nil))

	rec := getTableData(t, srv, url.Values{"1=1; DROP TABLE users": {"eq.x"}})
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("unknown column accepted: status %d body %s", rec.Code, rec.Body.String())
	}
}

func TestQueryRejectsTransactions(t *testing.T) {
	srv := newTestServer(t, mustAuth(t, false, nil))

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/query",
		strings.NewReader(`{"database":"shop","query":"BEGIN;"}`))
	srv.apiMux().ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("BEGIN over HTTP accepted: status %d body %s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "transactions") {
		t.Fatalf("error message not explanatory: %s", rec.Body.String())
	}
}

func TestLiveQueryRequiresAuth(t *testing.T) {
	srv := newTestServer(t, mustAuth(t, true, map[string]string{"sekret": "ci"}))

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/live?database=shop&query=SELECT+*+FROM+users%3B", nil)
	srv.apiMux().ServeHTTP(rec, req)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("unauthenticated /api/live: status %d, want 401", rec.Code)
	}
}

func TestLiveQueryStreamsWithToken(t *testing.T) {
	srv := newTestServer(t, mustAuth(t, true, map[string]string{"sekret": "ci"}))

	// A cancelled context makes the SSE loop exit right after the initial result.
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet,
		"/api/live?database=shop&query=SELECT+*+FROM+users%3B", nil).WithContext(ctx)
	req.Header.Set("Accept", "text/event-stream")
	req.Header.Set("Authorization", "Bearer sekret")
	srv.apiMux().ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("authorized /api/live: status %d body %s", rec.Code, rec.Body.String())
	}
	if ct := rec.Header().Get("Content-Type"); ct != "text/event-stream" {
		t.Fatalf("Content-Type %q, want text/event-stream", ct)
	}
	if !strings.Contains(rec.Body.String(), "data: ") {
		t.Fatalf("no SSE payload in response: %q", rec.Body.String())
	}
}
