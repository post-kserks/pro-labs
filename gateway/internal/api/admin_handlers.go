package api

import (
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

var domainTables = []string{
	"patients", "doctors", "visits", "diagnoses",
	"prescriptions", "lab_results", "allergies",
}

// AdminStats returns headline counts for the dashboard / admin panel.
func (h *Handler) AdminStats(w http.ResponseWriter, r *http.Request, _ Params) {
	count := func(table string) int {
		res, err := h.DB.Query("SELECT COUNT(*) FROM " + table + ";")
		if err != nil || len(res.Rows) == 0 || len(res.Rows[0]) == 0 {
			return 0
		}
		return atoi(res.Rows[0][0])
	}

	today := time.Now().UTC().Format("2006-01-02")
	visits, _ := h.queryMaps("SELECT * FROM visits;")
	visitsToday, completedToday := 0, 0
	for _, m := range visits {
		if strings.HasPrefix(m["visit_date"], today) {
			visitsToday++
			if m["status"] == "completed" {
				completedToday++
			}
		}
	}

	diag, _ := h.queryMaps("SELECT * FROM diagnoses;")
	activeDiag := 0
	for _, m := range diag {
		if parseBool(m["is_active"]) {
			activeDiag++
		}
	}

	WriteJSON(w, http.StatusOK, map[string]interface{}{
		"patients_total":   count("patients"),
		"doctors_total":    count("doctors"),
		"visits_total":     count("visits"),
		"diagnoses_total":  count("diagnoses"),
		"active_diagnoses": activeDiag,
		"visits_today":     visitsToday,
		"completed_today":  completedToday,
	})
}

// Explain runs EXPLAIN [ANALYZE] for a SELECT statement and returns the plan.
func (h *Handler) Explain(w http.ResponseWriter, r *http.Request, _ Params) {
	var req struct {
		SQL     string `json:"sql"`
		Analyze bool   `json:"analyze"`
	}
	if err := decodeJSON(r, &req); err != nil {
		WriteError(w, http.StatusBadRequest, "INVALID_REQUEST", "invalid body")
		return
	}
	sql := strings.TrimSpace(req.SQL)
	if sql == "" {
		WriteError(w, http.StatusBadRequest, "INVALID_REQUEST", "sql is required")
		return
	}
	prefix := "EXPLAIN "
	if req.Analyze {
		prefix = "EXPLAIN ANALYZE "
	}
	res, err := h.DB.Query(prefix + sql)
	if err != nil {
		WriteError(w, http.StatusBadRequest, "INVALID_SQL", err.Error())
		return
	}
	WriteJSON(w, http.StatusOK, map[string]interface{}{
		"plan": res.PlanText(),
		"sql":  sql,
	})
}

// vacuumTableStat is one row of pre-vacuum statistics.
type vacuumTableStat struct {
	Table    string `json:"table"`
	LiveRows int    `json:"live_rows"`
}

// VacuumStats lists per-table live row counts (pre-vacuum view).
func (h *Handler) VacuumStats(w http.ResponseWriter, r *http.Request, _ Params) {
	stats := make([]vacuumTableStat, 0, len(domainTables))
	for _, t := range domainTables {
		res, err := h.DB.Query("SELECT COUNT(*) FROM " + t + ";")
		if err != nil {
			continue
		}
		n := 0
		if len(res.Rows) > 0 && len(res.Rows[0]) > 0 {
			n = atoi(res.Rows[0][0])
		}
		stats = append(stats, vacuumTableStat{Table: t, LiveRows: n})
	}
	WriteJSON(w, http.StatusOK, map[string]interface{}{
		"tables": stats,
		"note":   "Освобождённые версии и размер показываются после запуска VACUUM.",
	})
}

// RunVacuum runs VACUUM across all tables and returns reclaim statistics.
func (h *Handler) RunVacuum(w http.ResponseWriter, r *http.Request, _ Params) {
	res, err := h.DB.Query("VACUUM;")
	if err != nil {
		WriteError(w, http.StatusInternalServerError, "VAULTDB_ERROR", err.Error())
		return
	}
	type row struct {
		Table        string `json:"table"`
		RowsBefore   int    `json:"rows_before"`
		RowsAfter    int    `json:"rows_after"`
		Reclaimed    int    `json:"reclaimed"`
		SizeBeforeKB int    `json:"size_before_kb"`
		SizeAfterKB  int    `json:"size_after_kb"`
		DurationMs   string `json:"duration_ms"`
	}
	var out []row
	totalReclaimed, totalSavedKB := 0, 0
	for _, m := range res.Maps() {
		r := row{
			Table:        m["table"],
			RowsBefore:   atoi(m["rows_before"]),
			RowsAfter:    atoi(m["rows_after"]),
			Reclaimed:    atoi(m["reclaimed"]),
			SizeBeforeKB: atoi(m["size_before_kb"]),
			SizeAfterKB:  atoi(m["size_after_kb"]),
			DurationMs:   m["duration_ms"],
		}
		totalReclaimed += r.Reclaimed
		totalSavedKB += r.SizeBeforeKB - r.SizeAfterKB
		out = append(out, r)
	}
	WriteJSON(w, http.StatusOK, map[string]interface{}{
		"tables":          out,
		"total_reclaimed": totalReclaimed,
		"total_saved_kb":  totalSavedKB,
	})
}

// ListIndexes aggregates SHOW INDEXES ON <table> across the domain tables.
func (h *Handler) ListIndexes(w http.ResponseWriter, r *http.Request, _ Params) {
	type idx struct {
		Table string `json:"table"`
		Index string `json:"index"`
	}
	var out []idx
	for _, t := range domainTables {
		res, err := h.DB.Query("SHOW INDEXES ON " + t + ";")
		if err != nil {
			continue
		}
		for _, row := range res.Rows {
			if len(row) > 0 && row[0] != "" {
				out = append(out, idx{Table: t, Index: row[0]})
			}
		}
	}
	WriteJSON(w, http.StatusOK, map[string]interface{}{"indexes": out})
}

// WALStatus reports VaultDB WAL / engine health from the monitor endpoint.
func (h *Handler) WALStatus(w http.ResponseWriter, r *http.Request, _ Params) {
	body, _ := h.fetchMonitor("/health")
	WriteJSON(w, http.StatusOK, map[string]interface{}{
		"active":  true,
		"health":  jsonOrRaw(body),
		"summary": "WAL активен: каждая операция сначала пишется в журнал, поэтому данные не теряются при сбое. При старте сервера незафиксированные операции воспроизводятся автоматически (WAL recovery).",
	})
}

// AdminMetrics proxies the VaultDB Prometheus metrics text.
func (h *Handler) AdminMetrics(w http.ResponseWriter, r *http.Request, _ Params) {
	body, err := h.fetchMonitor("/metrics")
	if err != nil {
		WriteError(w, http.StatusServiceUnavailable, "VAULTDB_UNAVAILABLE", err.Error())
		return
	}
	WriteJSON(w, http.StatusOK, map[string]interface{}{"metrics": string(body)})
}

func (h *Handler) fetchMonitor(path string) ([]byte, error) {
	if h.MonitorURL == "" {
		return nil, fmt.Errorf("monitor url not configured")
	}
	client := &http.Client{Timeout: 4 * time.Second}
	resp, err := client.Get(strings.TrimRight(h.MonitorURL, "/") + path)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	return io.ReadAll(resp.Body)
}

func jsonOrRaw(b []byte) string { return string(b) }
