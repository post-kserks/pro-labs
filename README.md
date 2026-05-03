# PixelDB

PixelDB is an educational SQL database with a retro RPG terminal style.

## Components

- `server/` — Go TCP server with SQL lexer/parser, command executor, and JSON file storage.
- `client/` — C++17 shared library (`libpixeldb`), line shell (`pixeldb-shell`), and fullscreen FTXUI client (`pixeldb-tui`).

## Supported SQL

- DDL: `CREATE DATABASE`, `DROP DATABASE`, `CREATE TABLE`, `DROP TABLE`, `USE`
- Metadata: `SHOW DATABASES`, `SHOW TABLES [FROM db]`, `DESCRIBE table [FROM db]`
- DML: `SELECT`, `INSERT`, `UPDATE`, `DELETE`
- `SELECT COUNT(*)` and `SELECT ... LIMIT n`
- `WHERE` expressions with `AND`, `OR`, `NOT`, parentheses, and comparison operators
- Data types: `INT`, `FLOAT`, `BOOL`, `TEXT`, `VARCHAR(n)`

## Build

```bash
./build.sh
```

Artifacts are placed into `build/`:

- `build/pixeldb-server`
- `build/libpixeldb*`
- `build/pixeldb-shell`
- `build/pixeldb-tui`

## Run server

```bash
./run.sh
./run.sh 0.0.0.0 7777
```

## Run shell client

```bash
./build/pixeldb-shell
./build/pixeldb-shell 127.0.0.1 5432
```

## Run TUI client

```bash
./build/pixeldb-tui
./build/pixeldb-tui --host 127.0.0.1 --port 5432
```

## Tests

```bash
cd server
GOCACHE=/tmp/go-cache GOMODCACHE=/tmp/go-mod-cache go test ./...
```
