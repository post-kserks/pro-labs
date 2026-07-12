-- ============================================================
-- Этап 5: Полнотекстовый поиск (FTS, BM25)
-- ============================================================

-- GIN индекс для полнотекстового поиска
CREATE INDEX IF NOT EXISTS idx_documents_fts
ON documents (content) USING GIN;

-- FULLTEXT индекс для BM25
CREATE INDEX IF NOT EXISTS idx_documents_fulltext
ON documents (title, content) FULLTEXT;

-- Поиск по содержимому (GIN + LIKE)
SELECT id, title, content
FROM documents
WHERE content LIKE '%договор%' OR content LIKE '%поставк%';

-- BM25 поиск (MATCH)
SELECT id, title, content,
       bm25_score(documents, content) AS score
FROM documents
WHERE content MATCH 'договор поставка'
ORDER BY score DESC;

-- BM25 поиск по title
SELECT id, title, content,
       bm25_score(documents, title) AS score
FROM documents
WHERE title MATCH 'договор отчет'
ORDER BY score DESC;

-- BM25 + фильтр по department
SELECT id, title, department,
       bm25_score(documents, content) AS score
FROM documents
WHERE content MATCH 'отчет финансовый'
  AND department = 'finance'
ORDER BY score DESC;
