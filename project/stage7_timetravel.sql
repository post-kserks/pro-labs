-- ============================================================
-- Этап 7: Time Travel (путешествие во времени)
-- ============================================================

-- Текущее состояние
SELECT COUNT(*) as current_count FROM documents;

-- Вносим изменения
UPDATE documents SET status = 'archived' WHERE created_at < '2024-01-01';
DELETE FROM documents WHERE status = 'deleted';

-- Состояние на момент времени (AS OF)
SELECT COUNT(*) as count_2024_01_01
FROM documents AS OF '2024-01-01 00:00:00';

-- Сравнение состояний
SELECT
    (SELECT COUNT(*) FROM documents AS OF '2024-01-01') as before_count,
    (SELECT COUNT(*) FROM documents) as after_count;

-- История изменений документа
SELECT * FROM documents AS OF '2024-01-01' WHERE id = 1;
SELECT * FROM documents AS OF '2024-06-01' WHERE id = 1;
SELECT * FROM documents WHERE id = 1;  -- текущая версия

-- Восстановление данных
CREATE TABLE IF NOT EXISTS recovered_docs AS
SELECT * FROM documents AS OF '2024-01-01 00:00:00';

SELECT COUNT(*) as recovered_count FROM recovered_docs;
