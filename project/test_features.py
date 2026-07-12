#!/usr/bin/env python3
"""Test all VaultDB features on the dev build."""
import subprocess, time, os, sys, shutil

PORT = 5470
HTTP_PORT = 8090
MONITOR_PORT = 5436
DATA_DIR = '/tmp/vaultdb_test_data'

# Fresh data dir
if os.path.exists(DATA_DIR):
    shutil.rmtree(DATA_DIR)
os.makedirs(DATA_DIR)

env = os.environ.copy()
env['VAULTDB_AUTH_ENABLED'] = 'false'
env['VAULTDB_AUTH_SECRET'] = 'test_secret'

proc = subprocess.Popen(
    [os.path.join(os.path.dirname(__file__), '..', 'server', 'vaultdb-server'),
     '--port', str(PORT), '--http-port', str(HTTP_PORT),
     '--monitor-port', str(MONITOR_PORT),
     '--data', DATA_DIR],
    env=env, stdout=subprocess.PIPE, stderr=subprocess.PIPE
)
time.sleep(3)
print(f'Server PID: {proc.pid}')

sys.path.insert(0, os.path.join(os.path.dirname(__file__), '..', 'client', 'python'))
from vaultdb import Client

c = Client('localhost', PORT)
c.connect()

def test(sql, label=''):
    r = c.query(sql)
    s = r.get('status', '?')
    rt = r.get('type', '?')
    msg = r.get('message', '')
    rows = r.get('rows', [])
    ok = 'OK' if s == 'ok' else 'ERR'
    if rt == 'rows':
        print(f'  [{ok}] {label}: {len(rows)} rows')
    else:
        print(f'  [{ok}] {label}: {msg[:70]}')
    return r

# ===== SETUP =====
test('CREATE DATABASE docvault;')
test('USE docvault;')

# ===== 1. DDL =====
print('\n=== 1. DDL ===')
test('CREATE TABLE t1 (id INT PRIMARY KEY, name TEXT NOT NULL);', 'PK + NOT NULL')
test('CREATE TABLE t2 (id INT PRIMARY KEY AUTO_INCREMENT, name TEXT);', 'AUTO_INCREMENT')
test('CREATE TABLE t3 (id SERIAL PRIMARY KEY, name TEXT);', 'SERIAL')
test('CREATE TABLE t4 (id INT, val TEXT, UNIQUE(val));', 'UNIQUE constraint')
test('CREATE INDEX idx_t1 ON t1(name);', 'B-tree INDEX')
test('CREATE UNIQUE INDEX idx_t4 ON t4(val);', 'UNIQUE INDEX')
test('CREATE TABLE t5 (id INT, a INT, b TEXT, PRIMARY KEY(id), UNIQUE(a));', 'PK+UNIQUE')
test('DESCRIBE t1;', 'DESCRIBE')

# ===== 2. DML =====
print('\n=== 2. DML ===')
test("INSERT INTO t1 VALUES (1, 'hello');", 'INSERT')
test("INSERT INTO t1 VALUES (2, 'world');", 'INSERT')
test("INSERT INTO t1 (name) VALUES ('auto');", 'INSERT no PK')
test("INSERT INTO t1 VALUES (10, 'dup1'), (11, 'dup2');", 'MULTI INSERT')
test('SELECT * FROM t1;', 'SELECT')
test("UPDATE t1 SET name = 'UPD' WHERE id = 1;", 'UPDATE')
test('DELETE FROM t1 WHERE id = 2;', 'DELETE')

# ===== 3. UPSERT =====
print('\n=== 3. UPSERT ===')
test("INSERT INTO t1 VALUES (1, 'dup') ON CONFLICT (id) DO UPDATE SET name = 'upserted';", 'UPSERT')

# ===== 4. JSONB =====
print('\n=== 4. JSONB ===')
test('CREATE TABLE tj (id INT PRIMARY KEY, data JSONB);')
test("INSERT INTO tj VALUES (1, '{\"type\":\"contract\",\"amount\":100}');", 'INSERT JSONB')
test("SELECT data->>'type' FROM tj;", 'JSONB ->>')
test("SELECT * FROM tj WHERE data @> '{\"type\":\"contract\"}';", 'JSONB @>')
test("SELECT * FROM tj WHERE data ? 'type';", 'JSONB ?')
test("UPDATE tj SET data = data || '{\"status\":\"active\"}' WHERE id = 1;", 'JSONB || merge')
test("SELECT JSONB_TYPEOF(data) FROM tj;", 'JSONB_TYPEOF')

# ===== 5. FULLTEXT =====
print('\n=== 5. FULLTEXT SEARCH ===')
test('CREATE TABLE tf (id INT PRIMARY KEY, body TEXT, FULLTEXT(body));', 'FULLTEXT index')
test("INSERT INTO tf VALUES (1, 'hello world database engine');", 'INSERT')
test("INSERT INTO tf VALUES (2, 'fast sql query processor');", 'INSERT')
test('SELECT * FROM tf WHERE body MATCH \"database engine\";', 'FTS MATCH')
test('SELECT bm25_score(tf, body) AS s FROM tf WHERE body MATCH \"database\" ORDER BY s DESC;', 'BM25')

# ===== 6. MERGE =====
print('\n=== 6. MERGE ===')
test('CREATE TABLE tgt (id INT PRIMARY KEY, val TEXT);')
test('CREATE TABLE src (id INT PRIMARY KEY, val TEXT);')
test("INSERT INTO src VALUES (1, 'new');", 'INSERT src')
test('MERGE INTO tgt USING src ON tgt.id = src.id WHEN MATCHED THEN UPDATE SET val = src.val WHEN NOT MATCHED THEN INSERT (id, val) VALUES (src.id, src.val);', 'MERGE')
test('SELECT * FROM tgt;', 'verify merge')

# ===== 7. WINDOW =====
print('\n=== 7. WINDOW FUNCTIONS ===')
test('SELECT name, ROW_NUMBER() OVER (ORDER BY id) FROM t1;')
test('SELECT name, RANK() OVER (ORDER BY id) FROM t1;')
test('SELECT name, LAG(name,1) OVER (ORDER BY id) FROM t1;')
test('SELECT name, LEAD(name,1) OVER (ORDER BY id) FROM t1;')

# ===== 8. CTE =====
print('\n=== 8. CTE ===')
test('WITH cte AS (SELECT * FROM t1 WHERE id > 0) SELECT * FROM cte;')

# ===== 9. PARTITION =====
print('\n=== 9. PARTITION BY RANGE ===')
test('CREATE TABLE tp (id INT, ts TIMESTAMP) PARTITION BY RANGE (ts);')

# ===== 10. TRIGGER =====
print('\n=== 10. TRIGGER ===')
test("CREATE TRIGGER trg AFTER INSERT ON t1 BEGIN SELECT 1; END;", 'AFTER INSERT trigger')

# ===== 11. FK =====
print('\n=== 11. FOREIGN KEY ===')
test('CREATE TABLE tfk (id INT PRIMARY KEY, t1_id INT REFERENCES t1(id));', 'FK reference')

# ===== 12. COPY =====
print('\n=== 12. COPY ===')
test("COPY t1 TO '/tmp/t1.csv' WITH (FORMAT CSV, HEADER);", 'COPY TO CSV')
test("COPY t1 TO '/tmp/t1.json' WITH (FORMAT JSON);", 'COPY TO JSON')

# ===== 13. EXPLAIN =====
print('\n=== 13. EXPLAIN ===')
test('EXPLAIN SELECT * FROM t1 WHERE id = 1;', 'EXPLAIN')

# ===== 14. SHOW =====
print('\n=== 14. SHOW ===')
test('SHOW DATABASES;')
test('SHOW TABLES;')
test('SHOW INDEXES ON t1;')

# ===== 15. TRANSACTIONS =====
print('\n=== 15. TRANSACTIONS ===')
test('BEGIN;')
test("INSERT INTO t1 VALUES (100, 'tx1');", 'INSERT in tx')
test('COMMIT;')
test('SELECT * FROM t1 WHERE id = 100;', 'verify commit')
test('BEGIN;')
test("INSERT INTO t1 VALUES (200, 'tx2');", 'INSERT in tx2')
test('ROLLBACK;')
test('SELECT COUNT(*) FROM t1 WHERE id = 200;', 'verify rollback')

# ===== 16. VERIFY AUDIT =====
print('\n=== 16. VERIFY AUDIT ===')
test('VERIFY AUDIT LOG;')

# ===== CLEANUP =====
print('\n=== CLEANUP ===')
for t in ['t1', 't2', 't3', 't4', 't5', 'tj', 'tf', 'tgt', 'src', 'tp', 'tfk']:
    c.query(f'DROP TABLE IF EXISTS {t};')
c.query('DROP DATABASE IF EXISTS docvault;')
c.close()
proc.terminate()
proc.wait()
# Clean up data dir
try:
    shutil.rmtree(DATA_DIR)
except Exception:
    pass
print('\nALL TESTS COMPLETE!')
