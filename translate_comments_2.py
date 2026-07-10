#!/usr/bin/env python3
"""Second pass: translate remaining Russian comments to English in Go files."""
import re
import os

def has_russian(text):
    return bool(re.search(r'[а-яА-ЯёЁ]', text))

def translate_file(filepath):
    with open(filepath, 'r', encoding='utf-8') as f:
        content = f.read()
    
    if not has_russian(content):
        return False
    
    original = content
    
    # Phase 1: Comment-only Russian line translations (// style)
    # These are whole-line comments with Russian text
    replacements = [
        # txmanager/manager.go
        ("// TxState — состояние транзакции.", "// TxState — transaction state."),
        ("// нет активной транзакции", "// no active transaction"),
        ("// BEGIN выполнен, ожидаем COMMIT/ROLLBACK", "// BEGIN executed, awaiting COMMIT/ROLLBACK"),
        ("// ErrTxConflict — конфликт транзакций.", "// ErrTxConflict — transaction conflict."),
        ("// OCCConfig — настройки optimistic concurrency control: retry и backoff.", "// OCCConfig — optimistic concurrency control settings: retry and backoff."),
        ("// PendingOp — одна буферизованная операция внутри транзакции.", "// PendingOp — a single buffered operation within a transaction."),
        ("// Transaction — активная транзакция одной сессии.", "// Transaction — active transaction of a single session."),
        ("// opCounter — сквозной счётчик добавленных операций. Растёт в AddOp вне", "// opCounter — monotonically increasing counter of added operations. Grows in AddOp regardless"),
        ("// зависимости от того, лежат ли операции в памяти или в spill-файле.", "// of whether operations are in memory or in the spill file."),
        ("// Используется savepoint'ами как стабильный маркер позиции.", "// Used by savepoints as a stable position marker."),
        ("// savepoints: имя → opCounter на момент создания; savepointOrder хранит", "// savepoints: name → opCounter at creation time; savepointOrder stores"),
        ("// порядок создания, чтобы при ROLLBACK TO удалять savepoint'ы, созданные", "// creation order so that ROLLBACK TO removes savepoints created"),
        ("// позже указанного.", "// after the specified one."),
        ("// spillErr — «липкая» ошибка spill'а. Если запись на диск не удалась,", "// spillErr — sticky spill error. If disk write failed,"),
        ("// ReadOps (а значит и Commit) вернёт эту ошибку, чтобы коммит упал, а не", "// ReadOps (and thus Commit) will return this error so commit fails rather than"),
        ("// потерял часть операций молча (Bug #4).", "// silently losing some operations (Bug #4)."),
        ("// Manager управляет транзакциями всех сессий.", "// Manager manages transactions across all sessions."),
        ("// RecordAccess фиксирует версию таблицы при ПЕРВОМ обращении (чтении или", "// RecordAccess records the table version on FIRST access (read or"),
        ("// записи). Снимок берётся только если его ещё нет. Благодаря этому любая", "// write). A snapshot is taken only if one doesn't exist. Thanks to this, any"),
        ("// таблица, которую транзакция читала ИЛИ писала, проверяется на конкурентную", "// table that the transaction read OR wrote is checked for concurrent"),
        ("// модификацию во время Commit (Bug #2a).", "// modification during Commit (Bug #2a)."),
        ("// TableKey возвращает ключ таблицы в том же формате, что использует Commit для", "// TableKey returns the table key in the same format used by Commit for"),
        ("// commit-локов. Нужен внешним пакетам, чтобы брать тот же per-table lock.", "// commit locks. Needed by external packages to acquire the same per-table lock."),
        ("// LockTables берёт commit-локи на указанные ключи таблиц и возвращает функцию", "// LockTables acquires commit locks on specified table keys and returns a"),
        ("// разблокировки. Публичная обёртка над lockTables: позволяет autocommit-записям", "// unlock function. Public wrapper over lockTables: allows autocommit writes"),
        ("// сериализоваться с коммитами транзакций (Bug #2b).", "// to serialize with transaction commits (Bug #2b)."),
        ("// AddOp добавляет операцию в буфер транзакции.", "// AddOp adds an operation to the transaction buffer."),
        ("// При превышении SpillThreshold сериализует буфер во временный файл.", "// When SpillThreshold is exceeded, serializes the buffer to a temporary file."),
        ("// EncodePendingOp/DecodePendingOp — точки расширения для сериализации операций", "// EncodePendingOp/DecodePendingOp — extension points for serializing operations"),
        ("// при spill'е. Пакет txmanager не знает о типах parser/storage, поэтому executor", "// during spill. The txmanager package doesn't know about parser/storage types, so executor"),
        ("// регистрирует здесь кодек, умеющий восстанавливать типизированный Payload", "// registers a codec here that can restore typed Payload"),
        ("// (parser.*Statement). Если кодек не задан — используется обычный JSON.", "// (parser.*Statement). If no codec is set, plain JSON is used."),
        ("// writeOpsToFile сериализует операции построчно (по одному JSON-объекту в", "// writeOpsToFile serializes operations line by line (one JSON object per"),
        ("// строке). Возвращает ошибку, чтобы вызывающий мог зафиксировать spillErr и не", "// line). Returns an error so the caller can record spillErr and not"),
        ("// потерять операции молча (Bug #4).", "// silently lose operations (Bug #4)."),
        ("// ReadOps возвращает операции: из памяти или из файла. Если spill завершился", "// ReadOps returns operations: from memory or from file. If spill failed,"),
        ("// ошибкой — возвращает её (а не усечённый/пустой набор), чтобы Commit упал.", "// it returns the error (not a truncated/empty set) so Commit fails."),
        ("// Savepoint фиксирует текущую позицию буфера под именем name. Повторное имя", "// Savepoint records the current buffer position under the given name. Duplicate name"),
        ("// перезаписывает прежний маркер (семантика SQL).", "// overwrites the previous marker (SQL semantics)."),
        ("// ReleaseSavepoint удаляет маркер savepoint'а. Возвращает false, если имя", "// ReleaseSavepoint removes the savepoint marker. Returns false if the name"),
        ("// неизвестно. Буферизованные операции сохраняются.", "// is unknown. Buffered operations are preserved."),
        ("// RollbackToSavepoint усекает буфер до позиции savepoint'а и удаляет", "// RollbackToSavepoint truncates the buffer to the savepoint position and removes"),
        ("// savepoint'ы, созданные позже. Транзакция остаётся активной.", "// savepoints created later. The transaction remains active."),
        ("// Commit проверяет конфликты и применяет операции транзакции.", "// Commit checks conflicts and applies transaction operations."),
        ("// Rollback очищает буфер и удаляет spill файл.", "// Rollback clears the buffer and deletes the spill file."),
        ("// IsCommitted возвращает true, если транзакция с указанным xid считается завершённой.", "// IsCommitted returns true if the transaction with the given xid is considered committed."),
        ("// Упрощение: все xid < текущего счётчика считаются committed.", "// Simplification: all xid < current counter are considered committed."),
        ("// EnsureCounterAtLeast гарантирует, что счётчик txid не меньше n.", "// EnsureCounterAtLeast guarantees the txid counter is at least n."),
        ("// Используется при загрузке catalog page engine, чтобы ранее выделенные", "// Used when loading catalog page engine so that previously allocated"),
        ("// txid считались committed.", "// txids are considered committed."),
        ("// CleanupSpillFiles удаляет старые spill файлы (вызывается при старте сервера).", "// CleanupSpillFiles removes old spill files (called on server startup)."),
        ("tx.Ops = nil // освобождаем RAM", "tx.Ops = nil // free RAM"),
        ("// bufio.Reader.ReadBytes растёт под произвольный размер строки — нет", "// bufio.Reader.ReadBytes grows for arbitrary line length — no"),
        ("// ограничения в 64KB, как у bufio.Scanner по умолчанию (Bug #4).", "// 64KB limit like bufio.Scanner has by default (Bug #4)."),
        ("// Удаляем savepoint'ы, созданные позже указанного (по порядку создания).", "// Remove savepoints created after the specified one (by creation order)."),
        
        # config/config.go
        ("// Package config загружает vaultdb.yaml.", "// Package config loads vaultdb.yaml."),
        ("// LiveQueriesConfig управляет поведением Live Queries при медленных клиентах.", "// LiveQueriesConfig controls Live Query behavior for slow clients."),
        ("// TLSConfig — параметры TLS.", "// TLSConfig — TLS parameters."),
        ("// ServerConfig — сетевые параметры сервера.", "// ServerConfig — server network parameters."),
        ("// StorageConfig — параметры хранилища.", "// StorageConfig — storage parameters."),
        ("// AuthConfig — параметры аутентификации.", "// AuthConfig — authentication parameters."),
        ("// AIConfig — параметры внешнего embedding-провайдера для SEMANTIC_MATCH/AI_EMBED.", "// AIConfig — external embedding provider parameters for SEMANTIC_MATCH/AI_EMBED."),
        ("// EncryptionConfig — параметры Transparent Data Encryption (TDE).", "// EncryptionConfig — Transparent Data Encryption (TDE) parameters."),
        ("// AuditConfig — параметры журналирования аудита.", "// AuditConfig — audit logging parameters."),
        ("// Config — корневая конфигурация vaultdb.yaml.", "// Config — root configuration of vaultdb.yaml."),
        ("DefaultMaxRequestSize         = 64 * 1024 * 1024 // 64 МБ", "DefaultMaxRequestSize         = 64 * 1024 * 1024 // 64 MB"),
        ("// Default возвращает конфигурацию со значениями по умолчанию.", "// Default returns configuration with default values."),
        ("// Load читает конфигурацию из файла. Отсутствующие ключи получают значения", "// Load reads configuration from a file. Missing keys get"),
        ("// по умолчанию. Если path пустой — возвращаются значения по умолчанию.", "// default values. If path is empty — default values are returned."),
        ("// ApplyEnvOverrides применяет переменные окружения, перекрывая значения из файла.", "// ApplyEnvOverrides applies environment variables, overriding file values."),
        ("// Reload перезагружает конфигурацию из файла.", "// Reload reloads configuration from a file."),
        ("// Возвращает ошибку если конфиг невалиден.", "// Returns an error if config is invalid."),
        
        # tls/tls.go
        ("// Config — конфигурация TLS.", "// Config — TLS configuration."),
        ("CertFile string // путь к файлу сертификата", "CertFile string // path to certificate file"),
        ("KeyFile  string // путь к файлу ключа", "KeyFile  string // path to key file"),
        ("// LoadTLSConfig загружает TLS конфигурацию из файлов.", "// LoadTLSConfig loads TLS configuration from files."),
        ("// GenerateSelfSignedCert генерирует самоподписанный сертификат для тестов.", "// GenerateSelfSignedCert generates a self-signed certificate for tests."),
        ("// SaveCertToFile сохраняет сертификат и ключ в файлы.", "// SaveCertToFile saves certificate and key to files."),
        ("// LoadMTLSConfig загружает TLS конфигурацию с поддержкой mTLS (mutual TLS).", "// LoadMTLSConfig loads TLS configuration with mTLS (mutual TLS) support."),
        ("// Помимо серверного сертификата загружает CA для верификации клиентских сертификатов.", "// In addition to the server certificate, loads CA for verifying client certificates."),
        ("// WrapListener оборачивает net.Listener в TLS listener.", "// WrapListener wraps a net.Listener in a TLS listener."),
        
        # protocol.go
        ("// sendError отправляет ошибку клиенту. Возвращает false, если запись в сокет", "// sendError sends an error to the client. Returns false if socket write"),
        ("// не удалась (клиент отвалился) — в этом случае обрабатывать соединение дальше", "// failed (client disconnected) — in that case, handling the connection further"),
        ("// бессмысленно.", "// is pointless."),
        ("// sanitizeErrorMessage удаляет внутренние детали из сообщений об ошибках", "// sanitizeErrorMessage strips internal details from error messages"),
        ("// перед отправкой клиенту. Whitelist подход: безопасные сообщения проходят,", "// before sending to the client. Whitelist approach: safe messages pass through,"),
        ("// всё остальное заменяется на generic \"internal error\".", "// everything else is replaced with a generic \"internal error\"."),
        ("// Безопасные паттерны — можно показать клиенту", "// Safe patterns — safe to show to the client"),
        ("// Всё остальное — generic ошибка без деталей", "// Everything else — generic error without details"),
        
        # main.go
        ("// version и buildDate перезаписываются через ldflags при сборке", "// version and buildDate are overwritten via ldflags at build time"),
        ("// (единый источник истины — файл VERSION в корне репозитория).", "// (single source of truth — VERSION file in the repository root)."),
        ("// CLI-флаги имеют приоритет над vaultdb.yaml: значения из конфига", "// CLI flags take priority over vaultdb.yaml: config values"),
        ("// применяются только для флагов, которые не были заданы явно.", "// are applied only for flags that were not explicitly set."),
        ("// Embedding-провайдер для SEMANTIC_MATCH/AI_EMBED. Без настроенного AI", "// Embedding provider for SEMANTIC_MATCH/AI_EMBED. Without configured AI,"),
        ("// эти операции возвращают понятную ошибку (NoopEmbedder в executor).", "// these operations return a clear error (NoopEmbedder in executor)."),
        
        # pool.go
        ("// Connection — соединение в пуле, оборачивает реальное TCP-соединение.", "// Connection — a connection in the pool, wraps a real TCP connection."),
        ("// Read читает данные из соединения, обновляя LastUsed.", "// Read reads data from the connection, updating LastUsed."),
        ("// Write записывает данные в соединение, обновляя LastUsed.", "// Write writes data to the connection, updating LastUsed."),
        ("// Close закрывает底层 TCP-соединение.", "// Close closes the underlying TCP connection."),
        ("// RemoteAddr возвращает адрес удалённой стороны.", "// RemoteAddr returns the remote side address."),
        ("// SetDeadline устанавливает deadline на соединении.", "// SetDeadline sets the deadline on the connection."),
        ("// SetReadDeadline устанавливает read deadline.", "// SetReadDeadline sets the read deadline."),
        ("// SetWriteDeadline устанавливает write deadline.", "// SetWriteDeadline sets the write deadline."),
        ("// Acquire получает соединение из пула.", "// Acquire gets a connection from the pool."),
        ("// Release возвращает соединение в пул.", "// Release returns a connection to the pool."),
        ("// AcquireConn оборачивает существующее соединение в пул.", "// AcquireConn wraps an existing connection into the pool."),
        ("// Используется когда соединение уже принято (listener.Accept),", "// Used when the connection is already accepted (listener.Accept),"),
        ("// а не создаётся через factory.", "// rather than created via factory."),
        ("// Close закрывает пул и все соединения.", "// Close closes the pool and all connections."),
        ("// Stats возвращает статистику пула.", "// Stats returns pool statistics."),
        ("// ConnectionLimiterStats статистика пула соединений.", "// ConnectionLimiterStats holds connection pool statistics."),
        ("// io.EOF означает, что remote side закрыл соединение — оно мёртвое", "// io.EOF means the remote side closed the connection — it's dead"),
        ("// removeConnLocked удаляет соединение из списка (должно вызываться с p.mu).", "// removeConnLocked removes a connection from the list (must be called with p.mu)."),
        ("// SessionPool — пул сессий для повторного использования executor.Session.", "// SessionPool — session pool for reusing executor.Session objects."),
        ("// Аналогично тому, как PostgreSQL переиспользует соединения через пул,", "// Similar to how PostgreSQL reuses connections via a pool,"),
        ("// SessionPool позволяет HTTP-хендлерам переиспользовать сессии между запросами.", "// SessionPool allows HTTP handlers to reuse sessions between requests."),
        ("// NewSessionPool создаёт новый пул сессий.", "// NewSessionPool creates a new session pool."),
        ("// factory — функция создания новой сессии.", "// factory — function to create a new session."),
        ("// maxIdle — максимальное количество безделовых сессий в пуле.", "// maxIdle — maximum number of idle sessions in the pool."),
        ("// maxOpen — максимальное количество одновременно активных сессий.", "// maxOpen — maximum number of simultaneously active sessions."),
        ("// idleTimeout — максимальное время простоя сессии перед закрытием.", "// idleTimeout — maximum session idle time before closing."),
        ("// Get получает сессию из пула или создаёт новую.", "// Get gets a session from the pool or creates a new one."),
        ("// Попытка взять из пула (non-blocking)", "// Attempt to get from pool (non-blocking)"),
        ("// Проверяем лимит", "// Check limit"),
        ("// Создаём новую сессию", "// Create new session"),
        ("// Put возвращает сессию в пул для повторного использования.", "// Put returns a session to the pool for reuse."),
        ("// Попытка вернуть в пул (non-blocking)", "// Attempt to return to pool (non-blocking)"),
        ("// Пул полон — закрываем сессию", "// Pool is full — close session"),
        ("// Close закрывает пул и все сессии в нём.", "// Close closes the pool and all sessions in it."),
        ("// Закрываем все оставшиеся сессии", "// Close all remaining sessions"),
        ("// Stats возвращает статистику пула сессий.", "// Stats returns session pool statistics."),
        ("// SessionConnectionLimiterStats статистика пула сессий.", "// SessionConnectionLimiterStats holds session pool statistics."),
        ("// Сессия ещё жива — возвращаем в пул", "// Session is still alive — return to pool"),
        
        # wal.go
        ("// вставка tuple на страницу", "// insert tuple into page"),
        ("// пометка tuple как dead (XMax)", "// mark tuple as dead (XMax)"),
        ("// обновление XMax (при DELETE/UPDATE)", "// update XMax (on DELETE/UPDATE)"),
        ("// выделение новой страницы", "// allocate new page"),
        ("// полный образ страницы перед модификацией", "// full page image before modification"),
        ("// payload для", "// payload for"),
        ("// транзакция, создавшая tuple", "// transaction that created the tuple"),
        ("// полные данные tuple (header + attrs)", "// full tuple data (header + attrs)"),
        ("// XID транзакции удаляющей tuple", "// XID of the transaction deleting the tuple"),
        ("// JSON schema", "// JSON schema"),
        ("// полный образ страницы (8KB)", "// full page image (8KB)"),
        ("// Checkpoint усекает WAL после checkpoint.", "// Checkpoint truncates WAL after checkpoint."),
        ("// Для корректного checkpoint", "// For correct checkpoint"),
        ("// используйте WriteCheckpointRecord() + сохранение каталога + TruncateWAL().", "// use WriteCheckpointRecord() + catalog save + TruncateWAL()."),
        ("// Этот метод усекает WAL ДО сохранения каталога — crash между truncate и", "// This method truncates WAL BEFORE saving catalog — crash between truncate and"),
        ("// сохранением каталога приведёт к потере всех записей.", "// catalog save will lose all entries."),
        ("// Deprecated: используйте doCheckpoint() в page_engine.go.", "// Deprecated: use doCheckpoint() in page_engine.go."),
        ("// WriteCheckpointRecord записывает checkpoint record в WAL и синхронизирует,", "// WriteCheckpointRecord writes a checkpoint record to WAL and syncs,"),
        ("// но НЕ усекает файл. Возвращает LSN (TxID) checkpoint записи.", "// but does NOT truncate the file. Returns LSN (TxID) of the checkpoint record."),
        ("// Используется doCheckpoint для порядка: сначала checkpoint record,", "// Used by doCheckpoint for ordering: checkpoint record first,"),
        ("// потом сохранение каталога (чтобы recovery могла определить checkpoint LSN).", "// then catalog save (so recovery can determine checkpoint LSN)."),
        ("// TruncateWAL усекает WAL файл после checkpoint.", "// TruncateWAL truncates WAL file after checkpoint."),
        ("// Вызывается после сохранения каталога, чтобы recovery могла", "// Called after saving catalog so recovery can"),
        ("// определить checkpoint LSN из каталога trước чтением WAL.", "// determine checkpoint LSN from catalog before reading WAL."),
        ("// AppendWithTx записывает запись в WAL с указанным txID (не инкрементирует автоматически).", "// AppendWithTx writes a WAL entry with the given txID (does not auto-increment)."),
        ("// WriteFullPageImage записывает полный образ страницы в WAL для защиты от torn pages.", "// WriteFullPageImage writes a full page image to WAL for torn page protection."),
        ("// Вызывается ПЕРЕД модификацией страницы на диске.", "// Called BEFORE modifying the page on disk."),
        ("// Анализирует WAL потоково, не загружая все записи в память.", "// Analyzes WAL streaming, without loading all entries into memory."),
        ("// Определяет какие транзакции закоммичены, а какие остались незавершёнными.", "// Determines which transactions are committed and which remain in-progress."),
        ("// Воспроизводит все записи WAL, вызывая callback для каждой операции.", "// Replays all WAL entries, calling a callback for each operation."),
        ("// Записи сначала собираются под w.mu, затем callback вызывается без удержания", "// Entries are first collected under w.mu, then callback is called without holding"),
        ("// блокировки — это предотвращает deadlock WAL↔PageEngine (Bug lock ordering).", "// the lock — this prevents WAL↔PageEngine deadlock (Bug lock ordering)."),
        ("// Воспроизводит записи конкретной транзакции.", "// Replays entries of a specific transaction."),
        ("// Записи сначала собираются под w.mu, затем callback вызывается без удержания", "// Entries are first collected under w.mu, then callback is called without holding"),
        ("// блокировки — предотвращает deadlock WAL↔PageEngine.", "// the lock — prevents WAL↔PageEngine deadlock."),
        ("// Flush синхронизирует WAL файл на диск и возвращает текущий LSN.", "// Flush syncs WAL file to disk and returns current LSN."),
        ("// FindLastVacuumCommit ищет последний OpVacuumCommit для указанной таблицы потоково.", "// FindLastVacuumCommit searches for the last OpVacuumCommit for the given table streaming."),
        ("// Сначала fsync все heap-файлы", "// First fsync all heap files"),
    ]
    
    for ru, en in replacements:
        if ru in content:
            content = content.replace(ru, en)
    
    if content != original:
        with open(filepath, 'w', encoding='utf-8') as f:
            f.write(content)
        return True
    return False

def main():
    base = "/home/golem/g_proj/pro-labs/server"
    count = 0
    for root, dirs, files in os.walk(base):
        for f in files:
            if f.endswith('.go'):
                path = os.path.join(root, f)
                if translate_file(path):
                    count += 1
                    print(f"  translated: {os.path.relpath(path, base)}")
    print(f"\nTotal files translated in pass 2: {count}")
    
    # Count remaining
    remaining = 0
    for root, dirs, files in os.walk(base):
        for f in files:
            if f.endswith('.go'):
                path = os.path.join(root, f)
                with open(path, 'r') as fh:
                    if has_russian(fh.read()):
                        remaining += 1
    print(f"Files still with Russian: {remaining}")

if __name__ == '__main__':
    main()
