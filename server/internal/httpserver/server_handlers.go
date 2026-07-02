package httpserver

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"time"

	"vaultdb/internal/executor"
	"vaultdb/internal/parser"
	"vaultdb/internal/storage"
)

var validPathName = regexp.MustCompile(`^[a-zA-Z0-9_-]+$`)

type TransactionRequest struct {
	Action   string `json:"action"`
	Database string `json:"database"`
}

type BatchQueryItem struct {
	Query  string   `json:"query"`
	Params []string `json:"params"`
}

type BatchRequest struct {
	Queries  []BatchQueryItem `json:"queries"`
	Database string           `json:"database"`
}

type BatchResponseResult struct {
	Status     string           `json:"status"`
	Type       string           `json:"type"`
	Columns    []string         `json:"columns"`
	Rows       [][]interface{} `json:"rows"`
	Affected   int              `json:"affected"`
	DurationMs float64          `json:"duration_ms"`
	Message    string           `json:"message,omitempty"`
	Error      string           `json:"error,omitempty"`
}

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
	if s.cfg.MaxPreparedStmts > 0 {
		sess.SetMaxPreparedStatements(s.cfg.MaxPreparedStmts)
	}
	if s.cfg.ResultCacheSize > 0 {
		sess.SetResultCacheConfig(s.cfg.ResultCacheSize, s.cfg.ResultCacheTTLSec)
	}
	return sess
}

func (s *Server) handleQuery(w http.ResponseWriter, r *http.Request) {
	r.Body = http.MaxBytesReader(w, r.Body, int64(s.cfg.MaxRequestSizeBytes))
	var req struct {
		Database string   `json:"database"`
		Query    string   `json:"query"`
		Params   []string `json:"params"`
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

	if len(req.Params) > 0 {
		query = applyParams(query, req.Params)
	}

	stmt, err := parser.Parse(query)
	if err != nil {
		writeError(w, http.StatusBadRequest, errCodeParseError, err.Error())
		return
	}

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
		"rows":        convertRows(result.Rows, result.Schema),
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

func (s *Server) handleTransaction(w http.ResponseWriter, r *http.Request) {
	r.Body = http.MaxBytesReader(w, r.Body, int64(s.cfg.MaxRequestSizeBytes))
	var req TransactionRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		if err.Error() == "http: request body too large" {
			writeError(w, http.StatusRequestEntityTooLarge, errCodeBadRequest, "request body too large")
			return
		}
		writeError(w, http.StatusBadRequest, errCodeBadRequest, "invalid JSON body")
		return
	}

	var sql string
	switch req.Action {
	case "begin":
		sql = "BEGIN;"
	case "commit":
		sql = "COMMIT;"
	case "rollback":
		sql = "ROLLBACK;"
	default:
		writeError(w, http.StatusBadRequest, errCodeBadRequest, "action must be one of: begin, commit, rollback")
		return
	}

	stmt, err := parser.Parse(sql)
	if err != nil {
		writeError(w, http.StatusBadRequest, errCodeParseError, err.Error())
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
		"rows":        convertRows(result.Rows, result.Schema),
		"affected":    result.Affected,
		"duration_ms": duration,
	}
	if result.Message != "" {
		response["message"] = result.Message
	}

	writeJSON(w, http.StatusOK, response)
}

func (s *Server) handleBatch(w http.ResponseWriter, r *http.Request) {
	r.Body = http.MaxBytesReader(w, r.Body, int64(s.cfg.MaxRequestSizeBytes))
	var req BatchRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		if err.Error() == "http: request body too large" {
			writeError(w, http.StatusRequestEntityTooLarge, errCodeBadRequest, "request body too large")
			return
		}
		writeError(w, http.StatusBadRequest, errCodeBadRequest, "invalid JSON body")
		return
	}

	if len(req.Queries) == 0 {
		writeError(w, http.StatusBadRequest, errCodeBadRequest, "queries array cannot be empty")
		return
	}

	results := make([]BatchResponseResult, 0, len(req.Queries))

	for _, q := range req.Queries {
		query := strings.TrimSpace(q.Query)
		if query == "" {
			results = append(results, BatchResponseResult{
				Status: "error",
				Error:  "query cannot be empty",
			})
			continue
		}

		if len(q.Params) > 0 {
			query = applyParams(query, q.Params)
		}

		stmt, err := parser.Parse(query)
		if err != nil {
			results = append(results, BatchResponseResult{
				Status: "error",
				Error:  err.Error(),
			})
			continue
		}

		switch stmt.(type) {
		case *parser.BeginStatement, *parser.CommitStatement, *parser.RollbackStatement:
			results = append(results, BatchResponseResult{
				Status: "error",
				Error:  "transactions are not supported over the stateless HTTP API; use the TCP client on port 5432",
			})
			continue
		}

		session := s.newSession()
		if req.Database != "" {
			session.SetCurrentDatabase(req.Database)
		}

		start := time.Now()
		result, err := session.Execute(stmt)
		duration := float64(time.Since(start).Microseconds()) / 1000.0
		session.Close()

		if err != nil {
			results = append(results, BatchResponseResult{
				Status:     "error",
				Error:      err.Error(),
				DurationMs: duration,
			})
			continue
		}

		results = append(results, BatchResponseResult{
			Status:     "ok",
			Type:       result.Type,
			Columns:    emptyIfNil(result.Columns),
			Rows:       convertRows(result.Rows, result.Schema),
			Affected:   result.Affected,
			DurationMs: duration,
			Message:    result.Message,
		})
	}

	writeJSON(w, http.StatusOK, map[string]interface{}{
		"results": results,
	})
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
	defer s.activeSubscriptions.Add(-1)

	db := r.URL.Query().Get("database")
	if db != "" && !validPathName.MatchString(db) {
		http.Error(w, "invalid database name", http.StatusBadRequest)
		return
	}
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

	sub := s.br.NewSubscription(fmt.Sprintf("sub-%d", s.nextSubID.Add(1)), selectStmt, db, s.cfg.Storage.CurrentTxID())
	send := sub.Send

	s.br.Subscribe(sub)
	defer s.br.Unsubscribe(sub.ID)

	maxDuration := time.Duration(s.cfg.MaxLiveQueryDurationSec) * time.Second
	if maxDuration <= 0 {
		maxDuration = 1 * time.Hour
	}

	ctx, cancel := context.WithTimeout(r.Context(), maxDuration)
	defer cancel()

	sess := s.newSession()
	defer sess.Close()
	if db != "" {
		sess.SetCurrentDatabase(db)
	}

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
				return
			}
			data, _ := json.Marshal(res)
			fmt.Fprintf(w, "data: %s\n\n", data)
			flusher.Flush()
		}
	}
}

func (s *Server) handleOpenAPI(w http.ResponseWriter, _ *http.Request) {
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
		tables, err := s.cfg.Storage.ListTables(db)
		if err != nil {
			s.cfg.Logger.Warn("failed to list tables for OpenAPI spec", "db", db, "error", err)
			continue
		}
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

	rows, ok := body.([]interface{})
	if !ok {
		writeError(w, http.StatusBadRequest, errCodeBadRequest, "expected JSON array of row objects")
		return
	}

	schema, err := s.cfg.Storage.GetTableSchema(dbName, tableName)
	if err != nil {
		writeStorageError(w, http.StatusBadRequest, errCodeStorageError, err, s.cfg.Logger)
		return
	}

	storageRows := make([]storage.Row, 0, len(rows))
	for _, row := range rows {
		rowMap, ok := row.(map[string]interface{})
		if !ok {
			writeError(w, http.StatusBadRequest, errCodeBadRequest, "each row must be a JSON object")
			return
		}
		values := make([]storage.Value, len(schema.Columns))
		for i, col := range schema.Columns {
			if v, exists := rowMap[col.Name]; exists {
				values[i] = parseHTTPRowValue(v)
			} else {
				values[i] = nil
			}
		}
		storageRows = append(storageRows, storage.Row(values))
	}

	n, err := s.cfg.Storage.InsertRows(dbName, tableName, storageRows)
	if err != nil {
		writeStorageError(w, http.StatusInternalServerError, errCodeStorageError, err, s.cfg.Logger)
		return
	}

	writeJSON(w, http.StatusCreated, map[string]interface{}{
		"message": fmt.Sprintf("inserted %d rows", n),
	})
}

func (s *Server) handleQueryStream(w http.ResponseWriter, r *http.Request) {
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

	switch stmt.(type) {
	case *parser.BeginStatement, *parser.CommitStatement, *parser.RollbackStatement:
		writeError(w, http.StatusBadRequest, errCodeTxUnsupported,
			"transactions are not supported over the stateless HTTP API; use the TCP client on port 5432")
		return
	}

	flusher, ok := w.(http.Flusher)
	if !ok {
		writeError(w, http.StatusInternalServerError, errCodeInternal, "streaming not supported")
		return
	}

	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")

	session := s.newSession()
	defer session.Close()
	if req.Database != "" {
		session.SetCurrentDatabase(req.Database)
	}

	result, err := session.Execute(stmt)
	if err != nil {
		writeStorageError(w, http.StatusBadRequest, errCodeStorageError, err, s.cfg.Logger)
		return
	}

	type sseMsg struct {
		event string
		data  interface{}
	}

	ch := make(chan sseMsg, 16)

	go func() {
		defer close(ch)
		ctx := r.Context()
		send := func(msg sseMsg) bool {
			select {
			case ch <- msg:
				return true
			case <-ctx.Done():
				return false
			}
		}
		if len(result.Columns) > 0 {
			if !send(sseMsg{event: "columns", data: result.Columns}) {
				return
			}
		}
		for _, row := range result.Rows {
			if !send(sseMsg{event: "row", data: row}) {
				return
			}
		}
		send(sseMsg{event: "done", data: nil})
	}()

	for msg := range ch {
		var payload string
		if msg.data != nil {
			b, _ := json.Marshal(msg.data)
			payload = string(b)
		} else {
			payload = "{}"
		}
		fmt.Fprintf(w, "event: %s\ndata: %s\n\n", msg.event, payload)
		flusher.Flush()
	}
}

func parseHTTPRowValue(v interface{}) storage.Value {
	switch val := v.(type) {
	case float64:
		if val == float64(int64(val)) {
			return int64(val)
		}
		return val
	case string:
		return val
	case bool:
		return val
	case nil:
		return nil
	default:
		return fmt.Sprintf("%v", val)
	}
}

func isNumeric(s string) bool {
	_, err := strconv.ParseFloat(s, 64)
	return err == nil
}

func convertRowValue(value string, colType string) interface{} {
	if value == "" {
		return nil
	}
	switch strings.ToUpper(colType) {
	case "INT", "BIGINT", "INTEGER":
		if v, err := strconv.ParseInt(value, 10, 64); err == nil {
			return v
		}
	case "FLOAT", "NUMERIC", "DOUBLE", "REAL", "DECIMAL":
		if v, err := strconv.ParseFloat(value, 64); err == nil {
			return v
		}
	case "BOOL", "BOOLEAN":
		switch strings.ToLower(value) {
		case "true", "t", "1", "yes":
			return true
		case "false", "f", "0", "no":
			return false
		}
	case "JSONB", "JSON":
		var parsed interface{}
		if err := json.Unmarshal([]byte(value), &parsed); err == nil {
			return parsed
		}
	}
	return value
}

func convertRows(rows [][]string, schema *storage.TableSchema) [][]interface{} {
	if rows == nil {
		return [][]interface{}{}
	}
	result := make([][]interface{}, len(rows))
	for i, row := range rows {
		converted := make([]interface{}, len(row))
		for j, val := range row {
			if schema != nil && j < len(schema.Columns) {
				converted[j] = convertRowValue(val, schema.Columns[j].Type)
			} else {
				converted[j] = val
			}
		}
		result[i] = converted
	}
	return result
}

func applyParams(query string, params []string) string {
	var buf strings.Builder
	inString := false
	quoteChar := byte(0)
	escaped := false

	for i := 0; i < len(query); i++ {
		c := query[i]

		if escaped {
			buf.WriteByte(c)
			escaped = false
			continue
		}

		if c == '\\' && inString {
			buf.WriteByte(c)
			escaped = true
			continue
		}

		if inString {
			buf.WriteByte(c)
			if c == quoteChar {
				inString = false
			}
			continue
		}

		if c == '\'' || c == '"' {
			inString = true
			quoteChar = c
			buf.WriteByte(c)
			continue
		}

		if c == '$' && i+1 < len(query) {
			j := i + 1
			for j < len(query) && query[j] >= '0' && query[j] <= '9' {
				j++
			}
			if j > i+1 {
				numStr := query[i+1 : j]
				idx, err := strconv.Atoi(numStr)
				if err == nil && idx >= 1 && idx <= len(params) {
					val := params[idx-1]
					if isNumeric(val) {
						buf.WriteString(val)
					} else {
						val = strings.ReplaceAll(val, `\`, `\\`)
						val = strings.ReplaceAll(val, "'", "\\'")
						buf.WriteByte('\'')
						buf.WriteString(val)
						buf.WriteByte('\'')
					}
					i = j - 1
					continue
				}
			}
		}

		buf.WriteByte(c)
	}

	return buf.String()
}
