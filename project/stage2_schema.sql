-- ============================================================
-- Этап 2: Создание структуры данных
-- DDL, типы данных, индексы, секционирование
-- ============================================================

-- Основная таблица документов с секционированием RANGE
CREATE TABLE IF NOT EXISTS documents (
    id INT PRIMARY KEY,
    doc_number VARCHAR(50) UNIQUE NOT NULL,
    title TEXT NOT NULL,
    content TEXT,
    metadata JSONB,
    created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
    updated_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
    status VARCHAR(20) DEFAULT 'active',
    department VARCHAR(50),
    author VARCHAR(100),
    file_size INT,
    checksum VARCHAR(64)
) PARTITION BY RANGE (created_at) (
    PARTITION p2023 VALUES LESS THAN ('2024-01-01'),
    PARTITION p2024 VALUES LESS THAN ('2025-01-01'),
    PARTITION p2025 VALUES LESS THAN ('2026-01-01'),
    PARTITION p2026 VALUES LESS THAN ('2027-01-01')
);

-- Таблица версий документов
CREATE TABLE IF NOT EXISTS document_versions (
    id INT PRIMARY KEY,
    doc_id INT,
    version_number INT,
    content_hash VARCHAR(64),
    changes_description TEXT,
    changed_by VARCHAR(100),
    changed_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
    UNIQUE(doc_id, version_number)
);

-- B-tree индексы
CREATE INDEX IF NOT EXISTS idx_documents_department ON documents(department);
CREATE INDEX IF NOT EXISTS idx_documents_status ON documents(status);
CREATE INDEX IF NOT EXISTS idx_documents_created_at ON documents(created_at);

-- GIN индекс для JSONB
CREATE INDEX IF NOT EXISTS idx_documents_metadata ON documents USING GIN (metadata);

-- Триггер для обновления updated_at
CREATE TRIGGER IF NOT EXISTS trigger_update_updated_at
AFTER UPDATE ON documents
BEGIN
    UPDATE documents SET updated_at = CURRENT_TIMESTAMP WHERE id = NEW.id;
END;

-- Проверка структуры
DESCRIBE documents;
DESCRIBE document_versions;
SHOW INDEXES ON documents;
