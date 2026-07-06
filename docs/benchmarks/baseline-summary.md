# VaultDB Benchmark Baselines

> Established: 2026-07-05
> Go version: go1.25.7 darwin/arm64
> CPU: Apple M4 (10 cores), macOS
> Runs: 5 per benchmark (-count=5 -benchmem)

## Key Metrics (Median of 5 Runs)

### Regular Benchmarks (benchmarks/)

| Benchmark | ns/op | B/op | allocs/op | Dataset |
|-----------|------:|-----:|----------:|---------|
| InsertSingle | 258,216 | 65,348 | 131 | 1 row insert |
| InsertBatch100 | 11,463,537 | 387,511 | 4,777 | 100 rows/batch |
| InsertBatch1000 | 94,456,729 | 3,979,309 | 47,836 | 1,000 rows/batch |
| SelectScan | 208,612 | 305,436 | 7,717 | Full scan 1,000 rows |
| SelectWhere | 42,507 | 98,076 | 2,357 | Indexed lookup 1,000 rows |
| SelectJoin | 227,015 | 418,346 | 8,602 | Join 500+500 rows |
| UpdateSingle | 1,230,059 | 571,604 | 24,663 | Indexed update 1,000 rows |
| DeleteSingle | 1,347,573 | 543,423 | 20,728 | Delete from 2,000 rows |
| Transaction10 | 8,116,449 | 5,206,962 | 229,335 | BEGIN+10 updates+COMMIT |
| ConcurrentInserts | 2,610,820 | 661,983 | 1,313 | 10 goroutines insert |

### Stress Benchmarks (benchmark/)

| Benchmark | ns/op | B/op | allocs/op | Dataset |
|-----------|------:|-----:|----------:|---------|
| InsertSingle | 276,096 | 65,321 | 129 | 1 row insert |
| InsertBatch | 12,604,798 | 389,845 | 4,778 | 100 rows/batch |
| SelectFullScan | 2,196,455 | 3,025,294 | 79,741 | Full scan 10,000 rows |
| SelectIndexed | 302,428 | 432,239 | 21,281 | Indexed lookup 10,000 rows |
| UpdateSingle | 2,508,757 | 3,800,718 | 157,128 | Update in 10,000 rows |
| MixedWorkload | 319,338 | 355,682 | 14,040 | 40%R/30%U/20%I/10%D |
| TransactionThroughput | 573,813 | 727,297 | 28,755 | BEGIN+UPDATE+COMMIT |
| ConcurrentReads | 45,181 | 97,961 | 2,349 | Sequential indexed read |

## Regression Threshold

Any benchmark showing >10% regression vs these baselines will block PR merge.

## Notes

- Benchmarks use `b.TempDir()` for ephemeral on-disk databases (WAL + storage).
- All benchmark data is flushed to disk per iteration (fsync on WAL).
- `ConcurrentReads` measures sequential throughput because the storage engine uses exclusive locks even for reads — see benchmark source for details.
- `Transaction10` and `TransactionThroughput` show high variance due to WAL flush timing; use median for comparisons.
