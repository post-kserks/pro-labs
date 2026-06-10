import { useCallback, useEffect, useState } from "react";
import { ApiError, listDatabases, listTables, TableInfo } from "../api/vaultdb";

export interface DatabasesState {
  databases: string[];
  tables: Record<string, TableInfo[]>;
  error: string | null;
  unauthorized: boolean;
  refresh: () => void;
  loadTables: (db: string) => Promise<void>;
}

// useDatabases загружает список БД и (лениво) таблицы каждой БД.
export function useDatabases(token: string): DatabasesState {
  const [databases, setDatabases] = useState<string[]>([]);
  const [tables, setTables] = useState<Record<string, TableInfo[]>>({});
  const [error, setError] = useState<string | null>(null);
  const [unauthorized, setUnauthorized] = useState(false);

  const refresh = useCallback(() => {
    listDatabases(token)
      .then((dbs) => {
        setDatabases(dbs);
        setError(null);
        setUnauthorized(false);
      })
      .catch((e) => {
        const err = e as ApiError;
        setError(err.message);
        setUnauthorized(err.httpStatus === 401);
      });
  }, [token]);

  useEffect(() => {
    refresh();
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
