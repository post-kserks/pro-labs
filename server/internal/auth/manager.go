package auth

import (
	"context"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
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
	enabled    bool
	mu         sync.RWMutex
	tokens     map[string]string // HMAC-SHA256(token, secret) hex → label
	secret     []byte
	warnedOnce sync.Once
	logger     *slog.Logger
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
func New(enabled bool, tokens map[string]string, logger *slog.Logger) (*Manager, error) {
	secret := []byte(os.Getenv("VAULTDB_AUTH_SECRET"))

	if len(secret) == 0 {
		secret = make([]byte, 32)
		if _, err := rand.Read(secret); err != nil {
			return nil, fmt.Errorf("generate auth secret: %w", err)
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
		logger:  logger,
	}, nil
}

func (m *Manager) Enabled() bool {
	return m.enabled
}

// NewDisabled creates a disabled auth manager that allows all requests.
func NewDisabled() (*Manager, error) {
	return &Manager{enabled: false}, nil
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
			m.warnedOnce.Do(func() {
				if m.logger != nil {
					m.logger.Warn("AUTH DISABLED — server is open to all connections")
				}
			})
			next(w, r)
			return
		}

		token := tokenFromRequest(r)
		if token == "" || !m.ValidateToken(token) {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusUnauthorized)
			_ = json.NewEncoder(w).Encode(map[string]interface{}{
				"status":     "error",
				"error_code": 2001,
				"message":    "Unauthorized: invalid or missing token.",
			})
			return
		}
		if tokenFromQueryString(r) && !isSSERequest(r) {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusUnauthorized)
			_ = json.NewEncoder(w).Encode(map[string]interface{}{
				"status":     "error",
				"error_code": 2001,
				"message":    "Unauthorized: token query parameter is only allowed for SSE endpoints.",
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
	return r.URL.Query().Get("token")
}

func tokenFromQueryString(r *http.Request) bool {
	return r.URL.Query().Get("token") != ""
}

func isSSERequest(r *http.Request) bool {
	return strings.Contains(r.Header.Get("Accept"), "text/event-stream")
}
