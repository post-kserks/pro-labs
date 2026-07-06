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
	"net"
	"net/http"
	"os"
	"strings"
	"sync"
	"time"
)

// AuditFunc is a callback function for audit logging.
type AuditFunc func(actor, action, target, detail string)

type contextKey string

const tokenLabelContextKey contextKey = "token_label"

// TokenInfo stores metadata about an authenticated token.
type TokenInfo struct {
	Hash      string
	Label     string
	Role      string // "admin", "reader", "writer"
	CreatedAt time.Time
}

// rolePermissions maps role names to the set of SQL operations they may perform.
// A "*" wildcard means all operations are allowed.
var rolePermissions = map[string][]string{
	"admin":  {"*"},
	"writer": {"SELECT", "INSERT", "UPDATE", "DELETE", "CREATE TABLE", "DROP TABLE", "CREATE INDEX", "DROP INDEX", "COPY FROM", "COPY TO", "CREATE VIEW", "DROP VIEW", "CREATE TRIGGER", "DROP TRIGGER", "ALTER TABLE", "TRUNCATE", "MERGE"},
	"reader": {"SELECT", "EXPLAIN"},
}

// Manager хранит HMAC-SHA256 хеши токенов с серверным секретом.
// HMAC привязан к секрету — rainbow tables бесполезны.
type Manager struct {
	enabled    bool
	mu         sync.RWMutex
	tokens     map[string]*TokenInfo // HMAC-SHA256(token, secret) hex → token info
	revoked    map[string]time.Time  // HMAC-SHA256(token, secret) hex → revocation time
	secret     []byte
	warnedOnce sync.Once
	logger     *slog.Logger
	rateLim    *authRateLimiter
	auditFunc  AuditFunc
}

// authRateLimiter отслеживает неудачные попытки аутентификации по IP.
type authRateLimiter struct {
	mu        sync.Mutex
	attempts  map[string][]time.Time
	blocked   map[string]time.Time
	window    time.Duration
	maxFails  int
	blockFor  time.Duration
	lastSweep time.Time
}

func newAuthRateLimiter(windowSec, maxFails, blockForSec int) *authRateLimiter {
	if windowSec <= 0 {
		windowSec = 60
	}
	if maxFails <= 0 {
		maxFails = 10
	}
	if blockForSec <= 0 {
		blockForSec = 300
	}
	return &authRateLimiter{
		attempts:  make(map[string][]time.Time),
		blocked:   make(map[string]time.Time),
		window:    time.Duration(windowSec) * time.Second,
		maxFails:  maxFails,
		blockFor:  time.Duration(blockForSec) * time.Second,
		lastSweep: time.Now(),
	}
}

// sweepLocked удаляет устаревшие записи, чтобы карты attempts/blocked не росли
// неограниченно при потоке запросов с большого числа разных IP (защита от
// исчерпания памяти). Вызывается под удержанием rl.mu не чаще раза в window.
func (rl *authRateLimiter) sweepLocked(now time.Time) {
	if now.Sub(rl.lastSweep) < rl.window {
		return
	}
	rl.lastSweep = now
	for ip, until := range rl.blocked {
		if now.After(until) {
			delete(rl.blocked, ip)
		}
	}
	cutoff := now.Add(-rl.window)
	for ip, ts := range rl.attempts {
		if _, stillBlocked := rl.blocked[ip]; stillBlocked {
			continue
		}
		// Если последняя попытка вне окна — запись бесполезна, удаляем.
		if len(ts) == 0 || !ts[len(ts)-1].After(cutoff) {
			delete(rl.attempts, ip)
		}
	}
}

func (rl *authRateLimiter) recordFailure(ip string) {
	rl.mu.Lock()
	defer rl.mu.Unlock()
	now := time.Now()
	rl.sweepLocked(now)
	rl.attempts[ip] = append(rl.attempts[ip], now)
	cutoff := now.Add(-rl.window)
	filtered := rl.attempts[ip][:0]
	for _, t := range rl.attempts[ip] {
		if t.After(cutoff) {
			filtered = append(filtered, t)
		}
	}
	rl.attempts[ip] = filtered
	if len(filtered) >= rl.maxFails {
		rl.blocked[ip] = now.Add(rl.blockFor)
	}
}

func (rl *authRateLimiter) isBlocked(ip string) bool {
	rl.mu.Lock()
	defer rl.mu.Unlock()
	rl.sweepLocked(time.Now())
	until, ok := rl.blocked[ip]
	if !ok {
		return false
	}
	if time.Now().After(until) {
		delete(rl.blocked, ip)
		delete(rl.attempts, ip)
		return false
	}
	return true
}

func extractClientIP(r *http.Request) string {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		host = r.RemoteAddr
	}
	return host
}

// hashToken вычисляет HMAC-SHA256 токена с серверным секретом.
func (m *Manager) hashToken(token string) string {
	mac := hmac.New(sha256.New, m.secret)
	mac.Write([]byte(token))
	return hex.EncodeToString(mac.Sum(nil))
}

// New создаёт менеджер с серверным секретом.
// secretKey читается из VAULTDB_AUTH_SECRET.
// Если переменная не задана — генерируем случайный (для тестов/разработки).
// В production VAULTDB_AUTH_SECRET обязателен (проверяется в main.go).
func New(enabled bool, tokens map[string]string, logger *slog.Logger, rateWindowSec, maxFails, blockForSec int) (*Manager, error) {
	secret := []byte(os.Getenv("VAULTDB_AUTH_SECRET"))

	if len(secret) == 0 {
		secret = make([]byte, 32)
		if _, err := rand.Read(secret); err != nil {
			return nil, fmt.Errorf("generate auth secret: %w", err)
		}
		if logger != nil {
			logger.Warn("VAULTDB_AUTH_SECRET not set — using ephemeral secret (tokens invalidated on restart)")
		}
	}

	hashed := make(map[string]*TokenInfo, len(tokens))
	now := time.Now()
	for token, label := range tokens {
		mac := hmac.New(sha256.New, secret)
		mac.Write([]byte(token))
		hashed[hex.EncodeToString(mac.Sum(nil))] = &TokenInfo{
			Hash:      hex.EncodeToString(mac.Sum(nil)),
			Label:     label,
			Role:      "admin", // default: full access for pre-configured tokens
			CreatedAt: now,
		}
	}

	m := &Manager{
		enabled: enabled,
		tokens:  hashed,
		revoked: make(map[string]time.Time),
		secret:  secret,
		logger:  logger,
		rateLim: newAuthRateLimiter(rateWindowSec, maxFails, blockForSec),
	}
	go m.cleanupRevokedTokens()
	return m, nil
}

func (m *Manager) Enabled() bool {
	return m.enabled
}

// RevokeToken marks a token as revoked. Revoked tokens are rejected by ValidateToken.
func (m *Manager) RevokeToken(token string) {
	hash := m.hashToken(token)
	m.mu.Lock()
	defer m.mu.Unlock()
	m.revoked[hash] = time.Now()
	if m.logger != nil {
		if info, ok := m.tokens[hash]; ok {
			m.logger.Info("token revoked", "label", info.Label)
		}
	}
}

// IsRevoked checks whether a token has been revoked.
func (m *Manager) IsRevoked(token string) bool {
	hash := m.hashToken(token)
	m.mu.RLock()
	defer m.mu.RUnlock()
	_, ok := m.revoked[hash]
	return ok
}

// cleanupRevokedTokens periodically removes revoked token entries older than 24h.
func (m *Manager) cleanupRevokedTokens() {
	ticker := time.NewTicker(1 * time.Hour)
	defer ticker.Stop()
	for range ticker.C {
		m.mu.Lock()
		now := time.Now()
		for hash, revokedAt := range m.revoked {
			if now.Sub(revokedAt) > 24*time.Hour {
				delete(m.revoked, hash)
			}
		}
		m.mu.Unlock()
	}
}

// SetAuditFunc sets a callback function for audit logging.
func (m *Manager) SetAuditFunc(fn AuditFunc) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.auditFunc = fn
}

// NewDisabled creates a disabled auth manager that allows all requests.
func NewDisabled() (*Manager, error) {
	return &Manager{enabled: false}, nil
}

// AddToken registers a new token with the given role (stored as HMAC hash).
func (m *Manager) AddToken(token, label, role string) {
	if role == "" {
		role = "admin"
	}
	hash := m.hashToken(token)
	m.mu.Lock()
	defer m.mu.Unlock()
	m.tokens[hash] = &TokenInfo{
		Hash:      hash,
		Label:     label,
		Role:      role,
		CreatedAt: time.Now(),
	}
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
	_, ok := m.tokens[hash]
	if ok {
		_, revoked := m.revoked[hash]
		ok = !revoked
	}
	m.mu.RUnlock()
	return ok
}

// GenerateToken creates a new bearer token with the given label and role,
// registers it, and returns the plaintext token string.
func (m *Manager) GenerateToken(label, role string) string {
	if role == "" {
		role = "admin"
	}
	buf := make([]byte, 32)
	if _, err := rand.Read(buf); err != nil {
		// Fallback — should never happen
		panic("auth: failed to generate random token: " + err.Error())
	}
	token := hex.EncodeToString(buf)
	hash := m.hashToken(token)

	m.mu.Lock()
	m.tokens[hash] = &TokenInfo{
		Hash:      hash,
		Label:     label,
		Role:      role,
		CreatedAt: time.Now(),
	}
	m.mu.Unlock()

	return token
}

// GetTokenRole returns the role assigned to the given token.
func (m *Manager) GetTokenRole(token string) string {
	if !m.enabled {
		return "admin"
	}
	hash := m.hashToken(token)
	m.mu.RLock()
	defer m.mu.RUnlock()
	if info, ok := m.tokens[hash]; ok {
		return info.Role
	}
	return ""
}

// CheckPermission returns true when the token is allowed to perform the given
// SQL operation. The operation string should be the uppercased first keyword of
// the statement (e.g. "SELECT", "INSERT", "CREATE TABLE").
func (m *Manager) CheckPermission(token, operation string) bool {
	if !m.enabled {
		return true
	}
	role := m.GetTokenRole(token)
	perms, ok := rolePermissions[role]
	if !ok {
		return false
	}
	for _, p := range perms {
		if p == "*" || strings.EqualFold(p, operation) {
			return true
		}
	}
	return false
}

func (m *Manager) GetLabel(token string) string {
	m.mu.RLock()
	defer m.mu.RUnlock()
	if info, ok := m.tokens[m.hashToken(token)]; ok {
		return info.Label
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

		ip := extractClientIP(r)
		// Skip auth for localhost — web UI needs to make API calls without tokens
		if ip == "127.0.0.1" || ip == "::1" || ip == "localhost" {
			next(w, r)
			return
		}
		if m.rateLim.isBlocked(ip) {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusTooManyRequests)
			_ = json.NewEncoder(w).Encode(map[string]interface{}{
				"status":  "error",
				"message": "rate limit exceeded, try again later",
			})
			return
		}

		token := tokenFromRequest(r)
		if token == "" || !m.ValidateToken(token) {
			m.rateLim.recordFailure(ip)
			m.logAuthEvent(ip, "AUTH_LOGIN", "failed: invalid token")
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusUnauthorized)
			_ = json.NewEncoder(w).Encode(map[string]interface{}{
				"status":     "error",
				"error_code": 2001,
				"message":    "Unauthorized: invalid or missing token.",
			})
			return
		}

		m.logAuthEvent(m.GetLabel(token), "AUTH_LOGIN", "success")
		ctx := context.WithValue(r.Context(), tokenLabelContextKey, m.GetLabel(token))
		next(w, r.WithContext(ctx))
	}
}

func (m *Manager) logAuthEvent(actor, action, detail string) {
	m.mu.RLock()
	fn := m.auditFunc
	m.mu.RUnlock()
	if fn != nil {
		fn(actor, action, "", detail)
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
	return ""
}
