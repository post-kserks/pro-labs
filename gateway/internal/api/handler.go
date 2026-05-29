package api

import (
	"context"
	"net/http"
	"strings"

	"medvault-gateway/internal/auth"
	"medvault-gateway/internal/vaultdb"
)

type ctxKey string

const userCtxKey ctxKey = "user"

// Handler bundles dependencies shared by all route handlers.
type Handler struct {
	DB         *vaultdb.Client
	Signer     *auth.Signer
	MonitorURL string // e.g. http://vaultdb:5433 — for /health and /metrics
}

// New creates a Handler.
func New(db *vaultdb.Client, signer *auth.Signer, monitorURL string) *Handler {
	return &Handler{DB: db, Signer: signer, MonitorURL: monitorURL}
}

// Routes builds the full API router.
func (h *Handler) Routes() *Router {
	r := NewRouter()

	// Auth
	r.Handle(http.MethodPost, "/api/v1/auth/login", h.Login)
	r.Handle(http.MethodPost, "/api/v1/auth/logout", h.Logout)
	r.Handle(http.MethodGet, "/api/v1/auth/me", h.auth(h.Me))

	// Patients
	r.Handle(http.MethodGet, "/api/v1/patients", h.auth(h.ListPatients))
	r.Handle(http.MethodPost, "/api/v1/patients", h.auth(h.CreatePatient))
	r.Handle(http.MethodGet, "/api/v1/patients/:id", h.auth(h.GetPatient))
	r.Handle(http.MethodGet, "/api/v1/patients/:id/visits", h.auth(h.PatientVisits))
	r.Handle(http.MethodGet, "/api/v1/patients/:id/diagnoses", h.auth(h.PatientDiagnoses))
	r.Handle(http.MethodGet, "/api/v1/patients/:id/prescriptions", h.auth(h.PatientPrescriptions))
	r.Handle(http.MethodGet, "/api/v1/patients/:id/lab_results", h.auth(h.PatientLabResults))
	r.Handle(http.MethodGet, "/api/v1/patients/:id/allergies", h.auth(h.PatientAllergies))

	// Time Travel
	r.Handle(http.MethodGet, "/api/v1/patients/:id/timeline", h.auth(h.PatientTimeline))
	r.Handle(http.MethodGet, "/api/v1/patients/:id/snapshot", h.auth(h.PatientSnapshot))
	r.Handle(http.MethodGet, "/api/v1/diagnoses/:id/history", h.auth(h.DiagnosisHistory))

	// Doctors
	r.Handle(http.MethodGet, "/api/v1/doctors", h.auth(h.ListDoctors))

	// Visits
	r.Handle(http.MethodGet, "/api/v1/visits", h.auth(h.ListVisits))
	r.Handle(http.MethodPost, "/api/v1/visits", h.auth(h.CreateVisit))
	r.Handle(http.MethodGet, "/api/v1/visits/:id", h.auth(h.GetVisit))
	r.Handle(http.MethodPost, "/api/v1/visits/:id/complete", h.auth(h.CompleteVisit))

	// Diagnoses / prescriptions write paths
	r.Handle(http.MethodPost, "/api/v1/diagnoses", h.auth(h.CreateDiagnosis))
	r.Handle(http.MethodPut, "/api/v1/diagnoses/:id", h.auth(h.UpdateDiagnosis))
	r.Handle(http.MethodDelete, "/api/v1/diagnoses/:id", h.auth(h.DeactivateDiagnosis))
	r.Handle(http.MethodPost, "/api/v1/prescriptions", h.auth(h.CreatePrescription))

	// Admin
	r.Handle(http.MethodGet, "/api/v1/admin/stats", h.auth(h.AdminStats))
	r.Handle(http.MethodGet, "/api/v1/admin/metrics", h.auth(h.AdminMetrics))
	r.Handle(http.MethodGet, "/api/v1/admin/vacuum", h.auth(h.VacuumStats))
	r.Handle(http.MethodPost, "/api/v1/admin/vacuum", h.auth(h.RunVacuum))
	r.Handle(http.MethodPost, "/api/v1/admin/explain", h.auth(h.Explain))
	r.Handle(http.MethodGet, "/api/v1/admin/wal_status", h.auth(h.WALStatus))
	r.Handle(http.MethodGet, "/api/v1/admin/indexes", h.auth(h.ListIndexes))

	// Health
	r.Handle(http.MethodGet, "/health", func(w http.ResponseWriter, _ *http.Request, _ Params) {
		err := h.DB.Ping()
		if err != nil {
			WriteJSON(w, http.StatusServiceUnavailable, map[string]any{"status": "degraded", "db": err.Error()})
			return
		}
		WriteJSON(w, http.StatusOK, map[string]any{"status": "ok"})
	})

	return r
}

// auth wraps a handler with JWT validation and injects the user into context.
func (h *Handler) auth(next HandlerFunc) HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request, ps Params) {
		token := bearer(r)
		if token == "" {
			WriteError(w, http.StatusUnauthorized, "UNAUTHORIZED", "missing token")
			return
		}
		claims, err := h.Signer.Parse(token)
		if err != nil {
			WriteError(w, http.StatusUnauthorized, "UNAUTHORIZED", "invalid or expired token")
			return
		}
		ctx := context.WithValue(r.Context(), userCtxKey, auth.UserFromClaims(claims))
		next(w, r.WithContext(ctx), ps)
	}
}

func bearer(r *http.Request) string {
	a := r.Header.Get("Authorization")
	if strings.HasPrefix(a, "Bearer ") {
		return strings.TrimPrefix(a, "Bearer ")
	}
	return ""
}

func currentUser(r *http.Request) auth.User {
	u, _ := r.Context().Value(userCtxKey).(auth.User)
	return u
}
