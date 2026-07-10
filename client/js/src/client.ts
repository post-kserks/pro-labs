import net from 'net';
import { randomUUID } from 'crypto';
import readline from 'readline';
import type { QueryResult, HandshakeResult } from './types.js';

export class Client {
  private socket: net.Socket | null = null;
  private rl: readline.Interface | null = null;
  private _host: string;
  private _port: number;
  private _token: string;
  private _protocolVersion = '';
  private _serverVersion = '';
  private _features: string[] = [];
  private _connected = false;

  constructor(host = 'localhost', port = 5432, token = '') {
    this._host = host;
    this._port = port;
    this._token = token;
  }

  get host(): string { return this._host; }
  get port(): number { return this._port; }
  get protocolVersion(): string { return this._protocolVersion; }
  get serverVersion(): string { return this._serverVersion; }
  get features(): string[] { return this._features; }
  get connected(): boolean { return this._connected; }

  async connect(): Promise<HandshakeResult> {
    return new Promise((resolve, reject) => {
      this.socket = net.createConnection(
        { host: this._host, port: this._port },
        () => {
          this.rl = readline.createInterface({ input: this.socket! });
          this._connected = true;
          this.handshake().then(resolve).catch(reject);
        },
      );
      this.socket.on('error', (err) => {
        this._connected = false;
        reject(err);
      });
    });
  }

  private async handshake(): Promise<HandshakeResult> {
    const req = {
      type: 'handshake',
      client_version: '2.0',
      client_name: 'vaultdb-js-client',
      supported_features: ['time_travel', 'transactions', 'prepared_statements'],
    };
    await this.send(req);
    const resp = await this.recv();
    if (resp.type !== 'handshake') {
      throw new Error(`expected handshake response, got ${resp.type}`);
    }
    this._protocolVersion = resp.protocol_version || '';
    this._serverVersion = resp.server_version || '';
    this._features = resp.supported_features || resp.features || [];
    return resp;
  }

  async query(sql: string, params?: any[], database?: string): Promise<QueryResult> {
    if (!this._connected || !this.socket) {
      throw new Error('client is not connected');
    }
    const req: Record<string, any> = {
      id: randomUUID(),
      token: this._token,
      query: sql,
    };
    if (database) req.database = database;
    if (params) req.params = params.map((p) => this.convertParam(p));
    await this.send(req);
    const resp = await this.recv();
    if (resp.status === 'error') {
      throw new Error(resp.message || 'query error');
    }
    return resp;
  }

  async begin(): Promise<QueryResult> {
    return this.query('BEGIN;');
  }

  async commit(): Promise<QueryResult> {
    return this.query('COMMIT;');
  }

  async rollback(): Promise<QueryResult> {
    return this.query('ROLLBACK;');
  }

  async close(): Promise<void> {
    this._connected = false;
    this.rl?.close();
    this.socket?.destroy();
    this.socket = null;
    this.rl = null;
  }

  async [Symbol.asyncDispose](): Promise<void> {
    await this.close();
  }

  private convertParam(val: any): any {
    if (typeof val === 'number' || typeof val === 'boolean') return val;
    if (typeof val === 'string') {
      if (val.toLowerCase() === 'true') return true;
      if (val.toLowerCase() === 'false') return false;
      const n = Number(val);
      if (!isNaN(n) && val !== '') return n;
    }
    return String(val);
  }

  private send(msg: any): Promise<void> {
    return new Promise((resolve, reject) => {
      const data = JSON.stringify(msg) + '\n';
      this.socket!.write(data, (err) => (err ? reject(err) : resolve()));
    });
  }

  private recv(): Promise<any> {
    return new Promise((resolve, reject) => {
      const onLine = (line: string) => {
        cleanup();
        try {
          resolve(JSON.parse(line));
        } catch (e) {
          reject(e);
        }
      };
      const onClose = () => {
        cleanup();
        reject(new Error('connection closed'));
      };
      const cleanup = () => {
        this.rl?.off('line', onLine);
        this.rl?.off('close', onClose);
      };
      this.rl!.once('line', onLine);
      this.rl!.once('close', onClose);
    });
  }
}
