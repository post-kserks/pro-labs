import { useState } from 'react';
import { useNavigate } from 'react-router-dom';
import { keepPreviousData, useQuery } from '@tanstack/react-query';
import { AlertTriangle, Search } from 'lucide-react';
import { listPatients } from '../api/client';
import { age, fmtDate } from '../lib/format';

export function PatientsPage() {
  const navigate = useNavigate();
  const [q, setQ] = useState('');
  const [page, setPage] = useState(1);
  const pageSize = 20;

  const { data, isLoading } = useQuery({
    queryKey: ['patients', q, page],
    queryFn: () => listPatients({ q, page, page_size: pageSize }),
    placeholderData: keepPreviousData,
  });

  const total = data?.total ?? 0;
  const totalPages = Math.max(1, Math.ceil(total / pageSize));

  return (
    <div className="space-y-4">
      <div className="flex items-center gap-3">
        <div className="relative flex-1 max-w-md">
          <Search className="w-4 h-4 text-gray-400 absolute left-3 top-1/2 -translate-y-1/2" />
          <input
            value={q}
            onChange={(e) => {
              setQ(e.target.value);
              setPage(1);
            }}
            placeholder="Поиск по фамилии, телефону, email…"
            className="w-full border border-gray-300 rounded-lg pl-9 pr-3 py-2 focus:ring-2 focus:ring-blue-500 focus:border-transparent"
          />
        </div>
        <span className="text-sm text-gray-500">Найдено: {total}</span>
      </div>

      <div className="bg-white border border-gray-200 rounded-xl overflow-hidden">
        <table className="w-full text-sm">
          <thead className="bg-gray-50 text-gray-500">
            <tr>
              <th className="text-left px-4 py-3 font-medium">ФИО</th>
              <th className="text-left px-4 py-3 font-medium">Возраст</th>
              <th className="text-left px-4 py-3 font-medium">Кровь</th>
              <th className="text-left px-4 py-3 font-medium">Телефон</th>
              <th className="text-left px-4 py-3 font-medium">Посл. визит</th>
            </tr>
          </thead>
          <tbody>
            {isLoading ? (
              <tr><td colSpan={5} className="px-4 py-8 text-center text-gray-400">Загрузка из VaultDB…</td></tr>
            ) : data && data.patients.length > 0 ? (
              data.patients.map((p) => (
                <tr
                  key={p.id}
                  onClick={() => navigate(`/patients/${p.id}`)}
                  className="border-t border-gray-100 hover:bg-blue-50/40 cursor-pointer"
                >
                  <td className="px-4 py-3 font-medium text-gray-900">
                    <div className="flex items-center gap-2">
                      {p.last_name} {p.first_name}
                      {p.has_allergies && <AlertTriangle className="w-3.5 h-3.5 text-amber-500" />}
                    </div>
                  </td>
                  <td className="px-4 py-3 text-gray-600">{age(p.birth_date) ?? '—'} лет</td>
                  <td className="px-4 py-3 text-gray-600">{p.blood_type}</td>
                  <td className="px-4 py-3 text-gray-600">{p.phone}</td>
                  <td className="px-4 py-3 text-gray-600">{p.last_visit ? fmtDate(p.last_visit) : '—'}</td>
                </tr>
              ))
            ) : (
              <tr><td colSpan={5} className="px-4 py-8 text-center text-gray-400">Пациенты не найдены</td></tr>
            )}
          </tbody>
        </table>
      </div>

      <div className="flex items-center justify-between">
        <button
          disabled={page <= 1}
          onClick={() => setPage((p) => Math.max(1, p - 1))}
          className="px-3 py-1.5 text-sm border border-gray-300 rounded-lg disabled:opacity-40"
        >
          ← Пред
        </button>
        <span className="text-sm text-gray-500">Стр {page} из {totalPages}</span>
        <button
          disabled={page >= totalPages}
          onClick={() => setPage((p) => Math.min(totalPages, p + 1))}
          className="px-3 py-1.5 text-sm border border-gray-300 rounded-lg disabled:opacity-40"
        >
          След →
        </button>
      </div>
    </div>
  );
}
