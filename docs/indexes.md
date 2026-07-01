# Indexes

Indexes improve query performance by providing fast lookup paths to rows. VaultDB supports five index types, each optimized for different query patterns.

## Creating Indexes

```sql
-- B-tree (default)
CREATE INDEX idx_users_email ON users (email);

-- Hash
CREATE INDEX idx_users_email ON users (email) USING HASH;

-- GIN (full-text search)
CREATE INDEX idx_articles_body ON articles (body) USING GIN;

-- GiST (range queries)
CREATE INDEX idx_events_time ON events (created_at) USING GiST;

-- Composite (multi-column)
CREATE INDEX idx_orders_customer_date ON orders (customer_id, order_date);

-- Unique index
CREATE INDEX idx_users_email ON users (email) UNIQUE;
```

## Dropping Indexes

```sql
DROP INDEX idx_users_email;
```

## Listing Indexes

```sql
SHOW INDEXES;
```

---

## B-Tree Index

The default index type. Suitable for equality lookups, range queries, and ordering.

### Structure

Sorted string keys with associated row position lists. Supports O(log n) lookup via binary search.

### When to Use

- Equality comparisons (`=`, `!=`)
- Range queries (`>`, `<`, `>=`, `<=`)
- `ORDER BY` optimization
- `GROUP BY` optimization

### Example

```sql
-- Create index
CREATE INDEX idx_users_email ON users (email);

-- Optimized queries
SELECT * FROM users WHERE email = 'alice@example.com';
SELECT * FROM users WHERE age > 25 AND age < 50;
SELECT * FROM users ORDER BY email;
```

### Internals

The B-tree index maintains:
- A sorted slice of string keys
- Row position lists for each key (handles duplicates)
- A reverse map (row position → key) for efficient deletion

On `INSERT`, the key is inserted into the sorted slice at the correct position. On `DELETE`, the reverse map locates the key in O(log n) time.

---

## Hash Index

Optimized for exact equality lookups. O(1) average case.

### When to Use

- Pure equality comparisons (`=`)
- High-cardinality columns

### Example

```sql
CREATE INDEX idx_users_id ON users (id) USING HASH;

-- Fast lookup
SELECT * FROM users WHERE id = 42;
```

### Limitations

- No support for range queries
- No support for ordering
- No support for `IS NULL` optimization

### Internals

Bidirectional map:
- `value → []rowPositions` for lookup
- `rowPosition → value` for deletion

---

## GIN Index (Generalized Inverted Index)

Optimized for full-text search and JSONB containment queries.

### When to Use

- Full-text search (`LIKE '%pattern%'`)
- JSONB containment (`@>`, `<@`, `?`)
- Multi-valued columns

### Text Mode

```sql
-- Full-text search index
CREATE INDEX idx_articles_body ON articles (body) USING GIN;

-- Queries that use the index
SELECT * FROM articles WHERE body LIKE '%database%';
SELECT * FROM articles WHERE body LIKE '%sql%index%';
```

**Tokenization**: Text is split on spaces, commas, periods, exclamation marks, question marks, semicolons, and colons. All matching is case-insensitive.

**Search semantics**: Returns rows containing ALL query tokens (AND semantics).

### JSONB Mode

```sql
CREATE INDEX idx_configs ON configs (settings) USING GIN JSONB;

-- JSONB containment
SELECT * FROM configs WHERE settings @> '{"theme": "dark"}';

-- Key existence
SELECT * FROM configs WHERE settings ? 'theme';
```

**Tokenization**: JSON keys become `key:<lowered-key>` tokens. JSON values become their string representations.

### Internals

Inverted index mapping:
- `token → []rowPositions` for lookup
- Token set is rebuilt during `Rebuild()` from all indexed values

---

## GiST Index (Generalized Search Tree)

R-tree implementation for numeric range and overlap queries.

### When to Use

- Numeric range overlap queries
- Bounding-box searches
- Interval containment

### Example

```sql
-- Range index on numeric intervals
CREATE INDEX idx_events_range ON events (time_range) USING GiST;

-- Overlap query
SELECT * FROM events WHERE time_range && '[100, 200]';
```

### Value Format

GiST index values should be in `min-max` format:
- `'10-20'` represents range [10, 20]
- `'5'` represents a single point

### Internals

R-tree with:
- MBR (Minimum Bounding Range) nodes
- Capacity of 4 entries per node
- Insertion via MBR enlargement minimization (`chooseSubtree`)
- Bulk loading via sorted insertion with recursive splitting

---

## Composite Index

Multi-column index using null-byte-separated composite keys.

### When to Use

- Queries filtering on multiple columns simultaneously
- `ORDER BY` on multiple columns

### Example

```sql
CREATE INDEX idx_orders ON orders (customer_id, order_date, status);

-- Uses the composite index
SELECT * FROM orders
WHERE customer_id = 42 AND order_date = '2026-07-01';

-- Partial prefix usage
SELECT * FROM orders WHERE customer_id = 42;
```

### Key Encoding

- Values are concatenated with null-byte separators
- Numeric values are zero-padded to 20 characters for correct lexicographic ordering
- Boolean values encoded as `0`/`1`

---

## Index Usage by the Optimizer

VaultDB's cost-based optimizer automatically selects the best index for each query:

| Query Pattern | Index Used |
|--------------|------------|
| `WHERE col = value` | B-tree or Hash |
| `WHERE col > value` | B-tree |
| `WHERE col BETWEEN a AND b` | B-tree |
| `WHERE col LIKE '%pattern%'` | GIN (full-text) |
| `WHERE jsonb @> pattern` | GIN (JSONB) |
| `WHERE range && pattern` | GiST |
| `ORDER BY col` | B-tree |
| `ORDER BY col1, col2` | Composite |

### EXPLAIN

Use `EXPLAIN` to see which index is selected:

```sql
EXPLAIN SELECT * FROM users WHERE email = 'alice@example.com';
```

Output shows the access method (SeqScan, IndexScan, or IndexOnlyScan) and estimated cost.

---

## Index Constraints

- Index names must be unique within a database
- Column names in indexes are case-insensitive
- `PRIMARY KEY` automatically creates a B-tree index
- `UNIQUE` constraints create unique B-tree indexes
- Index metadata is persisted to `.indexes.json` per table
- Indexes are rebuilt from table data during `Rebuild()` operations
