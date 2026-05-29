import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query';
import { RefreshCw, Trash2 } from 'lucide-react';
import { getVacuumStats, runVacuum } from '../../api/client';

export function VacuumPanel() {
  const queryClient = useQueryClient();
  const { data: stats } = useQuery({ queryKey: ['vacuum-stats'], queryFn: getVacuumStats });

  const { mutate, data: result, isPending } = useMutation({
    mutationFn: runVacuum,
    onSuccess: () => queryClient.invalidateQueries({ queryKey: ['vacuum-stats'] }),
  });

  return (
    <div className="space-y-5">
      <div>
        <h3 className="text-lg font-semibold">VACUUM</h3>
        <p className="text-sm text-gray-500">
          Очистка устаревших версий строк. VaultDB хранит историю всех изменений (MVCC); VACUUM удаляет версии,
          которые больше не нужны.
        </p>
      </div>

      {stats && (
        <div className="border border-gray-200 rounded-lg overflow-hidden">
          <table className="w-full text-sm">
            <thead className="bg-gray-50 text-gray-500">
              <tr>
                <th className="text-left px-4 py-2 font-medium">Таблица</th>
                <th className="text-right px-4 py-2 font-medium">Активных строк</th>
              </tr>
            </thead>
            <tbody>
              {stats.tables.map((t) => (
                <tr key={t.table} className="border-t border-gray-100">
                  <td className="px-4 py-2 font-mono text-gray-900">{t.table}</td>
                  <td className="px-4 py-2 text-right text-gray-700">{t.live_rows.toLocaleString('ru')}</td>
                </tr>
              ))}
            </tbody>
          </table>
        </div>
      )}

      <button
        onClick={() => mutate()}
        disabled={isPending}
        className="flex items-center gap-2 px-6 py-2.5 bg-red-600 hover:bg-red-700 text-white rounded-lg disabled:opacity-50"
      >
        {isPending ? (
          <><RefreshCw className="w-4 h-4 animate-spin" /> Выполняется…</>
        ) : (
          <><Trash2 className="w-4 h-4" /> Запустить VACUUM</>
        )}
      </button>

      {result && (
        <div className="space-y-3">
          <div className="grid grid-cols-2 gap-4">
            <div className="bg-green-50 rounded-lg p-4">
              <p className="text-sm text-green-700">Освобождено версий</p>
              <p className="text-2xl font-bold text-green-900">{result.total_reclaimed.toLocaleString('ru')}</p>
            </div>
            <div className="bg-blue-50 rounded-lg p-4">
              <p className="text-sm text-blue-700">Освобождено места</p>
              <p className="text-2xl font-bold text-blue-900">{(result.total_saved_kb / 1024).toFixed(2)} MB</p>
            </div>
          </div>
          <div className="border border-gray-200 rounded-lg overflow-hidden">
            <table className="w-full text-sm">
              <thead className="bg-gray-50 text-gray-500">
                <tr>
                  <th className="text-left px-4 py-2 font-medium">Таблица</th>
                  <th className="text-right px-4 py-2 font-medium">Было</th>
                  <th className="text-right px-4 py-2 font-medium">Стало</th>
                  <th className="text-right px-4 py-2 font-medium">Удалено</th>
                  <th className="text-right px-4 py-2 font-medium">Время</th>
                </tr>
              </thead>
              <tbody>
                {result.tables.map((t) => (
                  <tr key={t.table} className="border-t border-gray-100">
                    <td className="px-4 py-2 font-mono text-gray-900">{t.table}</td>
                    <td className="px-4 py-2 text-right">{t.rows_before}</td>
                    <td className="px-4 py-2 text-right">{t.rows_after}</td>
                    <td className="px-4 py-2 text-right text-amber-600">{t.reclaimed}</td>
                    <td className="px-4 py-2 text-right text-gray-500">{t.duration_ms} ms</td>
                  </tr>
                ))}
              </tbody>
            </table>
          </div>
        </div>
      )}
    </div>
  );
}
