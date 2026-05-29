import { useState } from 'react';
import { Link, useParams } from 'react-router-dom';
import { useQuery } from '@tanstack/react-query';
import { AlertTriangle, Clock } from 'lucide-react';
import {
  getPatient,
  getPatientAllergies,
  getPatientDiagnoses,
  getPatientLabResults,
  getPatientPrescriptions,
  getPatientVisits,
} from '../api/client';
import { DiagnosisHistory } from '../components/DiagnosisHistory';
import { age, fmtDate, fmtDateTime, severityLabel, statusLabel } from '../lib/format';

type Tab = 'visits' | 'diagnoses' | 'prescriptions' | 'labs' | 'allergies';

export function PatientDetailPage() {
  const { id } = useParams();
  const pid = Number(id);
  const [tab, setTab] = useState<Tab>('diagnoses');
  const [historyId, setHistoryId] = useState<number | null>(null);

  const { data: patient } = useQuery({ queryKey: ['patient', pid], queryFn: () => getPatient(pid) });
  const { data: visits } = useQuery({ queryKey: ['patient-visits', pid], queryFn: () => getPatientVisits(pid) });
  const { data: diagnoses } = useQuery({ queryKey: ['patient-diagnoses', pid], queryFn: () => getPatientDiagnoses(pid) });
  const { data: prescriptions } = useQuery({ queryKey: ['patient-prescriptions', pid], queryFn: () => getPatientPrescriptions(pid) });
  const { data: labs } = useQuery({ queryKey: ['patient-labs', pid], queryFn: () => getPatientLabResults(pid) });
  const { data: allergies } = useQuery({ queryKey: ['patient-allergies', pid], queryFn: () => getPatientAllergies(pid) });

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
            <ul className="space-y-3">
              {(diagnoses ?? []).map((d) => (
                <li key={d.id} className="border border-gray-100 rounded-lg p-3">
                  <div className="flex items-center justify-between">
                    <div>
                      <span className="font-mono font-medium text-gray-900">{d.icd_code}</span>{' '}
                      <span className="text-gray-700">{d.description}</span>
                      <div className="text-xs text-gray-500 mt-0.5">
                        {severityLabel(d.severity)} · {fmtDate(d.diagnosed_at)} · {d.doctor_name}
                        {!d.is_active && <span className="ml-2 text-red-500">снят</span>}
                      </div>
                    </div>
                    <button
                      onClick={() => setHistoryId(d.id)}
                      className="text-xs px-3 py-1.5 border border-gray-200 rounded-lg text-blue-600 hover:bg-blue-50"
                    >
                      История изменений
                    </button>
                  </div>
                </li>
              ))}
              {(diagnoses ?? []).length === 0 && <p className="text-gray-400 text-sm">Нет диагнозов</p>}
            </ul>
          )}

          {tab === 'prescriptions' && (
            <ul className="space-y-2 text-sm">
              {(prescriptions ?? []).map((p) => (
                <li key={p.id} className="border-b border-gray-50 pb-2">
                  <strong>{p.drug_name}</strong> {p.dosage} — {p.frequency} — {p.duration}
                  <span className="text-gray-400"> · {fmtDate(p.prescribed_at)}</span>
                </li>
              ))}
              {(prescriptions ?? []).length === 0 && <p className="text-gray-400">Нет назначений</p>}
            </ul>
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
