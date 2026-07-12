-- ============================================================
-- Этап 10: Производительность и оптимизация
-- Партицирование, кэширование, мониторинг
-- ============================================================

-- EXPLAIN (partition pruning)
EXPLAIN SELECT * FROM documents WHERE created_at >= '2024-06-01';

-- EXPLAIN ANALYZE
EXPLAIN ANALYZE
SELECT d.id, d.title, d.department
FROM documents d
WHERE d.department = 'finance'
  AND d.created_at BETWEEN '2024-01-01' AND '2024-06-30';

-- Параллельные агрегации
SELECT COUNT(*) as cnt,
       department,
       date_trunc('month', created_at) as month
FROM documents
GROUP BY department, date_trunc('month', created_at)
ORDER BY month DESC;

-- Тест кэширования планов
SELECT * FROM documents WHERE id = 1;
SELECT * FROM documents WHERE id = 2;
SELECT * FROM documents WHERE id = 3;

-- Мониторинг:
-- Health check: curl http://localhost:5433/health
-- Metrics: curl http://localhost:5433/metrics
