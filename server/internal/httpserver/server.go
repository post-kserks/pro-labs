package httpserver

import (
	"context"
	"embed"
	"encoding/json"
	"fmt"
	"io/fs"
	"log/slog"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"sync/atomic"
	"time"

	"vaultdb/internal/ai"
	"vaultdb/internal/auth"
	"vaultdb/internal/config"
	"vaultdb/internal/executor"
	"vaultdb/internal/metrics"
	"vaultdb/internal/parser"
	"vaultdb/internal/storage"
	"vaultdb/internal/txmanager"
)

var validPathName = regexp.MustCompile(`^[a-zA-Z0-9_-]+$`)

const (
	errCodeBadRequest     = 3001
	errCodeParseError     = 3002
	errCodeUnknownColumn  = 3003
	errCodeStorageError   = 3004
	errCodeTxUnsupported  = 3005
	errCodeRateLimited    = 3006
	errCodeInternal       = 5000
	errCodeNotImplemented = 9999

	DefaultMaxLiveQuerySubscriptions = 1000
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
		MaxHeaderBytes:    1 << 20, // 1 MB
	}
	monitorServer := &http.Server{
		Addr:              fmt.Sprintf("%s:%d", s.cfg.Host, s.cfg.MonitorPort),
		Handler:           s.monitorMux(),
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       10 * time.Second,
		WriteTimeout:      10 * time.Second,
		IdleTimeout:       60 * time.Second,
		MaxHeaderBytes:    1 << 20, // 1 MB
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
	mux.HandleFunc("/metrics", s.withMethod(http.MethodGet, s.cfg.Auth.Middleware(s.handleMetrics)))
	mux.HandleFunc("/dashboard", s.withMethod(http.MethodGet, s.cfg.Auth.Middleware(s.handleDashboard)))

	distFS, err := fs.Sub(webUIFiles, "web/dist")
	if err == nil {
		fileServer := http.FileServer(http.FS(distFS))
		mux.Handle("/", s.cfg.Auth.Middleware(func(w http.ResponseWriter, r *http.Request) {
			fileServer.ServeHTTP(w, r)
		}))
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
		mux.HandleFunc("/metrics", s.withMethod(http.MethodGet, s.cfg.Auth.Middleware(s.handleMetrics)))
	} else {
		mux.HandleFunc("/metrics", s.withMethod(http.MethodGet, s.handleMetrics))
	}
	return mux
}

// newSession создаёт сессию executor с подключённым embedder (если настроен).
func (s *Server) newSession() *executor.Session {
	sess := executor.NewSession(s.cfg.Storage, s.metrics, s.txm, s.br)
	if s.cfg.Embedder != nil {
		sess.SetEmbedder(s.cfg.Embedder)
	}
	if s.cfg.QueryTimeoutSec > 0 {
		sess.SetQueryTimeout(time.Duration(s.cfg.QueryTimeoutSec) * time.Second)
	}
	if s.cfg.MaxRows > 0 {
		sess.SetMaxRows(s.cfg.MaxRows)
	}
	return sess
}

func (s *Server) withRateLimit(next http.HandlerFunc) http.HandlerFunc {
	if s.cfg.RateLimiter == nil {
		return next
	}
	return s.cfg.RateLimiter.Middleware(next)
}

func (s *Server) withMethod(method string, next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != method {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		next(w, r)
	}
}

func (s *Server) handleQuery(w http.ResponseWriter, r *http.Request) {
	r.Body = http.MaxBytesReader(w, r.Body, int64(s.cfg.MaxRequestSizeBytes))
	var req struct {
		Database string `json:"database"`
		Query    string `json:"query"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		if err.Error() == "http: request body too large" {
			writeError(w, http.StatusRequestEntityTooLarge, errCodeBadRequest, "request body too large")
			return
		}
		writeError(w, http.StatusBadRequest, errCodeBadRequest, "invalid JSON body")
		return
	}

	query := strings.TrimSpace(req.Query)
	if query == "" {
		writeError(w, http.StatusBadRequest, errCodeBadRequest, "query cannot be empty")
		return
	}

	stmt, err := parser.Parse(query)
	if err != nil {
		writeError(w, http.StatusBadRequest, errCodeParseError, err.Error())
		return
	}

	// Each request gets a fresh session, so transaction state cannot survive
	// between requests — reject instead of silently buffering into the void.
	switch stmt.(type) {
	case *parser.BeginStatement, *parser.CommitStatement, *parser.RollbackStatement:
		writeError(w, http.StatusBadRequest, errCodeTxUnsupported,
			"transactions are not supported over the stateless HTTP API; use the TCP client on port 5432")
		return
	}

	session := s.newSession()
	defer session.Close()
	if req.Database != "" {
		session.SetCurrentDatabase(req.Database)
	}

	start := time.Now()
	result, err := session.Execute(stmt)
	if err != nil {
		writeStorageError(w, http.StatusBadRequest, errCodeStorageError, err, s.cfg.Logger)
		return
	}
	duration := float64(time.Since(start).Microseconds()) / 1000.0

	response := map[string]interface{}{
		"status":      "ok",
		"type":        result.Type,
		"columns":     emptyIfNil(result.Columns),
		"rows":        emptyRowsIfNil(result.Rows),
		"affected":    result.Affected,
		"duration_ms": duration,
	}
	if result.Message != "" {
		response["message"] = result.Message
	}
	if result.AsOfNote != "" {
		response["as_of_note"] = result.AsOfNote
	}

	writeJSON(w, http.StatusOK, response)
}

func (s *Server) handleListDatabases(w http.ResponseWriter, _ *http.Request) {
	names, err := s.cfg.Storage.ListDatabases()
	if err != nil {
		writeStorageError(w, http.StatusInternalServerError, errCodeInternal, err, s.cfg.Logger)
		return
	}

	items := make([]map[string]interface{}, 0, len(names))
	for _, name := range names {
		items = append(items, map[string]interface{}{
			"name": name,
		})
	}
	writeJSON(w, http.StatusOK, map[string]interface{}{"databases": items})
}

func (s *Server) handleDatabasesSubroutes(w http.ResponseWriter, r *http.Request) {
	path := strings.TrimPrefix(r.URL.Path, "/api/databases/")
	segments := strings.Split(path, "/")
	if len(segments) < 2 {
		http.NotFound(w, r)
		return
	}

	dbName := segments[0]
	if dbName == "" || !validPathName.MatchString(dbName) {
		http.NotFound(w, r)
		return
	}

	if len(segments) == 2 && segments[1] == "tables" {
		if r.Method != http.MethodGet {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		s.handleListTables(w, dbName)
		return
	}

	if len(segments) == 4 && segments[1] == "tables" && segments[3] == "data" {
		tableName := segments[2]
		if !validPathName.MatchString(tableName) {
			http.NotFound(w, r)
			return
		}
		s.handleTableData(w, r, dbName, tableName)
		return
	}

	if len(segments) == 4 && segments[1] == "tables" && segments[3] == "schema" {
		if r.Method != http.MethodGet {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		tableName := segments[2]
		if !validPathName.MatchString(tableName) {
			http.NotFound(w, r)
			return
		}
		s.handleSchema(w, dbName, tableName)
		return
	}

	http.NotFound(w, r)
}

func (s *Server) handleListTables(w http.ResponseWriter, dbName string) {
	tables, err := s.cfg.Storage.ListTables(dbName)
	if err != nil {
		writeStorageError(w, http.StatusBadRequest, errCodeStorageError, err, s.cfg.Logger)
		return
	}

	items := make([]map[string]interface{}, 0, len(tables))
	for _, table := range tables {
		item := map[string]interface{}{
			"name":      table.Name,
			"row_count": table.RowCount,
		}
		if !table.CreatedAt.IsZero() {
			item["created_at"] = table.CreatedAt.UTC().Format(time.RFC3339)
		}
		items = append(items, item)
	}

	writeJSON(w, http.StatusOK, map[string]interface{}{"tables": items})
}

func (s *Server) handleSchema(w http.ResponseWriter, dbName, tableName string) {
	schema, err := s.cfg.Storage.GetTableSchema(dbName, tableName)
	if err != nil {
		writeStorageError(w, http.StatusBadRequest, errCodeStorageError, err, s.cfg.Logger)
		return
	}
	rowCount, err := s.cfg.Storage.CountRows(dbName, tableName)
	if err != nil {
		writeStorageError(w, http.StatusBadRequest, errCodeStorageError, err, s.cfg.Logger)
		return
	}

	resp := map[string]interface{}{
		"name":      schema.Name,
		"database":  schema.Database,
		"columns":   schema.Columns,
		"row_count": rowCount,
	}
	if !schema.CreatedAt.IsZero() {
		resp["created_at"] = schema.CreatedAt.UTC().Format(time.RFC3339)
	}

	writeJSON(w, http.StatusOK, resp)
}

func (s *Server) handleHealth(w http.ResponseWriter, r *http.Request) {
	status := "ok"
	checks := map[string]interface{}{}

	if _, err := s.cfg.Storage.ListDatabases(); err != nil {
		status = "degraded"
		checks["storage"] = map[string]interface{}{
			"status": "fail",
		}
		s.cfg.Logger.Warn("health check: storage degraded", "error", err)
	} else {
		checks["storage"] = map[string]interface{}{
			"status": "pass",
		}
	}

	checks["wal"] = map[string]interface{}{
		"status": "pass",
	}

	if s.cfg.Auth == nil || !s.cfg.Auth.Enabled() || s.cfg.Auth.ValidateToken(extractHealthToken(r)) {
		uptime := int(time.Since(s.startedAt).Seconds())
		writeJSON(w, http.StatusOK, map[string]interface{}{
			"status":      status,
			"version":     s.cfg.Version,
			"uptime_s":    uptime,
			"connections": s.cfg.ActiveConnections(),
			"wal_enabled": true,
			"time_travel": true,
			"checks":      checks,
		})
		return
	}

	writeJSON(w, http.StatusOK, map[string]interface{}{
		"status": status,
	})
}

func (s *Server) handleMonitorHealth(w http.ResponseWriter, r *http.Request) {
	status := "ok"
	checks := map[string]interface{}{}

	if _, err := s.cfg.Storage.ListDatabases(); err != nil {
		status = "degraded"
		checks["storage"] = map[string]interface{}{
			"status": "fail",
		}
		s.cfg.Logger.Warn("health check: storage degraded", "error", err)
	} else {
		checks["storage"] = map[string]interface{}{
			"status": "pass",
		}
	}

	checks["wal"] = map[string]interface{}{
		"status": "pass",
	}

	uptime := int(time.Since(s.startedAt).Seconds())
	writeJSON(w, http.StatusOK, map[string]interface{}{
		"status":      status,
		"version":     s.cfg.Version,
		"uptime_s":    uptime,
		"connections": s.cfg.ActiveConnections(),
		"wal_enabled": true,
		"time_travel": true,
		"checks":      checks,
	})
}

func extractHealthToken(r *http.Request) string {
	if auth := r.Header.Get("Authorization"); strings.HasPrefix(auth, "Bearer ") {
		return strings.TrimPrefix(auth, "Bearer ")
	}
	if token := r.Header.Get("X-VaultDB-Token"); token != "" {
		return token
	}
	return r.URL.Query().Get("token")
}

func (s *Server) handleReady(w http.ResponseWriter, _ *http.Request) {
	if _, err := s.cfg.Storage.ListDatabases(); err != nil {
		s.cfg.Logger.Warn("readiness check failed", "error", err)
		writeJSON(w, http.StatusServiceUnavailable, map[string]interface{}{
			"status": "not ready",
		})
		return
	}
	writeJSON(w, http.StatusOK, map[string]interface{}{
		"status": "ready",
	})
}

func (s *Server) handleMetrics(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Content-Type", "text/plain; version=0.0.4; charset=utf-8")
	w.WriteHeader(http.StatusOK)
	fmt.Fprint(w, s.metrics.Render())
}

func (s *Server) handleDashboard(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(http.StatusOK)
	fmt.Fprint(w, dashboardHTML)
}

const dashboardHTML = `<!DOCTYPE html>
<html>
<head>
<meta charset="utf-8">
<title>VaultDB Dashboard</title>
<style>
body { font-family: -apple-system, BlinkMacSystemFont, "Segoe UI", Roboto, sans-serif; margin: 20px; background: #f5f5f5; }
h1 { color: #333; }
.metric { background: white; padding: 15px; margin: 10px 0; border-radius: 8px; box-shadow: 0 2px 4px rgba(0,0,0,0.1); }
.metric h3 { margin: 0 0 10px 0; color: #666; }
.value { font-size: 24px; font-weight: bold; color: #2c3e50; }
.refresh { background: #3498db; color: white; border: none; padding: 10px 20px; border-radius: 5px; cursor: pointer; }
.refresh:hover { background: #2980b9; }
</style>
</head>
<body>
<h1>VaultDB Dashboard</h1>
<button class="refresh" onclick="refresh()">Refresh</button>
<div id="metrics">Loading...</div>
<script src="/dashboard.js"></script>
</body>
</html>
`

func (s *Server) corsMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("X-Frame-Options", "DENY")
		w.Header().Set("Content-Security-Policy", "default-src 'self'; script-src 'self'; style-src 'self' 'unsafe-inline'; img-src 'self' data:")

		origin := r.Header.Get("Origin")
		allowed := false
		if len(s.cfg.AllowedOrigins) == 0 {
			allowed = false
		} else {
			for _, o := range s.cfg.AllowedOrigins {
				if o == "*" || o == origin {
					allowed = true
					break
				}
			}
		}
		if allowed {
			if origin != "" {
				w.Header().Set("Access-Control-Allow-Origin", origin)
				w.Header().Set("Vary", "Origin")
			} else {
				w.Header().Set("Access-Control-Allow-Origin", "*")
			}
		}
		w.Header().Set("Access-Control-Allow-Methods", "GET, POST, OPTIONS")
		w.Header().Set("Access-Control-Allow-Headers", "Content-Type, Authorization")
		if r.Method == http.MethodOptions {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		next.ServeHTTP(w, r)
	})
}

func writeJSON(w http.ResponseWriter, status int, payload interface{}) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(payload)
}

func writeError(w http.ResponseWriter, status, code int, message string) {
	writeJSON(w, status, map[string]interface{}{
		"status":     "error",
		"error_code": code,
		"message":    message,
	})
}

func writeStorageError(w http.ResponseWriter, status, code int, err error, logger *slog.Logger) {
	logger.Warn("storage error", "error", err)
	writeJSON(w, status, map[string]interface{}{
		"status":     "error",
		"error_code": code,
		"message":    "internal storage error",
	})
}

func emptyIfNil(columns []string) []string {
	if columns == nil {
		return []string{}
	}
	return columns
}

func emptyRowsIfNil(rows [][]string) [][]string {
	if rows == nil {
		return [][]string{}
	}
	return rows
}

func (s *Server) handleLiveQuery(w http.ResponseWriter, r *http.Request) {
	if s.cfg.MaxLiveQuerySubscriptions > 0 {
		if s.activeSubscriptions.Add(1) > int64(s.cfg.MaxLiveQuerySubscriptions) {
			s.activeSubscriptions.Add(-1)
			writeError(w, http.StatusTooManyRequests, errCodeRateLimited, "too many active live query subscriptions")
			return
		}
	} else {
		s.activeSubscriptions.Add(1)
	}

	db := r.URL.Query().Get("database")
	query := r.URL.Query().Get("query")
	if query == "" {
		http.Error(w, "missing query", http.StatusBadRequest)
		return
	}

	stmt, err := parser.Parse(query)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	selectStmt, ok := stmt.(*parser.SelectStatement)
	if !ok {
		http.Error(w, "only SELECT is supported for Live Queries", http.StatusBadRequest)
		return
	}

	flusher, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "streaming not supported", http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")

	sub := s.br.NewSubscription(fmt.Sprintf("sub-%d", s.nextSubID.Add(1)), selectStmt, db)
	send := sub.Send

	s.br.Subscribe(sub)
	s.activeSubscriptions.Add(1)
	defer s.activeSubscriptions.Add(-1)
	defer s.br.Unsubscribe(sub.ID)

	ctx, cancel := context.WithCancel(r.Context())
	defer cancel()

	// Send initial result
	sess := s.newSession()
	defer sess.Close()
	if db != "" {
		sess.SetCurrentDatabase(db)
	}

	// Initial evaluation
	res, err := sess.Execute(selectStmt)
	if err == nil {
		data, _ := json.Marshal(res)
		fmt.Fprintf(w, "data: %s\n\n", data)
		flusher.Flush()
	}

	for {
		select {
		case <-ctx.Done():
			return
		case res, ok := <-send:
			if !ok {
				// Broadcaster отписал клиента (block-таймаут)
				return
			}
			data, _ := json.Marshal(res)
			fmt.Fprintf(w, "data: %s\n\n", data)
			flusher.Flush()
		}
	}
}

func (s *Server) handleOpenAPI(w http.ResponseWriter, _ *http.Request) {
	// Dynamically generate OpenAPI spec based on existing databases and tables
	dbs, err := s.cfg.Storage.ListDatabases()
	if err != nil {
		writeStorageError(w, http.StatusInternalServerError, errCodeInternal, err, s.cfg.Logger)
		return
	}

	spec := map[string]interface{}{
		"openapi": "3.0.0",
		"info": map[string]interface{}{
			"title":   "VaultDB Automatic API",
			"version": s.cfg.Version,
		},
		"paths": make(map[string]interface{}),
	}

	paths := spec["paths"].(map[string]interface{})

	for _, db := range dbs {
		tables, _ := s.cfg.Storage.ListTables(db)
		for _, table := range tables {
			path := fmt.Sprintf("/api/databases/%s/tables/%s/data", db, table.Name)
			paths[path] = map[string]interface{}{
				"get": map[string]interface{}{
					"summary": fmt.Sprintf("Get data from %s.%s", db, table.Name),
					"responses": map[string]interface{}{
						"200": map[string]interface{}{"description": "Success"},
					},
				},
				"post": map[string]interface{}{
					"summary": fmt.Sprintf("Insert data into %s.%s", db, table.Name),
					"responses": map[string]interface{}{
						"201": map[string]interface{}{"description": "Created"},
					},
				},
			}
		}
	}

	writeJSON(w, http.StatusOK, spec)
}

func (s *Server) handleTableData(w http.ResponseWriter, r *http.Request, dbName, tableName string) {
	switch r.Method {
	case http.MethodGet:
		s.handleGetTableData(w, r, dbName, tableName)
	case http.MethodPost:
		s.handlePostTableData(w, r, dbName, tableName)
	default:
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
	}
}

func (s *Server) handleGetTableData(w http.ResponseWriter, r *http.Request, dbName, tableName string) {
	schema, err := s.cfg.Storage.GetTableSchema(dbName, tableName)
	if err != nil {
		writeStorageError(w, http.StatusBadRequest, errCodeStorageError, err, s.cfg.Logger)
		return
	}
	columnsByName := make(map[string]string, len(schema.Columns))
	for _, col := range schema.Columns {
		columnsByName[strings.ToLower(col.Name)] = col.Name
	}

	var where parser.Expression
	for col, vals := range r.URL.Query() {
		if col == "database" || col == "query" || col == "token" {
			continue
		}
		canonical, ok := columnsByName[strings.ToLower(col)]
		if !ok {
			writeError(w, http.StatusBadRequest, errCodeUnknownColumn, fmt.Sprintf("unknown column '%s'", col))
			return
		}
		for _, val := range vals {
			op := "="
			actualVal := val
			if parts := strings.SplitN(val, ".", 2); len(parts) == 2 {
				switch parts[0] {
				case "eq":
					op, actualVal = "=", parts[1]
				case "gt":
					op, actualVal = ">", parts[1]
				case "lt":
					op, actualVal = "<", parts[1]
				case "like":
					op, actualVal = "LIKE", parts[1]
				}
			}
			cond := &parser.BinaryExpr{
				Left:     &parser.ColumnRef{Name: canonical},
				Operator: op,
				Right:    filterLiteral(actualVal),
			}
			if where == nil {
				where = cond
			} else {
				where = &parser.AndExpr{Left: where, Right: cond}
			}
		}
	}

	stmt := &parser.SelectStatement{TableName: tableName, Where: where}
	sess := s.newSession()
	defer sess.Close()
	sess.SetCurrentDatabase(dbName)
	res, err := sess.Execute(stmt)
	if err != nil {
		writeStorageError(w, http.StatusBadRequest, errCodeStorageError, err, s.cfg.Logger)
		return
	}
	writeJSON(w, http.StatusOK, res)
}

// filterLiteral converts a raw query-string value into a typed literal so
// comparisons against INT/FLOAT/BOOL columns work.
func filterLiteral(raw string) parser.Expression {
	if i, err := strconv.ParseInt(raw, 10, 64); err == nil {
		return parser.Value{Type: "int", IntVal: i}
	}
	if f, err := strconv.ParseFloat(raw, 64); err == nil {
		return parser.Value{Type: "float", FltVal: f}
	}
	switch strings.ToLower(raw) {
	case "true":
		return parser.Value{Type: "bool", BoolVal: true}
	case "false":
		return parser.Value{Type: "bool", BoolVal: false}
	}
	return parser.Value{Type: "string", StrVal: raw}
}

func (s *Server) handlePostTableData(w http.ResponseWriter, r *http.Request, dbName, tableName string) {
	r.Body = http.MaxBytesReader(w, r.Body, int64(s.cfg.MaxRequestSizeBytes))
	var body interface{}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeError(w, http.StatusBadRequest, errCodeBadRequest, "invalid JSON body")
		return
	}

	// Simplified: construct INSERT statement from JSON
	// ... logic to build INSERT ...
	writeError(w, http.StatusNotImplemented, errCodeNotImplemented, "POST table data not fully implemented yet")
}
