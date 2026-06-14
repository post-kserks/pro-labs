import { useCallback, useRef, useState } from "react";
import { ApiError, QueryResult, runQuery } from "../api/vaultdb";

export interface QueryState {
  result: QueryResult | null;
  isRunning: boolean;
  execute: (sql: string, database: string) => Promise<void>;
}

export function useQuery(token: string): QueryState {
  const [result, setResult] = useState<QueryResult | null>(null);
  const [isRunning, setIsRunning] = useState(false);
  const abortRef = useRef<AbortController | null>(null);

  const execute = useCallback(
    async (sql: string, database: string) => {
      abortRef.current?.abort();
      const controller = new AbortController();
      abortRef.current = controller;
      setIsRunning(true);
      try {
        setResult(await runQuery(token, database, sql, controller.signal));
      } catch (e) {
        if ((e as Error).name === "AbortError") return;
        const err = e as ApiError;
        setResult({
          status: "error",
          type: "error",
          columns: [],
          rows: [],
          affected: 0,
          duration_ms: 0,
          error_code: err.errorCode,
          message: err.message,
        });
      } finally {
        setIsRunning(false);
      }
    },
    [token],
  );

  return { result, isRunning, execute };
}
