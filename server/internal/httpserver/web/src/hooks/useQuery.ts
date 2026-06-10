import { useCallback, useState } from "react";
import { ApiError, QueryResult, runQuery } from "../api/vaultdb";

export interface QueryState {
  result: QueryResult | null;
  isRunning: boolean;
  execute: (sql: string, database: string) => Promise<void>;
}

// useQuery выполняет SQL через POST /api/query и хранит последний результат.
// Ошибки API превращаются в result со status="error", чтобы ResultTable
// показывал их единообразно.
export function useQuery(token: string): QueryState {
  const [result, setResult] = useState<QueryResult | null>(null);
  const [isRunning, setIsRunning] = useState(false);

  const execute = useCallback(
    async (sql: string, database: string) => {
      setIsRunning(true);
      try {
        setResult(await runQuery(token, database, sql));
      } catch (e) {
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
