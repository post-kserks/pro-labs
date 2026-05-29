package api

import (
	"fmt"
	"net/http"
	"sort"
	"time"

	"medvault-gateway/internal/models"
)

// ListVisits returns visits, optionally filtered by ?patient_id, ?doctor_id,
// ?status, ?date (YYYY-MM-DD prefix on visit_date).
func (h *Handler) ListVisits(w http.ResponseWriter, r *http.Request, _ Params) {
	rows, err := h.queryMaps("SELECT * FROM visits;")
	if err != nil {
		WriteError(w, http.StatusInternalServerError, "VAULTDB_ERROR", err.Error())
		return
	}
	q := r.URL.Query()
	docs := h.doctorNames()
	out := make([]models.Visit, 0, len(rows))
	for _, m := range rows {
		v := toVisit(m)
		if pid := q.Get("patient_id"); pid != "" && atoi(pid) != v.PatientID {
			continue
		}
		if did := q.Get("doctor_id"); did != "" && atoi(did) != v.DoctorID {
			continue
		}
		if st := q.Get("status"); st != "" && st != v.Status {
			continue
		}
		if d := q.Get("date"); d != "" && (len(v.VisitDate) < len(d) || v.VisitDate[:len(d)] != d) {
			continue
		}
		v.DoctorName = docs[v.DoctorID]
		out = append(out, v)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].VisitDate > out[j].VisitDate })
	WriteJSON(w, http.StatusOK, map[string]interface{}{"visits": out})
}

// GetVisit returns one visit.
func (h *Handler) GetVisit(w http.ResponseWriter, r *http.Request, ps Params) {
	id := atoi(ps["id"])
	rows, err := h.queryMaps(fmt.Sprintf("SELECT * FROM visits WHERE id = %d;", id))
	if err != nil {
		WriteError(w, http.StatusInternalServerError, "VAULTDB_ERROR", err.Error())
		return
	}
	if len(rows) == 0 {
		WriteError(w, http.StatusNotFound, "VISIT_NOT_FOUND", "визит не найден")
		return
	}
	v := toVisit(rows[0])
	v.DoctorName = h.doctorNames()[v.DoctorID]
	WriteJSON(w, http.StatusOK, v)
}

// CreateVisit schedules a new visit (used by the receptionist).
func (h *Handler) CreateVisit(w http.ResponseWriter, r *http.Request, _ Params) {
	var req struct {
		PatientID      int    `json:"patient_id"`
		DoctorID       int    `json:"doctor_id"`
		VisitDate      string `json:"visit_date"`
		ChiefComplaint string `json:"chief_complaint"`
	}
	if err := decodeJSON(r, &req); err != nil {
		WriteError(w, http.StatusBadRequest, "INVALID_REQUEST", "invalid body")
		return
	}
	id, err := h.nextID("visits")
	if err != nil {
		WriteError(w, http.StatusInternalServerError, "VAULTDB_ERROR", err.Error())
		return
	}
	now := time.Now().UTC().Format(time.RFC3339)
	if req.VisitDate == "" {
		req.VisitDate = now
	}
	sql := fmt.Sprintf(
		"INSERT INTO visits VALUES (%d, %d, %d, %s, 'scheduled', %s, '', %s);",
		id, req.PatientID, req.DoctorID, sqlStr(req.VisitDate), sqlStr(req.ChiefComplaint), sqlStr(now))
	if err := h.DB.Exec(sql); err != nil {
		WriteError(w, http.StatusInternalServerError, "VAULTDB_ERROR", err.Error())
		return
	}
	WriteJSON(w, http.StatusCreated, map[string]interface{}{"id": id, "status": "scheduled"})
}

// completeVisitRequest is the payload for finishing an appointment.
type completeVisitRequest struct {
	PatientID int    `json:"patient_id"`
	DoctorID  int    `json:"doctor_id"`
	Notes     string `json:"notes"`
	Diagnoses []struct {
		ICDCode     string `json:"icd_code"`
		Description string `json:"description"`
		Severity    string `json:"severity"`
	} `json:"diagnoses"`
	Prescriptions []struct {
		DrugName     string `json:"drug_name"`
		Dosage       string `json:"dosage"`
		Frequency    string `json:"frequency"`
		Duration     string `json:"duration"`
		Instructions string `json:"instructions"`
	} `json:"prescriptions"`
}

// CompleteVisit atomically finishes an appointment: it updates the visit and
// inserts every diagnosis and prescription inside a single VaultDB transaction.
// If any step fails the whole transaction is rolled back and nothing persists.
func (h *Handler) CompleteVisit(w http.ResponseWriter, r *http.Request, ps Params) {
	visitID := atoi(ps["id"])

	var req completeVisitRequest
	if err := decodeJSON(r, &req); err != nil {
		WriteError(w, http.StatusBadRequest, "INVALID_REQUEST", "invalid body")
		return
	}

	// Allocate ids before opening the transaction (buffered inserts are not
	// visible to SELECT until COMMIT).
	diagID, err := h.nextID("diagnoses")
	if err != nil {
		WriteError(w, http.StatusInternalServerError, "VAULTDB_ERROR", err.Error())
		return
	}
	presID, err := h.nextID("prescriptions")
	if err != nil {
		WriteError(w, http.StatusInternalServerError, "VAULTDB_ERROR", err.Error())
		return
	}

	tx, err := h.DB.Begin()
	if err != nil {
		WriteError(w, http.StatusServiceUnavailable, "VAULTDB_UNAVAILABLE", err.Error())
		return
	}
	defer tx.Rollback() // no-op after a successful Commit

	now := time.Now().UTC().Format(time.RFC3339)

	// 1. Mark the visit completed.
	if err := tx.Exec(fmt.Sprintf(
		"UPDATE visits SET status = 'completed', notes = %s WHERE id = %d;",
		sqlStr(req.Notes), visitID)); err != nil {
		WriteError(w, http.StatusInternalServerError, "TX_FAILED", "rolled back: "+err.Error())
		return
	}

	// 2. Insert diagnoses.
	for _, d := range req.Diagnoses {
		if err := tx.Exec(fmt.Sprintf(
			"INSERT INTO diagnoses VALUES (%d, %d, %d, %d, %s, %s, %s, %s, true);",
			diagID, visitID, req.PatientID, req.DoctorID,
			sqlStr(d.ICDCode), sqlStr(d.Description), sqlStr(severityOrDefault(d.Severity)), sqlStr(now))); err != nil {
			WriteError(w, http.StatusInternalServerError, "TX_FAILED", "rolled back: "+err.Error())
			return
		}
		diagID++
	}

	// 3. Insert prescriptions.
	for _, p := range req.Prescriptions {
		if err := tx.Exec(fmt.Sprintf(
			"INSERT INTO prescriptions VALUES (%d, %d, %d, %d, %s, %s, %s, %s, %s, %s, true);",
			presID, visitID, req.PatientID, req.DoctorID,
			sqlStr(p.DrugName), sqlStr(p.Dosage), sqlStr(p.Frequency),
			sqlStr(p.Duration), sqlStr(p.Instructions), sqlStr(now))); err != nil {
			WriteError(w, http.StatusInternalServerError, "TX_FAILED", "rolled back: "+err.Error())
			return
		}
		presID++
	}

	// 4. Commit.
	if err := tx.Commit(); err != nil {
		WriteError(w, http.StatusInternalServerError, "TX_FAILED", "commit failed, rolled back: "+err.Error())
		return
	}

	WriteJSON(w, http.StatusOK, map[string]interface{}{
		"status":              "completed",
		"visit_id":            visitID,
		"diagnoses_added":     len(req.Diagnoses),
		"prescriptions_added": len(req.Prescriptions),
	})
}

func severityOrDefault(s string) string {
	if s == "" {
		return "moderate"
	}
	return s
}
