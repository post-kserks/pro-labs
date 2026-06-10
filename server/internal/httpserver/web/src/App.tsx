import { useCallback, useState } from "react";
import { DatabaseTree } from "./components/DatabaseTree";
import { LoginScreen } from "./components/LoginScreen";
import { QueryEditor } from "./components/QueryEditor";
import { ResultTable } from "./components/ResultTable";
import { SchemaView } from "./components/SchemaView";
import { StatusBar } from "./components/StatusBar";
import { useDatabases } from "./hooks/useDatabases";
import { useQuery } from "./hooks/useQuery";

const TOKEN_KEY = "vaultdb_token";

export function App() {
  const [token, setToken] = useState(localStorage.getItem(TOKEN_KEY) || "");
  const [currentDB, setCurrentDB] = useState("");
  const [pendingQuery, setPendingQuery] = useState<string | null>(null);
  const [schemaTarget, setSchemaTarget] = useState<{ db: string; table: string } | null>(null);

  const dbs = useDatabases(token);
  const query = useQuery(token);

  const handleLogin = useCallback(
    (newToken: string) => {
      localStorage.setItem(TOKEN_KEY, newToken);
      setToken(newToken);
    },
    [setToken],
  );

  const handleLogout = useCallback(() => {
    localStorage.removeItem(TOKEN_KEY);
    setToken("");
  }, [setToken]);

  if (dbs.unauthorized) {
    return <LoginScreen onLogin={handleLogin} hadToken={token !== ""} />;
  }

  const handlePickTable = (db: string, table: string) => {
    setCurrentDB(db);
    setSchemaTarget({ db, table });
    setPendingQuery(`SELECT * FROM ${table} LIMIT 100;`);
  };

  return (
    <div className="app">
      <aside className="sidebar">
        <div className="logo">
          Vault<span className="logo-accent">DB</span>
        </div>
        <DatabaseTree
          databases={dbs.databases}
          tables={dbs.tables}
          currentDB={currentDB}
          onSelectDB={setCurrentDB}
          onExpandDB={dbs.loadTables}
          onPickTable={handlePickTable}
          onRefresh={dbs.refresh}
        />
        {dbs.error && !dbs.unauthorized && <p className="sidebar-error">{dbs.error}</p>}
        {token && (
          <button className="btn btn-ghost logout" onClick={handleLogout}>
            Sign out
          </button>
        )}
      </aside>

      <main className="main">
        <StatusBar currentDB={currentDB} lastResult={query.result} />
        <QueryEditor
          isRunning={query.isRunning}
          externalQuery={pendingQuery}
          onExternalQueryConsumed={() => setPendingQuery(null)}
          onExecute={(sql) => {
            void query.execute(sql, currentDB);
            dbs.refresh();
          }}
        />
        <div className="results">
          <ResultTable result={query.result} />
        </div>
        {schemaTarget && (
          <SchemaView
            token={token}
            db={schemaTarget.db}
            table={schemaTarget.table}
            onClose={() => setSchemaTarget(null)}
          />
        )}
      </main>
    </div>
  );
}
