package httpserver

import (
	"encoding/json"
	"log/slog"
	"net/http"
	"strings"
)

const (
	errCodeBadRequest        = 3001
	errCodeParseError        = 3002
	errCodeUnknownColumn     = 3003
	errCodeStorageError      = 3004
	errCodeTxUnsupported     = 3005
	errCodeRateLimited       = 3006
	errCodeNotNullViolation  = 3007
	errCodeTypeMismatch      = 3008
	errCodeTableNotFound     = 3009
	errCodeDatabaseNotFound  = 3010
	errCodeDuplicateValue    = 3011
	errCodeCheckConstraint   = 3012
	errCodeForeignKey        = 3013
	errCodeQueryTimeout      = 3014
	errCodeInternal          = 5000
	errCodeNotImplemented    = 9999

	DefaultMaxLiveQuerySubscriptions = 1000
)

func (s *Server) corsMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("X-Frame-Options", "DENY")
		w.Header().Set("Content-Security-Policy", "default-src 'self'; script-src 'self' 'unsafe-inline'; style-src 'self' 'unsafe-inline'; img-src 'self' data:")

		origin := r.Header.Get("Origin")
		allowed := false
		if origin != "" && len(s.cfg.AllowedOrigins) > 0 {
			for _, o := range s.cfg.AllowedOrigins {
				if o == origin {
					allowed = true
					break
				}
			}
		}
		if allowed {
			w.Header().Set("Access-Control-Allow-Origin", origin)
			w.Header().Set("Vary", "Origin")
			w.Header().Set("Access-Control-Allow-Methods", "GET, POST, OPTIONS")
			w.Header().Set("Access-Control-Allow-Headers", "Content-Type, Authorization")
		}
		if r.Method == http.MethodOptions {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		next.ServeHTTP(w, r)
	})
}

func (s *Server) withRateLimit(next http.HandlerFunc) http.HandlerFunc {
	if s.cfg.RateLimiter == nil {
		return next
	}
	return s.cfg.RateLimiter.Middleware(next)
}

func (s *Server) withMethod(method string, next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != method {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		next(w, r)
	}
}

func writeJSON(w http.ResponseWriter, status int, payload interface{}) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(payload)
}

func writeError(w http.ResponseWriter, status, code int, message string) {
	writeJSON(w, status, map[string]interface{}{
		"status":     "error",
		"error_code": code,
		"message":    message,
	})
}

func writeStorageError(w http.ResponseWriter, status, code int, err error, logger *slog.Logger) {
	logger.Warn("storage error", "error", err)
	msg := err.Error()
	if len(msg) > 200 {
		msg = msg[:200] + "..."
	}

	// Classify error and use specific error code
	specificCode := classifyError(err)
	if specificCode != 0 {
		code = specificCode
	}

	writeJSON(w, status, map[string]interface{}{
		"status":     "error",
		"error_code": code,
		"message":    msg,
	})
}

func classifyError(err error) int {
	msg := err.Error()

	// NOT NULL constraint violations
	if containsAny(msg, "NOT NULL constraint failed") {
		return errCodeNotNullViolation
	}

	// Type mismatch / conversion errors
	if containsAny(msg, "cannot convert", "cannot parse", "invalid ENUM value") {
		return errCodeTypeMismatch
	}

	// Table not found
	if containsAny(msg, "does not exist") && containsAny(msg, "table") {
		return errCodeTableNotFound
	}

	// Database not found
	if containsAny(msg, "does not exist") && containsAny(msg, "database") {
		return errCodeDatabaseNotFound
	}

	// Duplicate value / unique violations
	if containsAny(msg, "duplicate primary key", "already exists", "UNIQUE constraint") {
		return errCodeDuplicateValue
	}

	// CHECK constraint violations
	if containsAny(msg, "CHECK constraint") {
		return errCodeCheckConstraint
	}

	// FOREIGN KEY violations
	if containsAny(msg, "foreign key constraint") {
		return errCodeForeignKey
	}

	// Query timeout
	if containsAny(msg, "query timeout") {
		return errCodeQueryTimeout
	}

	return 0
}

func containsAny(s string, substrs ...string) bool {
	for _, sub := range substrs {
		if strings.Contains(s, sub) {
			return true
		}
	}
	return false
}

func emptyIfNil(columns []string) []string {
	if columns == nil {
		return []string{}
	}
	return columns
}

func emptyRowsIfNil(rows [][]string) [][]string {
	if rows == nil {
		return [][]string{}
	}
	return rows
}

func extractHealthToken(r *http.Request) string {
	if auth := r.Header.Get("Authorization"); strings.HasPrefix(auth, "Bearer ") {
		return strings.TrimPrefix(auth, "Bearer ")
	}
	if token := r.Header.Get("X-VaultDB-Token"); token != "" {
		return token
	}
	return ""
}

const dashboardHTML = `<!DOCTYPE html>
<html>
<head>
<meta charset="utf-8">
<title>VaultDB Dashboard</title>
<style>
body { font-family: -apple-system, BlinkMacSystemFont, "Segoe UI", Roboto, sans-serif; margin: 20px; background: #f5f5f5; }
h1 { color: #333; }
.metric { background: white; padding: 15px; margin: 10px 0; border-radius: 8px; box-shadow: 0 2px 4px rgba(0,0,0,0.1); }
.metric h3 { margin: 0 0 10px 0; color: #666; }
.value { font-size: 24px; font-weight: bold; color: #2c3e50; }
.refresh { background: #3498db; color: white; border: none; padding: 10px 20px; border-radius: 5px; cursor: pointer; }
.refresh:hover { background: #2980b9; }
</style>
</head>
<body>
<h1>VaultDB Dashboard</h1>
<button class="refresh" onclick="refresh()">Refresh</button>
<div id="metrics">Loading...</div>
<script src="/dashboard.js"></script>
</body>
</html>
`
