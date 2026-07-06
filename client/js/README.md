# @vaultdb/client

VaultDB TCP client for Node.js (protocol v2).

## Install

```bash
npm install @vaultdb/client
```

Or from source:

```bash
cd client/js
npm install
npm run build
```

## Usage

```typescript
import { Client } from '@vaultdb/client';

const client = new Client('localhost', 5432, 'vdb_sk_your_token');
const handshake = await client.connect();
console.log(`Connected to ${handshake.server} v${handshake.server_version}`);

// Simple query
const result = await client.query('SELECT * FROM users WHERE id = $1;', [42]);
console.log(result.columns, result.rows);

// Parameterized query
const insert = await client.query(
  'INSERT INTO users (name, email) VALUES ($1, $2);',
  ['alice', 'alice@example.com'],
);
console.log(`Inserted ${insert.affected} row(s)`);

// Transactions
await client.begin();
try {
  await client.query('UPDATE accounts SET balance = balance - $1 WHERE id = $2;', [100, 1]);
  await client.query('UPDATE accounts SET balance = balance + $1 WHERE id = $2;', [100, 2]);
  await client.commit();
} catch (err) {
  await client.rollback();
  throw err;
}

// Using a specific database
const res = await client.query('SELECT count(*) FROM orders;', [], 'shopdb');

// Cleanup
await client.close();
```

## `await using` pattern

```typescript
await using client = new Client('localhost', 5432, 'vdb_sk_...');
await client.connect();
const result = await client.query('SELECT 1;');
// client.close() called automatically at scope exit
```

## API

### `new Client(host?, port?, token?)`

Creates a new client instance.

- `host` — server host (default: `localhost`)
- `port` — server port (default: `5432`)
- `token` — authentication token

### `client.connect(): Promise<HandshakeResult>`

Connects to the server and performs protocol v2 handshake.

Returns `{ protocol_version, server, server_version, supported_features }`.

### `client.query(sql, params?, database?): Promise<QueryResult>`

Executes a SQL query with optional parameters and database name.

Returns `{ status, type, columns, rows, affected, message?, duration_ms? }`.

### `client.begin()` / `client.commit()` / `client.rollback()`

Transaction convenience methods. Each sends the corresponding SQL command.

### `client.close(): Promise<void>`

Closes the TCP connection.

### Properties

- `client.connected` — whether the client is currently connected
- `client.protocolVersion` — negotiated protocol version
- `client.serverVersion` — server version string
- `client.features` — server-supported features list
