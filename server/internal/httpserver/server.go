package httpserver

import (
	"context"
	"embed"
	"encoding/json"
	"fmt"
	"io/fs"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"vaultdb/internal/auth"
	"vaultdb/internal/executor"
	"vaultdb/internal/metrics"
	"vaultdb/internal/parser"
	"vaultdb/internal/storage"
	"vaultdb/internal/txmanager"
)

//go:embed web/dist/*
var webUIFiles embed.FS

type Config struct {
	Host              string
	Port              int
	MonitorPort       int
	Version           string
	Storage           storage.StorageEngine
	Auth              *auth.Manager
	Logger            *slog.Logger
	Metrics           *metrics.Collector
	TxManager         *txmanager.Manager
	ActiveConnections func() int64
}

type Server struct {
	cfg       Config
	startedAt time.Time
	metrics   *metrics.Collector
	txm       *txmanager.Manager
}

func New(cfg Config) *Server {
	if cfg.Logger == nil {
		cfg.Logger = slog.Default()
	}
	if cfg.Auth == nil {
		cfg.Auth = auth.New(false, nil)
	}
	if cfg.ActiveConnections == nil {
		cfg.ActiveConnections = func() int64 { return 0 }
	}
	m := cfg.Metrics
	if m == nil {
		m = metrics.New()
	}
	txm := cfg.TxManager
	if txm == nil {
		txm = txmanager.NewManager()
	}
	return &Server{
		cfg:       cfg,
		startedAt: time.Now().UTC(),
		metrics:   m,
		txm:       txm,
	}
}

func (s *Server) Start(ctx context.Context) error {
	apiServer := &http.Server{
		Addr:    fmt.Sprintf("%s:%d", s.cfg.Host, s.cfg.Port),
		Handler: corsMiddleware(s.apiMux()),
	}
	monitorServer := &http.Server{
		Addr:    fmt.Sprintf("%s:%d", s.cfg.Host, s.cfg.MonitorPort),
		Handler: s.monitorMux(),
	}

	errCh := make(chan error, 2)

	go func() {
		err := apiServer.ListenAndServe()
		if err != nil && err != http.ErrServerClosed {
			errCh <- err
		}
	}()

	go func() {
		err := monitorServer.ListenAndServe()
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

	mux.HandleFunc("/api/query", s.withMethod(http.MethodPost, s.cfg.Auth.Middleware(s.handleQuery)))
	mux.HandleFunc("/api/databases", s.withMethod(http.MethodGet, s.cfg.Auth.Middleware(s.handleListDatabases)))
	mux.HandleFunc("/api/databases/", s.cfg.Auth.Middleware(s.handleDatabasesSubroutes))

	mux.HandleFunc("/health", s.withMethod(http.MethodGet, s.handleHealth))
	mux.HandleFunc("/metrics", s.withMethod(http.MethodGet, s.handleMetrics))

	distFS, err := fs.Sub(webUIFiles, "web/dist")
	if err == nil {
		fileServer := http.FileServer(http.FS(distFS))
		mux.Handle("/", fileServer)
	} else {
		mux.HandleFunc("/", func(w http.ResponseWriter, _ *http.Request) {
			http.Error(w, "web UI is not embedded", http.StatusNotFound)
		})
	}

	return mux
}

func (s *Server) monitorMux() *http.ServeMux {
	mux := http.NewServeMux()
	mux.HandleFunc("/health", s.withMethod(http.MethodGet, s.handleHealth))
	mux.HandleFunc("/metrics", s.withMethod(http.MethodGet, s.handleMetrics))
	return mux
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
	var req struct {
		Database string `json:"database"`
		Query    string `json:"query"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, 3001, "invalid JSON body")
		return
	}

	query := strings.TrimSpace(req.Query)
	if query == "" {
		writeError(w, http.StatusBadRequest, 3001, "query cannot be empty")
		return
	}

	stmt, err := parser.Parse(query)
	if err != nil {
		writeError(w, http.StatusBadRequest, 3002, err.Error())
		return
	}

	session := executor.NewSession(s.cfg.Storage, s.metrics, s.txm)
	if req.Database != "" {
		session.SetCurrentDatabase(req.Database)
	}

	start := time.Now()
	result, err := session.Execute(stmt)
	if err != nil {
		writeError(w, http.StatusBadRequest, 3004, err.Error())
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
		writeError(w, http.StatusInternalServerError, 5000, err.Error())
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
	if dbName == "" {
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

	if len(segments) == 4 && segments[1] == "tables" && segments[3] == "schema" {
		if r.Method != http.MethodGet {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		s.handleSchema(w, dbName, segments[2])
		return
	}

	http.NotFound(w, r)
}

func (s *Server) handleListTables(w http.ResponseWriter, dbName string) {
	tables, err := s.cfg.Storage.ListTables(dbName)
	if err != nil {
		writeError(w, http.StatusBadRequest, 3004, err.Error())
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
		writeError(w, http.StatusBadRequest, 3004, err.Error())
		return
	}
	rowCount, err := s.cfg.Storage.CountRows(dbName, tableName)
	if err != nil {
		writeError(w, http.StatusBadRequest, 3004, err.Error())
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

func (s *Server) handleHealth(w http.ResponseWriter, _ *http.Request) {
	uptime := int(time.Since(s.startedAt).Seconds())
	writeJSON(w, http.StatusOK, map[string]interface{}{
		"status":      "ok",
		"version":     s.cfg.Version,
		"uptime_s":    uptime,
		"connections": s.cfg.ActiveConnections(),
		"wal_enabled": true,
		"time_travel": true,
	})
}

func (s *Server) handleMetrics(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Content-Type", "text/plain; version=0.0.4; charset=utf-8")
	w.WriteHeader(http.StatusOK)
	fmt.Fprint(w, s.metrics.Render())
}

func corsMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Access-Control-Allow-Origin", "*")
		w.Header().Set("Access-Control-Allow-Methods", "GET, POST, OPTIONS")
		w.Header().Set("Access-Control-Allow-Headers", "Content-Type, Authorization, X-VaultDB-Token")
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
