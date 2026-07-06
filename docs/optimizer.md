# Query Optimizer

VaultDB includes a cost-based optimizer that selects access methods, join strategies, and query plans.

## Access Methods

| Method | Description | When Used |
|--------|-------------|-----------|
| **Sequential Scan** | Read all pages | No suitable index, small tables |
| **Index Scan** | Use index, fetch rows | Index available, selective query |
| **Index-Only Scan** | Read from index only | All needed columns in index |

## Join Methods

| Method | Description | When Used |
|--------|-------------|-----------|
| **Nested Loop Join** | For each row in outer, scan inner | Small outer, indexed inner |
| **Hash Join** | Build hash table on inner, probe with outer | Equality joins, larger datasets |
| **Merge Join** | Sort both inputs, merge | Pre-sorted data, range joins |

## Optimization Techniques

### Predicate Pushdown

WHERE conditions are pushed down to individual tables before joins:

```sql
-- Before: scan all rows, then filter
-- After: filter at each table, then join
SELECT * FROM orders o JOIN customers c ON o.cust_id = c.id
WHERE c.country = 'US';
```

### Join Reordering

Tables are reordered to minimize intermediate result sizes:

```sql
-- Optimizer may reorder: users JOIN orders → orders JOIN users
SELECT * FROM users u JOIN orders o ON u.id = o.cust_id
JOIN products p ON o.prod_id = p.id;
```

### Projection Pushdown

Only required columns are read from storage:

```sql
-- Only reads id and name columns, not all columns
SELECT id, name FROM users WHERE age > 25;
```

### Subquery Decorrelation

Correlated subqueries are transformed into joins when possible.

## EXPLAIN

View the query plan:

```sql
EXPLAIN SELECT * FROM users WHERE email = 'alice@example.com';
```

Output includes:
- Access method (SeqScan, IndexScan, IndexOnlyScan)
- Estimated cost
- Estimated row count
- Join methods used

## EXPLAIN ANALYZE

Execute the query and show actual execution statistics:

```sql
EXPLAIN ANALYZE SELECT * FROM users WHERE age > 25;
```

Output includes:
- Actual execution time
- Rows matched at each step
- Index hit/miss information

## Statistics

The optimizer uses table statistics for cost estimation:

- **Distinct count**: Number of unique values per column
- **Null count**: Number of NULL values per column
- **Selectivity estimation**: Based on distinct count and query pattern
- **Sample-based**: Reads up to 1000 rows for statistics
- **Cached**: Results cached per table, invalidated on mutation

### Selectivity Rules

| Pattern | Selectivity |
|---------|-------------|
| Equality (`=`) | `1 / distinctCount` |
| Range (`>`, `<`) | 30% (assumed) |
| LIKE | 20% (assumed) |
| AND | Product of selectivities |
| OR | Inclusion-exclusion |

## Plan Cache

Query plans are cached to avoid re-optimization:

- Plans cached by query signature
- Cache invalidated on DDL changes (CREATE/DROP/ALTER TABLE)
- Cache size configurable

## Result Cache

SELECT results are cached for repeated identical queries:

```yaml
storage:
  result_cache_size: 256
  result_cache_ttl_seconds: 30
```

- Cache invalidated on any mutation to the affected tables
- TTL-based expiration
- LRU eviction when full

## Parallel Query Execution

VaultDB supports parallel query execution for large datasets to improve performance.

### Configuration

```yaml
server:
  parallel:
    enabled: true
    num_workers: 4  # default: runtime.NumCPU()
    min_rows: 10000  # minimum rows to trigger parallelism
```

### How It Works

1. **Query Analysis**: The optimizer determines if parallel execution is beneficial
2. **Work Distribution**: Rows are split into chunks across worker goroutines
3. **Parallel Processing**: Each worker processes its chunk independently
4. **Result Aggregation**: Results are combined in order

### When Parallel Execution Is Used

- Large table scans (> 10,000 rows)
- Queries without ORDER BY (parallelism may break ordering)
- Aggregation queries (COUNT, SUM, AVG)
- Filter-heavy queries

### When Parallel Execution Is NOT Used

- Small tables (< 10,000 rows)
- Queries with ORDER BY (unless results are sorted after)
- Queries with window functions
- Queries with LIMIT (unless combined with other optimizations)

### Performance Benefits

| Query Type | Sequential | Parallel (4 workers) | Speedup |
|------------|------------|----------------------|---------|
| Full table scan | 1.2s | 0.35s | 3.4x |
| Aggregation | 0.8s | 0.25s | 3.2x |
| Filter-heavy | 0.9s | 0.28s | 3.2x |

### Limitations

- Parallelism adds overhead for small datasets
- Results may not be ordered (use ORDER BY if needed)
- Memory usage increases with worker count
- Not suitable for all query patterns
