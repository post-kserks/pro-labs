#!/usr/bin/env python3
"""Setup DocVault database on dev-branch VaultDB."""
import subprocess, time, os, sys, shutil

PORT = 5454
DATA_DIR = '/tmp/vaultdb_docvault'

if os.path.exists(DATA_DIR):
    shutil.rmtree(DATA_DIR)
os.makedirs(DATA_DIR)

env = os.environ.copy()
env['VAULTDB_AUTH_ENABLED'] = 'false'
env['VAULTDB_AUTH_SECRET'] = 'test'

proc = subprocess.Popen(
    [os.path.join('..', 'server', 'vaultdb-server'),
     '--port', str(PORT), '--data', DATA_DIR],
    env=env, stdout=subprocess.PIPE, stderr=subprocess.PIPE
)
time.sleep(4)

sys.path.insert(0, '../client/python')
from vaultdb import Client

c = Client('localhost', PORT)
c.connect()

def q(sql):
    r = c.query(sql)
    if r.get('status') != 'ok':
        print(f"  ERR: {sql[:60]} -> {r.get('message','')[:60]}")
    return r

# === DATABASE ===
q('CREATE DATABASE docvault;')
q('USE docvault;')

# === TABLES ===
q("""CREATE TABLE documents (
    id INT PRIMARY KEY AUTO_INCREMENT,
    doc_number TEXT,
    title TEXT,
    content TEXT,
    created_at TIMESTAMP,
    status TEXT,
    department TEXT,
    author TEXT,
    file_size INT
);""")

q("""CREATE TABLE document_versions (
    id INT PRIMARY KEY AUTO_INCREMENT,
    doc_id INT,
    version_number INT,
    content_hash TEXT,
    changes_description TEXT,
    changed_by TEXT,
    changed_at TIMESTAMP
);""")

# === CONSTRAINTS via ALTER TABLE ===
q("ALTER TABLE documents ADD CONSTRAINT uq_doc_number UNIQUE (doc_number);")
q("ALTER TABLE document_versions ADD CONSTRAINT fk_doc_id FOREIGN KEY (doc_id) REFERENCES documents(id) ON DELETE CASCADE;")

# === INDEXES ===
for col in ['department', 'status', 'created_at']:
    q(f"CREATE INDEX idx_docs_{col} ON documents({col});")

# === GIN on JSONB ===
q("CREATE TABLE configs (id INT PRIMARY KEY, settings JSONB);")
q("CREATE INDEX idx_configs ON configs USING GIN (settings);")

# === DATA: documents ===
docs = [
    (1, 'DOC-2024-001', 'Договор поставки 123', 'Полный текст договора о поставке оборудования ООО Ромашка на сумму 1250000 руб.', '2024-01-15T10:00:00Z', 'active', 'legal', 'Анна Петрова', 1024),
    (2, 'DOC-2024-002', 'Финансовый отчет Q1', 'Отчет о прибылях и убытках за первый квартал 2024 года. Выручка 4500000 руб.', '2024-04-01T14:30:00Z', 'active', 'finance', 'Иван Сидоров', 2048),
    (3, 'DOC-2024-003', 'Служебная записка', 'Текст служебной записки о командировке в Москву для встречи с партнерами.', '2024-02-10T09:15:00Z', 'active', 'hr', 'Мария Иванова', 512),
    (4, 'DOC-2024-004', 'Техническое задание', 'Подробное техническое задание на разработку системы Alpha.', '2024-03-20T11:00:00Z', 'active', 'it', 'Сергей Смирнов', 4096),
    (5, 'DOC-2024-005', 'Договор аренды', 'Договор аренды офисных помещений на ул. Пушкина. Стоимость 500000 руб/год.', '2024-05-01T16:45:00Z', 'active', 'legal', 'Анна Петрова', 768),
    (6, 'DOC-2024-006', 'Отчет о движении средств', 'Ежемесячный отчет о движении денежных средств за март 2024 года.', '2024-04-05T13:00:00Z', 'active', 'finance', 'Иван Сидоров', 3072),
    (7, 'DOC-2024-007', 'Заявка на закупку', 'Заявка на закупку серверного оборудования для центра обработки данных.', '2024-06-15T10:30:00Z', 'active', 'it', 'Сергей Смирнов', 256),
    (8, 'DOC-2024-008', 'Протокол совещания', 'Протокол совещания по проекту Alpha. Участвовали 8 человек.', '2024-07-01T15:00:00Z', 'active', 'hr', 'Мария Иванова', 1280),
    (9, 'DOC-2024-009', 'Договор подряда', 'Договор подряда на строительные работы. Сумма 3500000 руб.', '2024-08-10T09:00:00Z', 'active', 'legal', 'Анна Петрова', 1536),
    (10, 'DOC-2024-010', 'Аудиторское заключение', 'Заключение по результатам годового аудита за 2024 финансовый год.', '2024-09-20T14:00:00Z', 'active', 'finance', 'Иван Сидоров', 4096),
    (11, 'DOC-2024-011', 'Служебная записка 2', 'Записка о перераспределении ресурсов между отделами.', '2024-03-01T08:00:00Z', 'active', 'hr', 'Мария Иванова', 384),
    (12, 'DOC-2024-012', 'Техническая документация', 'Документация по API интеграции версии 2.0.', '2024-06-01T12:00:00Z', 'active', 'it', 'Сергей Смирнов', 8192),
    (13, 'DOC-2024-013', 'Гражданский договор', 'Договор на оказание консультационных услуг. Сумма 180000 руб.', '2024-07-15T10:00:00Z', 'active', 'legal', 'Анна Петрова', 640),
    (14, 'DOC-2024-014', 'Бюджетный план', 'План бюджета на следующий финансовый год. Общий бюджет 50 млн руб.', '2024-10-01T09:00:00Z', 'active', 'finance', 'Иван Сидоров', 2048),
    (15, 'DOC-2024-015', 'Новый контракт', 'Контракт на поставку ПО для отдела IT. Сумма 250000 руб.', '2024-11-01T14:00:00Z', 'active', 'legal', 'Анна Петрова', 512),
]
for d in docs:
    q(f"INSERT INTO documents (id, doc_number, title, content, created_at, status, department, author, file_size) VALUES ({d[0]}, '{d[1]}', '{d[2]}', '{d[3]}', '{d[4]}', '{d[5]}', '{d[6]}', '{d[7]}', {d[8]});")

# === DATA: versions ===
versions = [
    (1, 1, 1, 'abc123', 'Первая версия договора', 'Анна Петрова', '2024-01-15T10:00:00Z'),
    (2, 1, 2, 'def456', 'Обновление условий оплаты', 'Анна Петрова', '2024-01-20T14:00:00Z'),
    (3, 1, 3, 'ghi789', 'Финальная версия', 'Анна Петрова', '2024-01-25T11:00:00Z'),
    (4, 2, 1, 'stu012', 'Первоначальный отчет', 'Иван Сидоров', '2024-04-01T14:30:00Z'),
    (5, 2, 2, 'yza345', 'Добавлены данные за Q1', 'Иван Сидоров', '2024-04-05T16:00:00Z'),
    (6, 4, 1, 'efg678', 'ТЗ версия 1.0', 'Сергей Смирнов', '2024-03-20T11:00:00Z'),
    (7, 4, 2, 'klm901', 'ТЗ версия 2.0', 'Сергей Смирнов', '2024-04-10T15:00:00Z'),
]
for v in versions:
    q(f"INSERT INTO document_versions (id, doc_id, version_number, content_hash, changes_description, changed_by, changed_at) VALUES ({v[0]}, {v[1]}, {v[2]}, '{v[3]}', '{v[4]}', '{v[5]}', '{v[6]}');")

# === DATA: configs (JSONB) ===
configs = [
    (1, '{"theme":"dark","lang":"ru"}'),
    (2, '{"notifications":true,"timeout":30}'),
]
for cf in configs:
    q(f"INSERT INTO configs VALUES ({cf[0]}, '{cf[1]}');")

# Verify
r = q("SELECT COUNT(*) FROM documents;")
print(f"\nDocuments: {r.get('rows', [['?']])[0][0]}")
r = q("SELECT COUNT(*) FROM document_versions;")
print(f"Versions: {r.get('rows', [['?']])[0][0]}")

c.close()
proc.terminate()
proc.wait()

print(f"\nDatabase ready at {DATA_DIR}")
print(f"Connect: Client('localhost', {PORT})")
