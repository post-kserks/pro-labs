package api

import (
	"net/http"
	"strconv"
	"strings"

	"medvault-gateway/internal/models"
)

// sqlStr escapes a value for use as a single-quoted SQL string literal.
// VaultDB has no parameter binding over the wire, so we escape defensively.
func sqlStr(s string) string {
	s = strings.ReplaceAll(s, "'", "''")
	// Strip characters that would break the newline-delimited JSON protocol.
	s = strings.ReplaceAll(s, "\n", " ")
	s = strings.ReplaceAll(s, "\r", " ")
	return "'" + s + "'"
}

func atoi(s string) int {
	n, _ := strconv.Atoi(strings.TrimSpace(s))
	return n
}

func parseBool(s string) bool {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "true", "1", "t", "yes":
		return true
	default:
		return false
	}
}

type roleError struct {
	status  int
	code    string
	message string
}

func clinicalDoctorID(r *http.Request, requested int) (int, *roleError) {
	user := currentUser(r)
	switch user.Role {
	case "doctor":
		if user.DoctorID <= 0 {
			return 0, &roleError{status: http.StatusForbidden, code: "FORBIDDEN", message: "doctor account is not linked to a doctor record"}
		}
		if requested != 0 && requested != user.DoctorID {
			return 0, &roleError{status: http.StatusForbidden, code: "FORBIDDEN", message: "doctor can write only under their own doctor record"}
		}
		return user.DoctorID, nil
	case "admin":
		if requested <= 0 {
			return 0, &roleError{status: http.StatusBadRequest, code: "INVALID_REQUEST", message: "doctor_id is required"}
		}
		return requested, nil
	default:
		return 0, &roleError{status: http.StatusForbidden, code: "FORBIDDEN", message: "operation requires doctor or admin role"}
	}
}

// --- model mappers (column->value map to typed struct) ---

func toPatient(m map[string]string) models.Patient {
	return models.Patient{
		ID:        atoi(m["id"]),
		FirstName: m["first_name"],
		LastName:  m["last_name"],
		BirthDate: m["birth_date"],
		Gender:    m["gender"],
		Phone:     m["phone"],
		Email:     m["email"],
		BloodType: m["blood_type"],
		CreatedAt: m["created_at"],
		IsActive:  parseBool(m["is_active"]),
	}
}

func toDoctor(m map[string]string) models.Doctor {
	return models.Doctor{
		ID:             atoi(m["id"]),
		FirstName:      m["first_name"],
		LastName:       m["last_name"],
		Specialization: m["specialization"],
		LicenseNumber:  m["license_number"],
		Phone:          m["phone"],
		Email:          m["email"],
		IsActive:       parseBool(m["is_active"]),
	}
}

func toVisit(m map[string]string) models.Visit {
	return models.Visit{
		ID:             atoi(m["id"]),
		PatientID:      atoi(m["patient_id"]),
		DoctorID:       atoi(m["doctor_id"]),
		VisitDate:      m["visit_date"],
		Status:         m["status"],
		ChiefComplaint: m["chief_complaint"],
		Notes:          m["notes"],
		CreatedAt:      m["created_at"],
	}
}

func toDiagnosis(m map[string]string) models.Diagnosis {
	return models.Diagnosis{
		ID:          atoi(m["id"]),
		VisitID:     atoi(m["visit_id"]),
		PatientID:   atoi(m["patient_id"]),
		DoctorID:    atoi(m["doctor_id"]),
		ICDCode:     m["icd_code"],
		Description: m["description"],
		Severity:    m["severity"],
		DiagnosedAt: m["diagnosed_at"],
		IsActive:    parseBool(m["is_active"]),
	}
}

func toPrescription(m map[string]string) models.Prescription {
	return models.Prescription{
		ID:           atoi(m["id"]),
		VisitID:      atoi(m["visit_id"]),
		PatientID:    atoi(m["patient_id"]),
		DoctorID:     atoi(m["doctor_id"]),
		DrugName:     m["drug_name"],
		Dosage:       m["dosage"],
		Frequency:    m["frequency"],
		Duration:     m["duration"],
		Instructions: m["instructions"],
		PrescribedAt: m["prescribed_at"],
		IsActive:     parseBool(m["is_active"]),
	}
}

func toLabResult(m map[string]string) models.LabResult {
	return models.LabResult{
		ID:           atoi(m["id"]),
		PatientID:    atoi(m["patient_id"]),
		VisitID:      atoi(m["visit_id"]),
		TestName:     m["test_name"],
		ResultValue:  m["result_value"],
		Unit:         m["unit"],
		ReferenceMin: m["reference_min"],
		ReferenceMax: m["reference_max"],
		IsNormal:     parseBool(m["is_normal"]),
		TestedAt:     m["tested_at"],
	}
}

func toAllergy(m map[string]string) models.Allergy {
	return models.Allergy{
		ID:           atoi(m["id"]),
		PatientID:    atoi(m["patient_id"]),
		Allergen:     m["allergen"],
		Reaction:     m["reaction"],
		Severity:     m["severity"],
		DiscoveredAt: m["discovered_at"],
		IsActive:     parseBool(m["is_active"]),
	}
}
