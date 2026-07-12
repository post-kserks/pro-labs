-- ============================================================
-- Этап 8: Аудит и проверка целостности
-- Audit Log с hash-chain
-- ============================================================

-- Просмотр аудит-лога
SELECT
    id,
    username,
    action,
    object_type,
    details,
    occurred_at
FROM vaultdb_audit_log
ORDER BY occurred_at DESC
LIMIT 20;

-- Поиск по аудиту
SELECT * FROM vaultdb_audit_log
WHERE action IN ('CREATE TABLE', 'INSERT', 'UPDATE')
ORDER BY occurred_at DESC
LIMIT 10;

-- Проверка целостности цепочки аудита
VERIFY AUDIT LOG;

-- Анализ аудита: кто и когда менял данные
SELECT
    username,
    COUNT(*) as operations,
    MIN(occurred_at) as first_action,
    MAX(occurred_at) as last_action
FROM vaultdb_audit_log
WHERE action IN ('INSERT', 'UPDATE', 'DELETE')
GROUP BY username
ORDER BY operations DESC;

-- Архивирование аудита
-- COPY vaultdb_audit_log TO '/backup/audit_export.json' WITH (FORMAT JSON);
