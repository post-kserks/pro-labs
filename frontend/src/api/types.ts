export interface User {
  email: string;
  name: string;
  role: 'doctor' | 'admin' | 'receptionist' | string;
  doctor_id?: number;
}

export interface Patient {
  id: number;
  first_name: string;
  last_name: string;
  birth_date: string;
  gender: string;
  phone: string;
  email: string;
  blood_type: string;
  created_at: string;
  is_active: boolean;
  last_visit?: string;
  has_allergies: boolean;
}

export interface Doctor {
  id: number;
  first_name: string;
  last_name: string;
  specialization: string;
  license_number: string;
  phone: string;
  email: string;
  is_active: boolean;
}

export interface Visit {
  id: number;
  patient_id: number;
  doctor_id: number;
  visit_date: string;
  status: string;
  chief_complaint: string;
  notes: string;
  created_at: string;
  doctor_name?: string;
}

export interface Diagnosis {
  id: number;
  visit_id: number;
  patient_id: number;
  doctor_id: number;
  icd_code: string;
  description: string;
  severity: string;
  diagnosed_at: string;
  is_active: boolean;
  doctor_name?: string;
}

export interface Prescription {
  id: number;
  visit_id: number;
  patient_id: number;
  doctor_id: number;
  drug_name: string;
  dosage: string;
  frequency: string;
  duration: string;
  instructions: string;
  prescribed_at: string;
  is_active: boolean;
}

export interface LabResult {
  id: number;
  patient_id: number;
  visit_id: number;
  test_name: string;
  result_value: string;
  unit: string;
  reference_min: string;
  reference_max: string;
  is_normal: boolean;
  tested_at: string;
}

export interface Allergy {
  id: number;
  patient_id: number;
  allergen: string;
  reaction: string;
  severity: string;
  discovered_at: string;
  is_active: boolean;
}

export interface TimelineMarker {
  label: string;
  at: string;
  note: string;
}

export interface PatientTimeline {
  patient_id: number;
  from: string;
  to: string;
  markers: TimelineMarker[];
}

export interface PatientSnapshot {
  patient: Patient | null;
  as_of: string;
  diagnoses: Diagnosis[];
  prescriptions: Prescription[];
  visits: Visit[];
  sql: string;
}

export interface DiagnosisVersion {
  created_tx: string;
  deleted_tx: string;
  is_current: boolean;
  icd_code: string;
  description: string;
  severity: string;
  doctor_id: number;
  diagnosed_at: string;
}

export interface DiagnosisHistoryResponse {
  diagnosis_id: number;
  versions: DiagnosisVersion[];
  sql: string;
}

export interface AdminStats {
  patients_total: number;
  doctors_total: number;
  visits_total: number;
  diagnoses_total: number;
  active_diagnoses: number;
  visits_today: number;
  completed_today: number;
}

export interface VacuumTableStat {
  table: string;
  live_rows: number;
}

export interface VacuumResultRow {
  table: string;
  rows_before: number;
  rows_after: number;
  reclaimed: number;
  size_before_kb: number;
  size_after_kb: number;
  duration_ms: string;
}
