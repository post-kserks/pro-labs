// Package models defines the MedVault domain entities returned by the API.
// VaultDB returns every column as a string, so these structs are assembled by
// the handlers from column->value maps.
package models

type Patient struct {
	ID        int    `json:"id"`
	FirstName string `json:"first_name"`
	LastName  string `json:"last_name"`
	BirthDate string `json:"birth_date"`
	Gender    string `json:"gender"`
	Phone     string `json:"phone"`
	Email     string `json:"email"`
	BloodType string `json:"blood_type"`
	CreatedAt string `json:"created_at"`
	IsActive  bool   `json:"is_active"`

	// Derived / joined fields (not stored columns).
	LastVisit    string `json:"last_visit,omitempty"`
	HasAllergies bool   `json:"has_allergies"`
}

type Doctor struct {
	ID             int    `json:"id"`
	FirstName      string `json:"first_name"`
	LastName       string `json:"last_name"`
	Specialization string `json:"specialization"`
	LicenseNumber  string `json:"license_number"`
	Phone          string `json:"phone"`
	Email          string `json:"email"`
	IsActive       bool   `json:"is_active"`
}

type Visit struct {
	ID             int    `json:"id"`
	PatientID      int    `json:"patient_id"`
	DoctorID       int    `json:"doctor_id"`
	VisitDate      string `json:"visit_date"`
	Status         string `json:"status"`
	ChiefComplaint string `json:"chief_complaint"`
	Notes          string `json:"notes"`
	CreatedAt      string `json:"created_at"`

	DoctorName string `json:"doctor_name,omitempty"`
}

type Diagnosis struct {
	ID          int    `json:"id"`
	VisitID     int    `json:"visit_id"`
	PatientID   int    `json:"patient_id"`
	DoctorID    int    `json:"doctor_id"`
	ICDCode     string `json:"icd_code"`
	Description string `json:"description"`
	Severity    string `json:"severity"`
	DiagnosedAt string `json:"diagnosed_at"`
	IsActive    bool   `json:"is_active"`

	DoctorName string `json:"doctor_name,omitempty"`
}

type Prescription struct {
	ID           int    `json:"id"`
	VisitID      int    `json:"visit_id"`
	PatientID    int    `json:"patient_id"`
	DoctorID     int    `json:"doctor_id"`
	DrugName     string `json:"drug_name"`
	Dosage       string `json:"dosage"`
	Frequency    string `json:"frequency"`
	Duration     string `json:"duration"`
	Instructions string `json:"instructions"`
	PrescribedAt string `json:"prescribed_at"`
	IsActive     bool   `json:"is_active"`
}

type LabResult struct {
	ID           int    `json:"id"`
	PatientID    int    `json:"patient_id"`
	VisitID      int    `json:"visit_id"`
	TestName     string `json:"test_name"`
	ResultValue  string `json:"result_value"`
	Unit         string `json:"unit"`
	ReferenceMin string `json:"reference_min"`
	ReferenceMax string `json:"reference_max"`
	IsNormal     bool   `json:"is_normal"`
	TestedAt     string `json:"tested_at"`
}

type Allergy struct {
	ID           int    `json:"id"`
	PatientID    int    `json:"patient_id"`
	Allergen     string `json:"allergen"`
	Reaction     string `json:"reaction"`
	Severity     string `json:"severity"`
	DiscoveredAt string `json:"discovered_at"`
	IsActive     bool   `json:"is_active"`
}
