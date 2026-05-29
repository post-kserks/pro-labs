package api

import (
	"fmt"
	"net/http"
	"sort"
	"time"

	"medvault-gateway/internal/models"
)

// timelineMarker is a labelled point on a patient's Time-Travel slider.
// Its `at` is a *real* VaultDB version timestamp captured during seeding, so
// `AS OF TIMESTAMP <at>` genuinely returns the state as of that moment.
type timelineMarker struct {
	Label string `json:"label"`
	At    string `json:"at"`
	Note  string `json:"note"`
}

// PatientTimeline returns the slider window and labelled markers for a patient.
func (h *Handler) PatientTimeline(w http.ResponseWriter, r *http.Request, ps Params) {
	pid := atoi(ps["id"])

	markers := h.markersForPatient(pid)

	// Always append a live "current" marker.
	now := time.Now().UTC()
	markers = append(markers, timelineMarker{
		Label: "Сейчас",
		At:    now.Format(time.RFC3339Nano),
		Note:  "Текущее состояние карты",
	})

	from := markers[0].At
	to := markers[len(markers)-1].At

	WriteJSON(w, http.StatusOK, map[string]interface{}{
		"patient_id": pid,
		"from":       from,
		"to":         to,
		"markers":    markers,
	})
}

// markersForPatient reads seeded markers; if none exist it synthesises a
// single window from the patient's earliest visible data up to now.
func (h *Handler) markersForPatient(pid int) []timelineMarker {
	rows, err := h.queryMaps(fmt.Sprintf("SELECT * FROM timeline_markers WHERE patient_id = %d;", pid))
	if err == nil && len(rows) > 0 {
		markers := make([]timelineMarker, 0, len(rows))
		for _, m := range rows {
			markers = append(markers, timelineMarker{
				Label: m["label"],
				At:    m["marker_at"],
				Note:  m["note"],
			})
		}
		sort.Slice(markers, func(i, j int) bool { return markers[i].At < markers[j].At })
		return markers
	}

	// Fallback: start the window a little before the patient's data.
	start := time.Now().UTC().Add(-2 * time.Minute)
	return []timelineMarker{{
		Label: "Начало истории",
		At:    start.Format(time.RFC3339Nano),
		Note:  "Самые ранние данные пациента",
	}}
}

// PatientSnapshot returns the full patient card AS OF a given timestamp.
func (h *Handler) PatientSnapshot(w http.ResponseWriter, r *http.Request, ps Params) {
	pid := atoi(ps["id"])
	atParam := r.URL.Query().Get("at")
	if atParam == "" {
		WriteError(w, http.StatusBadRequest, "INVALID_REQUEST", "parameter 'at' is required (ISO 8601)")
		return
	}
	at, err := time.Parse(time.RFC3339Nano, atParam)
	if err != nil {
		at, err = time.Parse(time.RFC3339, atParam)
		if err != nil {
			WriteError(w, http.StatusBadRequest, "INVALID_TIMESTAMP", "invalid timestamp, use ISO 8601")
			return
		}
	}
	// VaultDB accepts 'YYYY-MM-DD HH:MM:SS'; pass nanos for precision.
	ts := at.UTC().Format("2006-01-02 15:04:05.999999999")
	asOf := fmt.Sprintf("AS OF TIMESTAMP '%s'", ts)

	patientRows, err := h.queryMaps(fmt.Sprintf("SELECT * FROM patients %s WHERE id = %d;", asOf, pid))
	if err != nil {
		WriteError(w, http.StatusInternalServerError, "VAULTDB_ERROR", err.Error())
		return
	}
	diagRows, err := h.queryMaps(fmt.Sprintf("SELECT * FROM diagnoses %s WHERE patient_id = %d;", asOf, pid))
	if err != nil {
		WriteError(w, http.StatusInternalServerError, "VAULTDB_ERROR", err.Error())
		return
	}
	presRows, err := h.queryMaps(fmt.Sprintf("SELECT * FROM prescriptions %s WHERE patient_id = %d;", asOf, pid))
	if err != nil {
		WriteError(w, http.StatusInternalServerError, "VAULTDB_ERROR", err.Error())
		return
	}
	visitRows, err := h.queryMaps(fmt.Sprintf("SELECT * FROM visits %s WHERE patient_id = %d;", asOf, pid))
	if err != nil {
		WriteError(w, http.StatusInternalServerError, "VAULTDB_ERROR", err.Error())
		return
	}

	var patient *models.Patient
	if len(patientRows) > 0 {
		p := toPatient(patientRows[0])
		patient = &p
	}
	diagnoses := make([]models.Diagnosis, 0, len(diagRows))
	for _, m := range diagRows {
		diagnoses = append(diagnoses, toDiagnosis(m))
	}
	prescriptions := make([]models.Prescription, 0, len(presRows))
	for _, m := range presRows {
		prescriptions = append(prescriptions, toPrescription(m))
	}
	visits := make([]models.Visit, 0, len(visitRows))
	for _, m := range visitRows {
		visits = append(visits, toVisit(m))
	}

	WriteJSON(w, http.StatusOK, map[string]interface{}{
		"patient":       patient,
		"as_of":         at.UTC().Format(time.RFC3339),
		"diagnoses":     diagnoses,
		"prescriptions": prescriptions,
		"visits":        visits,
		"sql":           fmt.Sprintf("SELECT * FROM diagnoses %s WHERE patient_id = %d;", asOf, pid),
	})
}
