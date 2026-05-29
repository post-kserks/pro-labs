import { useQuery } from '@tanstack/react-query';
import { getAdminStats } from '../api/client';
import { useAuth } from '../store/auth';
import { fmtDate } from '../lib/format';

function StatCard({ title, value, sub }: { title: string; value: number | string; sub?: string }) {
  return (
    <div className="bg-white rounded-xl border border-gray-200 p-5">
      <p className="text-xs uppercase tracking-wide text-gray-400">{title}</p>
      <p className="text-3xl font-bold text-gray-900 mt-1">{value}</p>
      {sub && <p className="text-xs text-gray-500 mt-1">{sub}</p>}
    </div>
  );
}

export function DashboardPage() {
  const { user } = useAuth();
  const { data: stats } = useQuery({ queryKey: ['admin-stats'], queryFn: getAdminStats });

  return (
    <div className="space-y-6">
      <div className="bg-white rounded-xl border border-gray-200 p-5 flex items-center justify-between">
        <div>
          <h2 className="text-lg font-semibold text-gray-900">Добрый день, {user?.name}!</h2>
          <p className="text-sm text-gray-500">{fmtDate(new Date().toISOString(), 'EEEE, d MMMM yyyy')}</p>
        </div>
        <span className="text-sm text-gray-400">{user?.role}</span>
      </div>

      <div className="grid grid-cols-1 md:grid-cols-4 gap-4">
        <StatCard title="Пациентов" value={stats?.patients_total ?? '—'} />
        <StatCard title="Визитов сегодня" value={stats?.visits_today ?? '—'} sub={`${stats?.completed_today ?? 0} завершено`} />
        <StatCard title="Активных диагнозов" value={stats?.active_diagnoses ?? '—'} />
        <StatCard title="Врачей" value={stats?.doctors_total ?? '—'} />
      </div>

      <div className="bg-blue-50 border border-blue-200 rounded-xl p-5">
        <h3 className="font-semibold text-blue-900 mb-1">🕐 Демонстрация Time Travel</h3>
        <p className="text-sm text-blue-700">
          Откройте карту пациента <strong>#1</strong> и нажмите «История» — слайдер показывает реальные
          версии диагноза (J00 → J06.9), полученные запросами <code>AS OF TIMESTAMP</code> к VaultDB.
        </p>
      </div>
    </div>
  );
}
