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
	doctorID, apiErr := clinicalDoctorID(r, req.DoctorID)
	if apiErr != nil {
		WriteError(w, apiErr.status, apiErr.code, apiErr.message)
		return
	}
	if req.VisitID <= 0 || req.PatientID <= 0 || req.DrugName == "" || req.Dosage == "" {
		WriteError(w, http.StatusBadRequest, "INVALID_REQUEST", "visit_id, patient_id, drug_name and dosage are required")
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
		id, req.VisitID, req.PatientID, doctorID,
		sqlStr(req.DrugName), sqlStr(req.Dosage), sqlStr(req.Frequency),
		sqlStr(req.Duration), sqlStr(req.Instructions), sqlStr(now))
	if err := h.DB.Exec(sql); err != nil {
		WriteError(w, http.StatusInternalServerError, "VAULTDB_ERROR", err.Error())
		return
	}
	WriteJSON(w, http.StatusCreated, map[string]interface{}{"id": id})
}

// DeactivatePrescription "removes" a prescription from the current treatment
// plan while keeping the old row version available through VaultDB history.
func (h *Handler) DeactivatePrescription(w http.ResponseWriter, r *http.Request, ps Params) {
	id := atoi(ps["id"])
	if err := h.DB.Exec(fmt.Sprintf("UPDATE prescriptions SET is_active = false WHERE id = %d;", id)); err != nil {
		WriteError(w, http.StatusInternalServerError, "VAULTDB_ERROR", err.Error())
		return
	}
	WriteJSON(w, http.StatusOK, map[string]interface{}{"id": id, "status": "deactivated"})
}
