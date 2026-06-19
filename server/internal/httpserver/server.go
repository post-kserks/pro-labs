package httpserver

import (
	"context"
	"crypto/tls"
	"embed"
	"fmt"
	"io/fs"
	"log/slog"
	"net/http"
	"strings"
	"sync/atomic"
	"time"

	"vaultdb/internal/ai"
	"vaultdb/internal/auth"
	"vaultdb/internal/config"
	"vaultdb/internal/executor"
	"vaultdb/internal/metrics"
	"vaultdb/internal/storage"
	"vaultdb/internal/txmanager"
)

//go:embed web/dist/*
var webUIFiles embed.FS

type Config struct {
	Host                      string
	Port                      int
	MonitorPort               int
	Version                   string
	MaxRequestSizeBytes       int
	MaxRows                   int
	QueryTimeoutSec           int
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
	MaxLiveQuerySubscriptions int
	MaxLiveQueryDurationSec   int
}

type Server struct {
	cfg                 Config
	startedAt           time.Time
	metrics             *metrics.Collector
	txm                 *txmanager.Manager
	br                  *executor.Broadcaster
	activeSubscriptions atomic.Int64
	nextSubID           atomic.Int64
}

func New(cfg Config) *Server {
	if cfg.Logger == nil {
		cfg.Logger = slog.Default()
	}
	if cfg.Auth == nil {
		mgr, err := auth.New(false, nil, cfg.Logger)
		if err != nil {
			cfg.Logger.Error("failed to create auth manager", "error", err)
			cfg.Logger.Warn("continuing with auth disabled")
			mgr, _ = auth.NewDisabled()
		}
		cfg.Auth = mgr
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
	}
}

func (s *Server) Start(ctx context.Context) error {
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
		tlsCfg := &tls.Config{
			MinVersion: tls.VersionTLS12,
			CipherSuites: []uint16{
				tls.TLS_ECDHE_ECDSA_WITH_AES_256_GCM_SHA384,
				tls.TLS_ECDHE_RSA_WITH_AES_256_GCM_SHA384,
				tls.TLS_ECDHE_ECDSA_WITH_AES_128_GCM_SHA256,
				tls.TLS_ECDHE_RSA_WITH_AES_128_GCM_SHA256,
			},
		}
		apiServer.TLSConfig = tlsCfg
		monitorServer.TLSConfig = tlsCfg
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

	return nil
}

func (s *Server) apiMux() *http.ServeMux {
	mux := http.NewServeMux()

	mux.HandleFunc("/api/query", s.withRateLimit(s.withMethod(http.MethodPost, s.cfg.Auth.Middleware(s.handleQuery))))
	mux.HandleFunc("/api/live", s.withRateLimit(s.cfg.Auth.Middleware(s.handleLiveQuery)))
	mux.HandleFunc("/api/docs/openapi.json", s.withRateLimit(s.withMethod(http.MethodGet, s.cfg.Auth.Middleware(s.handleOpenAPI))))
	mux.HandleFunc("/api/databases", s.withRateLimit(s.withMethod(http.MethodGet, s.cfg.Auth.Middleware(s.handleListDatabases))))
	mux.HandleFunc("/api/databases/", s.withRateLimit(s.cfg.Auth.Middleware(s.handleDatabasesSubroutes)))

	mux.HandleFunc("/health", s.withMethod(http.MethodGet, s.handleHealth))
	mux.HandleFunc("/ready", s.withMethod(http.MethodGet, s.handleReady))
	mux.HandleFunc("/metrics", s.withRateLimit(s.withMethod(http.MethodGet, s.cfg.Auth.Middleware(s.handleMetrics))))
	mux.HandleFunc("/dashboard", s.withMethod(http.MethodGet, s.cfg.Auth.Middleware(s.handleDashboard)))

	distFS, err := fs.Sub(webUIFiles, "web/dist")
	if err == nil {
		fileServer := http.FileServer(http.FS(distFS))
		mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
			if r.URL.Path == "/health" || r.URL.Path == "/ready" || r.URL.Path == "/metrics" {
				return
			}

			if s.cfg.Auth != nil && s.cfg.Auth.Enabled() {
				token := r.Header.Get("Authorization")
				if token == "" {
					token = r.URL.Query().Get("token")
				}
				if !s.cfg.Auth.ValidateToken(strings.TrimPrefix(token, "Bearer ")) {
					writeError(w, http.StatusUnauthorized, errCodeInternal, "unauthorized")
					return
				}
			}

			fileServer.ServeHTTP(w, r)
		})
	} else {
		mux.HandleFunc("/", func(w http.ResponseWriter, _ *http.Request) {
			http.Error(w, "web UI is not embedded", http.StatusNotFound)
		})
	}

	return mux
}

func (s *Server) monitorMux() *http.ServeMux {
	mux := http.NewServeMux()
	mux.HandleFunc("/health", s.withMethod(http.MethodGet, s.handleMonitorHealth))
	mux.HandleFunc("/ready", s.withMethod(http.MethodGet, s.handleReady))
	if s.cfg.Auth.Enabled() {
		mux.HandleFunc("/metrics", s.withRateLimit(s.withMethod(http.MethodGet, s.cfg.Auth.Middleware(s.handleMetrics))))
	} else {
		mux.HandleFunc("/metrics", s.withRateLimit(s.withMethod(http.MethodGet, s.handleMetrics)))
	}
	return mux
}
