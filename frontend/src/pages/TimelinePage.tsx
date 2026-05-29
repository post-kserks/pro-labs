import { useMemo, useState } from 'react';
import { Link, useParams } from 'react-router-dom';
import { useQuery } from '@tanstack/react-query';
import { ArrowLeft, Clock } from 'lucide-react';
import { getPatient, getPatientSnapshot, getPatientTimeline } from '../api/client';
import type { Diagnosis } from '../api/types';
import { fmtDateTime, severityLabel } from '../lib/format';

function DiagnosesColumn({
  title,
  diagnoses,
  highlight,
}: {
  title: string;
  diagnoses: Diagnosis[];
  highlight?: boolean;
}) {
  return (
    <div className={`rounded-xl border p-4 ${highlight ? 'border-blue-200 bg-blue-50' : 'border-gray-200 bg-white'}`}>
      <h4 className="text-sm font-semibold text-gray-700 mb-3">{title}</h4>
      {diagnoses.length === 0 ? (
        <p className="text-sm text-gray-400">Нет диагнозов на эту дату</p>
      ) : (
        <ul className="space-y-2">
          {diagnoses.map((d) => (
            <li key={`${d.id}-${d.icd_code}-${d.severity}`} className="text-sm">
              <span className="font-mono font-medium text-gray-900">{d.icd_code}</span>{' '}
              <span className="text-gray-700">{d.description}</span>
              <div className="text-xs text-gray-500">{severityLabel(d.severity)}</div>
            </li>
          ))}
        </ul>
      )}
    </div>
  );
}

export function TimelinePage() {
  const { id } = useParams();
  const pid = Number(id);
  const [pos, setPos] = useState(0);

  const { data: patient } = useQuery({ queryKey: ['patient', pid], queryFn: () => getPatient(pid) });
  const { data: timeline } = useQuery({ queryKey: ['timeline', pid], queryFn: () => getPatientTimeline(pid) });

  const markers = timeline?.markers ?? [];
  const maxPos = Math.max(0, markers.length - 1);
  const safePos = Math.min(pos, maxPos);
  const selected = markers[safePos];
  const current = markers[markers.length - 1];

  const { data: snapshot } = useQuery({
    queryKey: ['snapshot', pid, selected?.at],
    queryFn: () => getPatientSnapshot(pid, selected!.at),
    enabled: !!selected,
  });

  const { data: currentSnap } = useQuery({
    queryKey: ['snapshot', pid, current?.at],
    queryFn: () => getPatientSnapshot(pid, current!.at),
    enabled: !!current,
  });

  const selectedDiag = useMemo(() => snapshot?.diagnoses ?? [], [snapshot]);
  const currentDiag = useMemo(() => currentSnap?.diagnoses ?? [], [currentSnap]);

  return (
    <div className="space-y-5">
      <Link to={`/patients/${pid}`} className="inline-flex items-center gap-2 text-sm text-gray-500 hover:text-gray-700">
        <ArrowLeft className="w-4 h-4" /> К карте пациента
      </Link>

      <h2 className="text-xl font-semibold text-gray-900">
        История: {patient ? `${patient.last_name} ${patient.first_name}` : '…'}
      </h2>

      <div className="bg-blue-50 border border-blue-200 rounded-xl p-5">
        <div className="flex items-center gap-2 text-blue-900 font-semibold">
          <Clock className="w-5 h-5" /> Машина времени
        </div>
        <p className="text-sm text-blue-700 mt-1">
          Просматриваете карту на: <strong>{selected ? fmtDateTime(selected.at) : '—'}</strong>
          {selected && <span className="text-blue-500"> — {selected.label}</span>}
        </p>

        {markers.length > 0 && (
          <div className="mt-4">
            <input
              type="range"
              min={0}
              max={maxPos}
              step={1}
              value={safePos}
              onChange={(e) => setPos(Number(e.target.value))}
              className="w-full accent-blue-600"
            />
            <div className="flex justify-between mt-2">
              {markers.map((m, i) => (
                <button
                  key={m.at}
                  onClick={() => setPos(i)}
                  className={`text-xs ${i === safePos ? 'font-bold text-blue-700' : 'text-gray-500'}`}
                >
                  {m.label}
                </button>
              ))}
            </div>
          </div>
        )}

        {snapshot?.sql && (
          <pre className="mt-4 bg-gray-900 text-green-400 rounded-lg p-3 text-xs overflow-auto font-mono">
            {snapshot.sql}
          </pre>
        )}
      </div>

      <div className="grid grid-cols-1 md:grid-cols-2 gap-4">
        <DiagnosesColumn title={`Диагнозы — ${selected?.label ?? ''}`} diagnoses={selectedDiag} />
        <DiagnosesColumn title="Сейчас (актуальное)" diagnoses={currentDiag} highlight />
      </div>

      {selected && current && selected.at !== current.at && (
        <div className="bg-white border border-gray-200 rounded-xl p-4 text-sm text-gray-600">
          ↑ Перетащите слайдер: диагноз меняется, потому что каждая позиция — это реальный запрос{' '}
          <code>AS OF TIMESTAMP</code> к версионированному хранилищу VaultDB.
        </div>
      )}
    </div>
  );
}
