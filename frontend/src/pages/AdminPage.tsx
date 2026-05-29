import { useState } from 'react';
import { useQuery } from '@tanstack/react-query';
import { getAdminStats, getIndexes, getMetrics, getWalStatus } from '../api/client';
import { ExplainPanel } from '../components/admin/ExplainPanel';
import { VacuumPanel } from '../components/admin/VacuumPanel';
import { useAuth } from '../store/auth';

type Tab = 'stats' | 'explain' | 'vacuum' | 'wal' | 'metrics' | 'indexes';

function StatsTab() {
  const { data } = useQuery({ queryKey: ['admin-stats'], queryFn: getAdminStats });
  const items = [
    ['Пациентов', data?.patients_total],
    ['Врачей', data?.doctors_total],
    ['Визитов', data?.visits_total],
    ['Диагнозов (всего)', data?.diagnoses_total],
    ['Активных диагнозов', data?.active_diagnoses],
    ['Визитов сегодня', data?.visits_today],
  ] as const;
  return (
    <div className="grid grid-cols-2 md:grid-cols-3 gap-4">
      {items.map(([label, value]) => (
        <div key={label} className="bg-white border border-gray-200 rounded-xl p-4">
          <p className="text-xs uppercase tracking-wide text-gray-400">{label}</p>
          <p className="text-2xl font-bold text-gray-900 mt-1">{value ?? '—'}</p>
        </div>
      ))}
    </div>
  );
}

function WalTab() {
  const { data } = useQuery({ queryKey: ['wal'], queryFn: getWalStatus });
  return (
    <div className="space-y-4">
      <div className="flex items-center gap-2">
        <span className="w-2.5 h-2.5 rounded-full bg-green-500" />
        <span className="font-medium text-gray-900">WAL активен</span>
      </div>
      <p className="text-sm text-gray-600 max-w-2xl">{data?.summary}</p>
      <pre className="bg-gray-900 text-green-400 rounded-lg p-4 text-xs overflow-auto font-mono">
        {data?.health ?? '…'}
      </pre>
      <p className="text-xs text-gray-400">
        Демо краша: убейте контейнер БД (<code>docker kill medvault-db</code>), затем поднимите снова
        (<code>docker start medvault-db</code>) — в логах появится «WAL recovery», данные сохранятся.
      </p>
    </div>
  );
}

function MetricsTab() {
  const { data } = useQuery({ queryKey: ['metrics'], queryFn: getMetrics, refetchInterval: 5000 });
  return (
    <pre className="bg-gray-900 text-green-400 rounded-lg p-4 text-xs overflow-auto font-mono max-h-[60vh]">
      {data?.metrics ?? 'Загрузка метрик…'}
    </pre>
  );
}

function IndexesTab() {
  const { data } = useQuery({ queryKey: ['indexes'], queryFn: getIndexes });
  return (
    <ul className="space-y-1 text-sm">
      {(data?.indexes ?? []).map((i, idx) => (
        <li key={idx} className="font-mono text-gray-700">
          <span className="text-blue-600">{i.index}</span> on {i.table}
        </li>
      ))}
      {(data?.indexes ?? []).length === 0 && <p className="text-gray-400">Индексы не найдены</p>}
    </ul>
  );
}

export function AdminPage() {
  const { user } = useAuth();
  const [tab, setTab] = useState<Tab>('explain');

  if (user?.role !== 'admin') {
    return <p className="text-gray-500">Доступ только для администратора.</p>;
  }

  const tabs: { key: Tab; label: string }[] = [
    { key: 'stats', label: 'Статистика' },
    { key: 'explain', label: 'EXPLAIN' },
    { key: 'vacuum', label: 'VACUUM' },
    { key: 'wal', label: 'WAL' },
    { key: 'metrics', label: 'Метрики' },
    { key: 'indexes', label: 'Индексы' },
  ];

  return (
    <div className="space-y-5">
      <div className="flex gap-1 bg-white border border-gray-200 rounded-xl p-2">
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

      <div className="bg-white border border-gray-200 rounded-xl p-6">
        {tab === 'stats' && <StatsTab />}
        {tab === 'explain' && <ExplainPanel />}
        {tab === 'vacuum' && <VacuumPanel />}
        {tab === 'wal' && <WalTab />}
        {tab === 'metrics' && <MetricsTab />}
        {tab === 'indexes' && <IndexesTab />}
      </div>
    </div>
  );
}
