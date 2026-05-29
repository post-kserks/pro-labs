package api

import (
	"fmt"
	"net/http"
	"sort"
	"strings"
	"time"

	"medvault-gateway/internal/models"
)

// ListPatients returns patients with in-Go search, sort and pagination
// (VaultDB has no LIKE / ORDER BY / OFFSET).
func (h *Handler) ListPatients(w http.ResponseWriter, r *http.Request, _ Params) {
	rows, err := h.queryMaps("SELECT * FROM patients;")
	if err != nil {
		WriteError(w, http.StatusInternalServerError, "VAULTDB_ERROR", err.Error())
		return
	}

	patients := make([]models.Patient, 0, len(rows))
	for _, m := range rows {
		patients = append(patients, toPatient(m))
	}

	// Search across name / phone / email / id.
	if q := strings.ToLower(strings.TrimSpace(r.URL.Query().Get("q"))); q != "" {
		filtered := patients[:0]
		for _, p := range patients {
			hay := strings.ToLower(fmt.Sprintf("%d %s %s %s %s", p.ID, p.FirstName, p.LastName, p.Phone, p.Email))
			if strings.Contains(hay, q) {
				filtered = append(filtered, p)
			}
		}
		patients = filtered
	}

	// Enrich with last visit + allergy flag.
	lastVisit := h.lastVisitByPatient()
	allergic := h.patientsWithAllergies()
	for i := range patients {
		patients[i].LastVisit = lastVisit[patients[i].ID]
		patients[i].HasAllergies = allergic[patients[i].ID]
	}

	sort.Slice(patients, func(i, j int) bool {
		if patients[i].LastName != patients[j].LastName {
			return patients[i].LastName < patients[j].LastName
		}
		return patients[i].FirstName < patients[j].FirstName
	})

	total := len(patients)
	page := atoiDefault(r.URL.Query().Get("page"), 1)
	pageSize := atoiDefault(r.URL.Query().Get("page_size"), 20)
	if page < 1 {
		page = 1
	}
	if pageSize < 1 || pageSize > 200 {
		pageSize = 20
	}
	start := (page - 1) * pageSize
	end := start + pageSize
	if start > total {
		start = total
	}
	if end > total {
		end = total
	}

	WriteJSON(w, http.StatusOK, map[string]interface{}{
		"patients":  patients[start:end],
		"total":     total,
		"page":      page,
		"page_size": pageSize,
	})
}

// GetPatient returns one patient by id with enrichment.
func (h *Handler) GetPatient(w http.ResponseWriter, r *http.Request, ps Params) {
	id := atoi(ps["id"])
	rows, err := h.queryMaps(fmt.Sprintf("SELECT * FROM patients WHERE id = %d;", id))
	if err != nil {
		WriteError(w, http.StatusInternalServerError, "VAULTDB_ERROR", err.Error())
		return
	}
	if len(rows) == 0 {
		WriteError(w, http.StatusNotFound, "PATIENT_NOT_FOUND", fmt.Sprintf("пациент с ID %d не найден", id))
		return
	}
	p := toPatient(rows[0])
	p.LastVisit = h.lastVisitByPatient()[id]
	p.HasAllergies = h.patientsWithAllergies()[id]
	WriteJSON(w, http.StatusOK, p)
}

// CreatePatient inserts a new patient.
func (h *Handler) CreatePatient(w http.ResponseWriter, r *http.Request, _ Params) {
	var p models.Patient
	if err := decodeJSON(r, &p); err != nil {
		WriteError(w, http.StatusBadRequest, "INVALID_REQUEST", "invalid body")
		return
	}
	id, err := h.nextID("patients")
	if err != nil {
		WriteError(w, http.StatusInternalServerError, "VAULTDB_ERROR", err.Error())
		return
	}
	now := time.Now().UTC().Format(time.RFC3339)
	sql := fmt.Sprintf(
		"INSERT INTO patients VALUES (%d, %s, %s, %s, %s, %s, %s, %s, %s, true);",
		id, sqlStr(p.FirstName), sqlStr(p.LastName), sqlStr(p.BirthDate), sqlStr(p.Gender),
		sqlStr(p.Phone), sqlStr(p.Email), sqlStr(p.BloodType), sqlStr(now))
	if err := h.DB.Exec(sql); err != nil {
		WriteError(w, http.StatusInternalServerError, "VAULTDB_ERROR", err.Error())
		return
	}
	p.ID = id
	p.CreatedAt = now
	p.IsActive = true
	WriteJSON(w, http.StatusCreated, p)
}

// PatientVisits lists a patient's visits (newest first) with doctor names.
func (h *Handler) PatientVisits(w http.ResponseWriter, r *http.Request, ps Params) {
	id := atoi(ps["id"])
	rows, err := h.queryMaps(fmt.Sprintf("SELECT * FROM visits WHERE patient_id = %d;", id))
	if err != nil {
		WriteError(w, http.StatusInternalServerError, "VAULTDB_ERROR", err.Error())
		return
	}
	docs := h.doctorNames()
	visits := make([]models.Visit, 0, len(rows))
	for _, m := range rows {
		v := toVisit(m)
		v.DoctorName = docs[v.DoctorID]
		visits = append(visits, v)
	}
	sort.Slice(visits, func(i, j int) bool { return visits[i].VisitDate > visits[j].VisitDate })
	WriteJSON(w, http.StatusOK, map[string]interface{}{"visits": visits})
}

// PatientDiagnoses lists a patient's active diagnoses with doctor names.
func (h *Handler) PatientDiagnoses(w http.ResponseWriter, r *http.Request, ps Params) {
	id := atoi(ps["id"])
	rows, err := h.queryMaps(fmt.Sprintf("SELECT * FROM diagnoses WHERE patient_id = %d;", id))
	if err != nil {
		WriteError(w, http.StatusInternalServerError, "VAULTDB_ERROR", err.Error())
		return
	}
	docs := h.doctorNames()
	out := make([]models.Diagnosis, 0, len(rows))
	for _, m := range rows {
		d := toDiagnosis(m)
		d.DoctorName = docs[d.DoctorID]
		out = append(out, d)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].DiagnosedAt > out[j].DiagnosedAt })
	WriteJSON(w, http.StatusOK, map[string]interface{}{"diagnoses": out})
}

// PatientPrescriptions lists a patient's prescriptions.
func (h *Handler) PatientPrescriptions(w http.ResponseWriter, r *http.Request, ps Params) {
	id := atoi(ps["id"])
	rows, err := h.queryMaps(fmt.Sprintf("SELECT * FROM prescriptions WHERE patient_id = %d;", id))
	if err != nil {
		WriteError(w, http.StatusInternalServerError, "VAULTDB_ERROR", err.Error())
		return
	}
	out := make([]models.Prescription, 0, len(rows))
	for _, m := range rows {
		out = append(out, toPrescription(m))
	}
	sort.Slice(out, func(i, j int) bool { return out[i].PrescribedAt > out[j].PrescribedAt })
	WriteJSON(w, http.StatusOK, map[string]interface{}{"prescriptions": out})
}

// PatientLabResults lists a patient's lab results.
func (h *Handler) PatientLabResults(w http.ResponseWriter, r *http.Request, ps Params) {
	id := atoi(ps["id"])
	rows, err := h.queryMaps(fmt.Sprintf("SELECT * FROM lab_results WHERE patient_id = %d;", id))
	if err != nil {
		WriteError(w, http.StatusInternalServerError, "VAULTDB_ERROR", err.Error())
		return
	}
	out := make([]models.LabResult, 0, len(rows))
	for _, m := range rows {
		out = append(out, toLabResult(m))
	}
	sort.Slice(out, func(i, j int) bool { return out[i].TestedAt > out[j].TestedAt })
	WriteJSON(w, http.StatusOK, map[string]interface{}{"lab_results": out})
}

// PatientAllergies lists a patient's allergies.
func (h *Handler) PatientAllergies(w http.ResponseWriter, r *http.Request, ps Params) {
	id := atoi(ps["id"])
	rows, err := h.queryMaps(fmt.Sprintf("SELECT * FROM allergies WHERE patient_id = %d;", id))
	if err != nil {
		WriteError(w, http.StatusInternalServerError, "VAULTDB_ERROR", err.Error())
		return
	}
	out := make([]models.Allergy, 0, len(rows))
	for _, m := range rows {
		out = append(out, toAllergy(m))
	}
	WriteJSON(w, http.StatusOK, map[string]interface{}{"allergies": out})
}

// ListDoctors returns all doctors.
func (h *Handler) ListDoctors(w http.ResponseWriter, r *http.Request, _ Params) {
	rows, err := h.queryMaps("SELECT * FROM doctors;")
	if err != nil {
		WriteError(w, http.StatusInternalServerError, "VAULTDB_ERROR", err.Error())
		return
	}
	out := make([]models.Doctor, 0, len(rows))
	for _, m := range rows {
		out = append(out, toDoctor(m))
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	WriteJSON(w, http.StatusOK, map[string]interface{}{"doctors": out})
}

// --- shared lookups ---

func (h *Handler) doctorNames() map[int]string {
	out := map[int]string{}
	rows, err := h.queryMaps("SELECT * FROM doctors;")
	if err != nil {
		return out
	}
	for _, m := range rows {
		out[atoi(m["id"])] = "Д-р " + m["last_name"] + " " + m["first_name"]
	}
	return out
}

func (h *Handler) lastVisitByPatient() map[int]string {
	out := map[int]string{}
	rows, err := h.queryMaps("SELECT * FROM visits;")
	if err != nil {
		return out
	}
	for _, m := range rows {
		pid := atoi(m["patient_id"])
		if m["visit_date"] > out[pid] {
			out[pid] = m["visit_date"]
		}
	}
	return out
}

func (h *Handler) patientsWithAllergies() map[int]bool {
	out := map[int]bool{}
	rows, err := h.queryMaps("SELECT * FROM allergies;")
	if err != nil {
		return out
	}
	for _, m := range rows {
		if parseBool(m["is_active"]) {
			out[atoi(m["patient_id"])] = true
		}
	}
	return out
}

func atoiDefault(s string, def int) int {
	if strings.TrimSpace(s) == "" {
		return def
	}
	return atoi(s)
}
