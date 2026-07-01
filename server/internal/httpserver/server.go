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
		mgr, err := auth.New(false, nil, cfg.Logger, 60, 10, 300)
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

	return nil
}

func (s *Server) apiMux() *http.ServeMux {
	mux := http.NewServeMux()

	mux.HandleFunc("/api/query", s.withRateLimit(s.withMethod(http.MethodPost, s.cfg.Auth.Middleware(s.handleQuery))))
	mux.HandleFunc("/api/query/stream", s.withRateLimit(s.withMethod(http.MethodPost, s.cfg.Auth.Middleware(s.handleQueryStream))))
	mux.HandleFunc("/api/transaction", s.withRateLimit(s.withMethod(http.MethodPost, s.cfg.Auth.Middleware(s.handleTransaction))))
	mux.HandleFunc("/api/batch", s.withRateLimit(s.withMethod(http.MethodPost, s.cfg.Auth.Middleware(s.handleBatch))))
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

			// Auth check for static files
			if s.cfg.Auth != nil && s.cfg.Auth.Enabled() {
				token := r.Header.Get("Authorization")
				if token == "" {
					token = r.Header.Get("X-VaultDB-Token")
				}
				if !s.cfg.Auth.ValidateToken(strings.TrimPrefix(token, "Bearer ")) {
					// Login page assets must be accessible without auth
					if r.URL.Path == "/login.js" || r.URL.Path == "/style.css" {
						// proceed to serve static file
					} else if !strings.HasPrefix(r.URL.Path, "/api/") {
						// Show login page for web UI (inline script)
						loginHTML := []byte("<!DOCTYPE html><html lang=\"ru\"><head><meta charset=\"UTF-8\"><meta name=\"viewport\" content=\"width=device-width, initial-scale=1.0\"><title>VaultDB — Авторизация</title><style>*{margin:0;padding:0;box-sizing:border-box}body{background:#0d1117;color:#e6edf3;font-family:-apple-system,BlinkMacSystemFont,sans-serif;display:flex;justify-content:center;align-items:center;min-height:100vh}.login-card{background:#161b22;border:1px solid #30363d;border-radius:12px;padding:40px;width:420px;max-width:90vw}.logo{text-align:center;margin-bottom:32px}.logo-icon{font-size:48px;color:#3fb950}.logo-text{font-size:28px;font-weight:600;margin-top:8px}h2{text-align:center;font-size:18px;color:#8b949e;margin-bottom:24px}label{display:block;font-size:13px;color:#8b949e;margin-bottom:6px}input{width:100%;padding:12px 14px;background:#0d1117;border:1px solid #30363d;border-radius:6px;color:#e6edf3;font-size:14px;font-family:monospace;outline:none}input:focus{border-color:#3fb950}.field{margin-bottom:16px}.btn{width:100%;padding:12px;background:#238636;border:none;border-radius:6px;color:#fff;font-size:15px;font-weight:600;cursor:pointer;margin-top:8px}.btn:hover{background:#2ea043}.hint{background:#1c2128;border:1px solid #30363d;border-radius:6px;padding:14px;margin-top:24px;font-size:12px;color:#8b949e;line-height:1.7}.hint strong{color:#e6edf3}.hint code{background:#0d1117;padding:2px 6px;border-radius:4px;font-family:monospace;font-size:11px}.error{color:#f85149;text-align:center;margin-top:12px;font-size:13px;display:none}</style></head><body><div class=\"login-card\"><div class=\"logo\"><div class=\"logo-icon\">⬡</div><div class=\"logo-text\">VaultDB</div></div><h2>Авторизация для доступа к Web UI</h2><form id=\"lf\"><div class=\"field\"><label>API Token</label><input type=\"text\" id=\"ti\" placeholder=\"vdb_sk_...\" autocomplete=\"off\" spellcheck=\"false\"></div><div class=\"error\" id=\"er\">Неверный токен</div><button type=\"submit\" class=\"btn\">Войти</button></form><div class=\"hint\"><strong>Как получить токен:</strong><br>Токен задаётся в VAULTDB_API_TOKENS при запуске сервера.<br><br><strong>Пример:</strong><br><code>VAULTDB_API_TOKENS=vdb_test_token_123</code><br><br><strong>Формат:</strong> <code>vdb_</code> + произвольная строка</div></div><script>document.getElementById('lf').onsubmit=function(e){e.preventDefault();var t=document.getElementById('ti').value.trim();if(!t){document.getElementById('er').style.display='block';return}localStorage.setItem('vaultdb_token',t);fetch('/health',{headers:{Authorization:'Bearer '+t}}).then(function(r){return r.json()}).then(function(h){						if(h.status==='ok'){window.location.replace('/')}else{document.getElementById('er').style.display='block'}}).catch(function(){document.getElementById('er').textContent='Ошибка подключения';document.getElementById('er').style.display='block'})};						var s=localStorage.getItem('vaultdb_token');if(s)document.getElementById('ti').value=s;</script></body></html>")
						w.Header().Set("Content-Type", "text/html; charset=utf-8")
						w.Write([]byte(loginHTML))
						return
					}
					writeError(w, http.StatusUnauthorized, errCodeInternal, "unauthorized")
					return
				}
			}

			// SPA fallback: if the file doesn't exist, serve index.html for client-side routing
			f, err := distFS.Open(strings.TrimPrefix(r.URL.Path, "/"))
			if err != nil {
				r.URL.Path = "/"
			} else {
				f.Close()
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
