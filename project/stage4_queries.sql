-- ============================================================
-- Этап 4: Сложные запросы и аналитика
-- JOIN, CTE, оконные функции, подзапросы
-- ============================================================

-- Агрегация по отделам
SELECT
    department,
    COUNT(*) as doc_count,
    SUM(file_size) as total_size,
    AVG(file_size) as avg_size,
    MAX(created_at) as latest_doc
FROM documents
GROUP BY department
ORDER BY doc_count DESC;

-- CTE для статистики версий
WITH version_stats AS (
    SELECT
        doc_id,
        COUNT(*) as version_count,
        MIN(changed_at) as first_version,
        MAX(changed_at) as last_version
    FROM document_versions
    GROUP BY doc_id
)
SELECT
    d.doc_number,
    d.title,
    vs.version_count,
    vs.first_version,
    vs.last_version
FROM documents d
JOIN version_stats vs ON d.id = vs.doc_id;

-- Оконные функции
SELECT
    id,
    title,
    department,
    created_at,
    ROW_NUMBER() OVER (PARTITION BY department ORDER BY created_at) as doc_num_in_dept,
    LAG(title, 1) OVER (PARTITION BY department ORDER BY created_at) as previous_doc,
    LEAD(title, 1) OVER (PARTITION BY department ORDER BY created_at) as next_doc,
    SUM(file_size) OVER (PARTITION BY department ORDER BY created_at) as cumulative_size
FROM documents
WHERE created_at >= '2024-01-01'
LIMIT 20;

-- EXISTS подзапрос
SELECT DISTINCT department
FROM documents d1
WHERE EXISTS (
    SELECT 1
    FROM documents d2
    WHERE d2.department = d1.department
    AND d2.file_size > 1024
);

-- ANY подзапрос
SELECT id, title, department, file_size
FROM documents d
WHERE file_size > ANY (
    SELECT AVG(file_size)
    FROM documents
    WHERE department = d.department
    GROUP BY department
);

-- Cascading CTE
WITH
dept_stats AS (
    SELECT department, COUNT(*) as cnt FROM documents GROUP BY department
),
ordered AS (
    SELECT department, cnt FROM dept_stats ORDER BY cnt DESC
)
SELECT * FROM ordered;
