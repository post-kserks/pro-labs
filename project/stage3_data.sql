-- ============================================================
-- Этап 3: Наполнение данными
-- DML, COPY, транзакции
-- ============================================================

-- Вставка документов (пакетная)
INSERT INTO documents (id, doc_number, title, content, metadata, department, author, file_size)
VALUES
(1, 'DOC-2024-001', 'Договор поставки №123', 'Полный текст договора о поставке оборудования...',
 '{"type": "contract", "amount": 1250000, "counterparty": "ООО Ромашка", "signed": true}'::JSONB,
 'legal', 'Анна Петрова', 1024),
(2, 'DOC-2024-002', 'Финансовый отчет Q1', 'Отчет о прибылях и убытках за первый квартал...',
 '{"type": "report", "period": "Q1-2024", "total_revenue": 4500000, "approved": false}'::JSONB,
 'finance', 'Иван Сидоров', 2048),
(3, 'DOC-2024-003', 'Служебная записка о командировке', 'Текст служебной записки о командировке в Москву...',
 '{"type": "memo", "priority": "high", "to": "HR"}'::JSONB, 'hr', 'Мария Иванова', 512),
(4, 'DOC-2024-004', 'Техническое задание на проект', 'Подробное техническое задание на разработку системы...',
 '{"type": "spec", "project": "Alpha", "deadline": "2024-12-31"}'::JSONB, 'it', 'Сергей Смирнов', 4096);

-- COPY FROM CSV (путь к CSV файлу)
-- COPY documents (id, doc_number, title, content, metadata, department, author, file_size)
-- FROM '/tmp/import_documents.csv' WITH (FORMAT CSV, HEADER true);

-- COPY TO JSON
-- COPY documents TO '/tmp/legal_documents.json' WITH (FORMAT JSON);

-- Транзакция с UPSERT
BEGIN;
INSERT INTO documents (id, doc_number, title, content, metadata, department, author)
VALUES (5, 'DOC-2024-005', 'Новый контракт', 'Текст контракта...',
        '{"type": "contract", "amount": 250000}'::JSONB, 'legal', 'Анна Петрова')
ON CONFLICT (doc_number)
DO UPDATE SET
    title = EXCLUDED.title,
    content = EXCLUDED.content,
    updated_at = CURRENT_TIMESTAMP,
    metadata = documents.metadata || EXCLUDED.metadata;
COMMIT;

-- MERGE операция
MERGE INTO documents USING (
    VALUES (6, 'DOC-2024-006', 'Обновленный отчет', 'Новый текст отчета...',
            '{"type": "report", "period": "Q2-2024"}'::JSONB, 'finance', 'Иван Сидоров')
) AS src(id, doc_number, title, content, metadata, department, author)
ON documents.doc_number = src.doc_number
WHEN MATCHED THEN UPDATE SET
    title = src.title,
    content = src.content,
    updated_at = CURRENT_TIMESTAMP
WHEN NOT MATCHED THEN INSERT (id, doc_number, title, content, metadata, department, author)
VALUES (src.id, src.doc_number, src.title, src.content, src.metadata, src.department, src.author);

-- Проверка
SELECT COUNT(*) as total_documents FROM documents;
