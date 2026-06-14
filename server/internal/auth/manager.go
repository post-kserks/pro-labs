package auth

import (
	"context"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"log/slog"
	"net/http"
	"os"
	"strings"
	"sync"
)

type contextKey string

const tokenLabelContextKey contextKey = "token_label"

// Manager хранит HMAC-SHA256 хеши токенов с серверным секретом.
// HMAC привязан к секрету — rainbow tables бесполезны.
type Manager struct {
	enabled bool
	mu      sync.RWMutex
	tokens  map[string]string // HMAC-SHA256(token, secret) hex → label
	secret  []byte
}

// hashToken вычисляет HMAC-SHA256 токена с серверным секретом.
func (m *Manager) hashToken(token string) string {
	mac := hmac.New(sha256.New, m.secret)
	mac.Write([]byte(token))
	return hex.EncodeToString(mac.Sum(nil))
}

// New создаёт менеджер с серверным секретом.
// secretKey читается из VAULTDB_AUTH_SECRET.
// Если переменная не задана — генерируем случайный и логируем предупреждение.
func New(enabled bool, tokens map[string]string, logger *slog.Logger) *Manager {
	secret := []byte(os.Getenv("VAULTDB_AUTH_SECRET"))

	if len(secret) == 0 {
		secret = make([]byte, 32)
		if _, err := rand.Read(secret); err != nil {
			panic("failed to generate auth secret: " + err.Error())
		}
		if logger != nil {
			logger.Warn("VAULTDB_AUTH_SECRET not set, using ephemeral secret. " +
				"Tokens will be invalidated on restart.")
		}
	}

	hashed := make(map[string]string, len(tokens))
	for token, label := range tokens {
		mac := hmac.New(sha256.New, secret)
		mac.Write([]byte(token))
		hashed[hex.EncodeToString(mac.Sum(nil))] = label
	}

	return &Manager{
		enabled: enabled,
		tokens:  hashed,
		secret:  secret,
	}
}

func (m *Manager) Enabled() bool {
	return m.enabled
}

// AddToken регистрирует новый токен (хранится только HMAC-хеш).
func (m *Manager) AddToken(token, label string) {
	hash := m.hashToken(token)
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
	hash := m.hashToken(token)
	m.mu.RLock()
	defer m.mu.RUnlock()
	_, ok := m.tokens[hash]
	return ok
}

func (m *Manager) GetLabel(token string) string {
	m.mu.RLock()
	defer m.mu.RUnlock()
	if label, ok := m.tokens[m.hashToken(token)]; ok {
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
	// SECURITY NOTE: ?token= в URL передаёт токен в открытом виде.
	// Он виден в логах серверов/прокси, Referer headers, browser history.
	// Используется ТОЛЬКО для SSE (EventSource API не поддерживает заголовки).
	// Для всех остальных клиентов (C++ TUI, Shell, REST API) используйте
	// Authorization: Bearer <token> или X-VaultDB-Token заголовок.
	return r.URL.Query().Get("token")
}
