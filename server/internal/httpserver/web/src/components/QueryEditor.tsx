import { useEffect, useRef, useState } from "react";

const HISTORY_KEY = "vaultdb_query_history";
const HISTORY_LIMIT = 20;

function loadHistory(): string[] {
  try {
    return JSON.parse(localStorage.getItem(HISTORY_KEY) || "[]") as string[];
  } catch {
    return [];
  }
}

export function QueryEditor({
  onExecute,
  isRunning,
  externalQuery,
  onExternalQueryConsumed,
}: {
  onExecute: (sql: string) => void;
  isRunning: boolean;
  externalQuery: string | null;
  onExternalQueryConsumed: () => void;
}) {
  const [query, setQuery] = useState("SHOW DATABASES;");
  const [history, setHistory] = useState<string[]>(loadHistory);
  const textareaRef = useRef<HTMLTextAreaElement>(null);

  // Запрос, выбранный в дереве БД, подставляется в редактор
  useEffect(() => {
    if (externalQuery !== null) {
      setQuery(externalQuery);
      onExternalQueryConsumed();
      textareaRef.current?.focus();
    }
  }, [externalQuery, onExternalQueryConsumed]);

  const run = () => {
    const sql = query.trim();
    if (!sql || isRunning) return;
    const newHistory = [sql, ...history.filter((h) => h !== sql)].slice(0, HISTORY_LIMIT);
    setHistory(newHistory);
    localStorage.setItem(HISTORY_KEY, JSON.stringify(newHistory));
    onExecute(sql);
  };

  return (
    <div className="editor">
      <textarea
        ref={textareaRef}
        className="editor-input"
        value={query}
        spellCheck={false}
        onChange={(e) => setQuery(e.target.value)}
        onKeyDown={(e) => {
          if ((e.ctrlKey || e.metaKey) && e.key === "Enter") {
            e.preventDefault();
            run();
          }
          if (e.key === "F5") {
            e.preventDefault();
            run();
          }
        }}
        placeholder="SELECT * FROM users WHERE age > 18;"
        aria-label="SQL query editor"
      />
      <div className="editor-toolbar">
        <button className="btn btn-primary" onClick={run} disabled={isRunning || !query.trim()}>
          {isRunning ? "⟳ Running…" : "▶ Run (Ctrl+Enter)"}
        </button>
        <button className="btn btn-ghost" onClick={() => setQuery("")}>
          Clear
        </button>
        {history.length > 0 && (
          <select
            className="history-select"
            value=""
            onChange={(e) => {
              if (e.target.value) setQuery(e.target.value);
            }}
          >
            <option value="">History…</option>
            {history.map((h, i) => (
              <option key={i} value={h}>
                {h.length > 80 ? h.slice(0, 77) + "…" : h}
              </option>
            ))}
          </select>
        )}
      </div>
    </div>
  );
}
