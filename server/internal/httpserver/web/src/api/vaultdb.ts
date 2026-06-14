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
  const headers: Record<string, string> = {};
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

async function apiFetch<T>(
  token: string,
  url: string,
  options: RequestInit = {},
  signal?: AbortSignal,
): Promise<T> {
  const resp = await fetch(url, {
    ...options,
    headers: { ...authHeaders(token), ...(options.headers as Record<string, string> || {}) },
    signal,
  });
  return parseResponse<T>(resp);
}

export async function runQuery(
  token: string,
  database: string,
  sql: string,
  signal?: AbortSignal,
): Promise<QueryResult> {
  return apiFetch<QueryResult>(
    token,
    "/api/query",
    {
      method: "POST",
      body: JSON.stringify({ database, query: sql }),
    },
    signal,
  );
}

export async function listDatabases(token: string, signal?: AbortSignal): Promise<string[]> {
  const data = await apiFetch<{ databases: { name: string }[] }>(token, "/api/databases", {}, signal);
  return data.databases.map((d) => d.name);
}

export async function listTables(token: string, db: string, signal?: AbortSignal): Promise<TableInfo[]> {
  const data = await apiFetch<{ tables: TableInfo[] }>(
    token,
    `/api/databases/${encodeURIComponent(db)}/tables`,
    {},
    signal,
  );
  return data.tables;
}

export async function getTableSchema(
  token: string,
  db: string,
  table: string,
  signal?: AbortSignal,
): Promise<TableSchemaInfo> {
  return apiFetch<TableSchemaInfo>(
    token,
    `/api/databases/${encodeURIComponent(db)}/tables/${encodeURIComponent(table)}/schema`,
    {},
    signal,
  );
}

export interface HealthInfo {
  status: string;
  version?: string;
}

export async function getHealth(signal?: AbortSignal): Promise<HealthInfo> {
  const resp = await fetch("/health", { signal });
  return parseResponse<HealthInfo>(resp);
}
