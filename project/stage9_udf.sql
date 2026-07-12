-- ============================================================
-- Этап 9: Хранимые процедуры и UDF
-- ============================================================
-- VaultDB: функции возвращают скалярное значение через SELECT
-- Нет plpgsql — тело функции это SELECT выражение

-- Функция для подсчета документов отдела
CREATE FUNCTION IF NOT EXISTS get_dept_doc_count(dept_name TEXT)
RETURNS INT AS
    SELECT COUNT(*)::INT FROM documents WHERE department = $1;

SELECT get_dept_doc_count('legal') as legal_docs;
SELECT get_dept_doc_count('finance') as finance_docs;

-- Функция для вычисления хеша
CREATE FUNCTION IF NOT EXISTS compute_hash(val TEXT)
RETURNS TEXT AS
    SELECT LENGTH($1)::TEXT || '_' || SUBSTR($1, 1, 10);

SELECT compute_hash('Hello VaultDB') as hash_result;

-- Функция для информации о документе
CREATE FUNCTION IF NOT EXISTS get_doc_info(doc_id INT)
RETURNS TEXT AS
    SELECT doc_number || ': ' || title FROM documents WHERE id = $1;

SELECT get_doc_info(1) as doc_info;

-- Процедура для архивации
CREATE PROCEDURE IF NOT EXISTS archive_old_documents(age_days INT)
AS
    UPDATE documents
    SET status = 'archived'
    WHERE created_at < CURRENT_TIMESTAMP - (CAST(age_days AS TEXT) || ' days');

CALL archive_old_documents(365);

-- Проверка
SELECT id, doc_number, status FROM documents WHERE status = 'archived';

-- WASM UDF (пример синтаксиса)
-- CREATE FUNCTION hash_wasm(val TEXT) RETURNS TEXT
-- LANGUAGE WASM AS 'file:///plugins/hash.wasm'
-- WITH (MEMORY_LIMIT '16MB', TIMEOUT '100ms');
