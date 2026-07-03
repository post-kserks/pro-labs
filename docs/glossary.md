# Glossary

| Term | Definition |
|------|------------|
| **ARIES** | Algorithm for Recovery and Isolation Exploiting Semantics — the WAL recovery algorithm used by VaultDB |
| **B-tree** | Balanced tree data structure for sorted data, supporting O(log n) lookup, insert, and delete |
| **Buffer Pool** | In-memory cache of disk pages with LRU eviction |
| **Catalog** | Metadata file tracking databases, tables, row counts, and transaction timestamps |
| **Checkpoint** | Process of flushing dirty pages to disk and writing a checkpoint record to the WAL |
| **Command Pattern** | Design pattern where each SQL statement type is a Command with an Execute() method |
| **Composite Index** | Multi-column index using concatenated keys |
| **CTE** | Common Table Expression — a named subquery defined with WITH |
| **Dead Tuple** | A tuple that has been deleted (deletedTx != 0) but not yet reclaimed by vacuum |
| **FIFO** | First In, First Out — buffer pool eviction order |
| **Full Page Image** | Complete 8KB page image stored in WAL for torn-page protection |
| **GiST** | Generalized Search Tree — R-tree implementation for range/overlap queries |
| **GIN** | Generalized Inverted Index — inverted index for full-text search and JSONB containment |
| **Hash Index** | Index using a hash table for O(1) equality lookups |
| **Heap File** | File storing table data as a sequence of pages |
| **Item Pointer** | Reference to a tuple's location on a page (offset + length + flag) |
| **LSN** | Log Sequence Number — identifies a position in the WAL |
| **LRU** | Least Recently Used — buffer pool eviction policy |
| **MVCC** | Multi-Version Concurrency Control — enables concurrent reads/writes via tuple versioning |
| **OCC** | Optimistic Concurrency Control — conflict detection at commit time |
| **Page** | Fixed-size (8KB) unit of storage I/O |
| **Page Lock** | Per-page mutex enabling concurrent writes to different pages |
| **Primary Key** | Column(s) uniquely identifying each row; automatically indexed |
| **RLS** | Row-Level Security — per-user data access policies |
| **Segment** | File containing up to 65,536 pages (512 MB) of a heap file |
| **Shadow File** | Temporary file used during vacuum/ALTER for atomic replacement |
| **Slotted Page** | Page layout with item pointers growing from the start and tuples growing from the end |
| **Spill-to-Disk** | Writing large transaction operations to temporary files |
| **Tuple** | A single row of data including its version header |
| **TX** | Transaction — a unit of work with ACID properties |
| **UNION** | Set operation combining results from two queries (with deduplication) |
| **Vacuum** | Process of reclaiming space from dead tuples |
| **WAL** | Write-Ahead Logging — durability protocol ensuring crash recovery |
| **XMax** | Transaction ID that deleted a tuple (0 if live) — field in tuple header |
| **XMin** | Transaction ID that created a tuple — field in tuple header |
| **TDE** | Transparent Data Encryption — page-level encryption using AES-256-GCM |
| **DEK** | Data Encryption Key — encrypts actual page data (32 bytes for AES-256) |
| **KEK** | Key Encryption Key — encrypts the DEK (from passphrase, keychain, or KMS) |
| **Envelope Encryption** | Two-level key scheme where KEK encrypts DEK, enabling fast key rotation |
| **AES-256-GCM** | Authenticated encryption providing confidentiality and integrity in one pass |
| **Argon2id** | Memory-hard key derivation function resistant to GPU/ASIC brute-force |
