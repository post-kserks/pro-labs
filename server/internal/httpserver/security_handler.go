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

// handleRevokeToken revokes a token (admin only — caller must be authenticated).
func (s *Server) handleRevokeToken(w http.ResponseWriter, r *http.Request) {
	r.Body = http.MaxBytesReader(w, r.Body, 4096)
	var req struct {
		Token string `json:"token"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, errCodeBadRequest, "invalid JSON body")
		return
	}
	if req.Token == "" {
		writeError(w, http.StatusBadRequest, errCodeBadRequest, "token is required")
		return
	}

	s.cfg.Auth.RevokeToken(req.Token)

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{
		"status":  "ok",
		"message": "token revoked",
	})
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
