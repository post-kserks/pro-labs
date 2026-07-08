package httpserver

import (
	"bytes"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
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
