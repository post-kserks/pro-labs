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

export interface TableInfo {
  name: string;
  row_count: number;
  created_at?: string;
}

export interface ColumnInfo {
  name: string;
  type: string;
  varchar_len?: number;
}

export interface TableSchemaInfo {
  database: string;
  table: string;
  columns: ColumnInfo[];
  row_count: number;
}

export class ApiError extends Error {
  constructor(
    message: string,
    public httpStatus: number,
    public errorCode?: number,
  ) {
    super(message);
  }
}

function authHeaders(token: string): Record<string, string> {
  const headers: Record<string, string> = { "Content-Type": "application/json" };
  if (token) headers["Authorization"] = `Bearer ${token}`;
  return headers;
}

async function parseResponse<T>(resp: Response): Promise<T> {
  let data: unknown;
  try {
    data = await resp.json();
  } catch {
    throw new ApiError(`HTTP ${resp.status}`, resp.status);
  }
  if (!resp.ok) {
    const err = data as { message?: string; error_code?: number };
    throw new ApiError(err.message || `HTTP ${resp.status}`, resp.status, err.error_code);
  }
  return data as T;
}

export async function runQuery(token: string, database: string, sql: string): Promise<QueryResult> {
  const resp = await fetch("/api/query", {
    method: "POST",
    headers: authHeaders(token),
    body: JSON.stringify({ database, query: sql }),
  });
  return parseResponse<QueryResult>(resp);
}

export async function listDatabases(token: string): Promise<string[]> {
  const resp = await fetch("/api/databases", { headers: authHeaders(token) });
  const data = await parseResponse<{ databases: { name: string }[] }>(resp);
  return data.databases.map((d) => d.name);
}

export async function listTables(token: string, db: string): Promise<TableInfo[]> {
  const resp = await fetch(`/api/databases/${encodeURIComponent(db)}/tables`, {
    headers: authHeaders(token),
  });
  const data = await parseResponse<{ tables: TableInfo[] }>(resp);
  return data.tables;
}

export async function getTableSchema(token: string, db: string, table: string): Promise<TableSchemaInfo> {
  const resp = await fetch(
    `/api/databases/${encodeURIComponent(db)}/tables/${encodeURIComponent(table)}/schema`,
    { headers: authHeaders(token) },
  );
  return parseResponse<TableSchemaInfo>(resp);
}

export interface HealthInfo {
  status: string;
  version?: string;
}

export async function getHealth(): Promise<HealthInfo> {
  const resp = await fetch("/health");
  return parseResponse<HealthInfo>(resp);
}
