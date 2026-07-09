package httpserver

import (
	"bytes"
	"context"
	"fmt"
	"log/slog"
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

func TestWithPanicRecovery(t *testing.T) {
	var logBuf bytes.Buffer
	logger := slog.New(slog.NewJSONHandler(&logBuf, nil))

	// Handler that panics
	panickingHandler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		panic("test panic")
	})

	handler := withPanicRecovery(panickingHandler)

	// We need to override the package-level slog temporarily or use the logger in the middleware.
	// The current implementation uses slog.Error directly. For testing, I'll capture the log output.
	// Since withPanicRecovery uses slog.Error, I'll set the default logger for the test.
	orig := slog.Default()
	slog.SetDefault(logger)
	defer slog.SetDefault(orig)

	req := httptest.NewRequest(http.MethodGet, "/test", nil)
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("expected 500, got %d", rec.Code)
	}

	body := rec.Body.String()
	if !strings.Contains(body, "internal server error") {
		t.Fatalf("expected error message in body, got: %s", body)
	}

	logOutput := logBuf.String()
	if !strings.Contains(logOutput, "panic recovered") {
		t.Fatalf("expected 'panic recovered' in logs, got: %s", logOutput)
	}
	if !strings.Contains(logOutput, "test panic") {
		t.Fatalf("expected 'test panic' in logs, got: %s", logOutput)
	}
	if !strings.Contains(logOutput, "runtime/debug.Stack") {
		t.Fatalf("expected stack trace in logs, got: %s", logOutput)
	}
}

func TestTLSDisabledWarningLogged(t *testing.T) {
	var logBuf bytes.Buffer
	logger := slog.New(slog.NewJSONHandler(&logBuf, nil))

	dir := t.TempDir()
	txm := txmanager.NewManager()
	store, err := storage.NewPageStorageEngine(dir, nil, txm)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })

	br := executor.NewBroadcaster()
	sess := executor.NewSession(store, metrics.New(), txmanager.NewManager(), br)
	stmt, _ := parser.Parse("CREATE DATABASE testdb;")
	sess.Execute(stmt)

	srv := New(Config{
		Storage:   store,
		Auth:      mustAuth(t, false, nil),
		Metrics:   metrics.New(),
		TxManager: txmanager.NewManager(),
		Broadcaster: br,
		Logger:    logger,
	})
	// TLS not configured (empty cert/key)
	srv.cfg.TLSCertFile = ""
	srv.cfg.TLSKeyFile = ""

	apiPort := freePort(t)
	monitorPort := freePort(t)
	srv.cfg.Port = apiPort
	srv.cfg.MonitorPort = monitorPort

	// Start in background; it will log the warning and then we shut it down.
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	errCh := make(chan error, 1)
	go func() { errCh <- srv.Start(ctx) }()

	// Give the server time to start and log.
	time.Sleep(300 * time.Millisecond)

	cancel()
	select {
	case <-errCh:
	case <-time.After(5 * time.Second):
		t.Fatal("server did not shut down in time")
	}

	logOutput := logBuf.String()
	if !strings.Contains(logOutput, "auth tokens are transmitted in plaintext") {
		t.Fatalf("expected plaintext warning in logs, got: %s", logOutput)
	}
}

func TestRequireTLSForTokenRejection(t *testing.T) {
	var logBuf bytes.Buffer
	logger := slog.New(slog.NewJSONHandler(&logBuf, nil))

	dir := t.TempDir()
	txm := txmanager.NewManager()
	store, err := storage.NewPageStorageEngine(dir, nil, txm)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })

	br := executor.NewBroadcaster()
	sess := executor.NewSession(store, metrics.New(), txmanager.NewManager(), br)
	stmt, _ := parser.Parse("CREATE DATABASE testdb;")
	sess.Execute(stmt)

	srv := New(Config{
		Storage:   store,
		Auth:      mustAuth(t, true, map[string]string{"testtoken": "test"}),
		Metrics:   metrics.New(),
		TxManager: txmanager.NewManager(),
		Broadcaster: br,
		Logger:    logger,
	})
	srv.cfg.TLSCertFile = ""
	srv.cfg.TLSKeyFile = ""
	srv.cfg.AuthRequireTLSForToken = true

	apiPort := freePort(t)
	monitorPort := freePort(t)
	srv.cfg.Port = apiPort
	srv.cfg.MonitorPort = monitorPort

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	errCh := make(chan error, 1)
	go func() { errCh <- srv.Start(ctx) }()

	time.Sleep(300 * time.Millisecond)

	addr := fmt.Sprintf("127.0.0.1:%d", apiPort)

	// Request with token should be rejected
	req, _ := http.NewRequest(http.MethodGet, "http://"+addr+"/api/databases", nil)
	req.Header.Set("Authorization", "Bearer testtoken")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("request failed: %v", err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("expected 400 for token over plain HTTP, got %d", resp.StatusCode)
	}

	// Request without token should pass through
	req2, _ := http.NewRequest(http.MethodGet, "http://"+addr+"/health", nil)
	resp2, err := http.DefaultClient.Do(req2)
	if err != nil {
		t.Fatalf("health request failed: %v", err)
	}
	resp2.Body.Close()
	if resp2.StatusCode != http.StatusOK {
		t.Fatalf("health without token should pass, got %d", resp2.StatusCode)
	}

	cancel()
	select {
	case <-errCh:
	case <-time.After(5 * time.Second):
		t.Fatal("server did not shut down in time")
	}
}
