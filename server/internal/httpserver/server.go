package httpserver

import (
	"context"
	"crypto/tls"
	"fmt"
	"log/slog"
	"net/http"
	"sync"
	"sync/atomic"
	"time"

	"vaultdb/internal/auth"
	"vaultdb/internal/config"
	"vaultdb/internal/core/ai"
	"vaultdb/internal/core/audit"
	"vaultdb/internal/core/executor"
	"vaultdb/internal/core/metrics"
	"vaultdb/internal/core/storage"
	"vaultdb/internal/core/txmanager"
	"vaultdb/internal/pool"
)

type Config struct {
	Host                      string
	Port                      int
	MonitorPort               int
	Version                   string
	MaxRequestSizeBytes       int
	MaxRows                   int
	QueryTimeoutSec           int
	MaxPreparedStmts          int
	ResultCacheSize           int
	ResultCacheTTLSec         int
	AllowedOrigins            []string
	Storage                   storage.StorageEngine
	Auth                      *auth.Manager
	Logger                    *slog.Logger
	Metrics                   *metrics.Collector
	TxManager                 *txmanager.Manager
	ActiveConnections         func() int64
	Broadcaster               *executor.Broadcaster
	Embedder                  ai.Embedder
	RateLimiter               *RateLimiter
	TLSCertFile               string
	TLSKeyFile                string
	TLSMinVersion             string // "1.2" or "1.3"
	TLSEnforce                bool
	TLSRedirectHTTP           bool
	AuthRequireTLSForToken    bool
	MaxLiveQuerySubscriptions int
	MaxLiveQueryDurationSec   int
	SessionPoolMaxIdle        int
	SessionPoolMaxOpen        int
	SessionPoolIdleTimeoutSec int
	AuditTable                *audit.TableLog
	AuditVerifyInterval       time.Duration
}

type Server struct {
	cfg                 Config
	startedAt           time.Time
	metrics             *metrics.Collector
	txm                 *txmanager.Manager
	br                  *executor.Broadcaster
	activeSubscriptions atomic.Int64
	sessions            *sessionStore
	sessionPool         *pool.SessionPool
}

// sessionStore manages HTTP transaction sessions keyed by session ID.
type sessionStore struct {
	mu       sync.Mutex
	sessions map[string]*httpSessionEntry
	stopOnce sync.Once
	stopCh   chan struct{}
}

type httpSessionEntry struct {
	session    *executor.Session
	database   string
	lastAccess time.Time
}

const sessionIdleTimeout = 5 * time.Minute

func newSessionStore() *sessionStore {
	ss := &sessionStore{
		sessions: make(map[string]*httpSessionEntry),
		stopCh:   make(chan struct{}),
	}
	go ss.cleanupLoop()
	return ss
}

func (ss *sessionStore) cleanupLoop() {
	ticker := time.NewTicker(1 * time.Minute)
	defer ticker.Stop()
	for {
		select {
		case <-ss.stopCh:
			return
		case <-ticker.C:
			ss.cleanup()
		}
	}
}

func (ss *sessionStore) cleanup() {
	ss.mu.Lock()
	defer ss.mu.Unlock()
	now := time.Now()
	for id, entry := range ss.sessions {
		if now.Sub(entry.lastAccess) > sessionIdleTimeout {
			entry.session.Close()
			delete(ss.sessions, id)
		}
	}
}

func (ss *sessionStore) get(id string) *httpSessionEntry {
	ss.mu.Lock()
	defer ss.mu.Unlock()
	entry, ok := ss.sessions[id]
	if !ok {
		return nil
	}
	entry.lastAccess = time.Now()
	return entry
}

func (ss *sessionStore) put(id string, entry *httpSessionEntry) {
	ss.mu.Lock()
	defer ss.mu.Unlock()
	ss.sessions[id] = entry
}

func (ss *sessionStore) remove(id string) {
	ss.mu.Lock()
	defer ss.mu.Unlock()
	entry, ok := ss.sessions[id]
	if ok {
		entry.session.Close()
		delete(ss.sessions, id)
	}
}

func (ss *sessionStore) stop() {
	ss.stopOnce.Do(func() {
		close(ss.stopCh)
	})
}

func New(cfg Config) *Server {
	if cfg.Logger == nil {
		cfg.Logger = slog.Default()
	}
	if cfg.Auth == nil {
		mgr, err := auth.NewWithCollector(false, nil, cfg.Logger, 60, 10, 300, cfg.Metrics)
		if err != nil {
			cfg.Logger.Error("failed to create auth manager", "error", err)
			cfg.Logger.Warn("continuing with auth disabled")
			mgr, _ = auth.NewDisabled()
		}
		cfg.Auth = mgr
	}
	cfg.Auth.SetLocalhostBypass(config.Default().Auth.LocalhostBypass)
	if cfg.Storage != nil {
		cfg.Auth.SetDataDir(cfg.Storage.DataDir())
	}
	if cfg.AuditTable != nil {
		cfg.Auth.SetAuditFunc(func(actor, action, target, detail string) {
			cfg.AuditTable.Append(audit.Entry{
				Actor:  actor,
				Action: action,
				Target: target,
				Detail: detail,
			})
		})
	}
	if cfg.ActiveConnections == nil {
		cfg.ActiveConnections = func() int64 { return 0 }
	}
	if cfg.MaxRequestSizeBytes == 0 {
		cfg.MaxRequestSizeBytes = config.DefaultMaxRequestSize
	}
	if cfg.MaxLiveQuerySubscriptions == 0 {
		cfg.MaxLiveQuerySubscriptions = DefaultMaxLiveQuerySubscriptions
	}
	m := cfg.Metrics
	if m == nil {
		m = metrics.New()
	}
	txm := cfg.TxManager
	if txm == nil {
		txm = txmanager.NewManager()
	}
	br := cfg.Broadcaster
	if br == nil {
		br = executor.NewBroadcaster()
	}
	return &Server{
		cfg:       cfg,
		startedAt: time.Now().UTC(),
		metrics:   m,
		txm:       txm,
		br:        br,
		sessions:  newSessionStore(),
		sessionPool: pool.NewSessionPool(
			func() *executor.Session {
				return newSessionWithConfig(cfg)
			},
			cfg.SessionPoolMaxIdle,
			cfg.SessionPoolMaxOpen,
			time.Duration(cfg.SessionPoolIdleTimeoutSec)*time.Second,
		),
	}
}

func (s *Server) Start(ctx context.Context) error {
	tlsEnabled := s.cfg.TLSCertFile != "" && s.cfg.TLSKeyFile != ""

	if s.cfg.TLSEnforce && !tlsEnabled {
		return fmt.Errorf("TLS enforcement is enabled but TLS is not configured (cert_file=%q, key_file=%q)", s.cfg.TLSCertFile, s.cfg.TLSKeyFile)
	}

	if !tlsEnabled {
		s.cfg.Logger.Warn("SECURITY WARNING: TLS is disabled — auth tokens are transmitted in plaintext. Enable TLS (tls.cert_file + tls.key_file) in production.")
		if s.cfg.AuthRequireTLSForToken {
			s.cfg.Logger.Warn("auth.require_tls_for_token is enabled — requests with auth tokens will be rejected when TLS is not active")
		}
	}

	// Start audit chain verifier if configured.
	if s.cfg.AuditTable != nil && s.cfg.AuditVerifyInterval > 0 {
		s.cfg.AuditTable.StartVerifier(s.cfg.AuditVerifyInterval, s.cfg.Logger)
	}

	apiServer := &http.Server{
		Addr:              fmt.Sprintf("%s:%d", s.cfg.Host, s.cfg.Port),
		Handler:           s.corsMiddleware(s.apiMux()),
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       15 * time.Second,
		WriteTimeout:      60 * time.Second,
		IdleTimeout:       120 * time.Second,
		MaxHeaderBytes:    1 << 20,
	}
	monitorServer := &http.Server{
		Addr:              fmt.Sprintf("%s:%d", s.cfg.Host, s.cfg.MonitorPort),
		Handler:           s.monitorMux(),
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       10 * time.Second,
		WriteTimeout:      10 * time.Second,
		IdleTimeout:       60 * time.Second,
		MaxHeaderBytes:    1 << 20,
	}

	if s.cfg.TLSCertFile != "" && s.cfg.TLSKeyFile != "" {
		minVer := uint16(tls.VersionTLS12)
		if s.cfg.TLSMinVersion == "1.3" {
			minVer = tls.VersionTLS13
		}
		tlsCfg := &tls.Config{
			MinVersion: minVer,
			CipherSuites: []uint16{
				tls.TLS_ECDHE_ECDSA_WITH_AES_256_GCM_SHA384,
				tls.TLS_ECDHE_RSA_WITH_AES_256_GCM_SHA384,
				tls.TLS_ECDHE_ECDSA_WITH_AES_128_GCM_SHA256,
				tls.TLS_ECDHE_RSA_WITH_AES_128_GCM_SHA256,
			},
		}
		apiServer.TLSConfig = tlsCfg
		monitorServer.TLSConfig = tlsCfg.Clone()
	}

	errCh := make(chan error, 2)

	go func() {
		var err error
		if s.cfg.TLSCertFile != "" && s.cfg.TLSKeyFile != "" {
			err = apiServer.ListenAndServeTLS(s.cfg.TLSCertFile, s.cfg.TLSKeyFile)
		} else {
			err = apiServer.ListenAndServe()
		}
		if err != nil && err != http.ErrServerClosed {
			errCh <- err
		}
	}()

	go func() {
		var err error
		if s.cfg.TLSCertFile != "" && s.cfg.TLSKeyFile != "" {
			err = monitorServer.ListenAndServeTLS(s.cfg.TLSCertFile, s.cfg.TLSKeyFile)
		} else {
			err = monitorServer.ListenAndServe()
		}
		if err != nil && err != http.ErrServerClosed {
			errCh <- err
		}
	}()

	select {
	case <-ctx.Done():
	case err := <-errCh:
		_ = apiServer.Close()
		_ = monitorServer.Close()
		return err
	}

	shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_ = apiServer.Shutdown(shutdownCtx)
	_ = monitorServer.Shutdown(shutdownCtx)

	s.sessions.stop()
	s.sessionPool.Close()

	// Stop audit chain verifier.
	if s.cfg.AuditTable != nil {
		s.cfg.AuditTable.StopVerifier()
	}

	return nil
}

func (s *Server) apiMux() http.Handler {
	mux := http.NewServeMux()

	mux.HandleFunc("/api/query", s.withRateLimit(s.withMethod(http.MethodPost, s.cfg.Auth.Middleware(s.handleQuery))))
	mux.HandleFunc("/api/query/stream", s.withRateLimit(s.withMethod(http.MethodPost, s.cfg.Auth.Middleware(s.handleQueryStream))))
	mux.HandleFunc("/api/transaction", s.withRateLimit(s.withMethod(http.MethodPost, s.cfg.Auth.Middleware(s.handleTransaction))))
	mux.HandleFunc("/api/batch", s.withRateLimit(s.withMethod(http.MethodPost, s.cfg.Auth.Middleware(s.handleBatch))))
	mux.HandleFunc("/api/live", s.withRateLimit(s.cfg.Auth.Middleware(s.handleLiveQuery)))
	mux.HandleFunc("/api/docs/openapi.json", s.withRateLimit(s.withMethod(http.MethodGet, s.cfg.Auth.Middleware(s.handleOpenAPI))))
	mux.HandleFunc("/api/databases", s.withRateLimit(s.withMethod(http.MethodGet, s.cfg.Auth.Middleware(s.handleListDatabases))))
	mux.HandleFunc("/api/databases/", s.withRateLimit(s.cfg.Auth.Middleware(s.handleDatabasesSubroutes)))
	mux.HandleFunc("/api/v2/handshake", s.withRateLimit(s.withMethod(http.MethodPost, s.cfg.Auth.Middleware(s.handleHandshake))))

	mux.HandleFunc("/health", s.withMethod(http.MethodGet, s.handleHealth))
	mux.HandleFunc("/ready", s.withMethod(http.MethodGet, s.handleReady))
	mux.HandleFunc("/metrics", s.withRateLimit(s.withMethod(http.MethodGet, s.cfg.Auth.Middleware(s.handleMetrics))))
	mux.HandleFunc("/dashboard", s.withMethod(http.MethodGet, s.cfg.Auth.Middleware(s.handleDashboard)))
	mux.HandleFunc("/admin/security-status", s.withMethod(http.MethodGet, s.cfg.Auth.Middleware(s.handleSecurityStatus)))
	mux.HandleFunc("/admin/revoke-token", s.withMethod(http.MethodPost, s.cfg.Auth.Middleware(s.handleRevokeToken)))

	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/health" || r.URL.Path == "/ready" || r.URL.Path == "/metrics" {
			return
		}
		http.NotFound(w, r)
	})

	return withPanicRecovery(s.withHTTPRedirect(s.withRequireTLSForToken(s.withTLSEnforcement(mux))))
}

func (s *Server) monitorMux() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("/health", s.withMethod(http.MethodGet, s.handleMonitorHealth))
	mux.HandleFunc("/ready", s.withMethod(http.MethodGet, s.handleReady))
	if s.cfg.Auth.Enabled() {
		mux.HandleFunc("/metrics", s.withRateLimit(s.withMethod(http.MethodGet, s.cfg.Auth.Middleware(s.handleMetrics))))
	} else {
		mux.HandleFunc("/metrics", s.withRateLimit(s.withMethod(http.MethodGet, s.handleMetrics)))
	}
	return withPanicRecovery(s.withHTTPRedirect(s.withRequireTLSForToken(s.withTLSEnforcement(mux))))
}
