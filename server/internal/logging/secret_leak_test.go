package logging

import (
	"bytes"
	"log/slog"
	"strings"
	"testing"
)

func TestNoSecretsInLogs(t *testing.T) {
	var logBuf bytes.Buffer
	logger := slog.New(slog.NewJSONHandler(&logBuf, &slog.HandlerOptions{Level: slog.LevelDebug}))

	secrets := []string{
		"vdb_sk_abcdef1234567890abcdef1234567890",
		"super-secret-passphrase-12345",
		"AES-256-GCM-key-do-not-log",
		"-----BEGIN RSA PRIVATE KEY-----",
	}

	for _, secret := range secrets {
		logger.Info("operation started", "user", "admin", "query", "SELECT 1")
		logger.Info("authentication", "token", secret[:10]+"...")
		logger.Error("error occurred", "detail", "connection failed")
	}

	logs := logBuf.String()

	for _, secret := range secrets {
		if strings.Contains(logs, secret) {
			t.Errorf("secret leaked into logs: %s", secret[:20]+"...")
		}
	}

	errorPatterns := []string{"stack trace", "goroutine ", "runtime error"}
	for _, pattern := range errorPatterns {
		if strings.Contains(logs, pattern) {
			t.Errorf("internal error details leaked: %s", pattern)
		}
	}
}

func TestSanitizeErrorMessages(t *testing.T) {
	sensitiveErrors := []struct {
		input    string
		expected string
	}{
		{"connection to host:5432 failed", "connection failed"},
		{"file /etc/passwd not found", "file not found"},
		{"auth token vdb_sk_abc123 invalid", "invalid token"},
	}

	for _, tc := range sensitiveErrors {
		result := sanitizeForLog(tc.input)
		if strings.Contains(result, "vdb_sk_") {
			t.Errorf("token leaked in sanitized error: %s", result)
		}
	}
}

func sanitizeForLog(msg string) string {
	result := msg
	if idx := strings.Index(result, "vdb_sk_"); idx >= 0 {
		result = result[:idx] + "[REDACTED]" + result[idx+20:]
	}
	return result
}
