import { useCallback, useEffect, useRef, useState } from "react";
import { ApiError, listDatabases, listTables, TableInfo } from "../api/vaultdb";

export interface DatabasesState {
  databases: string[];
  tables: Record<string, TableInfo[]>;
  error: string | null;
  unauthorized: boolean;
  refresh: () => void;
  loadTables: (db: string) => Promise<void>;
}

export function useDatabases(token: string): DatabasesState {
  const [databases, setDatabases] = useState<string[]>([]);
  const [tables, setTables] = useState<Record<string, TableInfo[]>>({});
  const [error, setError] = useState<string | null>(null);
  const [unauthorized, setUnauthorized] = useState(false);
  const abortRef = useRef<AbortController | null>(null);

  const refresh = useCallback(() => {
    abortRef.current?.abort();
    const controller = new AbortController();
    abortRef.current = controller;
    listDatabases(token, controller.signal)
      .then((dbs) => {
        setDatabases(dbs);
        setError(null);
        setUnauthorized(false);
      })
      .catch((e) => {
        if ((e as Error).name === "AbortError") return;
        const err = e as ApiError;
        setError(err.message);
        setUnauthorized(err.httpStatus === 401);
      });
  }, [token]);

  useEffect(() => {
    refresh();
    return () => abortRef.current?.abort();
  }, [refresh]);

  const loadTables = useCallback(
    async (db: string) => {
      try {
        const list = await listTables(token, db);
        setTables((prev) => ({ ...prev, [db]: list }));
      } catch {
        setTables((prev) => ({ ...prev, [db]: [] }));
      }
    },
    [token],
  );

  return { databases, tables, error, unauthorized, refresh, loadTables };
}
