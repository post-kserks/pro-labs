package main

import (
	"bufio"
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"time"

	"vaultdb/internal/auth"
	"vaultdb/internal/executor"
	"vaultdb/internal/httpserver"
	"vaultdb/internal/metrics"
	"vaultdb/internal/parser"
	"vaultdb/internal/storage"
	"vaultdb/internal/txmanager"
)

var (
	version   = "1.0.0"
	buildDate = "unknown"
)

type Request struct {
	ID    string `json:"id"`
	Token string `json:"token,omitempty"`
	Query string `json:"query"`
}

type Response struct {
	ID       string     `json:"id"`
	Status   string     `json:"status"`
	Type     string     `json:"type"`
	Columns  []string   `json:"columns"`
	Rows     [][]string `json:"rows"`
	Affected int        `json:"affected"`
	Message  string     `json:"message,omitempty"`
	AsOfNote string     `json:"as_of_note,omitempty"`
}

func main() {
	host := flag.String("host", "127.0.0.1", "Host to listen on")
	port := flag.Int("port", 5432, "TCP port for SQL clients")
	httpPort := flag.Int("http-port", 8080, "HTTP port for REST API and Web UI")
	monitorPort := flag.Int("monitor-port", 5433, "HTTP port for health and metrics")
	dataDir := flag.String("data", "./data", "Path to data directory")
	configPath := flag.String("config", "", "Optional config file path")
	healthCheck := flag.Bool("health-check", false, "Run one health check against monitor port and exit")
	flag.Parse()

	if *healthCheck {
		os.Exit(runHealthCheck(*monitorPort))
	}

	logger := slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelDebug}))
	slog.SetDefault(logger)
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

	store := storage.NewFileStorageEngine(*dataDir, metricsCollector)
	defer func() {
		if err := store.Close(); err != nil {
			logger.Warn("storage close failed", "error", err)
		}
	}()

	txm := txmanager.NewManager()

	var activeConnections atomic.Int64

	// Start storage metrics background updater
	go func() {
		ticker := time.NewTicker(30 * time.Second)
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

	authEnabled := envBool("VAULTDB_AUTH_ENABLED", true)
	authManager := auth.New(authEnabled, tokensFromEnv())

	httpSrv := httpserver.New(httpserver.Config{
		Host:        *host,
		Port:        *httpPort,
		MonitorPort: *monitorPort,
		Version:     version,
		Storage:     store,
		Auth:        authManager,
		Logger:      logger,
		Metrics:     metricsCollector,
		ActiveConnections: func() int64 {
			return activeConnections.Load()
		},
	})

	httpErrCh := make(chan error, 1)
	go func() {
		if err := httpSrv.Start(ctx); err != nil {
			httpErrCh <- err
		}
	}()

	addr := fmt.Sprintf("%s:%d", *host, *port)
	listener, err := net.Listen("tcp", addr)
	if err != nil {
		logger.Error("tcp listen failed", "error", err)
		os.Exit(1)
	}
	logger.Info("tcp server started", "addr", addr)

	go func() {
		<-ctx.Done()
		_ = listener.Close()
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

			activeConnections.Add(1)
			metricsCollector.IncConnections()
			wg.Add(1)
			go func(c net.Conn) {
				defer wg.Done()
				defer activeConnections.Add(-1)
				defer metricsCollector.DecConnections()
				handleConnection(c, store, metricsCollector, txm, logger)
			}(conn)
		}
	}()

	select {
	case <-ctx.Done():
	case err := <-httpErrCh:
		logger.Error("http server failed", "error", err)
		stop()
	}

	<-acceptDone
	wg.Wait()

	logger.Info("shutdown signal received, writing WAL checkpoint")
	if err := store.FinalCheckpoint(); err != nil {
		logger.Error("final checkpoint failed", "error", err)
	}
	logger.Info("shutdown complete")
}

func runHealthCheck(monitorPort int) int {
	url := fmt.Sprintf("http://127.0.0.1:%d/health", monitorPort)
	client := &http.Client{Timeout: 3 * time.Second}

	resp, err := client.Get(url)
	if err != nil {
		fmt.Fprintf(os.Stderr, "health check failed: %v\n", err)
		return 1
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		fmt.Fprintf(os.Stderr, "health check failed: status=%d body=%s\n", resp.StatusCode, string(body))
		return 1
	}
	return 0
}

func handleConnection(conn net.Conn, store storage.StorageEngine, m *metrics.Collector, txm *txmanager.Manager, logger *slog.Logger) {
	defer conn.Close()

	session := executor.NewSession(store, m, txm)
	defer func() {
		if session.IsInTx() {
			logger.Warn("connection closed with active transaction, rolling back",
				"tx_id", session.ActiveTx.ID)
			session.ActiveTx.Rollback()
		}
	}()

	scanner := bufio.NewScanner(conn)
	scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)

	for scanner.Scan() {
		line := scanner.Bytes()

		var req Request
		if err := json.Unmarshal(line, &req); err != nil {
			sendError(conn, "", "invalid JSON request")
			continue
		}

		stmt, err := parser.Parse(req.Query)
		if err != nil {
			sendError(conn, req.ID, err.Error())
			continue
		}

		result, err := session.Execute(stmt)
		if err != nil {
			sendError(conn, req.ID, err.Error())
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

func sendError(conn net.Conn, id, message string) {
	resp := Response{
		ID:      id,
		Status:  "error",
		Type:    "error",
		Columns: []string{},
		Rows:    [][]string{},
		Message: message,
	}
	_ = writeResponse(conn, resp)
}

func sendResult(conn net.Conn, id string, result *executor.Result) error {
	columns := result.Columns
	if columns == nil {
		columns = []string{}
	}

	rows := result.Rows
	if rows == nil {
		rows = [][]string{}
	}

	resp := Response{
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

func writeResponse(conn net.Conn, response Response) error {
	bytes, err := json.Marshal(response)
	if err != nil {
		return err
	}
	bytes = append(bytes, '\n')
	_, err = conn.Write(bytes)
	return err
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
		return
	}
	for _, db := range dbs {
		tables, err := s.ListTables(db)
		if err != nil {
			continue
		}
		for _, t := range tables {
			m.UpdateStorageRows(db, t.Name, int64(t.RowCount))
		}
	}
}
