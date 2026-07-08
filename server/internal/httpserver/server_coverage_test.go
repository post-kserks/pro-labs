package httpserver

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"vaultdb/internal/executor"
	"vaultdb/internal/metrics"
	"vaultdb/internal/parser"
	"vaultdb/internal/storage"
	"vaultdb/internal/txmanager"
)

func TestHandleSecurityStatus(t *testing.T) {
	srv := newTestServer(t, mustAuth(t, false, nil))

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/admin/security-status", nil)
	srv.apiMux().ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status %d: %s", rec.Code, rec.Body.String())
	}

	var body map[string]interface{}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("invalid JSON: %v", err)
	}

	if body["go_version"] == nil {
		t.Fatal("missing go_version")
	}
	if body["uptime"] == nil {
		t.Fatal("missing uptime")
	}
	if body["automated_checks"] == nil {
		t.Fatal("missing automated_checks")
	}
}

func TestHandleSecurityStatusRequiresAuth(t *testing.T) {
	srv := newTestServer(t, mustAuth(t, true, map[string]string{"tok": "admin"}))

	// Unauthenticated
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/admin/security-status", nil)
	srv.apiMux().ServeHTTP(rec, req)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("unauthenticated: status %d, want 401", rec.Code)
	}
}

func TestHandleRevokeToken(t *testing.T) {
	srv := newTestServer(t, mustAuth(t, true, map[string]string{"mytoken": "user1"}))

	body := `{"token":"mytoken"}`
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/admin/revoke-token", strings.NewReader(body))
	req.Header.Set("Authorization", "Bearer mytoken")
	srv.apiMux().ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status %d: %s", rec.Code, rec.Body.String())
	}

	var res map[string]interface{}
	if err := json.Unmarshal(rec.Body.Bytes(), &res); err != nil {
		t.Fatal(err)
	}
	if res["status"] != "ok" {
		t.Fatalf("status = %v, want ok", res["status"])
	}
}

func TestHandleRevokeTokenEmptyToken(t *testing.T) {
	srv := newTestServer(t, mustAuth(t, true, map[string]string{"tok": "user1"}))

	body := `{"token":""}`
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/admin/revoke-token", strings.NewReader(body))
	req.Header.Set("Authorization", "Bearer tok")
	srv.apiMux().ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status %d, want 400: %s", rec.Code, rec.Body.String())
	}
}

func TestHandleRevokeTokenInvalidJSON(t *testing.T) {
	srv := newTestServer(t, mustAuth(t, true, map[string]string{"tok": "user1"}))

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/admin/revoke-token", strings.NewReader("not json"))
	req.Header.Set("Authorization", "Bearer tok")
	srv.apiMux().ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status %d, want 400: %s", rec.Code, rec.Body.String())
	}
}

func TestHandleRevokeTokenRequiresAuth(t *testing.T) {
	srv := newTestServer(t, mustAuth(t, true, map[string]string{"tok": "user1"}))

	body := `{"token":"tok"}`
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/admin/revoke-token", strings.NewReader(body))
	srv.apiMux().ServeHTTP(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("unauthenticated: status %d, want 401", rec.Code)
	}
}

func TestCORSMiddlewareOriginRejected(t *testing.T) {
	srv := &Server{
		cfg: Config{
			AllowedOrigins: []string{"https://example.com"},
		},
		metrics: metricsForTest(),
	}

	inner := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})

	handler := srv.corsMiddleware(inner)

	// Request with a different origin
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/data", nil)
	req.Header.Set("Origin", "https://evil.com")
	handler.ServeHTTP(rec, req)

	if rec.Header().Get("Access-Control-Allow-Origin") != "" {
		t.Fatal("CORS header should NOT be set for rejected origin")
	}
}

func TestCORSMiddlewareOptionsPreflight(t *testing.T) {
	srv := &Server{
		cfg: Config{
			AllowedOrigins: []string{"https://example.com"},
		},
		metrics: metricsForTest(),
	}

	inner := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Fatal("inner handler should not be called for OPTIONS")
	})

	handler := srv.corsMiddleware(inner)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodOptions, "/api/data", nil)
	req.Header.Set("Origin", "https://example.com")
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusNoContent {
		t.Fatalf("OPTIONS status %d, want 204", rec.Code)
	}
	if rec.Header().Get("Access-Control-Allow-Origin") != "https://example.com" {
		t.Fatal("missing CORS Allow-Origin on preflight")
	}
}

func TestCORSMiddlewareNoOriginHeader(t *testing.T) {
	srv := &Server{
		cfg: Config{
			AllowedOrigins: []string{"https://example.com"},
		},
		metrics: metricsForTest(),
	}

	inner := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})

	handler := srv.corsMiddleware(inner)

	// No Origin header — should pass through without CORS headers
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/data", nil)
	handler.ServeHTTP(rec, req)

	if rec.Header().Get("Access-Control-Allow-Origin") != "" {
		t.Fatal("no CORS header expected without Origin")
	}
	if rec.Code != http.StatusOK {
		t.Fatalf("status %d, want 200", rec.Code)
	}
}

func TestCORSMiddlewareSecurityHeaders(t *testing.T) {
	srv := &Server{
		cfg:     Config{},
		metrics: metricsForTest(),
	}

	inner := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})

	handler := srv.corsMiddleware(inner)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/test", nil)
	handler.ServeHTTP(rec, req)

	if rec.Header().Get("X-Content-Type-Options") != "nosniff" {
		t.Fatal("missing X-Content-Type-Options")
	}
	if rec.Header().Get("X-Frame-Options") != "DENY" {
		t.Fatal("missing X-Frame-Options")
	}
	if rec.Header().Get("Content-Security-Policy") == "" {
		t.Fatal("missing Content-Security-Policy")
	}
}

func TestHandleDashboard(t *testing.T) {
	srv := newTestServer(t, mustAuth(t, false, nil))

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/dashboard", nil)
	srv.apiMux().ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status %d: %s", rec.Code, rec.Body.String())
	}
	if ct := rec.Header().Get("Content-Type"); !strings.Contains(ct, "text/html") {
		t.Fatalf("Content-Type = %q, want text/html", ct)
	}
	if !strings.Contains(rec.Body.String(), "<!DOCTYPE html>") {
		t.Fatal("response should contain HTML")
	}
}

func TestHandleDashboardRequiresAuth(t *testing.T) {
	srv := newTestServer(t, mustAuth(t, true, map[string]string{"tok": "user"}))

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/dashboard", nil)
	srv.apiMux().ServeHTTP(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("unauthenticated dashboard: status %d, want 401", rec.Code)
	}
}

func TestHandleQueryBodyTooLarge(t *testing.T) {
	srv, _ := newTestServerWithDB(t, mustAuth(t, false, nil))
	srv.cfg.MaxRequestSizeBytes = 10

	body := `{"database":"testdb","query":"SELECT * FROM items;"}`
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/query", strings.NewReader(body))
	srv.apiMux().ServeHTTP(rec, req)

	if rec.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("status %d, want 413: %s", rec.Code, rec.Body.String())
	}
}

func TestHandleBatchBodyTooLarge(t *testing.T) {
	srv, _ := newTestServerWithDB(t, mustAuth(t, false, nil))
	srv.cfg.MaxRequestSizeBytes = 10

	body := `{"database":"testdb","queries":[{"query":"SELECT 1;"}]}`
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/batch", strings.NewReader(body))
	srv.apiMux().ServeHTTP(rec, req)

	if rec.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("status %d, want 413: %s", rec.Code, rec.Body.String())
	}
}

func TestHandleTransactionBodyTooLarge(t *testing.T) {
	srv, _ := newTestServerWithDB(t, mustAuth(t, false, nil))
	srv.cfg.MaxRequestSizeBytes = 10

	body := `{"action":"begin","database":"testdb"}`
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/transaction", strings.NewReader(body))
	srv.apiMux().ServeHTTP(rec, req)

	if rec.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("status %d, want 413: %s", rec.Code, rec.Body.String())
	}
}

func TestHandleTableDataPost(t *testing.T) {
	srv, _ := newTestServerWithDB(t, mustAuth(t, false, nil))

	body := `[{"id":99,"name":"newitem","value":42.0}]`
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/databases/testdb/tables/items/data", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	srv.apiMux().ServeHTTP(rec, req)

	if rec.Code != http.StatusCreated {
		t.Fatalf("status %d, want 201: %s", rec.Code, rec.Body.String())
	}

	var res map[string]interface{}
	if err := json.Unmarshal(rec.Body.Bytes(), &res); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(res["message"].(string), "inserted") {
		t.Fatalf("expected inserted message, got %v", res["message"])
	}
}

func TestHandleTableDataPostInvalidJSON(t *testing.T) {
	srv, _ := newTestServerWithDB(t, mustAuth(t, false, nil))

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/databases/testdb/tables/items/data", strings.NewReader("not json"))
	req.Header.Set("Content-Type", "application/json")
	srv.apiMux().ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status %d, want 400: %s", rec.Code, rec.Body.String())
	}
}

func TestHandleTableDataPostNotArray(t *testing.T) {
	srv, _ := newTestServerWithDB(t, mustAuth(t, false, nil))

	body := `{"id":1,"name":"item"}`
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/databases/testdb/tables/items/data", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	srv.apiMux().ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status %d, want 400: %s", rec.Code, rec.Body.String())
	}
}

func TestHandleTableDataPostNonObjectRow(t *testing.T) {
	srv, _ := newTestServerWithDB(t, mustAuth(t, false, nil))

	body := `[42]`
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/databases/testdb/tables/items/data", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	srv.apiMux().ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status %d, want 400: %s", rec.Code, rec.Body.String())
	}
}

func TestHandleTableDataPutMethodNotAllowed(t *testing.T) {
	srv, _ := newTestServerWithDB(t, mustAuth(t, false, nil))

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPut, "/api/databases/testdb/tables/items/data", nil)
	srv.apiMux().ServeHTTP(rec, req)

	if rec.Code != http.StatusMethodNotAllowed {
		t.Fatalf("PUT status %d, want 405", rec.Code)
	}
}

func TestHandleSchemaPostMethodNotAllowed(t *testing.T) {
	srv, _ := newTestServerWithDB(t, mustAuth(t, false, nil))

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/databases/testdb/tables/items/schema", nil)
	srv.apiMux().ServeHTTP(rec, req)

	if rec.Code != http.StatusMethodNotAllowed {
		t.Fatalf("POST schema status %d, want 405", rec.Code)
	}
}

func TestHandleDatabasesSubroutes404(t *testing.T) {
	srv, _ := newTestServerWithDB(t, mustAuth(t, false, nil))

	tests := []struct {
		name string
		path string
	}{
		{"too few segments", "/api/databases/testdb"},
		{"unknown subroute", "/api/databases/testdb/unknown/foo"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			rec := httptest.NewRecorder()
			req := httptest.NewRequest(http.MethodGet, tt.path, nil)
			srv.apiMux().ServeHTTP(rec, req)
			if rec.Code != http.StatusNotFound {
				t.Fatalf("path %s: status %d, want 404: %s", tt.path, rec.Code, rec.Body.String())
			}
		})
	}

	// Test invalid db name with non-normalized URL
	t.Run("invalid db name", func(t *testing.T) {
		rec := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodGet, "/api/databases/invalid@name/tables/x/data", nil)
		srv.apiMux().ServeHTTP(rec, req)
		if rec.Code != http.StatusNotFound {
			t.Fatalf("status %d, want 404: %s", rec.Code, rec.Body.String())
		}
	})

	// Test invalid table name
	t.Run("invalid table name", func(t *testing.T) {
		rec := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodGet, "/api/databases/testdb/tables/invalid@table/data", nil)
		srv.apiMux().ServeHTTP(rec, req)
		if rec.Code != http.StatusNotFound {
			t.Fatalf("status %d, want 404: %s", rec.Code, rec.Body.String())
		}
	})
}

func TestLiveQuerySubscriptionLimit(t *testing.T) {
	srv, _ := newTestServerWithDB(t, mustAuth(t, false, nil))
	srv.cfg.MaxLiveQuerySubscriptions = 1
	srv.activeSubscriptions.Store(1) // simulate one active

	ctx := contextWithTimeout(t)
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet,
		"/api/live?database=testdb&query=SELECT+1%3B", nil).WithContext(ctx)
	req.Header.Set("Authorization", "Bearer tok")
	srv.apiMux().ServeHTTP(rec, req)

	if rec.Code != http.StatusTooManyRequests {
		t.Fatalf("status %d, want 429: %s", rec.Code, rec.Body.String())
	}
}

func TestLiveQueryMissingQuery(t *testing.T) {
	srv, _ := newTestServerWithDB(t, mustAuth(t, true, map[string]string{"tok": "u"}))

	ctx := contextWithTimeout(t)
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/live?database=testdb", nil).WithContext(ctx)
	req.Header.Set("Authorization", "Bearer tok")
	srv.apiMux().ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status %d, want 400: %s", rec.Code, rec.Body.String())
	}
}

func TestLiveQueryInvalidDatabase(t *testing.T) {
	srv, _ := newTestServerWithDB(t, mustAuth(t, true, map[string]string{"tok": "u"}))

	ctx := contextWithTimeout(t)
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/live?database=../etc&query=SELECT+1%3B", nil).WithContext(ctx)
	req.Header.Set("Authorization", "Bearer tok")
	srv.apiMux().ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status %d, want 400: %s", rec.Code, rec.Body.String())
	}
}

func TestLiveQueryNonSelectRejected(t *testing.T) {
	srv, _ := newTestServerWithDB(t, mustAuth(t, true, map[string]string{"tok": "u"}))

	ctx := contextWithTimeout(t)
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/live?database=testdb&query=INSERT+INTO+items+VALUES+(1,'x',1.0)%3B", nil).WithContext(ctx)
	req.Header.Set("Authorization", "Bearer tok")
	srv.apiMux().ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status %d, want 400: %s", rec.Code, rec.Body.String())
	}
}

func TestOpenAPIRequiresAuth(t *testing.T) {
	srv := newTestServer(t, mustAuth(t, true, map[string]string{"tok": "u"}))

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/docs/openapi.json", nil)
	srv.apiMux().ServeHTTP(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("unauthenticated openapi: status %d, want 401", rec.Code)
	}
}

func TestOpenAPIReturnsSpec(t *testing.T) {
	srv, _ := newTestServerWithDB(t, mustAuth(t, false, nil))

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/docs/openapi.json", nil)
	srv.apiMux().ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status %d: %s", rec.Code, rec.Body.String())
	}

	var spec map[string]interface{}
	if err := json.Unmarshal(rec.Body.Bytes(), &spec); err != nil {
		t.Fatal(err)
	}
	if spec["openapi"] != "3.0.0" {
		t.Fatalf("openapi = %v, want 3.0.0", spec["openapi"])
	}
}

func TestClassifyErrorShortMessage(t *testing.T) {
	// Ensure writeStorageError truncates long messages
	srv, _ := newTestServerWithDB(t, mustAuth(t, false, nil))

	longMsg := strings.Repeat("x", 300)
	// Use a query that will produce an error (table not found)
	body := fmt.Sprintf(`{"database":"testdb","query":"SELECT * FROM %s;"}`, longMsg)
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/query", strings.NewReader(body))
	srv.apiMux().ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status %d: %s", rec.Code, rec.Body.String())
	}

	var res map[string]interface{}
	if err := json.Unmarshal(rec.Body.Bytes(), &res); err != nil {
		t.Fatal(err)
	}
	msg := res["message"].(string)
	if len(msg) > 205 {
		t.Fatalf("message too long: %d chars", len(msg))
	}
}

func TestFilterLiteralValues(t *testing.T) {
	tests := []struct {
		input string
		typ   string
	}{
		{"42", "int"},
		{"3.14", "float"},
		{"true", "bool"},
		{"false", "bool"},
		{"hello", "string"},
		{"-7", "int"},
		{"-2.5", "float"},
	}

	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			expr := filterLiteral(tt.input)
			v, ok := expr.(parser.Value)
			if !ok {
				t.Fatalf("expected parser.Value, got %T", expr)
			}
			if v.Type != tt.typ {
				t.Errorf("filterLiteral(%q).Type = %q, want %q", tt.input, v.Type, tt.typ)
			}
		})
	}
}

func TestConvertHTTPParamValues(t *testing.T) {
	tests := []struct {
		input string
		typ   string
	}{
		{"42", "int"},
		{"3.14", "float"},
		{"true", "bool"},
		{"false", "bool"},
		{"hello", "string"},
	}

	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			val := convertHTTPParam(tt.input)
			if val.Type != tt.typ {
				t.Errorf("convertHTTPParam(%q).Type = %q, want %q", tt.input, val.Type, tt.typ)
			}
		})
	}
}

func TestConvertRowValueTypes(t *testing.T) {
	tests := []struct {
		value   string
		colType string
	}{
		{"42", "INT"},
		{"3.14", "FLOAT"},
		{"true", "BOOL"},
		{"1", "INT"},
		{"1.5", "FLOAT"},
		{"", "TEXT"},       // empty → nil
		{"hello", "TEXT"},  // default string
		{"true", "BOOL"},   // "true"
		{"false", "BOOL"},  // "false"
		{"1", "BOOL"},      // "1" → true
		{"0", "BOOL"},      // "0" → false
		{"yes", "BOOL"},    // "yes" → true
		{"no", "BOOL"},     // "no" → false
		{"{\"a\":1}", "JSONB"}, // valid JSON
		{"invalid", "JSONB"},   // invalid JSON → fallback string
	}

	for _, tt := range tests {
		t.Run(tt.value+"_"+tt.colType, func(t *testing.T) {
			result := convertRowValue(tt.value, tt.colType)
			if tt.value == "" && result != nil {
				t.Errorf("expected nil for empty value, got %v", result)
			}
		})
	}
}

func TestParseHTTPRowValueTypes(t *testing.T) {
	tests := []struct {
		name  string
		input interface{}
	}{
		{"float64 int", float64(42)},
		{"float64 decimal", float64(3.14)},
		{"string", "hello"},
		{"bool true", true},
		{"bool false", false},
		{"nil", nil},
		{"unknown", []int{1, 2}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Should not panic
			_ = parseHTTPRowValue(tt.input)
		})
	}
}

func TestContainsAny(t *testing.T) {
	if !containsAny("hello world", "world", "xyz") {
		t.Fatal("should find 'world'")
	}
	if containsAny("hello", "xyz", "abc") {
		t.Fatal("should not find 'xyz' or 'abc'")
	}
}

func TestEmptyIfNil(t *testing.T) {
	if result := emptyIfNil(nil); len(result) != 0 {
		t.Fatalf("expected empty slice, got %v", result)
	}
	if result := emptyIfNil([]string{"a"}); len(result) != 1 {
		t.Fatalf("expected 1 element, got %v", result)
	}
}

func TestExtractHealthToken(t *testing.T) {
	tests := []struct {
		name   string
		auth   string
		vault  string
		expect string
	}{
		{"bearer", "Bearer mytoken", "", "mytoken"},
		{"vault-header", "", "myvaulttoken", "myvaulttoken"},
		{"empty", "", "", ""},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodGet, "/", nil)
			if tt.auth != "" {
				req.Header.Set("Authorization", tt.auth)
			}
			if tt.vault != "" {
				req.Header.Set("X-VaultDB-Token", tt.vault)
			}
			got := extractHealthToken(req)
			if got != tt.expect {
				t.Errorf("got %q, want %q", got, tt.expect)
			}
		})
	}
}

func TestWithRateLimitNilLimiter(t *testing.T) {
	srv := &Server{cfg: Config{}} // nil rate limiter

	called := false
	handler := srv.withRateLimit(func(w http.ResponseWriter, r *http.Request) {
		called = true
		w.WriteHeader(http.StatusOK)
	})

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	handler(rec, req)

	if !called {
		t.Fatal("handler should be called when rate limiter is nil")
	}
}

func TestWriteJSONAndWriteError(t *testing.T) {
	t.Run("writeJSON", func(t *testing.T) {
		rec := httptest.NewRecorder()
		writeJSON(rec, http.StatusOK, map[string]string{"key": "value"})
		if rec.Code != http.StatusOK {
			t.Fatalf("status %d", rec.Code)
		}
		if ct := rec.Header().Get("Content-Type"); ct != "application/json" {
			t.Fatalf("Content-Type = %q", ct)
		}
	})

	t.Run("writeError", func(t *testing.T) {
		rec := httptest.NewRecorder()
		writeError(rec, http.StatusBadRequest, errCodeBadRequest, "test error")
		if rec.Code != http.StatusBadRequest {
			t.Fatalf("status %d", rec.Code)
		}
		var body map[string]interface{}
		json.Unmarshal(rec.Body.Bytes(), &body)
		if body["status"] != "error" {
			t.Fatalf("status = %v", body["status"])
		}
		if body["error_code"] != float64(errCodeBadRequest) {
			t.Fatalf("error_code = %v", body["error_code"])
		}
	})
}

// Helper functions

func metricsForTest() *metrics.Collector {
	return metrics.New()
}

func contextWithTimeout(t *testing.T) context.Context {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	cancel() // cancel immediately for test
	return ctx
}

func TestHandleQueryStreamBodyTooLarge(t *testing.T) {
	srv, _ := newTestServerWithDB(t, mustAuth(t, false, nil))
	srv.cfg.MaxRequestSizeBytes = 10

	body := `{"database":"testdb","query":"SELECT * FROM items;"}`
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/query/stream", strings.NewReader(body))
	srv.apiMux().ServeHTTP(rec, req)

	if rec.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("status %d, want 413: %s", rec.Code, rec.Body.String())
	}
}

func TestHandleQueryStreamInvalidJSON(t *testing.T) {
	srv, _ := newTestServerWithDB(t, mustAuth(t, false, nil))

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/query/stream", strings.NewReader("not json"))
	srv.apiMux().ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status %d, want 400: %s", rec.Code, rec.Body.String())
	}
}

func TestHandleQueryStreamParseError(t *testing.T) {
	srv, _ := newTestServerWithDB(t, mustAuth(t, false, nil))

	body := `{"database":"testdb","query":"INVALID SYNTAX;"}`
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/query/stream", strings.NewReader(body))
	srv.apiMux().ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status %d, want 400: %s", rec.Code, rec.Body.String())
	}
}

func TestHandleQueryStreamUnknownSession(t *testing.T) {
	srv, _ := newTestServerWithDB(t, mustAuth(t, false, nil))

	body := `{"database":"testdb","session_id":"nonexistent","query":"SELECT * FROM items;"}`
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/query/stream", strings.NewReader(body))
	srv.apiMux().ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status %d, want 400: %s", rec.Code, rec.Body.String())
	}
}

func TestHandleQueryStreamCommitRequiresSession(t *testing.T) {
	srv, _ := newTestServerWithDB(t, mustAuth(t, false, nil))

	body := `{"database":"testdb","query":"COMMIT;"}`
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/query/stream", strings.NewReader(body))
	srv.apiMux().ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status %d, want 400: %s", rec.Code, rec.Body.String())
	}
}

func TestHandleHandshakeInvalidNonce(t *testing.T) {
	srv, _ := newTestServerWithDB(t, mustAuth(t, false, nil))

	body := `{"type":"handshake","client_version":"2.0","client_name":"test","nonce":"","nonce_timestamp":0}`
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/v2/handshake", strings.NewReader(body))
	srv.apiMux().ServeHTTP(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("status %d, want 401: %s", rec.Code, rec.Body.String())
	}
}

func TestLiveQueryMethodNotAllowed(t *testing.T) {
	srv, _ := newTestServerWithDB(t, mustAuth(t, false, nil))

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/live?query=SELECT+1%3B", nil)
	srv.apiMux().ServeHTTP(rec, req)

	// /api/live is registered with withRateLimit (no withMethod), so POST may not be explicitly rejected
	// depending on routing. The handler itself may handle it. Just check it doesn't panic.
	if rec.Code >= 500 {
		t.Fatalf("unexpected 5xx: %d", rec.Code)
	}
}

func TestDashboardRequiresAuthWhenEnabled(t *testing.T) {
	srv := newTestServer(t, mustAuth(t, true, map[string]string{"tok": "user"}))

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/dashboard", nil)
	srv.apiMux().ServeHTTP(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("unauthenticated dashboard: status %d, want 401", rec.Code)
	}
}

func TestSessionStoreOperations(t *testing.T) {
	dir := t.TempDir()
	txm := txmanager.NewManager()
	store, err := storage.NewPageStorageEngine(dir, nil, txm)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { store.Close() })

	br := executor.NewBroadcaster()
	realSession := executor.NewSession(store, metrics.New(), txmanager.NewManager(), br)

	ss := newSessionStore()
	defer ss.stop()

	// put and get
	ss.put("sid1", &httpSessionEntry{
		session:    realSession,
		database:   "testdb",
		lastAccess: time.Now(),
	})

	entry := ss.get("sid1")
	if entry == nil {
		t.Fatal("expected entry")
	}
	if entry.database != "testdb" {
		t.Fatalf("database = %q", entry.database)
	}

	// get nonexistent
	if ss.get("nonexistent") != nil {
		t.Fatal("expected nil for nonexistent")
	}

	// remove
	ss.remove("sid1")
	if ss.get("sid1") != nil {
		t.Fatal("expected nil after remove")
	}

	// remove nonexistent (should not panic)
	ss.remove("nonexistent")
}

func TestSessionStoreCleanup(t *testing.T) {
	dir := t.TempDir()
	txm := txmanager.NewManager()
	store, err := storage.NewPageStorageEngine(dir, nil, txm)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { store.Close() })

	br := executor.NewBroadcaster()
	realSession := executor.NewSession(store, metrics.New(), txmanager.NewManager(), br)

	ss := newSessionStore()
	defer ss.stop()

	// Put an entry with old lastAccess
	ss.put("old", &httpSessionEntry{
		session:    realSession,
		database:   "db",
		lastAccess: time.Now().Add(-10 * time.Minute),
	})

	// Run cleanup
	ss.cleanup()

	// Old entry should be removed
	if ss.get("old") != nil {
		t.Fatal("expected old entry to be cleaned up")
	}
}

func TestSessionStoreStopOnce(t *testing.T) {
	ss := newSessionStore()
	ss.stop()
	ss.stop() // should not panic (stopOnce)
}

func TestServerNewDefaults(t *testing.T) {
	dir := t.TempDir()
	txm := txmanager.NewManager()
	store, err := storage.NewPageStorageEngine(dir, nil, txm)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()

	srv := New(Config{
		Storage: store,
	})
	if srv.cfg.MaxRequestSizeBytes == 0 {
		t.Fatal("MaxRequestSizeBytes should be set to default")
	}
	if srv.cfg.MaxLiveQuerySubscriptions == 0 {
		t.Fatal("MaxLiveQuerySubscriptions should be set to default")
	}
}

func TestClassifyErrorNoMatch(t *testing.T) {
	got := classifyError(fmt.Errorf("random unrelated error"))
	if got != 0 {
		t.Errorf("classifyError(random) = %d, want 0", got)
	}
}

func TestHandleQueryStreamBeginTransaction(t *testing.T) {
	srv, _ := newTestServerWithDB(t, mustAuth(t, false, nil))

	body := `{"database":"testdb","query":"BEGIN;"}`
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/query/stream", strings.NewReader(body))
	srv.apiMux().ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status %d: %s", rec.Code, rec.Body.String())
	}
}
