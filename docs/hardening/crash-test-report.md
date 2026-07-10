# Crash Test Report

> Date: 2026-07-05

## Existing Crash Tests

| Test | Status | Notes |
|------|--------|-------|
| TestWALRecoveryAfterCrash | ✅ PASS | WAL replay recovers committed rows |
| TestWALRecoveryWithPartialWrite | ✅ PASS | Partial write recovery with WAL |
| TestWALRecoveryWithMultipleTables | ✅ PASS | Multi-table crash recovery |
| TestWALRecoveryAfterDelete | ✅ PASS | Delete operations survive crash |
| TestCheckpointAfterOperations | ✅ PASS | Checkpoint preserves data across restarts |
| TestBufferPoolFlush | ✅ PASS | Buffer pool data persistence |
| TestIndexPersistence | ✅ PASS | Index survives restart |
| TestConcurrentInserts | ✅ PASS | 10 concurrent goroutines, all rows present |
| TestConcurrentReadsAndWrites | ✅ PASS | Mixed read/write concurrency |
| TestTransactionRecovery | ✅ PASS | Transaction state after crash |
| TestBTreeIndexSaveLoad | ✅ PASS | B-tree index serialization roundtrip |
| TestAlterTableRewriteRecovery | ✅ PASS | Incomplete rewrite cleanup on recovery |
| TestAlterTableRewriteRecoveryNoTempDir | ✅ PASS | Recovery without pending rewrite |
| TestVacuumRecovery | ✅ PASS | Orphaned vacuum directory cleanup |
| TestFullPageWriteRecovery | ✅ PASS | Full page image restores torn page |
| TestCatalogRecalculationAfterWALRecovery | ✅ PASS | Catalog row counts recalculated from heap |
| TestNoPerBatchSync | ✅ PASS | 500 inserts + updates without per-batch sync |
| TestDurabilityAfterCrash | ✅ PASS | Committed data survives simulated crash |

## New Crash Tests Added

| Test | Status | Notes |
|------|--------|-------|
| TestConcurrentCrashMixedWorkload | ✅ PASS | 5 goroutines × 100 inserts, WAL recovery restores all 500 rows |
| TestCrashDuringWASMExecution | ⏭️ SKIP | Requires real WASM module (placeholder) |

## Security Verification

| Check | Status | Notes |
|-------|--------|-------|
| TestAuthEventLogging | ✅ PASS | Auth events logged correctly |
| TestKeyRotationLogging | ✅ PASS | Key rotation events logged |
| TestMixedOperationChainIntegrity | ✅ PASS | Mixed audit operations maintain chain integrity |
| TestHashChainComputation | ✅ PASS | Hash chain computed correctly |
| TestAppendAndReadBack | ✅ PASS | Audit log append/read roundtrip |
| TestChainVerificationValid | ✅ PASS | Valid chain passes verification |
| TestChainVerificationBroken | ✅ PASS | Broken chain detected |
| TestEmptyLogVerification | ✅ PASS | Empty log verifies correctly |
| TestLastHash | ✅ PASS | Last hash tracking correct |
| TestNoDefaultTokenInjected | ✅ PASS | No default auth token in config |
| TestEncryptDecryptRoundtrip | ✅ PASS | Encryption roundtrip works |
| TestMultiVersionEncryptDecrypt | ✅ PASS | Multi-version encryption works |
| TestShowEncryptionStatus | ✅ PASS | Encryption status display correct |
| TestHealthEndpointAuth | ✅ PASS | Health endpoint respects auth |
| TestAuthMiddlewareEnabled | ✅ PASS | Auth middleware blocks unauthenticated |
| TestAuthMiddlewareDisabled | ✅ PASS | Auth middleware allows all when disabled |
| TestTableDataInjectionAttempt | ✅ PASS | SQL injection attempts blocked |
| TestLiveQueryRequiresAuth | ✅ PASS | Live queries require authentication |
| TestDetectDiskEncryption | ✅ PASS | Disk encryption detection works |
| TestParseCreateDatabaseEncrypted | ✅ PASS | ENCRYPTED keyword parsed correctly |
| TestParseCreateTableEncrypted | ✅ PASS | Table-level encryption parsed |
