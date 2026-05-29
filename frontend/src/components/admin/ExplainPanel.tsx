import { useState } from 'react';
import { useMutation } from '@tanstack/react-query';
import { Play, Zap } from 'lucide-react';
import { explainQuery } from '../../api/client';

const DEMO_QUERIES = [
  'SELECT * FROM diagnoses WHERE patient_id = 1;',
  'SELECT * FROM patients WHERE id = 42;',
  'SELECT * FROM visits WHERE patient_id = 5;',
];

export function ExplainPanel() {
  const [sql, setSql] = useState(DEMO_QUERIES[0]);
  const [analyze, setAnalyze] = useState(true);

  const { mutate, data, isPending, error } = useMutation({
    mutationFn: () => explainQuery(sql, analyze),
  });

  const plan = data?.plan ?? '';
  const isIndexScan = plan.includes('Index Scan');
  const execTime = plan.match(/Execution Time:\s*([\d.]+)\s*ms/)?.[1];

  return (
    <div className="space-y-5">
      <div>
        <h3 className="text-lg font-semibold">EXPLAIN ANALYZE</h3>
        <p className="text-sm text-gray-500">Анализ плана выполнения запроса в VaultDB</p>
      </div>

      <div className="flex gap-2 flex-wrap">
        {DEMO_QUERIES.map((q) => (
          <button
            key={q}
            onClick={() => setSql(q)}
            className="text-xs px-3 py-1.5 bg-gray-100 hover:bg-gray-200 rounded-full text-gray-700"
          >
            {q.slice(0, 42)}…
          </button>
        ))}
      </div>

      <textarea
        value={sql}
        onChange={(e) => setSql(e.target.value)}
        rows={3}
        className="w-full font-mono text-sm border border-gray-300 rounded-lg p-3 focus:ring-2 focus:ring-blue-500"
      />

      <label className="flex items-center gap-2 text-sm text-gray-700 cursor-pointer">
        <input type="checkbox" checked={analyze} onChange={(e) => setAnalyze(e.target.checked)} className="w-4 h-4" />
        EXPLAIN ANALYZE (реальное выполнение + статистика)
      </label>

      <button
        onClick={() => mutate()}
        disabled={isPending || !sql.trim()}
        className="flex items-center gap-2 px-6 py-2.5 bg-blue-600 hover:bg-blue-700 text-white rounded-lg disabled:opacity-50"
      >
        <Play className="w-4 h-4" /> {isPending ? 'Выполнение…' : 'Выполнить'}
      </button>

      {error ? (
        <p className="text-sm text-red-600">{(error as any)?.response?.data?.error?.message ?? 'Ошибка запроса'}</p>
      ) : null}

      {data && (
        <div className="space-y-3">
          <div
            className={`flex items-center gap-2 p-3 rounded-lg ${
              isIndexScan ? 'bg-green-50 text-green-700' : 'bg-amber-50 text-amber-700'
            }`}
          >
            <Zap className="w-4 h-4" />
            <span className="font-medium">
              {isIndexScan ? 'Index Scan — индекс используется ✓' : 'Sequential Scan — полный перебор'}
            </span>
            {execTime && <span className="ml-auto text-sm">{execTime} ms</span>}
          </div>
          <pre className="bg-gray-900 text-green-400 rounded-lg p-4 text-xs overflow-auto whitespace-pre-wrap font-mono">
            {plan}
          </pre>
        </div>
      )}
    </div>
  );
}
