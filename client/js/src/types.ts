export interface QueryResult {
  status: string;
  type: string;
  columns: string[];
  rows: any[][];
  affected: number;
  message?: string;
  as_of_note?: string;
  duration_ms?: number;
}

export interface HandshakeResult {
  protocol_version: string;
  server: string;
  server_version: string;
  supported_features: string[];
}

export interface ClientOptions {
  host?: string;
  port?: number;
  token?: string;
}
