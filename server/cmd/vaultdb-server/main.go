package main

import (
	"bufio"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"flag"
	"fmt"
	"log/slog"
	"net"
	"os"
	"os/signal"
	"path/filepath"
	"runtime/debug"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"time"

	"vaultdb/internal/auth"
	"vaultdb/internal/config"
	"vaultdb/internal/core/ai"
	"vaultdb/internal/core/audit"
	"vaultdb/internal/core/executor"
	"vaultdb/internal/core/metrics"
	"vaultdb/internal/core/parser"
	"vaultdb/internal/core/storage"
	"vaultdb/internal/core/txmanager"
	"vaultdb/internal/core/wal"
	"vaultdb/internal/httpserver"
	"vaultdb/internal/protocol"
	"vaultdb/internal/protocol/pgwire"
)

// version and buildDate are overwritten via ldflags at build time
// (single source of truth — VERSION file in the repository root).
var (
	version   = "dev"
	buildDate = "unknown"
)

const (
	checkpointInterval    = 30 * time.Second
	metricsUpdateInterval = 30 * time.Second
	poolInitialCapacity   = 10
	poolIdleTimeout       = 5 * time.Minute
)

func setupLogger(logLevel string) *slog.Logger {
	level := slog.LevelInfo
	if logLevel == "debug" {
		level = slog.LevelDebug
	}
	logger := slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{Level: level}))
	slog.SetDefault(logger)
	return logger
}

func loadConfig(cfgPath string, logger *slog.Logger) *config.Config {
	if cfgPath != "" {
		if _, err := os.Stat(cfgPath); err != nil {
			logger.Warn("config file not found, using defaults", "path", cfgPath, "error", err)
			cfgPath = ""
		}
	}
	cfg, err := config.Load(cfgPath)
	if err != nil {
		logger.Error("failed to load config", "error", err)
		os.Exit(1)
	}
	config.ApplyEnvOverrides(cfg)
	return cfg
}

func setupStorage(cfg *config.Config, dataDir string, ctx context.Context, txm *txmanager.Manager, metricsCollector *metrics.Collector, logger *slog.Logger) (storage.StorageEngine, *wal.WAL) {
	walPath := filepath.Join(dataDir, "wal", "vaultdb.wal")
	w, err := wal.Open(walPath)
	if err != nil {
		logger.Error("failed to open WAL", "error", err)
		os.Exit(1)
	}

	w.OnAppend = func() { metricsCollector.IncWALEntries() }

	pageStore, err := storage.NewPageStorageEngine(dataDir, w, txm, &storage.StorageOptions{BufferPoolPages: cfg.Storage.BufferPoolPages})
	if err != nil {
		logger.Error("failed to open page storage engine", "error", err)
		os.Exit(1)
	}

	if err := pageStore.RecoverFromWAL(); err != nil {
		logger.Error("WAL recovery failed", "error", err)
		os.Exit(1)
	}

	go pageStore.CheckpointLoop(ctx, checkpointInterval)

	logger.Info("using page-based storage engine")
	return pageStore, w
}

func runHTTPServer(ctx context.Context, cfg *config.Config, host string, httpPort, monitorPort int, store storage.StorageEngine, authManager *auth.Manager, metricsCollector *metrics.Collector, txm *txmanager.Manager, br *executor.Broadcaster, embedder ai.Embedder, activeConnections func() int64, logger *slog.Logger, tlsCert, tlsKey string, auditLog *audit.TableLog) <-chan error {
	rateLimiter := httpserver.NewRateLimiter(cfg.Server.RateLimitRPS, cfg.Server.RateLimitBurst)
	httpSrv := httpserver.New(httpserver.Config{
		Host:                      host,
		Port:                      httpPort,
		MonitorPort:               monitorPort,
		Version:                   version,
		MaxRequestSizeBytes:       cfg.Server.MaxRequestSizeBytes,
		MaxRows:                   cfg.Server.MaxRows,
		QueryTimeoutSec:           cfg.Server.QueryTimeoutSec,
		MaxPreparedStmts:          cfg.Server.MaxPreparedStmts,
		ResultCacheSize:           cfg.Storage.ResultCacheSize,
		ResultCacheTTLSec:         cfg.Storage.ResultCacheTTLS,
		AllowedOrigins:            cfg.Server.AllowedOrigins,
		Storage:                   store,
		Auth:                      authManager,
		Logger:                    logger,
		Metrics:                   metricsCollector,
		TxManager:                 txm,
		ActiveConnections:         activeConnections,
		Broadcaster:               br,
		Embedder:                  embedder,
		RateLimiter:               rateLimiter,
		TLSCertFile:               tlsCert,
		TLSKeyFile:                tlsKey,
		MaxLiveQuerySubscriptions: cfg.Server.LiveQueries.BufferSize,
		MaxLiveQueryDurationSec:   cfg.Server.QueryTimeoutSec,
		AuditTable:                auditLog,
		AuditVerifyInterval:       time.Duration(cfg.Audit.VerifyIntervalSec) * time.Second,
	})
	httpErrCh := make(chan error, 1)
	go func() {
		if err := httpSrv.Start(ctx); err != nil {
			httpErrCh <- err
		}
	}()
	return httpErrCh
}

// ConnectionRateLimiter is a simple token bucket for per-connection rate limiting.
type ConnectionRateLimiter struct {
	mu        sync.Mutex
	tokens    float64
	lastTime  time.Time
	rate      float64
	maxTokens float64
}

func NewConnectionRateLimiter(rate, burst int) *ConnectionRateLimiter {
	return &ConnectionRateLimiter{
		tokens:    float64(burst),
		lastTime:  time.Now(),
		rate:      float64(rate),
		maxTokens: float64(burst),
	}
}

func (l *ConnectionRateLimiter) Allow() bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	now := time.Now()
	elapsed := now.Sub(l.lastTime).Seconds()
	l.tokens += elapsed * l.rate
	if l.tokens > l.maxTokens {
		l.tokens = l.maxTokens
	}
	l.lastTime = now

	if l.tokens >= 1 {
		l.tokens--
		return true
	}
	return false
}

func handleConnection(ctx context.Context, conn net.Conn, store storage.StorageEngine, m *metrics.Collector, txm *txmanager.Manager, br *executor.Broadcaster, authManager *auth.Manager, embedder ai.Embedder, serverWAL *wal.WAL, logger *slog.Logger, maxRequestSize int, queryTimeoutSec int, maxRows int, tcpKeepAliveSec int, tcpIdleTimeoutSec int, maxPreparedStmts int, rateLimitRPS int, rateLimitBurst int, auditLog *audit.TableLog) {
	defer func() {
		if r := recover(); r != nil {
			logger.Error("panic in connection handler",
				"remote", conn.RemoteAddr(),
				"panic", r,
				"stack", string(debug.Stack()))
			sendError(conn, "", "internal server error", logger)
		}
	}()
	defer conn.Close()

	// Set TCP keepalive and idle timeout
	if tcpConn, ok := conn.(*net.TCPConn); ok {
		tcpConn.SetKeepAlive(true)
		tcpConn.SetKeepAlivePeriod(time.Duration(tcpKeepAliveSec) * time.Second)
	}
	conn.SetDeadline(time.Now().Add(time.Duration(tcpIdleTimeoutSec) * time.Second))

	session := executor.NewSession(store, m, txm, br)
	session.SetServerContext(ctx)
	if embedder != nil {
		session.SetEmbedder(embedder)
	}
	if authManager != nil {
		session.SetAuthManager(authManager)
	}
	if serverWAL != nil {
		session.SetWAL(serverWAL)
	}
	if queryTimeoutSec > 0 {
		session.SetQueryTimeout(time.Duration(queryTimeoutSec) * time.Second)
	}
	if maxRows > 0 {
		session.SetMaxRows(maxRows)
	}
	if maxPreparedStmts > 0 {
		session.SetMaxPreparedStatements(maxPreparedStmts)
	}
	if auditLog != nil {
		session.AuditTable = auditLog
	}
	defer func() {
		if session.IsInTx() {
			logger.Warn("connection closed with active transaction, rolling back",
				"tx_id", session.ActiveTx.ID)
			session.ActiveTx.Rollback(nil)
		}
	}()

	// Per-connection rate limiter uses the same config as HTTP rate limiter
	connLimiter := NewConnectionRateLimiter(rateLimitRPS, rateLimitBurst)

	scanner := bufio.NewScanner(conn)
	scanner.Buffer(make([]byte, 0, 64*1024), maxRequestSize)

	for scanner.Scan() {
		// Reset deadline on successful read
		conn.SetDeadline(time.Now().Add(time.Duration(tcpIdleTimeoutSec) * time.Second))
		line := scanner.Bytes()

		// Rate limit check
		if !connLimiter.Allow() {
			if !sendError(conn, "", "rate limit exceeded", logger) {
				return
			}
			continue
		}

		// Peek at message type to detect handshake
		var rawMsg map[string]interface{}
		if err := json.Unmarshal(line, &rawMsg); err != nil {
			if !sendError(conn, "", "invalid JSON request", logger) {
				return
			}
			continue
		}

		if msgType, _ := rawMsg["type"].(string); msgType == "handshake" {
			var hs protocol.HandshakeRequest
			if err := json.Unmarshal(line, &hs); err != nil {
				if !sendError(conn, "", "invalid handshake", logger) {
					return
				}
				continue
			}

			// Validate major version
			if hs.ClientVersion != "" && len(hs.ClientVersion) > 0 && hs.ClientVersion[0] != '2' {
				errResp := protocol.HandshakeResponse{
					Type:            "handshake",
					ProtocolVersion: "2.0",
					Server:          "VaultDB",
					ServerVersion:   version,
				}
				writeResponse(conn, errResp)
				sendError(conn, "", "incompatible protocol version", logger)
				return
			}

			resp := protocol.HandshakeResponse{
				Type:              "handshake",
				ProtocolVersion:   "2.0",
				Server:            "VaultDB",
				ServerVersion:     version,
				SupportedFeatures: []string{"time_travel", "transactions", "prepared_statements"},
			}
			if err := writeResponse(conn, resp); err != nil {
				return
			}
			continue
		}

		// Non-handshake: process as regular request (v1 compatible)

		var req protocol.Request
		if err := json.Unmarshal(line, &req); err != nil {
			if !sendError(conn, "", "invalid JSON request", logger) {
				return
			}
			continue
		}

		if !authManager.ValidateToken(req.Token) {
			if !sendError(conn, req.ID, "unauthorized: invalid or missing token", logger) {
				return
			}
			continue
		}

		stmt, err := parser.Parse(req.Query)
		if err != nil {
			if !sendError(conn, req.ID, err.Error(), logger) {
				return
			}
			continue
		}

		result, err := session.Execute(stmt)
		if err != nil {
			if !sendError(conn, req.ID, err.Error(), logger) {
				return
			}
			continue
		}

		if err := sendResult(conn, req.ID, result); err != nil {
			logger.Warn("write response failed", "error", err)
			return
		}
	}

	if err := scanner.Err(); err != nil {
		logger.Warn("connection scanner error", "error", err)
	}
}

func main() {
	host := flag.String("host", "127.0.0.1", "Host to listen on")
	port := flag.Int("port", 5432, "TCP port for SQL clients")
	httpPort := flag.Int("http-port", 8080, "HTTP port for REST API and Web UI")
	monitorPort := flag.Int("monitor-port", 5433, "HTTP port for health and metrics")
	dataDir := flag.String("data", "./data", "Path to data directory")
	configPath := flag.String("config", "", "Optional config file path")
	healthCheck := flag.Bool("health-check", false, "Run one health check against monitor port and exit")
	tlsCert := flag.String("tls-cert", "", "Path to TLS certificate file")
	tlsKey := flag.String("tls-key", "", "Path to TLS private key file")
	// tlsCA removed
	flag.Parse()

	if *healthCheck {
		os.Exit(runHealthCheck(*monitorPort))
	}

	logger := setupLogger(os.Getenv("VAULTDB_LOG_LEVEL"))
	cfg := loadConfig(*configPath, logger)

	// CLI flags take priority over vaultdb.yaml: config values
	// are applied only for flags that were not explicitly set.
	setFlags := make(map[string]bool)
	flag.Visit(func(f *flag.Flag) { setFlags[f.Name] = true })

	if !setFlags["host"] {
		*host = cfg.Server.Host
	}
	if !setFlags["port"] {
		*port = cfg.Server.Port
	}
	if !setFlags["http-port"] {
		*httpPort = cfg.Server.HTTPPort
	}
	if !setFlags["monitor-port"] {
		*monitorPort = cfg.Server.MonitorPort
	}
	if !setFlags["data"] {
		*dataDir = cfg.Storage.DataDir
	}
	logger.Info("starting vaultdb server",
		"version", version,
		"build_date", buildDate,
		"host", *host,
		"port", *port,
		"http_port", *httpPort,
		"monitor_port", *monitorPort,
		"data_dir", *dataDir,
		"config", *configPath)

	metricsCollector := metrics.New()

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	txm := txmanager.NewManager()

	store, serverWAL := setupStorage(cfg, *dataDir, ctx, txm, metricsCollector, logger)
	if serverWAL != nil {
		defer serverWAL.Close()
	}
	defer func() {
		if err := store.Close(); err != nil {
			logger.Warn("storage close failed", "error", err)
		}
	}()

	br := executor.NewBroadcaster()
	br.Configure(
		executor.ParseDropPolicy(cfg.Server.LiveQueries.DropPolicy),
		time.Duration(cfg.Server.LiveQueries.BlockTimeoutS)*time.Second,
		cfg.Server.LiveQueries.BufferSize,
		logger)

	// Audit log table.
	auditLog := audit.NewTableLog(store)
	if err := auditLog.EnsureTable(); err != nil {
		logger.Warn("failed to create audit log table", "error", err)
	}

	var activeConnections atomic.Int64

	// Start storage metrics background updater
	go func() {
		defer func() {
			if r := recover(); r != nil {
				slog.Error("panic in metrics updater", "panic", r)
			}
		}()
		ticker := time.NewTicker(metricsUpdateInterval)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				updateStorageMetrics(store, metricsCollector)
			}
		}
	}()

	// Start autovacuum
	av := storage.NewAutoVacuum(store, 0.2, 1*time.Minute, logger)
	go av.Run(ctx)

	authEnabled := envBool("VAULTDB_AUTH_ENABLED", cfg.Auth.Enabled)
	if authEnabled && os.Getenv("VAULTDB_AUTH_SECRET") == "" {
		logger.Error("VAULTDB_AUTH_SECRET is REQUIRED. " +
			"Set it in the environment before starting the server.")
		os.Exit(1)
	}
	tokens := tokensFromEnv()
	if authEnabled && len(tokens) == 0 {
		token, err := generateToken()
		if err != nil {
			logger.Error("failed to generate auth token", "error", err)
			os.Exit(1)
		}
		tokens = map[string]string{token: "generated"}
		tokenPath := filepath.Join(cfg.Storage.DataDir, ".generated-token")
		f, ferr := os.OpenFile(tokenPath, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0o400)
		if ferr != nil {
			logger.Error("failed to save generated token to file",
				"path", tokenPath, "error", ferr)
		} else {
			if _, werr := f.WriteString(token + "\n"); werr != nil {
				logger.Warn("could not write token to file", "path", tokenPath, "error", werr)
			} else if serr := f.Sync(); serr != nil {
				logger.Warn("could not fsync token file", "path", tokenPath, "error", serr)
			}
			f.Close()
			logger.Warn("no API tokens configured; generated a one-time token",
				"token_file", tokenPath,
				"action", "read the token, then delete the file and set VAULTDB_API_TOKENS env var")
			defer func() {
				if err := os.Remove(tokenPath); err != nil {
					logger.Warn("could not delete generated token file",
						"path", tokenPath, "error", err)
				}
			}()
		}
	}
	authManager, err := auth.New(authEnabled, tokens, logger, cfg.Auth.RateWindowSec, cfg.Auth.MaxFails, cfg.Auth.BlockForSec)
	if err != nil {
		logger.Error("failed to create auth manager", "error", err)
		os.Exit(1)
	}
	authManager.SetLocalhostBypass(cfg.Auth.LocalhostBypass)

	// Embedding provider for SEMANTIC_MATCH/AI_EMBED. Without configured AI,
	// these operations return a clear error (NoopEmbedder in executor).
	var embedder ai.Embedder
	if cfg.AI.Endpoint != "" {
		apiKey := cfg.AI.APIKey
		if envKey := strings.TrimSpace(os.Getenv("VAULTDB_AI_API_KEY")); envKey != "" {
			apiKey = envKey
		}
		var e ai.Embedder = ai.NewHTTPEmbedder(cfg.AI.Endpoint, cfg.AI.Model, apiKey)
		if cfg.AI.CacheEnabled {
			cap := cfg.AI.CacheSize
			e = ai.NewCachedEmbedder(e, cap, 0)
		}
		embedder = e
		logger.Info("AI embedder configured",
			"provider", cfg.AI.Provider,
			"endpoint", cfg.AI.Endpoint,
			"model", cfg.AI.Model,
			"cache", cfg.AI.CacheEnabled)
	} else {
		logger.Info("AI embedder not configured; SEMANTIC_MATCH and AI_EMBED will return a configuration error")
	}

	httpErrCh := runHTTPServer(ctx, cfg, *host, *httpPort, *monitorPort, store, authManager, metricsCollector, txm, br, embedder, func() int64 {
		return activeConnections.Load()
	}, logger, *tlsCert, *tlsKey, auditLog)

	addr := fmt.Sprintf("%s:%d", *host, *port)
	pgServer := pgwire.NewServer(addr, store, metricsCollector, txm, br, serverWAL, authManager, logger)
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		if err := pgServer.Start(ctx); err != nil {
			logger.Error("PGWire server failed", "error", err)
		}
	}()

	select {
	case <-ctx.Done():
		logger.Info("shutdown signal received")
	case err := <-httpErrCh:
		logger.Error("http server failed", "error", err)
		stop()
	}

	pgServer.Stop()

	// Wait for active connections with timeout
	logger.Info("waiting for active connections to finish", "active", activeConnections.Load())
	done := make(chan struct{})
	go func() {
		wg.Wait()
		close(done)
	}()

	select {
	case <-done:
		logger.Info("all connections closed gracefully")
	case <-time.After(time.Duration(cfg.Server.ShutdownTimeoutSec) * time.Second):
		logger.Warn("shutdown timeout reached, forcing close of remaining connections")
	}

	// Final checkpoint
	logger.Info("writing final WAL checkpoint")
	if err := store.FinalCheckpoint(); err != nil {
		logger.Error("final checkpoint failed", "error", err)
	}
	logger.Info("shutdown complete")
}

func generateToken() (string, error) {
	buf := make([]byte, 24)
	if _, err := rand.Read(buf); err != nil {
		return "", err
	}
	return "vdb_sk_" + hex.EncodeToString(buf), nil
}

func tokensFromEnv() map[string]string {
	raw := strings.TrimSpace(os.Getenv("VAULTDB_API_TOKENS"))
	if raw == "" {
		return nil
	}

	parts := strings.Split(raw, ",")
	tokens := make(map[string]string, len(parts))
	for i, part := range parts {
		token := strings.TrimSpace(part)
		if token == "" {
			continue
		}
		tokens[token] = fmt.Sprintf("env-token-%d", i+1)
	}
	if len(tokens) == 0 {
		return nil
	}
	return tokens
}

func updateStorageMetrics(s storage.StorageEngine, m *metrics.Collector) {
	dbs, err := s.ListDatabases()
	if err != nil {
		slog.Warn("updateStorageMetrics: list databases", "error", err)
		return
	}
	active := make(map[string]map[string]bool)
	for _, db := range dbs {
		tables, err := s.ListTables(db)
		if err != nil {
			slog.Warn("updateStorageMetrics: list tables", "db", db, "error", err)
			continue
		}
		active[db] = make(map[string]bool)
		for _, t := range tables {
			m.UpdateStorageRows(db, t.Name, int64(t.RowCount))
			active[db][t.Name] = true
		}
	}
	m.CleanStaleStorageRows(active)
}
