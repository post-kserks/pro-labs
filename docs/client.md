# Clients

VaultDB provides official TCP clients for Go, Python, JavaScript/TypeScript, and C++.

All clients implement the [TCP Protocol](tcp-protocol.md) with v2 handshake support.

## Go TCP Client

### Installation

```bash
go get github.com/post-kserks/vaultdb/client/go
```

### Usage

```go
import vaultdb "github.com/post-kserks/vaultdb/client/go"

client, err := vaultdb.TCPDial("localhost:5432", "vdb_sk_...")
if err != nil {
    log.Fatal(err)
}
defer client.Close()

// Query
result, err := client.Query("mydb", "SELECT * FROM users WHERE id = $1", "42")
if err != nil {
    log.Fatal(err)
}
fmt.Println(result.Rows)

// Transactions
client.Begin()
client.Query("", "INSERT INTO users VALUES (1, 'alice')")
client.Commit()
```

### Features

- Protocol v2 handshake with automatic fallback to v1
- Parameterized queries
- Database selection per query
- Transaction support (BEGIN/COMMIT/ROLLBACK)
- Connection pooling
- Automatic reconnection

## Python TCP Client

### Installation

```bash
pip install vaultdb
```

### Usage

```python
from vaultdb import Client

with Client("localhost", 5432, "vdb_sk_...") as client:
    client.connect()
    
    # Query
    result = client.query("SELECT * FROM users WHERE id = $1", [42])
    print(result["rows"])
    
    # Transactions
    client.begin()
    client.query("INSERT INTO users VALUES (1, 'alice')")
    client.commit()
```

### Features

- Context manager support (`with` statement)
- Protocol v2 handshake
- Parameterized queries
- Transaction support
- Async support (coming soon)

## JavaScript/TypeScript TCP Client

### Installation

```bash
npm install @vaultdb/client
```

### Usage

```typescript
import { Client } from '@vaultdb/client';

const client = new Client('localhost', 5432, 'vdb_sk_...');
await client.connect();

// Query
const result = await client.query('SELECT * FROM users WHERE id = $1', [42]);
console.log(result.rows);

// Transactions
await client.begin();
await client.query('INSERT INTO users VALUES (1, $1)', ['alice']);
await client.commit();

await client.close();
```

### Features

- Promise-based async/await API
- TypeScript type definitions included
- Protocol v2 handshake
- Parameterized queries
- Transaction support

## C++ Client

### Building

#### Prerequisites

- CMake 3.14+
- C++17 compiler (GCC 7+, Clang 5+, MSVC 2017+)
- No external dependencies

#### Build Commands

```bash
cd client
cmake -S . -B build -DCMAKE_BUILD_TYPE=Release
cmake --build build -- -j$(nproc)
```

#### Running Tests

```bash
cd client/build
ctest --output-on-failure
```

### Usage

```cpp
#include "vaultdb/client.h"

vaultdb::Client client("localhost", 5432);
client.authenticate("vdb_sk_your_token_here");

auto result = client.query("mydb", "SELECT * FROM users WHERE age > 25;");

for (const auto& row : result.rows()) {
    std::cout << row[0] << ", " << row[1] << std::endl;
}

if (result.isError()) {
    std::cerr << "Error: " << result.errorMessage() << std::endl;
}
```

### CMake Integration

```cmake
add_subdirectory(client)
target_link_libraries(your_target PRIVATE vaultdb_client)
```

### Features

- Simple query execution
- Prepared statement support
- Transaction support (BEGIN/COMMIT/ROLLBACK)
- Connection pooling
- Automatic reconnection
- Error handling
