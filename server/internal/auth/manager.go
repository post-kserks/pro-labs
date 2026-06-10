package auth

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"strings"
	"sync"
)

type contextKey string

const tokenLabelContextKey contextKey = "token_label"

// Manager хранит только SHA-256 хеши токенов: даже при утечке памяти/дампа
// сами токены восстановить нельзя.
type Manager struct {
	enabled bool
	mu      sync.RWMutex
	tokens  map[string]string // SHA-256(token) hex → label
}

// hashToken вычисляет SHA-256 токена.
func hashToken(token string) string {
	h := sha256.Sum256([]byte(token))
	return hex.EncodeToString(h[:])
}

// New создаёт менеджер. Входные токены приходят в открытом виде (из env или
// конфига) и немедленно хешируются; plaintext дальше нигде не хранится.
func New(enabled bool, tokens map[string]string) *Manager {
	hashed := make(map[string]string, len(tokens))
	for token, label := range tokens {
		hashed[hashToken(token)] = label
	}

	return &Manager{
		enabled: enabled,
		tokens:  hashed,
	}
}

func (m *Manager) Enabled() bool {
	return m.enabled
}

// AddToken регистрирует новый токен (хранится только хеш).
func (m *Manager) AddToken(token, label string) {
	hash := hashToken(token)
	m.mu.Lock()
	defer m.mu.Unlock()
	m.tokens[hash] = label
}

func (m *Manager) ValidateToken(token string) bool {
	if !m.enabled {
		return true
	}
	if token == "" {
		return false
	}
	hash := hashToken(token)
	m.mu.RLock()
	defer m.mu.RUnlock()
	_, ok := m.tokens[hash]
	return ok
}

func (m *Manager) GetLabel(token string) string {
	m.mu.RLock()
	defer m.mu.RUnlock()
	if label, ok := m.tokens[hashToken(token)]; ok {
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
