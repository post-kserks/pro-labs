package httpserver

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

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
	m, err := auth.New(enabled, tokens, nil, 60, 10, 300)
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

func TestRateLimitingOnAllEndpoints(t *testing.T) {
	endpoints := []struct {
		method string
		path   string
		body   string
	}{
		{http.MethodPost, "/api/query", `{"database":"shop","query":"SELECT * FROM users;"}`},
		{http.MethodGet, "/api/databases", ""},
		{http.MethodGet, "/api/databases/shop/tables", ""},
		{http.MethodGet, "/api/databases/shop/tables/users/data", ""},
	}

	for _, ep := range endpoints {
		rl := NewRateLimiter(1, 1)
		defer rl.Close()

		srv := newTestServer(t, mustAuth(t, false, nil))
		srv.cfg.RateLimiter = rl
		mux := srv.apiMux()

		rec := httptest.NewRecorder()
		var req *http.Request
		if ep.body != "" {
			req = httptest.NewRequest(ep.method, ep.path, strings.NewReader(ep.body))
		} else {
			req = httptest.NewRequest(ep.method, ep.path, nil)
		}
		mux.ServeHTTP(rec, req)
		if rec.Code == http.StatusTooManyRequests {
			t.Fatalf("first request to %s should not be rate limited", ep.path)
		}

		rec = httptest.NewRecorder()
		if ep.body != "" {
			req = httptest.NewRequest(ep.method, ep.path, strings.NewReader(ep.body))
		} else {
			req = httptest.NewRequest(ep.method, ep.path, nil)
		}
		mux.ServeHTTP(rec, req)
		if rec.Code != http.StatusTooManyRequests {
			t.Fatalf("second request to %s should be rate limited, got %d", ep.path, rec.Code)
		}
	}
}

func TestRateLimitingOnMetrics(t *testing.T) {
	rl := NewRateLimiter(1, 1)
	defer rl.Close()

	srv := newTestServer(t, mustAuth(t, false, nil))
	srv.cfg.RateLimiter = rl

	mux := srv.apiMux()

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/metrics", nil)
	mux.ServeHTTP(rec, req)
	if rec.Code == http.StatusTooManyRequests {
		t.Fatal("first request to /metrics should not be rate limited")
	}

	rec = httptest.NewRecorder()
	req = httptest.NewRequest(http.MethodGet, "/metrics", nil)
	mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusTooManyRequests {
		t.Fatalf("second request to /metrics should be rate limited, got %d", rec.Code)
	}
}

func TestHealthEndpointMonitorPort(t *testing.T) {
	srv := newTestServer(t, mustAuth(t, true, map[string]string{"sekret": "ci"}))

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/health", nil)
	srv.monitorMux().ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("monitor /health status %d, want 200: %s", rec.Code, rec.Body.String())
	}

	var body map[string]interface{}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("invalid JSON: %v", err)
	}
	if body["version"] == nil {
		t.Fatal("monitor /health should return version field")
	}
	if body["uptime_s"] == nil {
		t.Fatal("monitor /health should return uptime_s field")
	}
	if body["connections"] == nil {
		t.Fatal("monitor /health should return connections field")
	}
	if body["wal_enabled"] == nil {
		t.Fatal("monitor /health should return wal_enabled field")
	}
	if body["time_travel"] == nil {
		t.Fatal("monitor /health should return time_travel field")
	}
	checks, ok := body["checks"].(map[string]interface{})
	if !ok {
		t.Fatal("monitor /health should return checks field")
	}
	if checks["storage"] == nil {
		t.Fatal("monitor /health should return storage check")
	}
}

func TestSSEMaxDuration(t *testing.T) {
	dir := t.TempDir()
	txm := txmanager.NewManager()
	store, err := storage.NewPageStorageEngine(dir, nil, txm)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })

	br := executor.NewBroadcaster()
	sess := executor.NewSession(store, metrics.New(), txmanager.NewManager(), br)
	for _, q := range []string{
		"CREATE DATABASE testdb;",
		"USE testdb;",
		"CREATE TABLE t1 (id INT, val TEXT);",
		"INSERT INTO t1 (id, val) VALUES (1, 'a');",
	} {
		stmt, err := parser.Parse(q)
		if err != nil {
			t.Fatalf("parse %q: %v", q, err)
		}
		if _, err := sess.Execute(stmt); err != nil {
			t.Fatalf("exec %q: %v", q, err)
		}
	}

	srv := New(Config{
		Storage:                  store,
		Auth:                     mustAuth(t, false, nil),
		Metrics:                  metrics.New(),
		TxManager:                txmanager.NewManager(),
		Broadcaster:              br,
		MaxLiveQueryDurationSec:  1,
	})

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet,
		"/api/live?database=testdb&query=SELECT+*+FROM+t1%3B", nil).WithContext(ctx)
	req.Header.Set("Accept", "text/event-stream")

	go srv.apiMux().ServeHTTP(rec, req)

	time.Sleep(2 * time.Second)
	cancel()

	if rec.Code != http.StatusOK {
		t.Fatalf("status %d, want 200: %s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "data: ") {
		t.Fatalf("no SSE payload in response: %q", rec.Body.String())
	}
}

func TestStaticFileAuth(t *testing.T) {
	t.Run("unauthenticated gets 401 when auth enabled", func(t *testing.T) {
		srv := newTestServer(t, mustAuth(t, true, map[string]string{"sekret": "user"}))

		rec := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodGet, "/", nil)
		srv.apiMux().ServeHTTP(rec, req)

		if rec.Code != http.StatusUnauthorized {
			t.Fatalf("unauthenticated /: status %d, want 401: %s", rec.Code, rec.Body.String())
		}
	})

	t.Run("authenticated gets 200", func(t *testing.T) {
		srv := newTestServer(t, mustAuth(t, true, map[string]string{"sekret": "user"}))

		rec := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodGet, "/", nil)
		req.Header.Set("Authorization", "Bearer sekret")
		srv.apiMux().ServeHTTP(rec, req)

		if rec.Code != http.StatusOK {
			t.Fatalf("authenticated /: status %d, want 200: %s", rec.Code, rec.Body.String())
		}
	})

	t.Run("X-VaultDB-Token header works", func(t *testing.T) {
		srv := newTestServer(t, mustAuth(t, true, map[string]string{"sekret": "user"}))

		rec := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodGet, "/", nil)
		req.Header.Set("X-VaultDB-Token", "sekret")
		srv.apiMux().ServeHTTP(rec, req)

		if rec.Code != http.StatusOK {
			t.Fatalf("X-VaultDB-Token /: status %d, want 200: %s", rec.Code, rec.Body.String())
		}
	})

	t.Run("no auth check when auth disabled", func(t *testing.T) {
		srv := newTestServer(t, mustAuth(t, false, nil))

		rec := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodGet, "/", nil)
		srv.apiMux().ServeHTTP(rec, req)

		if rec.Code != http.StatusOK {
			t.Fatalf("unauthenticated / with auth disabled: status %d, want 200: %s", rec.Code, rec.Body.String())
		}
	})
}
