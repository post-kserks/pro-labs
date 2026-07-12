-- ============================================================
-- Этап 6: JSONB операции
-- ============================================================

-- Извлечение данных (->, ->>, ?)
SELECT
    id,
    title,
    metadata->>'type' as doc_type,
    metadata->>'amount' as amount,
    metadata->'counterparty' as counterparty
FROM documents
WHERE metadata ? 'counterparty';

-- Contains (@>)
SELECT id, title, metadata
FROM documents
WHERE metadata @> '{"type": "contract", "signed": true}';

-- Reverse containment (<@)
SELECT id, title, metadata
FROM documents
WHERE metadata <@ '{"type": "report", "approved": false, "extra": true}';

-- Merge (||)
UPDATE documents
SET metadata = metadata || '{"status": "archived", "archive_date": "2024-12-01"}'::JSONB
WHERE id = 1;

-- Delete key (-)
UPDATE documents
SET metadata = metadata - 'temporary_flag'
WHERE metadata ? 'temporary_flag';

-- Nested structures (JSONB_SET)
UPDATE documents
SET metadata = JSONB_SET(
    metadata,
    'comments',
    '[{"user": "alice", "text": "Проверено", "date": "2024-01-15"},
      {"user": "bob", "text": "Требует доработки", "date": "2024-01-16"}]'::JSONB
)
WHERE id = 2;

-- Search in arrays
SELECT id, title, metadata
FROM documents
WHERE metadata->'comments' @> '[{"user": "alice"}]';

-- JSONB_TYPEOF
SELECT id, JSONB_TYPEOF(metadata) as json_type
FROM documents
LIMIT 5;
