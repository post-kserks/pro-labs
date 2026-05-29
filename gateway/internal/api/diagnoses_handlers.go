package api

import (
	"fmt"
	"net/http"
	"sort"
	"time"
)

// CreateDiagnosis adds a diagnosis to an existing visit.
func (h *Handler) CreateDiagnosis(w http.ResponseWriter, r *http.Request, _ Params) {
	var req struct {
		VisitID     int    `json:"visit_id"`
		PatientID   int    `json:"patient_id"`
		DoctorID    int    `json:"doctor_id"`
		ICDCode     string `json:"icd_code"`
		Description string `json:"description"`
		Severity    string `json:"severity"`
	}
	if err := decodeJSON(r, &req); err != nil {
		WriteError(w, http.StatusBadRequest, "INVALID_REQUEST", "invalid body")
		return
	}
	id, err := h.nextID("diagnoses")
	if err != nil {
		WriteError(w, http.StatusInternalServerError, "VAULTDB_ERROR", err.Error())
		return
	}
	now := time.Now().UTC().Format(time.RFC3339)
	sql := fmt.Sprintf(
		"INSERT INTO diagnoses VALUES (%d, %d, %d, %d, %s, %s, %s, %s, true);",
		id, req.VisitID, req.PatientID, req.DoctorID,
		sqlStr(req.ICDCode), sqlStr(req.Description), sqlStr(severityOrDefault(req.Severity)), sqlStr(now))
	if err := h.DB.Exec(sql); err != nil {
		WriteError(w, http.StatusInternalServerError, "VAULTDB_ERROR", err.Error())
		return
	}
	WriteJSON(w, http.StatusCreated, map[string]interface{}{"id": id})
}

// UpdateDiagnosis changes a diagnosis. VaultDB's MVCC keeps the prior version,
// which is exactly what the diagnosis-history view reads back.
func (h *Handler) UpdateDiagnosis(w http.ResponseWriter, r *http.Request, ps Params) {
	id := atoi(ps["id"])
	var req struct {
		ICDCode     *string `json:"icd_code"`
		Description *string `json:"description"`
		Severity    *string `json:"severity"`
	}
	if err := decodeJSON(r, &req); err != nil {
		WriteError(w, http.StatusBadRequest, "INVALID_REQUEST", "invalid body")
		return
	}
	var sets []string
	if req.ICDCode != nil {
		sets = append(sets, "icd_code = "+sqlStr(*req.ICDCode))
	}
	if req.Description != nil {
		sets = append(sets, "description = "+sqlStr(*req.Description))
	}
	if req.Severity != nil {
		sets = append(sets, "severity = "+sqlStr(*req.Severity))
	}
	if len(sets) == 0 {
		WriteError(w, http.StatusBadRequest, "INVALID_REQUEST", "no fields to update")
		return
	}
	sql := fmt.Sprintf("UPDATE diagnoses SET %s WHERE id = %d;", joinComma(sets), id)
	if err := h.DB.Exec(sql); err != nil {
		WriteError(w, http.StatusInternalServerError, "VAULTDB_ERROR", err.Error())
		return
	}
	WriteJSON(w, http.StatusOK, map[string]interface{}{"id": id, "status": "updated"})
}

// DeactivateDiagnosis "removes" a diagnosis by setting is_active = false.
func (h *Handler) DeactivateDiagnosis(w http.ResponseWriter, r *http.Request, ps Params) {
	id := atoi(ps["id"])
	if err := h.DB.Exec(fmt.Sprintf("UPDATE diagnoses SET is_active = false WHERE id = %d;", id)); err != nil {
		WriteError(w, http.StatusInternalServerError, "VAULTDB_ERROR", err.Error())
		return
	}
	WriteJSON(w, http.StatusOK, map[string]interface{}{"id": id, "status": "deactivated"})
}

// diagnosisVersion is one historical version of a diagnosis row.
type diagnosisVersion struct {
	CreatedTx   string `json:"created_tx"`
	DeletedTx   string `json:"deleted_tx"`
	IsCurrent   bool   `json:"is_current"`
	ICDCode     string `json:"icd_code"`
	Description string `json:"description"`
	Severity    string `json:"severity"`
	DoctorID    int    `json:"doctor_id"`
	DiagnosedAt string `json:"diagnosed_at"`
}

// DiagnosisHistory returns every MVCC version of a diagnosis via
// `HISTORY diagnoses KEY <id>`, newest first.
func (h *Handler) DiagnosisHistory(w http.ResponseWriter, r *http.Request, ps Params) {
	id := atoi(ps["id"])
	rows, err := h.queryMaps(fmt.Sprintf("HISTORY diagnoses KEY %d;", id))
	if err != nil {
		WriteError(w, http.StatusInternalServerError, "VAULTDB_ERROR", err.Error())
		return
	}
	versions := make([]diagnosisVersion, 0, len(rows))
	for _, m := range rows {
		versions = append(versions, diagnosisVersion{
			CreatedTx:   m["created_tx"],
			DeletedTx:   m["deleted_tx"],
			IsCurrent:   m["deleted_tx"] == "CURRENT",
			ICDCode:     m["icd_code"],
			Description: m["description"],
			Severity:    m["severity"],
			DoctorID:    atoi(m["doctor_id"]),
			DiagnosedAt: m["diagnosed_at"],
		})
	}
	// Newest version first (highest created_tx).
	sort.Slice(versions, func(i, j int) bool {
		return atoi(versions[i].CreatedTx) > atoi(versions[j].CreatedTx)
	})
	WriteJSON(w, http.StatusOK, map[string]interface{}{
		"diagnosis_id": id,
		"versions":     versions,
		"sql":          fmt.Sprintf("HISTORY diagnoses KEY %d", id),
	})
}

func joinComma(parts []string) string {
	out := ""
	for i, p := range parts {
		if i > 0 {
			out += ", "
		}
		out += p
	}
	return out
}
