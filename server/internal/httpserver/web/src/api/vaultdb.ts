export interface QueryResult {
  status: string;
  type: string;
  columns: string[];
  rows: (string | number | boolean | null)[][];
  affected: number;
  duration_ms: number;
  as_of_note?: string;
  error_code?: number;
  message?: string;
}

export interface VaultDBConfig {
  baseURL: string;
  token: string;
}

export class VaultDBClient {
  constructor(private cfg: VaultDBConfig) {}

  async query(database: string, sql: string): Promise<QueryResult> {
    const resp = await fetch(`${this.cfg.baseURL}/api/query`, {
      method: "POST",
      headers: {
        "Content-Type": "application/json",
        Authorization: `Bearer ${this.cfg.token}`,
      },
      body: JSON.stringify({ database, query: sql }),
    });

    const data = (await resp.json()) as QueryResult;
    if (!resp.ok) {
      throw new Error(data.message || "Query failed");
    }
    return data;
  }
}
