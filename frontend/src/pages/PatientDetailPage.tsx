import { type FormEvent, useState } from 'react';
import { Link, useParams } from 'react-router-dom';
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query';
import { AlertTriangle, Clock, Plus, Trash2, X } from 'lucide-react';
import {
  createDiagnosis,
  createPrescription,
  deleteDiagnosis,
  deletePrescription,
  getPatient,
  getPatientAllergies,
  getPatientDiagnoses,
  getPatientLabResults,
  getPatientPrescriptions,
  getPatientVisits,
  listDoctors,
} from '../api/client';
import { DiagnosisHistory } from '../components/DiagnosisHistory';
import { age, fmtDate, fmtDateTime, severityLabel, statusLabel } from '../lib/format';
import { useAuth } from '../store/auth';

type Tab = 'visits' | 'diagnoses' | 'prescriptions' | 'labs' | 'allergies';

const emptyDiagnosisForm = {
  visit_id: '',
  doctor_id: '',
  icd_code: '',
  description: '',
  severity: 'moderate',
};

const emptyPrescriptionForm = {
  visit_id: '',
  doctor_id: '',
  drug_name: '',
  dosage: '',
  frequency: '',
  duration: '',
  instructions: '',
};

function errorMessage(error: unknown, fallback: string) {
  const maybeHTTPError = error as { response?: { data?: { error?: { message?: string } } } };
  return maybeHTTPError.response?.data?.error?.message ?? fallback;
}

export function PatientDetailPage() {
  const { id } = useParams();
  const pid = Number(id);
  const queryClient = useQueryClient();
  const { user } = useAuth();
  const [tab, setTab] = useState<Tab>('diagnoses');
  const [historyId, setHistoryId] = useState<number | null>(null);
  const [showDiagnosisForm, setShowDiagnosisForm] = useState(false);
  const [showPrescriptionForm, setShowPrescriptionForm] = useState(false);
  const [diagnosisForm, setDiagnosisForm] = useState(emptyDiagnosisForm);
  const [prescriptionForm, setPrescriptionForm] = useState(emptyPrescriptionForm);

  const canManageClinicalData = user?.role === 'doctor' || user?.role === 'admin';

  const { data: patient } = useQuery({ queryKey: ['patient', pid], queryFn: () => getPatient(pid) });
  const { data: visits } = useQuery({ queryKey: ['patient-visits', pid], queryFn: () => getPatientVisits(pid) });
  const { data: diagnoses } = useQuery({ queryKey: ['patient-diagnoses', pid], queryFn: () => getPatientDiagnoses(pid) });
  const { data: prescriptions } = useQuery({ queryKey: ['patient-prescriptions', pid], queryFn: () => getPatientPrescriptions(pid) });
  const { data: labs } = useQuery({ queryKey: ['patient-labs', pid], queryFn: () => getPatientLabResults(pid) });
  const { data: allergies } = useQuery({ queryKey: ['patient-allergies', pid], queryFn: () => getPatientAllergies(pid) });
  const { data: doctors } = useQuery({
    queryKey: ['doctors'],
    queryFn: listDoctors,
    enabled: canManageClinicalData,
  });

  const refreshClinicalData = () => {
    queryClient.invalidateQueries({ queryKey: ['patient-diagnoses', pid] });
    queryClient.invalidateQueries({ queryKey: ['patient-prescriptions', pid] });
    queryClient.invalidateQueries({ queryKey: ['timeline', pid] });
    queryClient.invalidateQueries({ queryKey: ['snapshot', pid] });
    queryClient.invalidateQueries({ queryKey: ['admin-stats'] });
  };

  const createDiagnosisMutation = useMutation({
    mutationFn: createDiagnosis,
    onSuccess: () => {
      setDiagnosisForm(emptyDiagnosisForm);
      setShowDiagnosisForm(false);
      refreshClinicalData();
    },
  });

  const deleteDiagnosisMutation = useMutation({
    mutationFn: deleteDiagnosis,
    onSuccess: refreshClinicalData,
  });

  const createPrescriptionMutation = useMutation({
    mutationFn: createPrescription,
    onSuccess: () => {
      setPrescriptionForm(emptyPrescriptionForm);
      setShowPrescriptionForm(false);
      refreshClinicalData();
    },
  });

  const deletePrescriptionMutation = useMutation({
    mutationFn: deletePrescription,
    onSuccess: refreshClinicalData,
  });

  const visitOptions = visits ?? [];
  const doctorOptions = doctors ?? [];
  const defaultVisitId = visitOptions[0]?.id ?? 0;

  function selectedDoctorId(formDoctorId: string) {
    if (user?.role === 'doctor') {
      return user.doctor_id ?? 0;
    }
    return Number(formDoctorId || doctorOptions[0]?.id || 0);
  }

  function onDiagnosisSubmit(e: FormEvent<HTMLFormElement>) {
    e.preventDefault();
    const visitId = Number(diagnosisForm.visit_id || defaultVisitId);
    const doctorId = selectedDoctorId(diagnosisForm.doctor_id);
    if (visitId === 0 || doctorId === 0) {
      return;
    }
    createDiagnosisMutation.mutate({
      visit_id: visitId,
      patient_id: pid,
      doctor_id: doctorId,
      icd_code: diagnosisForm.icd_code.trim(),
      description: diagnosisForm.description.trim(),
      severity: diagnosisForm.severity,
    });
  }

  function onPrescriptionSubmit(e: FormEvent<HTMLFormElement>) {
    e.preventDefault();
    const visitId = Number(prescriptionForm.visit_id || defaultVisitId);
    const doctorId = selectedDoctorId(prescriptionForm.doctor_id);
    if (visitId === 0 || doctorId === 0) {
      return;
    }
    createPrescriptionMutation.mutate({
      visit_id: visitId,
      patient_id: pid,
      doctor_id: doctorId,
      drug_name: prescriptionForm.drug_name.trim(),
      dosage: prescriptionForm.dosage.trim(),
      frequency: prescriptionForm.frequency.trim(),
      duration: prescriptionForm.duration.trim(),
      instructions: prescriptionForm.instructions.trim(),
    });
  }

  function onDeleteDiagnosis(diagnosisId: number) {
    if (window.confirm('Снять диагноз из текущей карты пациента?')) {
      deleteDiagnosisMutation.mutate(diagnosisId);
    }
  }

  function onDeletePrescription(prescriptionId: number) {
    if (window.confirm('Удалить препарат из текущего плана лечения?')) {
      deletePrescriptionMutation.mutate(prescriptionId);
    }
  }

  const tabs: { key: Tab; label: string }[] = [
    { key: 'visits', label: 'Визиты' },
    { key: 'diagnoses', label: 'Диагнозы' },
    { key: 'prescriptions', label: 'Назначения' },
    { key: 'labs', label: 'Анализы' },
    { key: 'allergies', label: 'Аллергии' },
  ];

  return (
    <div className="space-y-5">
      <div className="grid grid-cols-1 md:grid-cols-3 gap-4">
        <div className="md:col-span-2 bg-white rounded-xl border border-gray-200 p-5">
          <div className="flex items-start justify-between">
            <div>
              <h2 className="text-xl font-semibold text-gray-900">
                {patient ? `${patient.last_name} ${patient.first_name}` : '…'}
              </h2>
              <p className="text-sm text-gray-500 mt-1">
                Д.р.: {patient ? fmtDate(patient.birth_date) : '—'}
                {patient && age(patient.birth_date) !== null && ` (${age(patient.birth_date)} лет)`} · Кровь:{' '}
                {patient?.blood_type} · {patient?.phone}
              </p>
              <p className="text-sm text-gray-400">{patient?.email}</p>
            </div>
            <Link
              to={`/patients/${pid}/timeline`}
              className="flex items-center gap-2 px-4 py-2 bg-blue-600 hover:bg-blue-700 text-white rounded-lg text-sm"
            >
              <Clock className="w-4 h-4" /> История (Time Travel)
            </Link>
          </div>
        </div>

        <div className="bg-amber-50 border border-amber-200 rounded-xl p-5">
          <div className="flex items-center gap-2 text-amber-800 font-medium mb-2">
            <AlertTriangle className="w-4 h-4" /> Аллергии
          </div>
          {allergies && allergies.length > 0 ? (
            <ul className="space-y-1 text-sm text-amber-900">
              {allergies.map((a) => (
                <li key={a.id}>
                  {a.allergen} — {severityLabel(a.severity)}
                </li>
              ))}
            </ul>
          ) : (
            <p className="text-sm text-amber-700">Не выявлены</p>
          )}
        </div>
      </div>

      <div className="bg-white rounded-xl border border-gray-200">
        <div className="flex gap-1 p-2 border-b border-gray-100">
          {tabs.map((t) => (
            <button
              key={t.key}
              onClick={() => setTab(t.key)}
              className={`px-4 py-2 text-sm rounded-lg ${
                tab === t.key ? 'bg-blue-50 text-blue-700 font-medium' : 'text-gray-600 hover:bg-gray-50'
              }`}
            >
              {t.label}
            </button>
          ))}
        </div>

        <div className="p-5">
          {tab === 'visits' && (
            <ul className="space-y-2">
              {(visits ?? []).map((v) => (
                <li key={v.id} className="flex items-center justify-between border-b border-gray-50 pb-2 text-sm">
                  <span>{fmtDateTime(v.visit_date)} — {v.chief_complaint || 'Визит'}</span>
                  <span className="text-gray-500">{v.doctor_name} · {statusLabel(v.status)}</span>
                </li>
              ))}
              {(visits ?? []).length === 0 && <p className="text-gray-400 text-sm">Нет визитов</p>}
            </ul>
          )}

          {tab === 'diagnoses' && (
            <div className="space-y-3">
              <div className="flex items-center justify-between gap-3">
                <h3 className="text-sm font-semibold text-gray-900">Диагнозы</h3>
                {canManageClinicalData && (
                  <button
                    type="button"
                    onClick={() => setShowDiagnosisForm((value) => !value)}
                    className="inline-flex items-center gap-2 px-3 py-1.5 bg-blue-600 hover:bg-blue-700 text-white rounded-lg text-sm"
                  >
                    {showDiagnosisForm ? <X className="w-4 h-4" /> : <Plus className="w-4 h-4" />}
                    {showDiagnosisForm ? 'Закрыть' : 'Диагноз'}
                  </button>
                )}
              </div>

              {showDiagnosisForm && (
                <form onSubmit={onDiagnosisSubmit} className="border border-blue-100 bg-blue-50/50 rounded-lg p-3">
                  <div className="grid grid-cols-1 md:grid-cols-4 gap-3">
                    <label className="text-xs text-gray-600">
                      Визит
                      <select
                        value={diagnosisForm.visit_id || String(defaultVisitId || '')}
                        onChange={(e) => setDiagnosisForm({ ...diagnosisForm, visit_id: e.target.value })}
                        className="mt-1 w-full border border-gray-300 rounded-lg px-3 py-2 text-sm bg-white"
                      >
                        {visitOptions.map((v) => (
                          <option key={v.id} value={v.id}>
                            #{v.id} · {fmtDateTime(v.visit_date)}
                          </option>
                        ))}
                      </select>
                    </label>
                    {user?.role === 'admin' && (
                      <label className="text-xs text-gray-600">
                        Врач
                        <select
                          value={diagnosisForm.doctor_id || String(doctorOptions[0]?.id || '')}
                          onChange={(e) => setDiagnosisForm({ ...diagnosisForm, doctor_id: e.target.value })}
                          className="mt-1 w-full border border-gray-300 rounded-lg px-3 py-2 text-sm bg-white"
                        >
                          {doctorOptions.map((d) => (
                            <option key={d.id} value={d.id}>
                              {d.last_name} {d.first_name}
                            </option>
                          ))}
                        </select>
                      </label>
                    )}
                    <label className="text-xs text-gray-600">
                      Код МКБ
                      <input
                        value={diagnosisForm.icd_code}
                        onChange={(e) => setDiagnosisForm({ ...diagnosisForm, icd_code: e.target.value })}
                        className="mt-1 w-full border border-gray-300 rounded-lg px-3 py-2 text-sm bg-white"
                        placeholder="J06.9"
                        required
                      />
                    </label>
                    <label className="text-xs text-gray-600">
                      Тяжесть
                      <select
                        value={diagnosisForm.severity}
                        onChange={(e) => setDiagnosisForm({ ...diagnosisForm, severity: e.target.value })}
                        className="mt-1 w-full border border-gray-300 rounded-lg px-3 py-2 text-sm bg-white"
                      >
                        <option value="mild">Лёгкая</option>
                        <option value="moderate">Средняя</option>
                        <option value="severe">Тяжёлая</option>
                      </select>
                    </label>
                  </div>
                  <label className="block text-xs text-gray-600 mt-3">
                    Описание
                    <input
                      value={diagnosisForm.description}
                      onChange={(e) => setDiagnosisForm({ ...diagnosisForm, description: e.target.value })}
                      className="mt-1 w-full border border-gray-300 rounded-lg px-3 py-2 text-sm bg-white"
                      required
                    />
                  </label>
                  {createDiagnosisMutation.error && (
                    <p className="text-xs text-red-600 mt-2">
                      {errorMessage(createDiagnosisMutation.error, 'Не удалось добавить диагноз')}
                    </p>
                  )}
                  <div className="flex justify-end mt-3">
                    <button
                      type="submit"
                      disabled={
                        createDiagnosisMutation.isPending ||
                        defaultVisitId === 0 ||
                        selectedDoctorId(diagnosisForm.doctor_id) === 0
                      }
                      className="px-4 py-2 bg-blue-600 hover:bg-blue-700 text-white rounded-lg text-sm disabled:opacity-50"
                    >
                      {createDiagnosisMutation.isPending ? 'Сохранение…' : 'Добавить'}
                    </button>
                  </div>
                </form>
              )}

              <ul className="space-y-3">
                {(diagnoses ?? []).map((d) => (
                  <li key={d.id} className="border border-gray-100 rounded-lg p-3">
                    <div className="flex items-center justify-between gap-3">
                      <div>
                        <span className="font-mono font-medium text-gray-900">{d.icd_code}</span>{' '}
                        <span className="text-gray-700">{d.description}</span>
                        <div className="text-xs text-gray-500 mt-0.5">
                          {severityLabel(d.severity)} · {fmtDate(d.diagnosed_at)} · {d.doctor_name}
                        </div>
                      </div>
                      <div className="flex items-center gap-2">
                        <button
                          onClick={() => setHistoryId(d.id)}
                          className="text-xs px-3 py-1.5 border border-gray-200 rounded-lg text-blue-600 hover:bg-blue-50"
                        >
                          История изменений
                        </button>
                        {canManageClinicalData && (
                          <button
                            type="button"
                            onClick={() => onDeleteDiagnosis(d.id)}
                            disabled={deleteDiagnosisMutation.isPending}
                            className="p-2 border border-gray-200 rounded-lg text-red-600 hover:bg-red-50 disabled:opacity-50"
                            aria-label="Снять диагноз"
                            title="Снять диагноз"
                          >
                            <Trash2 className="w-4 h-4" />
                          </button>
                        )}
                      </div>
                    </div>
                  </li>
                ))}
                {(diagnoses ?? []).length === 0 && <p className="text-gray-400 text-sm">Нет диагнозов</p>}
              </ul>
            </div>
          )}

          {tab === 'prescriptions' && (
            <div className="space-y-3">
              <div className="flex items-center justify-between gap-3">
                <h3 className="text-sm font-semibold text-gray-900">Назначения</h3>
                {canManageClinicalData && (
                  <button
                    type="button"
                    onClick={() => setShowPrescriptionForm((value) => !value)}
                    className="inline-flex items-center gap-2 px-3 py-1.5 bg-blue-600 hover:bg-blue-700 text-white rounded-lg text-sm"
                  >
                    {showPrescriptionForm ? <X className="w-4 h-4" /> : <Plus className="w-4 h-4" />}
                    {showPrescriptionForm ? 'Закрыть' : 'Препарат'}
                  </button>
                )}
              </div>

              {showPrescriptionForm && (
                <form onSubmit={onPrescriptionSubmit} className="border border-blue-100 bg-blue-50/50 rounded-lg p-3">
                  <div className="grid grid-cols-1 md:grid-cols-4 gap-3">
                    <label className="text-xs text-gray-600">
                      Визит
                      <select
                        value={prescriptionForm.visit_id || String(defaultVisitId || '')}
                        onChange={(e) => setPrescriptionForm({ ...prescriptionForm, visit_id: e.target.value })}
                        className="mt-1 w-full border border-gray-300 rounded-lg px-3 py-2 text-sm bg-white"
                      >
                        {visitOptions.map((v) => (
                          <option key={v.id} value={v.id}>
                            #{v.id} · {fmtDateTime(v.visit_date)}
                          </option>
                        ))}
                      </select>
                    </label>
                    {user?.role === 'admin' && (
                      <label className="text-xs text-gray-600">
                        Врач
                        <select
                          value={prescriptionForm.doctor_id || String(doctorOptions[0]?.id || '')}
                          onChange={(e) => setPrescriptionForm({ ...prescriptionForm, doctor_id: e.target.value })}
                          className="mt-1 w-full border border-gray-300 rounded-lg px-3 py-2 text-sm bg-white"
                        >
                          {doctorOptions.map((d) => (
                            <option key={d.id} value={d.id}>
                              {d.last_name} {d.first_name}
                            </option>
                          ))}
                        </select>
                      </label>
                    )}
                    <label className="text-xs text-gray-600">
                      Препарат
                      <input
                        value={prescriptionForm.drug_name}
                        onChange={(e) => setPrescriptionForm({ ...prescriptionForm, drug_name: e.target.value })}
                        className="mt-1 w-full border border-gray-300 rounded-lg px-3 py-2 text-sm bg-white"
                        required
                      />
                    </label>
                    <label className="text-xs text-gray-600">
                      Дозировка
                      <input
                        value={prescriptionForm.dosage}
                        onChange={(e) => setPrescriptionForm({ ...prescriptionForm, dosage: e.target.value })}
                        className="mt-1 w-full border border-gray-300 rounded-lg px-3 py-2 text-sm bg-white"
                        required
                      />
                    </label>
                  </div>
                  <div className="grid grid-cols-1 md:grid-cols-3 gap-3 mt-3">
                    <label className="text-xs text-gray-600">
                      Частота
                      <input
                        value={prescriptionForm.frequency}
                        onChange={(e) => setPrescriptionForm({ ...prescriptionForm, frequency: e.target.value })}
                        className="mt-1 w-full border border-gray-300 rounded-lg px-3 py-2 text-sm bg-white"
                      />
                    </label>
                    <label className="text-xs text-gray-600">
                      Длительность
                      <input
                        value={prescriptionForm.duration}
                        onChange={(e) => setPrescriptionForm({ ...prescriptionForm, duration: e.target.value })}
                        className="mt-1 w-full border border-gray-300 rounded-lg px-3 py-2 text-sm bg-white"
                      />
                    </label>
                    <label className="text-xs text-gray-600">
                      Инструкции
                      <input
                        value={prescriptionForm.instructions}
                        onChange={(e) => setPrescriptionForm({ ...prescriptionForm, instructions: e.target.value })}
                        className="mt-1 w-full border border-gray-300 rounded-lg px-3 py-2 text-sm bg-white"
                      />
                    </label>
                  </div>
                  {createPrescriptionMutation.error && (
                    <p className="text-xs text-red-600 mt-2">
                      {errorMessage(createPrescriptionMutation.error, 'Не удалось добавить препарат')}
                    </p>
                  )}
                  <div className="flex justify-end mt-3">
                    <button
                      type="submit"
                      disabled={
                        createPrescriptionMutation.isPending ||
                        defaultVisitId === 0 ||
                        selectedDoctorId(prescriptionForm.doctor_id) === 0
                      }
                      className="px-4 py-2 bg-blue-600 hover:bg-blue-700 text-white rounded-lg text-sm disabled:opacity-50"
                    >
                      {createPrescriptionMutation.isPending ? 'Сохранение…' : 'Добавить'}
                    </button>
                  </div>
                </form>
              )}

              <ul className="space-y-2 text-sm">
                {(prescriptions ?? []).map((p) => (
                  <li key={p.id} className="flex items-center justify-between gap-3 border-b border-gray-50 pb-2">
                    <span>
                      <strong>{p.drug_name}</strong> {p.dosage} — {p.frequency} — {p.duration}
                      <span className="text-gray-400"> · {fmtDate(p.prescribed_at)}</span>
                    </span>
                    {canManageClinicalData && (
                      <button
                        type="button"
                        onClick={() => onDeletePrescription(p.id)}
                        disabled={deletePrescriptionMutation.isPending}
                        className="p-2 border border-gray-200 rounded-lg text-red-600 hover:bg-red-50 disabled:opacity-50"
                        aria-label="Удалить препарат"
                        title="Удалить препарат"
                      >
                        <Trash2 className="w-4 h-4" />
                      </button>
                    )}
                  </li>
                ))}
                {(prescriptions ?? []).length === 0 && <p className="text-gray-400">Нет назначений</p>}
              </ul>
            </div>
          )}

          {tab === 'labs' && (
            <ul className="space-y-2 text-sm">
              {(labs ?? []).map((l) => (
                <li key={l.id} className="flex items-center justify-between border-b border-gray-50 pb-2">
                  <span>{l.test_name}: <strong>{l.result_value}</strong> {l.unit} ({l.reference_min}–{l.reference_max})</span>
                  <span className={l.is_normal ? 'text-green-600' : 'text-red-600'}>
                    {l.is_normal ? 'норма' : 'отклонение'}
                  </span>
                </li>
              ))}
              {(labs ?? []).length === 0 && <p className="text-gray-400">Нет анализов</p>}
            </ul>
          )}

          {tab === 'allergies' && (
            <ul className="space-y-2 text-sm">
              {(allergies ?? []).map((a) => (
                <li key={a.id} className="border-b border-gray-50 pb-2">
                  <strong>{a.allergen}</strong> — {severityLabel(a.severity)} · {a.reaction}
                </li>
              ))}
              {(allergies ?? []).length === 0 && <p className="text-gray-400">Аллергии не выявлены</p>}
            </ul>
          )}
        </div>
      </div>

      {historyId !== null && <DiagnosisHistory diagnosisId={historyId} onClose={() => setHistoryId(null)} />}
    </div>
  );
}
