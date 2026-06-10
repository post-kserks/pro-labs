package auth

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
)

type contextKey string

const tokenLabelContextKey contextKey = "token_label"

type Manager struct {
	enabled bool
	tokens  map[string]string
}

func New(enabled bool, tokens map[string]string) *Manager {
	copied := make(map[string]string, len(tokens))
	for token, label := range tokens {
		copied[token] = label
	}

	return &Manager{
		enabled: enabled,
		tokens:  copied,
	}
}

func (m *Manager) Enabled() bool {
	return m.enabled
}

func (m *Manager) ValidateToken(token string) bool {
	if !m.enabled {
		return true
	}
	if token == "" {
		return false
	}
	_, ok := m.tokens[token]
	return ok
}

func (m *Manager) GetLabel(token string) string {
	if label, ok := m.tokens[token]; ok {
		return label
	}
	return "unknown"
}

func (m *Manager) Middleware(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if !m.enabled {
			next(w, r)
			return
		}

		token := tokenFromRequest(r)
		if !m.ValidateToken(token) {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusUnauthorized)
			_ = json.NewEncoder(w).Encode(map[string]interface{}{
				"status":     "error",
				"error_code": 2001,
				"message":    "Unauthorized: invalid or missing token.",
			})
			return
		}

		ctx := context.WithValue(r.Context(), tokenLabelContextKey, m.GetLabel(token))
		next(w, r.WithContext(ctx))
	}
}

func tokenFromRequest(r *http.Request) string {
	authHeader := r.Header.Get("Authorization")
	if strings.HasPrefix(authHeader, "Bearer ") {
		return strings.TrimPrefix(authHeader, "Bearer ")
	}
	if token := r.Header.Get("X-VaultDB-Token"); token != "" {
		return token
	}
	// EventSource clients cannot set headers; allow ?token= for SSE endpoints.
	return r.URL.Query().Get("token")
}
