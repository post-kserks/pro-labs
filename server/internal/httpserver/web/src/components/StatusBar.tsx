import { useEffect, useState } from "react";
import { getHealth, QueryResult } from "../api/vaultdb";

export function StatusBar({
  currentDB,
  lastResult,
}: {
  currentDB: string;
  lastResult: QueryResult | null;
}) {
  const [connected, setConnected] = useState(false);
  const [version, setVersion] = useState("");

  useEffect(() => {
    let stopped = false;
    const check = () => {
      getHealth()
        .then((h) => {
          if (stopped) return;
          setConnected(h.status === "ok");
          if (h.version) setVersion(h.version);
        })
        .catch(() => {
          if (!stopped) setConnected(false);
        });
    };
    check();
    const timer = setInterval(check, 10000);
    return () => {
      stopped = true;
      clearInterval(timer);
    };
  }, []);

  return (
    <header className="status-bar">
      <span className={`status-dot ${connected ? "status-on" : "status-off"}`} />
      <span>{connected ? "connected" : "disconnected"}</span>
      {version && <span className="status-version">v{version}</span>}
      <span className="status-db">{currentDB ? `db: ${currentDB}` : "no database selected"}</span>
      {lastResult && lastResult.status === "ok" && (
        <span className="status-last">last query: {lastResult.duration_ms.toFixed(1)} ms</span>
      )}
    </header>
  );
}
