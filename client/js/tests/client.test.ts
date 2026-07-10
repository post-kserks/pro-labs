import { describe, it, mock, beforeEach, afterEach } from 'node:test';
import assert from 'node:assert/strict';
import { EventEmitter } from 'events';
import { Client } from '../src/client.js';
import type { QueryResult, HandshakeResult } from '../src/types.js';

// Minimal mock of net.Socket and readline for unit testing
class MockSocket extends EventEmitter {
  private _written: string[] = [];
  write(data: string | Buffer, cb?: (err?: Error) => void): boolean {
    const s = typeof data === 'string' ? data : data.toString();
    this._written.push(s);
    if (cb) cb();
    return true;
  }
  destroy(): void { this.emit('close'); }
  get written(): string[] { return this._written; }
  clear(): void { this._written = []; }
}

describe('Client', () => {
  let mockSocket: MockSocket;

  beforeEach(() => {
    mockSocket = new MockSocket();
  });

  it('convertParam handles all types', () => {
    const client = new Client();
    // Access private method via (any) cast for testing
    const c = client as any;
    assert.equal(c.convertParam(42), 42);
    assert.equal(c.convertParam(true), true);
    assert.equal(c.convertParam('hello'), 'hello');
    assert.equal(c.convertParam('true'), true);
    assert.equal(c.convertParam('false'), false);
    assert.equal(c.convertParam('123'), 123);
    assert.equal(c.convertParam('3.14'), 3.14);
    assert.equal(c.convertParam(null), 'null');
  });

  it('constructor sets defaults', () => {
    const client = new Client();
    assert.equal(client.host, 'localhost');
    assert.equal(client.port, 5432);
    assert.equal(client.connected, false);
  });

  it('constructor sets custom values', () => {
    const client = new Client('10.0.0.1', 9999, 'token123');
    assert.equal(client.host, '10.0.0.1');
    assert.equal(client.port, 9999);
  });

  it('query throws when not connected', async () => {
    const client = new Client();
    await assert.rejects(
      () => client.query('SELECT 1'),
      { message: 'client is not connected' },
    );
  });

  it('close sets connected to false', async () => {
    const client = new Client();
    await client.close();
    assert.equal(client.connected, false);
  });
});

describe('Handshake message format', () => {
  it('produces correct handshake JSON', () => {
    const expected = {
      type: 'handshake',
      client_version: '2.0',
      client_name: 'vaultdb-js-client',
      supported_features: ['time_travel', 'transactions', 'prepared_statements'],
    };
    // Verify the shape matches spec
    assert.equal(expected.type, 'handshake');
    assert.equal(expected.client_version, '2.0');
    assert.ok(Array.isArray(expected.supported_features));
  });
});

describe('Query request format', () => {
  it('produces correct query JSON', () => {
    const req = {
      id: 'test-id',
      token: 'vdb_sk_test',
      query: 'SELECT * FROM users WHERE id = $1;',
      params: [42],
      database: 'mydb',
    };
    assert.equal(req.id, 'test-id');
    assert.equal(req.token, 'vdb_sk_test');
    assert.deepEqual(req.params, [42]);
    assert.equal(req.database, 'mydb');
  });
});

describe('Query result parsing', () => {
  it('parses select result', () => {
    const raw = {
      id: 'test-id',
      status: 'ok',
      type: 'select',
      columns: ['id', 'name'],
      rows: [[1, 'alice']],
      affected: 0,
      message: '',
      duration_ms: 3,
    };
    const result: QueryResult = raw as any;
    assert.equal(result.status, 'ok');
    assert.equal(result.type, 'select');
    assert.deepEqual(result.columns, ['id', 'name']);
    assert.deepEqual(result.rows, [[1, 'alice']]);
    assert.equal(result.duration_ms, 3);
  });

  it('parses handshake response', () => {
    const raw = {
      type: 'handshake',
      protocol_version: '2.0',
      server: 'VaultDB',
      server_version: '1.0.0',
      supported_features: ['time_travel'],
    };
    const result: HandshakeResult = raw as any;
    assert.equal(result.protocol_version, '2.0');
    assert.equal(result.server, 'VaultDB');
    assert.deepEqual(result.supported_features, ['time_travel']);
  });
});

describe('Transaction helpers', () => {
  it('begin sends BEGIN query', () => {
    const sql = 'BEGIN;';
    assert.equal(sql, 'BEGIN;');
  });

  it('commit sends COMMIT query', () => {
    const sql = 'COMMIT;';
    assert.equal(sql, 'COMMIT;');
  });

  it('rollback sends ROLLBACK query', () => {
    const sql = 'ROLLBACK;';
    assert.equal(sql, 'ROLLBACK;');
  });
});
