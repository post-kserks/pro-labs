import { useQuery } from '@tanstack/react-query';
import { CheckCircle, Clock, X } from 'lucide-react';
import { getDiagnosisHistory } from '../api/client';
import type { DiagnosisVersion } from '../api/types';
import { fmtDateTime, severityLabel } from '../lib/format';

function Field({ label, value, changed }: { label: string; value: string; changed?: boolean }) {
  return (
    <div className="flex gap-2 text-sm">
      <span className="text-gray-500 w-24 shrink-0">{label}:</span>
      <span className={changed ? 'text-amber-700 font-medium' : 'text-gray-900'}>
        {value}
        {changed && <span className="ml-1 text-xs text-amber-500">← изменено</span>}
      </span>
    </div>
  );
}

export function DiagnosisHistory({ diagnosisId, onClose }: { diagnosisId: number; onClose: () => void }) {
  const { data, isLoading } = useQuery({
    queryKey: ['diagnosis-history', diagnosisId],
    queryFn: () => getDiagnosisHistory(diagnosisId),
  });

  const versions: DiagnosisVersion[] = data?.versions ?? [];

  return (
    <div className="fixed inset-0 bg-black/50 flex items-center justify-center z-50 p-4" onClick={onClose}>
      <div className="bg-white rounded-2xl shadow-2xl w-full max-w-lg" onClick={(e) => e.stopPropagation()}>
        <div className="flex items-center justify-between p-5 border-b border-gray-100">
          <div className="flex items-center gap-2">
            <Clock className="w-5 h-5 text-blue-500" />
            <h2 className="text-lg font-semibold">История диагноза (MVCC)</h2>
          </div>
          <button onClick={onClose} className="text-gray-400 hover:text-gray-600">
            <X className="w-5 h-5" />
          </button>
        </div>

        <div className="p-5 max-h-[70vh] overflow-y-auto">
          {isLoading ? (
            <p className="text-center text-gray-400">Загрузка из VaultDB…</p>
          ) : versions.length === 0 ? (
            <p className="text-center text-gray-400">Нет версий</p>
          ) : (
            <div className="space-y-4">
              {versions.map((v, idx) => {
                const prev = versions[idx + 1]; // older version
                return (
                  <div
                    key={v.created_tx}
                    className={`border rounded-lg p-4 ${
                      v.is_current ? 'border-blue-200 bg-blue-50' : 'border-gray-200'
                    }`}
                  >
                    <div className="flex items-center justify-between mb-2">
                      <div className="flex items-center gap-2">
                        <span className="text-sm font-medium text-gray-700">Версия {versions.length - idx}</span>
                        {v.is_current && (
                          <span className="flex items-center gap-1 text-xs text-blue-600 bg-blue-100 px-2 py-0.5 rounded-full">
                            <CheckCircle className="w-3 h-3" /> Текущая
                          </span>
                        )}
                      </div>
                      <span className="text-xs text-gray-400">tx #{v.created_tx}</span>
                    </div>
                    <div className="space-y-1">
                      <Field label="Код МКБ" value={v.icd_code} changed={!!prev && prev.icd_code !== v.icd_code} />
                      <Field label="Описание" value={v.description} changed={!!prev && prev.description !== v.description} />
                      <Field label="Тяжесть" value={severityLabel(v.severity)} changed={!!prev && prev.severity !== v.severity} />
                    </div>
                  </div>
                );
              })}
            </div>
          )}

          <div className="mt-4 p-3 bg-gray-900 text-green-400 rounded-lg text-xs font-mono">
            {data?.sql ?? `HISTORY diagnoses KEY ${diagnosisId}`}
          </div>
        </div>
      </div>
    </div>
  );
}
