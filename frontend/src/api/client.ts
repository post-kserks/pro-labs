import axios from 'axios';
import type {
  AdminStats,
  Allergy,
  Diagnosis,
  DiagnosisHistoryResponse,
  Doctor,
  LabResult,
  Patient,
  PatientSnapshot,
  PatientTimeline,
  Prescription,
  User,
  VacuumResultRow,
  VacuumTableStat,
  Visit,
} from './types';

export const TOKEN_KEY = 'medvault_token';

const client = axios.create({
  baseURL: import.meta.env.VITE_API_URL || 'http://localhost:4000',
  timeout: 15000,
});

client.interceptors.request.use((config) => {
  const token = localStorage.getItem(TOKEN_KEY);
  if (token) {
    config.headers.Authorization = `Bearer ${token}`;
  }
  return config;
});

client.interceptors.response.use(
  (response) => response,
  (error) => {
    if (error.response?.status === 401 && !error.config?.url?.includes('/auth/login')) {
      localStorage.removeItem(TOKEN_KEY);
      if (window.location.pathname !== '/login') {
        window.location.href = '/login';
      }
    }
    return Promise.reject(error);
  },
);

// ── Auth ──────────────────────────────────────────────────────────────
export async function login(email: string, password: string): Promise<{ token: string; user: User }> {
  const { data } = await client.post('/api/v1/auth/login', { email, password });
  return data;
}

export async function getMe(): Promise<User> {
  const { data } = await client.get('/api/v1/auth/me');
  return data;
}

// ── Patients ──────────────────────────────────────────────────────────
export async function listPatients(params: {
  q?: string;
  page?: number;
  page_size?: number;
}): Promise<{ patients: Patient[]; total: number; page: number; page_size: number }> {
  const { data } = await client.get('/api/v1/patients', { params });
  return data;
}

export async function getPatient(id: number): Promise<Patient> {
  const { data } = await client.get(`/api/v1/patients/${id}`);
  return data;
}

export async function createPatient(payload: Partial<Patient>): Promise<Patient> {
  const { data } = await client.post('/api/v1/patients', payload);
  return data;
}

export async function getPatientVisits(id: number): Promise<Visit[]> {
  const { data } = await client.get(`/api/v1/patients/${id}/visits`);
  return data.visits ?? [];
}

export async function getPatientDiagnoses(id: number): Promise<Diagnosis[]> {
  const { data } = await client.get(`/api/v1/patients/${id}/diagnoses`);
  return data.diagnoses ?? [];
}

export async function getPatientPrescriptions(id: number): Promise<Prescription[]> {
  const { data } = await client.get(`/api/v1/patients/${id}/prescriptions`);
  return data.prescriptions ?? [];
}

export async function getPatientLabResults(id: number): Promise<LabResult[]> {
  const { data } = await client.get(`/api/v1/patients/${id}/lab_results`);
  return data.lab_results ?? [];
}

export async function getPatientAllergies(id: number): Promise<Allergy[]> {
  const { data } = await client.get(`/api/v1/patients/${id}/allergies`);
  return data.allergies ?? [];
}

// ── Time Travel ───────────────────────────────────────────────────────
export async function getPatientTimeline(id: number): Promise<PatientTimeline> {
  const { data } = await client.get(`/api/v1/patients/${id}/timeline`);
  return data;
}

export async function getPatientSnapshot(id: number, at: string): Promise<PatientSnapshot> {
  const { data } = await client.get(`/api/v1/patients/${id}/snapshot`, { params: { at } });
  return data;
}

export async function getDiagnosisHistory(diagnosisId: number): Promise<DiagnosisHistoryResponse> {
  const { data } = await client.get(`/api/v1/diagnoses/${diagnosisId}/history`);
  return data;
}

// ── Doctors / Visits ──────────────────────────────────────────────────
export async function listDoctors(): Promise<Doctor[]> {
  const { data } = await client.get('/api/v1/doctors');
  return data.doctors ?? [];
}

export async function completeVisit(
  visitId: number,
  payload: unknown,
): Promise<{ status: string }> {
  const { data } = await client.post(`/api/v1/visits/${visitId}/complete`, payload);
  return data;
}

// ── Admin ─────────────────────────────────────────────────────────────
export async function getAdminStats(): Promise<AdminStats> {
  const { data } = await client.get('/api/v1/admin/stats');
  return data;
}

export async function explainQuery(
  sql: string,
  analyze: boolean,
): Promise<{ plan: string; sql: string }> {
  const { data } = await client.post('/api/v1/admin/explain', { sql, analyze });
  return data;
}

export async function getVacuumStats(): Promise<{ tables: VacuumTableStat[]; note: string }> {
  const { data } = await client.get('/api/v1/admin/vacuum');
  return data;
}

export async function runVacuum(): Promise<{
  tables: VacuumResultRow[];
  total_reclaimed: number;
  total_saved_kb: number;
}> {
  const { data } = await client.post('/api/v1/admin/vacuum', {});
  return data;
}

export async function getWalStatus(): Promise<{ active: boolean; health: string; summary: string }> {
  const { data } = await client.get('/api/v1/admin/wal_status');
  return data;
}

export async function getMetrics(): Promise<{ metrics: string }> {
  const { data } = await client.get('/api/v1/admin/metrics');
  return data;
}

export async function getIndexes(): Promise<{ indexes: { table: string; index: string }[] }> {
  const { data } = await client.get('/api/v1/admin/indexes');
  return data;
}

export default client;
