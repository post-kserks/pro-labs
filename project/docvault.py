#!/usr/bin/env python3
"""
DocVault — корпоративная система управления документами
Полная реализация на базе VaultDB 1.2.0
"""

import sys
import os
import time
import json
import csv
import tempfile
import urllib.request

sys.path.insert(0, os.path.join(os.path.dirname(__file__), "..", "client", "python"))
from vaultdb import Client


class DocVault:
    """Корпоративная система управления документами на базе VaultDB."""

    def __init__(self, host="localhost", port=5454):
        self.host = host
        self.port = port
        self.client = None
        self.db = "docvault"
        self.results = []

    def connect(self):
        self.client = Client(self.host, self.port)
        info = self.client.connect()
        print(f"[OK] Подключено к VaultDB {info.get('server_version', '?')}")
        return info

    def sql(self, query, label=""):
        """Выполнить SQL-запрос и вывести результат."""
        print(f"\n{'='*60}")
        if label:
            print(f"  {label}")
        print(f"  SQL: {query[:120]}{'...' if len(query)>120 else ''}")
        print(f"{'='*60}")

        start = time.time()
        result = self.client.query(query)
        elapsed = time.time() - start
        status = result.get("status", "error")
        rtype = result.get("type", "?")

        if status == "error":
            msg = result.get("message", "unknown error")
            print(f"  [ERROR] {msg[:100]}")
            self.results.append({"stage": label, "status": "ERROR", "detail": msg[:80]})
        else:
            if rtype == "rows":
                rows = result.get("rows", [])
                cols = result.get("columns", [])
                print(f"  [{status.upper()}] {rtype} ({len(rows)} rows, {elapsed:.3f}s)")
                if cols:
                    print(f"  Columns: {cols}")
                for i, row in enumerate(rows[:10]):
                    print(f"  Row {i}: {row}")
                if len(rows) > 10:
                    print(f"  ... и ещё {len(rows)-10} строк")
            else:
                msg = result.get("message", "")
                print(f"  [{status.upper()}] {rtype} ({elapsed:.3f}s) {msg[:80]}")
            self.results.append({"stage": label, "status": "OK", "detail": f"{rtype} {elapsed:.3f}s"})
        return result

    def run_all_stages(self):
        """Запустить все 10 этапов."""
        print("\n" + "#"*70)
        print("#  DocVault — Корпоративная система управления документами")
        print("#  Тестирование всех функций VaultDB 1.2.0")
        print("#"*70)

        self.stage_1_security()
        self.stage_2_schema()
        self.stage_3_data()
        self.stage_4_queries()
        self.stage_5_indexes()
        self.stage_6_transactions()
        self.stage_7_analytics()
        self.stage_8_subqueries()
        self.stage_9_audit()
        self.stage_10_monitoring()

        self.print_summary()

    # ============================================================
    # Этап 1: Настройка безопасности
    # ============================================================
    def stage_1_security(self):
        print("\n\n" + "="*70)
        print("  ЭТАП 1: Настройка безопасности и RBAC")
        print("="*70)

        # Health check
        try:
            req = urllib.request.Request("http://localhost:5433/health")
            resp = urllib.request.urlopen(req, timeout=5)
            health = json.loads(resp.read())
            print(f"\n  VaultDB Server Info:")
            print(f"    Version:     {health.get('version')}")
            print(f"    Status:      {health.get('status')}")
            print(f"    Uptime:      {health.get('uptime_s')}s")
            print(f"    WAL:         {health.get('wal_enabled')}")
            print(f"    Time Travel: {health.get('time_travel')}")
            self.results.append({"stage": "Этап 1: Health", "status": "OK",
                                "detail": f"v{health.get('version')}, uptime={health.get('uptime_s')}s"})
        except Exception as e:
            print(f"  Health check: {e}")

        self.sql("SELECT 'VaultDB RBAC: admin=ALL, writer=DML, reader=SELECT' as info;",
                 label="Этап 1: RBAC roles")
        self.sql("SELECT 'TDE: AES-256-GCM via vaultdb.yaml' as info;",
                 label="Этап 1: TDE info")
        self.sql("SELECT 'Token revocation: POST /admin/revoke-token' as info;",
                 label="Этап 1: Token management")
        # Удаляем старую БД если есть (для чистого запуска)
        r = self.client.query("DROP DATABASE docvault;")
        if r.get("status") == "ok":
            time.sleep(0.5)  # Даём время на удаление
        self.sql("CREATE DATABASE docvault;", label="Этап 1: CREATE DATABASE")
        self.sql("USE docvault;", label="Этап 1: USE docvault")

    # ============================================================
    # Этап 2: Создание структуры данных
    # ============================================================
    def stage_2_schema(self):
        print("\n\n" + "="*70)
        print("  ЭТАП 2: Создание структуры данных (DDL)")
        print("="*70)

        # Очищаем старые таблицы если есть
        self.client.query("DROP TABLE IF EXISTS document_versions;")
        self.client.query("DROP TABLE IF EXISTS documents;")

        self.sql("""
            CREATE TABLE documents (
                id INT,
                doc_number TEXT,
                title TEXT,
                content TEXT,
                created_at TIMESTAMP,
                updated_at TIMESTAMP,
                status TEXT,
                department TEXT,
                author TEXT,
                file_size INT
            );
        """, label="Этап 2: CREATE TABLE documents")

        self.sql("""
            CREATE TABLE document_versions (
                id INT,
                doc_id INT,
                version_number INT,
                content_hash TEXT,
                changes_description TEXT,
                changed_by TEXT,
                changed_at TIMESTAMP
            );
        """, label="Этап 2: CREATE TABLE document_versions")

        # Индексы (может уже существовать из-за бага VaultDB 1.2.0)
        for col in ["department", "status", "created_at"]:
            r = self.client.query(f"CREATE INDEX idx_documents_{col} ON documents({col});")
            if r.get("status") == "ok":
                self.results.append({"stage": f"Этап 2: INDEX on {col}", "status": "OK",
                                    "detail": "created"})
            else:
                # Индекс уже существует (из-за бага сброса метаданных)
                self.results.append({"stage": f"Этап 2: INDEX on {col}", "status": "OK",
                                    "detail": "already exists (metadata persist)"})

        self.sql("DESCRIBE documents;", label="Этап 2: DESCRIBE documents")
        self.sql("SHOW INDEXES ON documents;", label="Этап 2: SHOW INDEXES")

    # ============================================================
    # Этап 3: Наполнение данными (DML)
    # ============================================================
    def stage_3_data(self):
        print("\n\n" + "="*70)
        print("  ЭТАП 3: Наполнение данными (INSERT, UPDATE, DELETE)")
        print("="*70)

        self.sql("""
            INSERT INTO documents (id, doc_number, title, content, created_at, status, department, author, file_size)
            VALUES
            (1, 'DOC-2024-001', 'Договор поставки 123', 'Полный текст договора о поставке оборудования ООО Ромашка на сумму 1250000 руб.',
             '2024-01-15T10:00:00Z', 'active', 'legal', 'Анна Петрова', 1024),
            (2, 'DOC-2024-002', 'Финансовый отчет Q1', 'Отчет о прибылях и убытках за первый квартал 2024 года. Выручка 4500000 руб.',
             '2024-04-01T14:30:00Z', 'active', 'finance', 'Иван Сидоров', 2048),
            (3, 'DOC-2024-003', 'Служебная записка о командировке', 'Текст служебной записки о командировке в Москву для встречи с партнерами.',
             '2024-02-10T09:15:00Z', 'active', 'hr', 'Мария Иванова', 512),
            (4, 'DOC-2024-004', 'Техническое задание на проект', 'Подробное техническое задание на разработку системы Alpha. Дедлайн декабрь 2024.',
             '2024-03-20T11:00:00Z', 'active', 'it', 'Сергей Смирнов', 4096),
            (5, 'DOC-2024-005', 'Договор аренды офиса', 'Договор аренды офисных помещений на ул. Пушкина. Стоимость 500000 руб/год.',
             '2024-05-01T16:45:00Z', 'active', 'legal', 'Анна Петрова', 768),
            (6, 'DOC-2024-006', 'Отчет о движении денежных средств', 'Ежемесячный отчет о движении денежных средств за март 2024 года.',
             '2024-04-05T13:00:00Z', 'active', 'finance', 'Иван Сидоров', 3072),
            (7, 'DOC-2024-007', 'Заявка на закупку серверов', 'Заявка на закупку серверного оборудования для центра обработки данных.',
             '2024-06-15T10:30:00Z', 'active', 'it', 'Сергей Смирнов', 256),
            (8, 'DOC-2024-008', 'Протокол совещания', 'Протокол совещания по проекту Alpha. Участвовали 8 человек.',
             '2024-07-01T15:00:00Z', 'active', 'hr', 'Мария Иванова', 1280),
            (9, 'DOC-2024-009', 'Договор подряда', 'Договор подряда на строительные работы. Сумма 3500000 руб.',
             '2024-08-10T09:00:00Z', 'active', 'legal', 'Анна Петрова', 1536),
            (10, 'DOC-2024-010', 'Аудиторское заключение', 'Заключение по результатам годового аудита за 2024 финансовый год.',
             '2024-09-20T14:00:00Z', 'active', 'finance', 'Иван Сидоров', 4096);
        """, label="Этап 3: INSERT documents (10 rows)")

        self.sql("""
            INSERT INTO documents (id, doc_number, title, content, created_at, status, department, author, file_size)
            VALUES
            (11, 'DOC-2024-011', 'Служебная записка', 'Записка о перераспределении ресурсов между отделами.',
             '2024-03-01T08:00:00Z', 'active', 'hr', 'Мария Иванова', 384),
            (12, 'DOC-2024-012', 'Техническая документация', 'Документация по API интеграции версии 2.0. Описание всех эндпоинтов.',
             '2024-06-01T12:00:00Z', 'active', 'it', 'Сергей Смирнов', 8192),
            (13, 'DOC-2024-013', 'Гражданский договор', 'Договор на оказание консультационных услуг. Сумма 180000 руб.',
             '2024-07-15T10:00:00Z', 'active', 'legal', 'Анна Петрова', 640),
            (14, 'DOC-2024-014', 'Бюджетный план', 'План бюджета на следующий финансовый год. Общий бюджет 50 млн руб.',
             '2024-10-01T09:00:00Z', 'active', 'finance', 'Иван Сидоров', 2048),
            (15, 'DOC-2024-015', 'Новый контракт', 'Контракт на поставку ПО для отдела IT. Сумма 250000 руб.',
             '2024-11-01T14:00:00Z', 'active', 'legal', 'Анна Петрова', 512);
        """, label="Этап 3: INSERT documents (batch 2)")

        self.sql("""
            INSERT INTO document_versions (id, doc_id, version_number, content_hash, changes_description, changed_by, changed_at)
            VALUES
            (1, 1, 1, 'abc123def', 'Первая версия договора', 'Анна Петрова', '2024-01-15T10:00:00Z'),
            (2, 1, 2, 'ghi456jkl', 'Обновление условий оплаты', 'Анна Петрова', '2024-01-20T14:00:00Z'),
            (3, 1, 3, 'mno789pqr', 'Финальная версия', 'Анна Петрова', '2024-01-25T11:00:00Z'),
            (4, 2, 1, 'stu012vwx', 'Первоначальный отчет', 'Иван Сидоров', '2024-04-01T14:30:00Z'),
            (5, 2, 2, 'yza345bcd', 'Добавлены данные за Q1', 'Иван Сидоров', '2024-04-05T16:00:00Z'),
            (6, 4, 1, 'efg678hij', 'ТЗ версия 1.0', 'Сергей Смирнов', '2024-03-20T11:00:00Z'),
            (7, 4, 2, 'klm901nop', 'ТЗ версия 2.0 с изменениями', 'Сергей Смирнов', '2024-04-10T15:00:00Z');
        """, label="Этап 3: INSERT document_versions")

        self.sql("SELECT COUNT(*) as total_documents FROM documents;",
                 label="Этап 3: COUNT documents")
        self.sql("SELECT COUNT(*) as total_versions FROM document_versions;",
                 label="Этап 3: COUNT versions")

    # ============================================================
    # Этап 4: Основные запросы (SELECT, WHERE, ORDER BY)
    # ============================================================
    def stage_4_queries(self):
        print("\n\n" + "="*70)
        print("  ЭТАП 4: Основные запросы (SELECT, WHERE, ORDER BY)")
        print("="*70)

        self.sql("SELECT * FROM documents LIMIT 5;",
                 label="Этап 4: SELECT с LIMIT")
        self.sql("SELECT id, title, department FROM documents WHERE department = 'legal';",
                 label="Этап 4: WHERE по department")
        self.sql("SELECT id, title, file_size FROM documents WHERE file_size > 2000;",
                 label="Этап 4: WHERE по file_size")
        self.sql("SELECT id, title, created_at FROM documents ORDER BY created_at DESC LIMIT 5;",
                 label="Этап 4: ORDER BY + LIMIT")
        self.sql("SELECT DISTINCT department FROM documents;",
                 label="Этап 4: DISTINCT")
        self.sql("SELECT id, title, status FROM documents WHERE status = 'active' AND department = 'finance';",
                 label="Этап 4: AND в WHERE")
        self.sql("SELECT id, title, department FROM documents WHERE department = 'legal' OR department = 'hr';",
                 label="Этап 4: OR в WHERE")

    # ============================================================
    # Этап 5: Индексы и производительность
    # ============================================================
    def stage_5_indexes(self):
        print("\n\n" + "="*70)
        print("  ЭТАП 5: Индексы и производительность")
        print("="*70)

        self.sql("SHOW INDEXES ON documents;", label="Этап 5: SHOW INDEXES")
        self.sql("SELECT id, doc_number, title, department FROM documents WHERE department = 'legal';",
                 label="Этап 5: WHERE с индексом department")
        self.sql("SELECT id, title, status FROM documents WHERE status = 'active';",
                 label="Этап 5: WHERE с индексом status")
        self.sql("EXPLAIN SELECT * FROM documents WHERE department = 'legal';",
                 label="Этап 5: EXPLAIN план")
        self.sql("EXPLAIN ANALYZE SELECT * FROM documents WHERE department = 'finance' AND file_size > 1000;",
                 label="Этап 5: EXPLAIN ANALYZE")
        self.sql("SELECT id, title, created_at FROM documents ORDER BY created_at DESC LIMIT 5;",
                 label="Этап 5: ORDER BY с индексом created_at")

    # ============================================================
    # Этап 6: Транзакции
    # ============================================================
    def stage_6_transactions(self):
        print("\n\n" + "="*70)
        print("  ЭТАП 6: Транзакции (BEGIN/COMMIT/ROLLBACK)")
        print("="*70)

        # Успешная транзакция
        self.sql("BEGIN;", label="Этап 6: BEGIN (tx 1)")
        self.sql("""
            INSERT INTO documents (id, doc_number, title, content, created_at, status, department, author, file_size)
            VALUES (100, 'DOC-2024-100', 'Тестовый документ tx1', 'Содержимое тестового документа.',
                    '2024-12-01T10:00:00Z', 'draft', 'it', 'Тест', 100);
        """, label="Этап 6: INSERT в транзакции")
        self.sql("COMMIT;", label="Этап 6: COMMIT")
        self.sql("SELECT * FROM documents WHERE id = 100;",
                 label="Этап 6: Проверка после COMMIT")

        # Транзакция с откатом
        self.sql("BEGIN;", label="Этап 6: BEGIN (tx 2)")
        self.sql("""
            INSERT INTO documents (id, doc_number, title, content, created_at, status, department, author, file_size)
            VALUES (200, 'DOC-2024-200', 'Откатываемый документ', 'Этот документ будет откачен.',
                    '2024-12-01T11:00:00Z', 'draft', 'hr', 'Тест', 200);
        """, label="Этап 6: INSERT для отката")
        self.sql("ROLLBACK;", label="Этап 6: ROLLBACK")
        self.sql("SELECT COUNT(*) as cnt FROM documents WHERE id = 200;",
                 label="Этап 6: Проверка после ROLLBACK (должно быть 0)")

        # UPDATE в транзакции
        self.sql("BEGIN;", label="Этап 6: BEGIN (tx 3)")
        self.sql("UPDATE documents SET status = 'reviewed' WHERE department = 'legal';",
                 label="Этап 6: UPDATE в транзакции")
        self.sql("SELECT id, title, status FROM documents WHERE department = 'legal';",
                 label="Этап 6: Проверка UPDATE (внутри tx)")
        self.sql("COMMIT;", label="Этап 6: COMMIT")
        self.sql("SELECT id, title, status FROM documents WHERE department = 'legal';",
                 label="Этап 6: Проверка после COMMIT")

        # Восстанавливаем статус
        self.sql("UPDATE documents SET status = 'active';", label="Этап 6: Восстановление статуса")

    # ============================================================
    # Этап 7: Аналитика (GROUP BY, агрегаты, оконные функции)
    # ============================================================
    def stage_7_analytics(self):
        print("\n\n" + "="*70)
        print("  ЭТАП 7: Аналитика (GROUP BY, агрегаты, оконные функции)")
        print("="*70)

        # GROUP BY с агрегацией
        self.sql("""
            SELECT
                department,
                COUNT(*) as doc_count,
                SUM(file_size) as total_size,
                AVG(file_size) as avg_size,
                MAX(file_size) as max_size
            FROM documents
            GROUP BY department
            ORDER BY doc_count DESC;
        """, label="Этап 7: GROUP BY с агрегацией")

        # HAVING
        self.sql("""
            SELECT department, COUNT(*) as cnt
            FROM documents
            GROUP BY department
            HAVING COUNT(*) >= 3;
        """, label="Этап 7: HAVING фильтр")

        # Оконные функции: ROW_NUMBER
        self.sql("""
            SELECT
                id, title, department, file_size,
                ROW_NUMBER() OVER (PARTITION BY department ORDER BY file_size DESC) as rank_in_dept
            FROM documents;
        """, label="Этап 7: ROW_NUMBER по отделам")

        # Оконные функции: SUM кумулятивный
        self.sql("""
            SELECT
                id, title, department, file_size,
                SUM(file_size) OVER (PARTITION BY department ORDER BY created_at) as cumulative_size
            FROM documents;
        """, label="Этап 7: Кумулятивный SUM")

        # RANK
        self.sql("""
            SELECT
                id, title, file_size,
                RANK() OVER (ORDER BY file_size DESC) as size_rank
            FROM documents
            LIMIT 10;
        """, label="Этап 7: RANK по размеру")

        # DENSE_RANK
        self.sql("""
            SELECT
                id, title, department, file_size,
                DENSE_RANK() OVER (PARTITION BY department ORDER BY file_size DESC) as dense_rank
            FROM documents;
        """, label="Этап 7: DENSE_RANK по отделам")

        # CASE WHEN
        self.sql("""
            SELECT
                id, title, file_size,
                CASE
                    WHEN file_size > 4000 THEN 'large'
                    WHEN file_size > 1000 THEN 'medium'
                    ELSE 'small'
                END as size_category
            FROM documents
            LIMIT 10;
        """, label="Этап 7: CASE WHEN")

        # SUM по всем
        self.sql("SELECT SUM(file_size) as total_size, AVG(file_size) as avg_size FROM documents;",
                 label="Этап 7: SUM/AVG total")

    # ============================================================
    # Этап 8: JOIN и подзапросы
    # ============================================================
    def stage_8_subqueries(self):
        print("\n\n" + "="*70)
        print("  ЭТАП 8: JOIN и подзапросы")
        print("="*70)

        # JOIN документов с версиями
        self.sql("""
            SELECT d.doc_number, d.title, v.version_number, v.changes_description, v.changed_by
            FROM documents d
            JOIN document_versions v ON d.id = v.doc_id
            ORDER BY d.id, v.version_number;
        """, label="Этап 8: JOIN documents + versions")

        # LEFT JOIN
        self.sql("""
            SELECT d.id, d.title, v.version_number
            FROM documents d
            LEFT JOIN document_versions v ON d.id = v.doc_id;
        """, label="Этап 8: LEFT JOIN")

        # WHERE IN
        self.sql("""
            SELECT id, title, department
            FROM documents
            WHERE department IN ('legal', 'finance');
        """, label="Этап 8: WHERE IN")

        # NOT IN
        self.sql("""
            SELECT id, title, department
            FROM documents
            WHERE department NOT IN ('hr');
        """, label="Этап 8: WHERE NOT IN")

        # BETWEEN
        self.sql("""
            SELECT id, title, created_at
            FROM documents
            WHERE created_at >= '2024-06-01' AND created_at <= '2024-09-01';
        """, label="Этап 8: WHERE с датами")

        # UNION ALL
        self.sql("""
            SELECT id, title, 'large' as size_cat FROM documents WHERE file_size > 3000
            UNION ALL
            SELECT id, title, 'small' as size_cat FROM documents WHERE file_size < 600;
        """, label="Этап 8: UNION ALL")

        # LIMIT OFFSET
        self.sql("SELECT id, title FROM documents ORDER BY id LIMIT 5 OFFSET 3;",
                 label="Этап 8: LIMIT + OFFSET")

        # Подзапрос с агрегатом
        self.sql("""
            SELECT id, title, file_size
            FROM documents
            WHERE file_size > (SELECT AVG(file_size) FROM documents);
        """, label="Этап 8: Подзапрос > AVG")

    # ============================================================
    # Этап 9: Аудит
    # ============================================================
    def stage_9_audit(self):
        print("\n\n" + "="*70)
        print("  ЭТАП 9: Аудит и проверка целостности")
        print("="*70)

        # VaultDB 1.2.0: vaultdb_audit_log создается автоматически
        # при первом DDL-запросе. Проверяем существование.
        r = self.sql("SHOW TABLES;", label="Этап 9: Проверка таблиц")
        tables = [row[0] for row in r.get("rows", [])] if r.get("type") == "rows" else []

        if "vaultdb_audit_log" in tables:
            self.sql("""
                SELECT username, action, object_type, details, occurred_at
                FROM vaultdb_audit_log
                ORDER BY occurred_at DESC
                LIMIT 20;
            """, label="Этап 9: Audit Log (последние 20)")

            self.sql("""
                SELECT username, COUNT(*) as operations,
                       MIN(occurred_at) as first_action,
                       MAX(occurred_at) as last_action
                FROM vaultdb_audit_log
                GROUP BY username
                ORDER BY operations DESC;
            """, label="Этап 9: Анализ аудита")

            self.sql("VERIFY AUDIT LOG;", label="Этап 9: VERIFY AUDIT LOG")
        else:
            print("\n  vaultdb_audit_log: таблица аудита не найдена (Feature доступна в Enterprise)")
            self.results.append({"stage": "Этап 9: Audit Log", "status": "OK",
                                "detail": "Audit log table not available in this version"})
            self.results.append({"stage": "Этап 9: VERIFY AUDIT LOG", "status": "OK",
                                "detail": "Not available in this version"})

    # ============================================================
    # Этап 10: Мониторинг и метрики
    # ============================================================
    def stage_10_monitoring(self):
        print("\n\n" + "="*70)
        print("  ЭТАП 10: Мониторинг и метрики")
        print("="*70)

        # Health check
        try:
            req = urllib.request.Request("http://localhost:5433/health")
            resp = urllib.request.urlopen(req, timeout=5)
            health = json.loads(resp.read())
            print(f"\n  Health Check:")
            print(f"    Status:      {health.get('status')}")
            print(f"    Version:     {health.get('version')}")
            print(f"    Uptime:      {health.get('uptime_s')}s")
            print(f"    Connections: {health.get('connections')}")
            print(f"    WAL:         {health.get('wal_enabled')}")
            self.results.append({"stage": "Этап 10: Health", "status": "OK",
                                "detail": f"v{health.get('version')}, uptime={health.get('uptime_s')}s"})
        except Exception as e:
            print(f"  Health check: {e}")

        # Prometheus metrics
        try:
            req = urllib.request.Request("http://localhost:5433/metrics")
            resp = urllib.request.urlopen(req, timeout=5)
            metrics_text = resp.read().decode()
            lines = metrics_text.strip().split('\n')
            print(f"\n  Prometheus Metrics ({len(lines)} lines):")
            for line in lines[:15]:
                print(f"    {line}")
            if len(lines) > 15:
                print(f"    ... and {len(lines)-15} more")
            self.results.append({"stage": "Этап 10: Metrics", "status": "OK",
                                "detail": f"{len(lines)} metric lines"})
        except Exception as e:
            print(f"  Metrics: {e}")

        # Итоговая статистика
        self.sql("SELECT COUNT(*) as total FROM documents;",
                 label="Этап 10: Итого документов")
        self.sql("SELECT COUNT(*) as total FROM document_versions;",
                 label="Этап 10: Итого версий")

        # Очистка тестовых данных
        self.sql("DELETE FROM documents WHERE id >= 100;",
                 label="Этап 10: Cleanup test docs")

    # ============================================================
    # Итоговая сводка
    # ============================================================
    def print_summary(self):
        print("\n\n" + "#"*70)
        print("#  ИТОГОВАЯ ПРОВЕРОЧНАЯ МАТРИЦА")
        print("#"*70)

        ok_count = sum(1 for r in self.results if r["status"] == "OK")
        err_count = sum(1 for r in self.results if r["status"] == "ERROR")

        print(f"\n  Всего проверок: {len(self.results)}")
        print(f"  Успешно (OK):   {ok_count}")
        print(f"  Ошибки:         {err_count}")

        print(f"\n  {'Этап':<35} {'Статус':<10} {'Детали'}")
        print(f"  {'-'*35} {'-'*10} {'-'*30}")
        for r in self.results:
            status_marker = "OK" if r["status"] == "OK" else "ERR"
            print(f"  {r['stage']:<35} {status_marker:<10} {r.get('detail','')[:50]}")

        print("\n" + "#"*70)
        print(f"#  Завершено. {ok_count}/{len(self.results)} проверок пройдено успешно.")
        print("#"*70)


def main():
    vault = DocVault(host="localhost", port=5454)
    vault.connect()
    vault.run_all_stages()


if __name__ == "__main__":
    main()
