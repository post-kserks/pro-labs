# C++ Client

VaultDB includes a C++17 client library for connecting to the TCP protocol.

## Building

### Prerequisites

- CMake 3.14+
- C++17 compiler (GCC 7+, Clang 5+, MSVC 2017+)
- No external dependencies

### Build Commands

```bash
cd client
cmake -S . -B build -DCMAKE_BUILD_TYPE=Release
cmake --build build -- -j$(nproc)
```

### Running Tests

```bash
cd client/build
ctest --output-on-failure
```

## Usage

```cpp
#include "vaultdb/client.h"

// Connect to server
vaultdb::Client client("localhost", 5432);

// Authenticate
client.authenticate("vdb_sk_your_token_here");

// Execute query
auto result = client.query("mydb", "SELECT * FROM users WHERE age > 25;");

// Access results
for (const auto& row : result.rows()) {
    std::cout << row[0] << ", " << row[1] << std::endl;
}

// Check for errors
if (result.isError()) {
    std::cerr << "Error: " << result.errorMessage() << std::endl;
}
```

## Wire Protocol

The C++ client uses the same JSON-over-TCP protocol described in [TCP Protocol](tcp-protocol.md).

### Request

```json
{"id":"1","token":"vdb_sk_...","query":"SELECT 1;"}
```

### Response

```json
{"id":"1","status":"ok","type":"select","columns":["?"],"rows":[["1"]]}
```

## Features

- Simple query execution
- Prepared statement support
- Transaction support (BEGIN/COMMIT/ROLLBACK)
- Connection pooling
- Automatic reconnection
- Error handling

## CMake Integration

```cmake
add_subdirectory(client)
target_link_libraries(your_target PRIVATE vaultdb_client)
```
