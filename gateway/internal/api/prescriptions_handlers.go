package api

import (
	"fmt"
	"net/http"
	"time"
)

// CreatePrescription issues a prescription.
func (h *Handler) CreatePrescription(w http.ResponseWriter, r *http.Request, _ Params) {
	var req struct {
		VisitID      int    `json:"visit_id"`
		PatientID    int    `json:"patient_id"`
		DoctorID     int    `json:"doctor_id"`
		DrugName     string `json:"drug_name"`
		Dosage       string `json:"dosage"`
		Frequency    string `json:"frequency"`
		Duration     string `json:"duration"`
		Instructions string `json:"instructions"`
	}
	if err := decodeJSON(r, &req); err != nil {
		WriteError(w, http.StatusBadRequest, "INVALID_REQUEST", "invalid body")
		return
	}
	id, err := h.nextID("prescriptions")
	if err != nil {
		WriteError(w, http.StatusInternalServerError, "VAULTDB_ERROR", err.Error())
		return
	}
	now := time.Now().UTC().Format(time.RFC3339)
	sql := fmt.Sprintf(
		"INSERT INTO prescriptions VALUES (%d, %d, %d, %d, %s, %s, %s, %s, %s, %s, true);",
		id, req.VisitID, req.PatientID, req.DoctorID,
		sqlStr(req.DrugName), sqlStr(req.Dosage), sqlStr(req.Frequency),
		sqlStr(req.Duration), sqlStr(req.Instructions), sqlStr(now))
	if err := h.DB.Exec(sql); err != nil {
		WriteError(w, http.StatusInternalServerError, "VAULTDB_ERROR", err.Error())
		return
	}
	WriteJSON(w, http.StatusCreated, map[string]interface{}{"id": id})
}
