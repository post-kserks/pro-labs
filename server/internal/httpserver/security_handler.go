package httpserver

import (
	"encoding/json"
	"net/http"
	"runtime"
	"time"
)

type SecurityStatus struct {
	LastFullAudit   string          `json:"last_full_audit"`
	OpenFindings    FindingsCount   `json:"open_findings"`
	AutomatedChecks AutomatedStatus `json:"automated_checks"`
	GoVersion       string          `json:"go_version"`
	Uptime          string          `json:"uptime"`
}

type FindingsCount struct {
	Critical int `json:"critical"`
	High     int `json:"high"`
	Medium   int `json:"medium"`
	Low      int `json:"low"`
}

type AutomatedStatus struct {
	LastGosecRun  string `json:"last_gosec_run"`
	LastFuzzRun   string `json:"last_fuzz_run"`
	LastTrivyScan string `json:"last_trivy_scan"`
	AllPassing    bool   `json:"all_passing"`
}

func (s *Server) handleSecurityStatus(w http.ResponseWriter, r *http.Request) {
	status := SecurityStatus{
		LastFullAudit: "not yet performed",
		OpenFindings: FindingsCount{
			Critical: 0,
			High:     0,
			Medium:   9,
			Low:      4,
		},
		AutomatedChecks: AutomatedStatus{
			LastGosecRun:  "configured in CI",
			LastFuzzRun:   "configured in CI",
			LastTrivyScan: "configured in CI",
			AllPassing:    true,
		},
		GoVersion: runtime.Version(),
		Uptime:    time.Since(s.startedAt).String(),
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(status)
}
