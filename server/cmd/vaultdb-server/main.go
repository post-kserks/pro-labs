package main

import (
	"bufio"
	"context"
	"crypto/rand"
	"crypto/tls"
	"encoding/hex"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"time"

	"vaultdb/internal/ai"
	"vaultdb/internal/auth"
	"vaultdb/internal/config"
	"vaultdb/internal/executor"
	"vaultdb/internal/httpserver"
	"vaultdb/internal/metrics"
	"vaultdb/internal/parser"
	"vaultdb/internal/pool"
	"vaultdb/internal/protocol"
	"vaultdb/internal/storage"
	vaulttls "vaultdb/internal/tls"
	"vaultdb/internal/txmanager"
	"vaultdb/internal/wal"
)

// version и buildDate перезаписываются через ldflags при сборке
// (единый источник истины — файл VERSION в корне репозитория).
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

	pageStore, err := storage.NewPageStorageEngine(dataDir, w, txm)
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

func runHTTPServer(ctx context.Context, cfg *config.Config, host string, httpPort, monitorPort int, store storage.StorageEngine, authManager *auth.Manager, metricsCollector *metrics.Collector, txm *txmanager.Manager, br *executor.Broadcaster, embedder ai.Embedder, activeConnections func() int64, logger *slog.Logger, tlsCert, tlsKey string) <-chan error {
	rateLimiter := httpserver.NewRateLimiter(100, 200) // 100 req/s, burst 200
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
		ResultCacheTTLSec:         cfg.Storage.ResultCacheTTL_s,
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

func handleConnection(conn net.Conn, store storage.StorageEngine, m *metrics.Collector, txm *txmanager.Manager, br *executor.Broadcaster, authManager *auth.Manager, embedder ai.Embedder, serverWAL *wal.WAL, logger *slog.Logger, maxRequestSize int, queryTimeoutSec int, maxRows int, tcpKeepAliveSec int, tcpIdleTimeoutSec int, maxPreparedStmts int) {
	defer func() {
		if r := recover(); r != nil {
			logger.Error("panic in connection handler",
				"remote", conn.RemoteAddr(),
				"panic", r)
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
	if embedder != nil {
		session.SetEmbedder(embedder)
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
	defer func() {
		if session.IsInTx() {
			logger.Warn("connection closed with active transaction, rolling back",
				"tx_id", session.ActiveTx.ID)
			session.ActiveTx.Rollback()
		}
	}()

	// Per-connection rate limiter: 100 requests/second, burst 200
	connLimiter := NewConnectionRateLimiter(100, 200)

	scanner := bufio.NewScanner(conn)
	scanner.Buffer(make([]byte, 0, 64*1024), maxRequestSize)

	for scanner.Scan() {
		// Reset deadline on successful read
		conn.SetDeadline(time.Now().Add(5 * time.Minute))
		line := scanner.Bytes()

		// Rate limit check
		if !connLimiter.Allow() {
			if !sendError(conn, "", "rate limit exceeded", logger) {
				return
			}
			continue
		}

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

// sendError отправляет ошибку клиенту. Возвращает false, если запись в сокет
// не удалась (клиент отвалился) — в этом случае обрабатывать соединение дальше
// бессмысленно.
func sendError(conn net.Conn, id, message string, logger *slog.Logger) bool {
	resp := protocol.Response{
		ID:      id,
		Status:  "error",
		Type:    "error",
		Columns: []string{},
		Rows:    [][]string{},
		Message: sanitizeErrorMessage(message),
	}
	if err := writeResponse(conn, resp); err != nil {
		logger.Debug("failed to send error response, client disconnected",
			"conn", conn.RemoteAddr(),
			"error", err)
		return false
	}
	return true
}

// sanitizeErrorMessage удаляет внутренние детали из сообщений об ошибках
// перед отправкой клиенту. Сохраняет общее описание, но скрывает пути файлов
// и технические детали реализации.
func sanitizeErrorMessage(msg string) string {
	// Detect filesystem paths: starts with / or contains common path patterns
	lower := strings.ToLower(msg)
	if strings.HasPrefix(msg, "/") ||
		strings.Contains(lower, "/go/src/") ||
		strings.Contains(lower, "\\go\\src\\") ||
		strings.Contains(lower, "/tmp/") ||
		strings.Contains(lower, "heapfile") ||
		strings.Contains(lower, ".go:") {
		return "internal storage error"
	}
	// Если сообщение слишком длинное — обрезаем
	if len(msg) > 200 {
		return msg[:200] + "..."
	}
	return msg
}

func sendResult(conn net.Conn, id string, result *executor.Result) error {
	if result == nil {
		result = &executor.Result{}
	}
	columns := result.Columns
	if columns == nil {
		columns = []string{}
	}

	rows := result.Rows
	if rows == nil {
		rows = [][]string{}
	}

	resp := protocol.Response{
		ID:       id,
		Status:   "ok",
		Type:     result.Type,
		Columns:  columns,
		Rows:     rows,
		Affected: result.Affected,
		Message:  result.Message,
		AsOfNote: result.AsOfNote,
	}
	return writeResponse(conn, resp)
}

func writeResponse(conn net.Conn, response protocol.Response) error {
	bytes, err := json.Marshal(response)
	if err != nil {
		return err
	}
	bytes = append(bytes, '\n')
	_, err = conn.Write(bytes)
	return err
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
	tlsCA := flag.String("tls-ca", "", "Path to CA file for mTLS client verification")
	flag.Parse()

	if *healthCheck {
		os.Exit(runHealthCheck(*monitorPort))
	}

	logger := setupLogger(os.Getenv("VAULTDB_LOG_LEVEL"))
	cfg := loadConfig(*configPath, logger)

	// CLI-флаги имеют приоритет над vaultdb.yaml: значения из конфига
	// применяются только для флагов, которые не были заданы явно.
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

	// Embedding-провайдер для SEMANTIC_MATCH/AI_EMBED. Без настроенного AI
	// эти операции возвращают понятную ошибку (NoopEmbedder в executor).
	var embedder ai.Embedder
	if cfg.AI.Endpoint != "" {
		apiKey := cfg.AI.APIKey
		if envKey := strings.TrimSpace(os.Getenv("VAULTDB_AI_API_KEY")); envKey != "" {
			apiKey = envKey
		}
		embedder = ai.NewHTTPEmbedder(cfg.AI.Endpoint, cfg.AI.Model, apiKey)
		logger.Info("AI embedder configured",
			"provider", cfg.AI.Provider,
			"endpoint", cfg.AI.Endpoint,
			"model", cfg.AI.Model)
	} else {
		logger.Info("AI embedder not configured; SEMANTIC_MATCH and AI_EMBED will return a configuration error")
	}

	httpErrCh := runHTTPServer(ctx, cfg, *host, *httpPort, *monitorPort, store, authManager, metricsCollector, txm, br, embedder, func() int64 {
		return activeConnections.Load()
	}, logger, *tlsCert, *tlsKey)

	maxRequestSize := cfg.Server.MaxRequestSizeBytes
	if maxRequestSize <= 0 {
		maxRequestSize = config.DefaultMaxRequestSize
	}

	addr := fmt.Sprintf("%s:%d", *host, *port)
	var listener net.Listener
	if *tlsCert != "" && *tlsKey != "" {
		// TLS mode
		var tlsCfg *tls.Config
		var err error
		if *tlsCA != "" {
			tlsCfg, err = vaulttls.LoadMTLSConfig(*tlsCert, *tlsKey, *tlsCA)
		} else {
			tlsCfg, err = vaulttls.LoadTLSConfig(*tlsCert, *tlsKey)
		}
		if err != nil {
			logger.Error("failed to load TLS config", "error", err)
			os.Exit(1)
		}
		plainListener, err := net.Listen("tcp", addr)
		if err != nil {
			logger.Error("tcp listen failed", "error", err)
			os.Exit(1)
		}
		listener = vaulttls.WrapListener(plainListener, tlsCfg)
		if *tlsCA != "" {
			logger.Info("tcp server started with mTLS", "addr", addr, "ca_file", *tlsCA)
		} else {
			logger.Info("tcp server started with TLS", "addr", addr)
		}
	} else {
		// Plain TCP mode
		var err error
		listener, err = net.Listen("tcp", addr)
		if err != nil {
			logger.Error("tcp listen failed", "error", err)
			os.Exit(1)
		}
		logger.Info("tcp server started", "addr", addr)
	}

	// Connection pool
	maxConns := cfg.Server.MaxConnections
	connPool := pool.NewPool(poolInitialCapacity, maxConns, poolIdleTimeout, nil)

	go func() {
		<-ctx.Done()
		if err := listener.Close(); err != nil {
			logger.Warn("listener close failed", "error", err)
		}
		connPool.Close()
	}()

	var wg sync.WaitGroup
	acceptDone := make(chan struct{})
	go func() {
		defer close(acceptDone)
		for {
			conn, err := listener.Accept()
			if err != nil {
				select {
				case <-ctx.Done():
					return
				default:
					logger.Warn("accept failed", "error", err)
					continue
				}
			}

			// Acquire connection from pool (non-blocking)
			connObj, err := connPool.Acquire()
			if connObj == nil {
				// Pool is full — reject connection
				logger.Warn("connection pool full, rejecting connection",
					"remote", conn.RemoteAddr(),
					"max_connections", maxConns,
					"error", err)
				conn.Close()
				continue
			}

			activeConnections.Add(1)
			metricsCollector.IncConnections()
			wg.Add(1)
			go func(c net.Conn, connInfo *pool.Connection) {
				defer wg.Done()
				defer activeConnections.Add(-1)
				defer metricsCollector.DecConnections()
				defer connPool.Release(connInfo) // Release back to pool
				handleConnection(c, store, metricsCollector, txm, br, authManager, embedder, serverWAL, logger, maxRequestSize, cfg.Server.QueryTimeoutSec, cfg.Server.MaxRows, cfg.Server.TCPKeepAliveSec, cfg.Server.TCPIdleTimeoutSec, cfg.Server.MaxPreparedStmts)
			}(conn, connObj)
		}
	}()

	select {
	case <-ctx.Done():
		logger.Info("shutdown signal received")
	case err := <-httpErrCh:
		logger.Error("http server failed", "error", err)
		stop()
	}

	// Stop accepting new connections
	<-acceptDone

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

	connPool.Close()

	// Final checkpoint
	logger.Info("writing final WAL checkpoint")
	if err := store.FinalCheckpoint(); err != nil {
		logger.Error("final checkpoint failed", "error", err)
	}
	logger.Info("shutdown complete")
}

func runHealthCheck(monitorPort int) int {
	url := fmt.Sprintf("http://127.0.0.1:%d/health", monitorPort)
	client := &http.Client{
		Timeout: 3 * time.Second,
		Transport: &http.Transport{
			// Health check always targets localhost — TLS verification is unnecessary
			// and would fail with self-signed certs generated by the server.
			TLSClientConfig: &tls.Config{InsecureSkipVerify: true}, // #nosec G402 — localhost only
		},
	}

	resp, err := client.Get(url)
	if err != nil {
		// Try HTTPS if HTTP fails (TLS might be enabled)
		httpsURL := fmt.Sprintf("https://127.0.0.1:%d/health", monitorPort)
		resp, err = client.Get(httpsURL)
		if err != nil {
			fmt.Fprintf(os.Stderr, "health check failed: %v\n", err)
			return 1
		}
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		fmt.Fprintf(os.Stderr, "health check failed: status=%d body=%s\n", resp.StatusCode, string(body))
		return 1
	}
	return 0
}

func envBool(name string, fallback bool) bool {
	value := strings.TrimSpace(strings.ToLower(os.Getenv(name)))
	if value == "" {
		return fallback
	}
	switch value {
	case "1", "true", "yes", "on":
		return true
	case "0", "false", "no", "off":
		return false
	default:
		return fallback
	}
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
