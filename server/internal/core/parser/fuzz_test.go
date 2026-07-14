package parser

import (
	"fmt"
	"math/rand"
	"strings"
	"testing"
)

// generateRandomSQL returns a random SQL string from templates.
func generateRandomSQL(rng *rand.Rand) string {
	templates := []func(rng *rand.Rand) string{
		// SELECT with random columns and tables
		func(rng *rand.Rand) string {
			cols := []string{"*", "a", "a, b", "a, b, c", "COUNT(*)", "DISTINCT a"}
			tables := []string{"t", "users", "my_table", `"table"`, "`col`"}
			wheres := []string{"", "WHERE a = 1", "WHERE a > 1 AND b < 2", "WHERE a IN (1,2,3)", "WHERE a LIKE '%foo%'", "WHERE NOT a = 1", "WHERE a BETWEEN 1 AND 10", "WHERE EXISTS (SELECT 1 FROM t2)"}
			orders := []string{"", "ORDER BY a", "ORDER BY a DESC, b ASC"}
			limits := []string{"", "LIMIT 10", "LIMIT 10 OFFSET 5"}
			return fmt.Sprintf("SELECT %s FROM %s %s %s %s",
				cols[rng.Intn(len(cols))],
				tables[rng.Intn(len(tables))],
				wheres[rng.Intn(len(wheres))],
				orders[rng.Intn(len(orders))],
				limits[rng.Intn(len(limits))])
		},
		// JOIN queries
		func(rng *rand.Rand) string {
			joinTypes := []string{"JOIN", "INNER JOIN", "LEFT JOIN", "RIGHT JOIN", "FULL JOIN", "CROSS JOIN"}
			return fmt.Sprintf("SELECT * FROM t1 %s t2 ON t1.id = t2.id", joinTypes[rng.Intn(len(joinTypes))])
		},
		// INSERT with random row counts
		func(rng *rand.Rand) string {
			rows := rng.Intn(5) + 1
			vals := make([]string, rows)
			for i := range vals {
				cols := []string{"1, 'a'", "2, 'b'", "3, 'c'"}
				vals[i] = fmt.Sprintf("(%s)", cols[rng.Intn(len(cols))])
			}
			conflict := []string{"", " ON CONFLICT DO NOTHING", " ON CONFLICT (id) DO UPDATE SET a = EXCLUDED.a"}
			return fmt.Sprintf("INSERT INTO t (id, name) VALUES %s%s",
				strings.Join(vals, ", "),
				conflict[rng.Intn(len(conflict))])
		},
		// UPDATE with random WHERE
		func(rng *rand.Rand) string {
			wheres := []string{"WHERE id = 1", "WHERE id > 0 AND name = 'x'", "WHERE id IN (1,2,3)", ""}
			return fmt.Sprintf("UPDATE t SET a = 1, b = 'two' %s", wheres[rng.Intn(len(wheres))])
		},
		// DELETE with random WHERE
		func(rng *rand.Rand) string {
			wheres := []string{"WHERE id = 1", "WHERE id > 0", "WHERE id IN (SELECT id FROM t2)", ""}
			return fmt.Sprintf("DELETE FROM t %s", wheres[rng.Intn(len(wheres))])
		},
		// MERGE
		func(rng *rand.Rand) string {
			actions := []string{
				"WHEN MATCHED THEN UPDATE SET a = s.a",
				"WHEN NOT MATCHED THEN INSERT (id) VALUES (s.id)",
				"WHEN MATCHED AND s.a > 1 THEN DELETE",
			}
			return fmt.Sprintf("MERGE INTO t USING s ON t.id = s.id %s", actions[rng.Intn(len(actions))])
		},
		// Window functions
		func(rng *rand.Rand) string {
			funcs := []string{"ROW_NUMBER()", "RANK()", "DENSE_RANK()", "SUM(a) OVER", "COUNT(*) OVER"}
			over := []string{
				"(PARTITION BY a ORDER BY b)",
				"(ORDER BY a)",
				"(PARTITION BY a ORDER BY b ROWS BETWEEN UNBOUNDED PRECEDING AND CURRENT ROW)",
				"(PARTITION BY a ORDER BY b RANGE BETWEEN UNBOUNDED PRECEDING AND UNBOUNDED FOLLOWING)",
			}
			return fmt.Sprintf("SELECT %s %s AS rn FROM t", funcs[rng.Intn(len(funcs))], over[rng.Intn(len(over))])
		},
		// CTEs
		func(rng *rand.Rand) string {
			return fmt.Sprintf("WITH cte AS (SELECT id FROM t WHERE id > %d) SELECT * FROM cte LIMIT %d",
				rng.Intn(100), rng.Intn(50))
		},
		// Set operations
		func(rng *rand.Rand) string {
			ops := []string{"UNION", "UNION ALL", "INTERSECT", "EXCEPT"}
			return fmt.Sprintf("SELECT a FROM t1 %s SELECT a FROM t2", ops[rng.Intn(len(ops))])
		},
		// DDL
		func(rng *rand.Rand) string {
			types := []string{"INT", "FLOAT", "BOOL", "TEXT", "VARCHAR(255)", "DATE", "TIMESTAMPTZ", "JSONB", "BLOB", "BIGINT", "NUMERIC(10,2)", "VECTOR(128)"}
			return fmt.Sprintf("CREATE TABLE IF NOT EXISTS t_%d (id %s PRIMARY KEY, name TEXT NOT NULL, data JSONB)",
				rng.Intn(10000), types[rng.Intn(len(types))])
		},
		// SHOW statements
		func(rng *rand.Rand) string {
			stmts := []string{"SHOW DATABASES", "SHOW TABLES", "SHOW ENCRYPTION STATUS", "SHOW INDEXES", "SHOW INDEXES FROM t"}
			return stmts[rng.Intn(len(stmts))]
		},
		// Transaction statements
		func(rng *rand.Rand) string {
			stmts := []string{"BEGIN", "BEGIN TRANSACTION", "COMMIT", "ROLLBACK", "SAVEPOINT sp1", "ROLLBACK TO SAVEPOINT sp1", "RELEASE SAVEPOINT sp1"}
			return stmts[rng.Intn(len(stmts))]
		},
		// Expression parsing
		func(rng *rand.Rand) string {
			exprs := []string{
				"1 + 2 * 3",
				"CASE WHEN a > 1 THEN 'big' ELSE 'small' END",
				"COALESCE(a, b, 0)",
				"CAST(a AS INT)",
				"a->'key'",
				"a->>'key'",
				"t.col @> '{\"a\": 1}'::JSONB",
				"t.col ? 'key'",
			}
			return fmt.Sprintf("SELECT %s FROM t", exprs[rng.Intn(len(exprs))])
		},
		// Time travel
		func(rng *rand.Rand) string {
			return "SELECT * FROM t AS OF TIMESTAMP '2024-01-01T00:00:00Z'"
		},
		// Prepared statements
		func(rng *rand.Rand) string {
			stmts := []string{
				"PREPARE q AS SELECT * FROM t WHERE id = $1",
				"EXECUTE q USING 1",
				"DEALLOCATE q",
			}
			return stmts[rng.Intn(len(stmts))]
		},
		// Complex WHERE combinations
		func(rng *rand.Rand) string {
			return fmt.Sprintf("SELECT * FROM t WHERE (a > 1 OR b < 2) AND c IS NOT NULL AND d IN (1,2,3) AND e BETWEEN 10 AND 20 AND f LIKE '%%test%%' AND EXISTS (SELECT 1 FROM t2 WHERE t2.id = t.id)")
		},
		// CREATE other DDL types
		func(rng *rand.Rand) string {
			kinds := []string{
				"CREATE VIEW v AS SELECT id FROM t",
				"CREATE INDEX idx ON t (a, b)",
				"CREATE TRIGGER tr BEFORE INSERT ON t FOR EACH ROW EXECUTE FUNCTION f()",
				"CREATE FUNCTION f() RETURNS INT AS $$ BEGIN RETURN 1; END $$ LANGUAGE plpgsql",
				"CREATE PROCEDURE p() AS $$ BEGIN NULL; END $$ LANGUAGE plpgsql",
			}
			return kinds[rng.Intn(len(kinds))]
		},
		// UPSERT
		func(rng *rand.Rand) string {
			return "INSERT INTO t (id, val) VALUES (1, 'a') ON CONFLICT (id) DO UPDATE SET val = EXCLUDED.val"
		},
		// EXPLAIN
		func(rng *rand.Rand) string {
			return "EXPLAIN SELECT * FROM t WHERE id = 1"
		},
		// VACUUM and ANALYZE
		func(rng *rand.Rand) string {
			stmts := []string{"VACUUM", "ANALYZE"}
			return stmts[rng.Intn(len(stmts))]
		},
		// HISTORY (time travel)
		func(rng *rand.Rand) string {
			return "HISTORY FOR TABLE t"
		},
		// DESCRIBE
		func(rng *rand.Rand) string {
			stmts := []string{"DESCRIBE t", "DESC t"}
			return stmts[rng.Intn(len(stmts))]
		},
		// CALL procedure
		func(rng *rand.Rand) string {
			return "CALL my_proc(1, 'arg')"
		},
		// JSON operators in WHERE
		func(rng *rand.Rand) string {
			return "SELECT * FROM t WHERE data->>'key' = 'value'"
		},
		// Aggregate functions
		func(rng *rand.Rand) string {
			funcs := []string{"COUNT(*)", "SUM(a)", "AVG(b)", "MIN(c)", "MAX(d)", "COUNT(DISTINCT a)"}
			return fmt.Sprintf("SELECT %s FROM t GROUP BY a HAVING %s > 1",
				funcs[rng.Intn(len(funcs))], funcs[rng.Intn(len(funcs))])
		},
		// CREATE/DROP DATABASE
		func(rng *rand.Rand) string {
			stmts := []string{"CREATE DATABASE mydb", "CREATE DATABASE IF NOT EXISTS mydb", "DROP DATABASE mydb", "DROP DATABASE IF EXISTS mydb", "USE mydb"}
			return stmts[rng.Intn(len(stmts))]
		},
		// ALTER TABLE actions
		func(rng *rand.Rand) string {
			actions := []string{
				"ALTER TABLE t ADD COLUMN c INT",
				"ALTER TABLE t DROP COLUMN c",
				"ALTER TABLE t RENAME COLUMN a TO b",
				"ALTER TABLE t RENAME TO t2",
				"ALTER TABLE t ADD CONSTRAINT pk PRIMARY KEY (id)",
			}
			return actions[rng.Intn(len(actions))]
		},
		// TRUNCATE
		func(rng *rand.Rand) string {
			stmts := []string{"TRUNCATE t", "TRUNCATE TABLE t"}
			return stmts[rng.Intn(len(stmts))]
		},
		// ENABLE RLS
		func(rng *rand.Rand) string {
			return "ENABLE RLS ON t"
		},
		// CREATE POLICY
		func(rng *rand.Rand) string {
			return "CREATE POLICY p ON t FOR SELECT USING (true)"
		},
		// Comparison subquery
		func(rng *rand.Rand) string {
			quants := []string{"ALL", "ANY", "SOME"}
			ops := []string{">", "<", ">=", "<=", "=", "!="}
			return fmt.Sprintf("SELECT * FROM t WHERE a %s (SELECT MAX(b) FROM t2) %s 1",
				ops[rng.Intn(len(ops))], quants[rng.Intn(len(quants))])
		},
	}

	return templates[rng.Intn(len(templates))](rng)
}

// generateMalformedSQL returns randomly corrupted SQL.
func generateMalformedSQL(rng *rand.Rand) string {
	bases := []string{
		"SELECT * FROM",
		"INSERT INTO t VALUES",
		"UPDATE t SET",
		"DELETE FROM",
		"WHERE a =",
		"CREATE TABLE",
		"(",
		")",
		"'",
		"/*",
		"*/",
		"SELECT FROM FROM FROM",
		"JOIN JOIN JOIN",
		"AND AND AND OR OR",
	}
	base := bases[rng.Intn(len(bases))]
	// Randomly append noise
	noise := []string{
		"\x00",
		"\xff\xfe",
		string([]byte{0x00, 0x01, 0x02, 0xff}),
		"😀🎉",
		strings.Repeat("a", rng.Intn(500)),
		"123456789",
		"true false null",
		"$999999",
		"@@##%%",
	}
	return base + noise[rng.Intn(len(noise))]
}

func FuzzParseSQL(f *testing.F) {
	// --- Basic seed corpus ---
	f.Add("SELECT * FROM users;")
	f.Add("CREATE TABLE t (id INT);")
	f.Add("INSERT INTO t VALUES (1);")
	f.Add("UPDATE t SET id = 1;")
	f.Add("DELETE FROM t;")
	f.Add("DROP TABLE t;")
	f.Add("ALTER TABLE t ADD COLUMN c INT;")
	f.Add("CREATE INDEX idx ON t (c);")
	f.Add("BEGIN TRANSACTION;")
	f.Add("COMMIT;")
	f.Add("SELECT a, b FROM t WHERE a > 1 ORDER BY b;")
	f.Add("SELECT COUNT(*) FROM t GROUP BY a;")
	f.Add("SELECT * FROM t1 JOIN t2 ON t1.id = t2.id;")
	f.Add("SELECT * FROM t LIMIT 10 OFFSET 5;")

	// --- Empty / whitespace / null bytes ---
	f.Add("")
	f.Add("   ")
	f.Add("\t\n\r")
	f.Add("\x00")
	f.Add("\x00\x00\x00")
	f.Add(" \x00 ")
	f.Add(string([]byte{0xff, 0xfe}))

	// --- Very long identifiers ---
	f.Add("SELECT " + strings.Repeat("a", 10000) + " FROM t;")
	f.Add("SELECT * FROM " + strings.Repeat("tbl_", 5000) + ";")
	f.Add("SELECT " + strings.Repeat("col_", 2500) + ", b FROM t;")

	// --- Nested subqueries 10+ levels deep ---
	{
		q := "SELECT * FROM t"
		for i := 0; i < 15; i++ {
			q = fmt.Sprintf("SELECT * FROM (%s) AS sub%d", q, i)
		}
		f.Add(q + ";")
	}

	// --- Unicode / emoji in identifiers and strings ---
	f.Add("SELECT * FROM 用户 WHERE 名前 = 'テスト';")
	f.Add("SELECT '🎉' AS emoji_col FROM t;")
	f.Add("SELECT * FROM t WHERE a = 'Hello 🌍 World!';")
	f.Add("SELECT 'café' FROM t;")
	f.Add("SELECT * FROM \"t\" WHERE \"col\" = 'über';")
	f.Add("SELECT * FROM t WHERE a = '\u0000null';")
	f.Add("SELECT '😀' UNION SELECT '😎';")

	// --- SQL keyword combinations ---
	f.Add("SELECT DISTINCT a FROM t UNION ALL SELECT DISTINCT b FROM t2 INTERSECT SELECT c FROM t3 EXCEPT SELECT d FROM t4;")
	f.Add("SELECT a FROM t UNION SELECT b FROM t2 EXCEPT SELECT c FROM t3 INTERSECT SELECT d FROM t4;")

	// --- Malformed but plausible SQL ---
	f.Add("SELECT * FROM t WHERE a = 'unclosed")
	f.Add("SELECT * FROM t WHERE a = )")
	f.Add("SELECT * FROM (SELECT * FROM t;")
	f.Add("SELECT * FROM t WHERE (a > 1 AND b < 2;")
	f.Add("SELECT * FROM t GROUP BY a HAVING COUNT(*) > ;")
	f.Add("SELECT * FROM t ORDER BY ;")
	f.Add("INSERT INTO t () VALUES ();")
	f.Add("UPDATE t SET = 1;")
	f.Add("DELETE FROM ;")
	f.Add("CREATE TABLE ();")
	f.Add("SELECT FROM t;")
	f.Add("SELECT WHERE a = 1;")
	f.Add("FROM SELECT t;")
	f.Add("SELECT t SELECT * FROM t;")
	f.Add("SELECT * FROM t WHERE a = '' AND b = '';")

	// --- Binary data in strings ---
	f.Add("SELECT * FROM t WHERE a = '" + string([]byte{0x00, 0x01, 0x02, 0x03}) + "';")
	f.Add("SELECT * FROM t WHERE a = '" + strings.Repeat("\xff", 200) + "';")
	f.Add("SELECT * FROM t WHERE a = '" + string([]byte{0x08, 0x09, 0x0a, 0x0d}) + "';")

	// --- Numeric edge cases ---
	f.Add("SELECT 9223372036854775807 FROM t;")     // MAX_INT64
	f.Add("SELECT -9223372036854775808 FROM t;")    // MIN_INT64
	f.Add("SELECT 1e308 FROM t;")                   // large scientific
	f.Add("SELECT 1e-308 FROM t;")                  // small scientific
	f.Add("SELECT 1.7976931348623157e+308 FROM t;") // near max float64
	f.Add("SELECT 0.0 FROM t;")
	f.Add("SELECT -0.0 FROM t;")
	f.Add("SELECT 99999999999999999999 FROM t;") // overflow
	f.Add("SELECT .5 FROM t;")
	f.Add("SELECT 1. FROM t;")
	f.Add("SELECT 0x1F FROM t;")

	// --- Comments in various positions ---
	f.Add("SELECT /* comment */ * FROM t;")
	f.Add("/* leading */ SELECT * FROM t;")
	f.Add("SELECT * FROM t /* mid */ WHERE a = 1;")
	f.Add("SELECT * FROM t; -- trailing comment")
	f.Add("SELECT -- inline\n* FROM t;")
	f.Add("SELECT * FROM /* block\ncomment */ t;")
	f.Add("-- entire line\nSELECT 1;")

	// --- Multiple statements (should error, not panic) ---
	f.Add("SELECT 1; SELECT 2;")
	f.Add("SELECT 1; DROP TABLE t; SELECT 3;")
	f.Add(";;;")
	f.Add("; ; ;")

	// --- All DDL types ---
	f.Add("CREATE DATABASE testdb;")
	f.Add("CREATE DATABASE IF NOT EXISTS testdb;")
	f.Add("DROP DATABASE testdb;")
	f.Add("CREATE TABLE t1 (id INT PRIMARY KEY, name TEXT NOT NULL, created_at TIMESTAMPTZ DEFAULT NOW());")
	f.Add("CREATE TABLE t2 (id SERIAL, data JSONB, tags ARRAY, score DECIMAL(10,2));")
	f.Add("CREATE TABLE t3 (id INT, val VECTOR(128));")
	f.Add("CREATE TABLE t4 (id INT, enc_col TEXT ENCRYPTED);")
	f.Add("DROP TABLE IF EXISTS t1;")
	f.Add("CREATE INDEX idx1 ON t1 (name);")
	f.Add("CREATE INDEX IF NOT EXISTS idx2 ON t1 (name, created_at DESC);")
	f.Add("DROP INDEX idx1;")
	f.Add("DROP INDEX IF EXISTS idx2;")
	f.Add("SHOW INDEXES;")
	f.Add("SHOW INDEXES FROM t1;")
	f.Add("CREATE VIEW v1 AS SELECT id, name FROM t1;")
	f.Add("CREATE OR REPLACE VIEW v1 AS SELECT id FROM t1;")
	f.Add("DROP VIEW v1;")
	f.Add("DROP VIEW IF EXISTS v1;")
	f.Add("CREATE TRIGGER trg BEFORE INSERT ON t1 FOR EACH ROW EXECUTE FUNCTION fn();")
	f.Add("CREATE TRIGGER trg AFTER UPDATE ON t1 FOR EACH ROW EXECUTE FUNCTION fn();")
	f.Add("DROP TRIGGER IF EXISTS trg ON t1;")
	f.Add("CREATE FUNCTION my_func(a INT, b TEXT) RETURNS TEXT AS $$ BEGIN RETURN b; END $$ LANGUAGE plpgsql;")
	f.Add("DROP FUNCTION IF EXISTS my_func;")
	f.Add("CREATE PROCEDURE my_proc(a INT) AS $$ BEGIN NULL; END $$ LANGUAGE plpgsql;")
	f.Add("DROP PROCEDURE IF EXISTS my_proc;")
	f.Add("CALL my_proc(42);")

	// --- INSERT with multiple value rows ---
	f.Add("INSERT INTO t (a, b, c) VALUES (1, 'x', true), (2, 'y', false), (3, 'z', NULL);")
	f.Add("INSERT INTO t VALUES (1), (2), (3), (4), (5), (6), (7), (8), (9), (10);")
	f.Add("INSERT INTO t (a) VALUES ($1);")

	// --- Complex WHERE clauses ---
	f.Add("SELECT * FROM t WHERE a > 1 AND b < 2 AND c >= 3 AND d <= 4 AND e = 5 AND f != 6;")
	f.Add("SELECT * FROM t WHERE a = 1 OR b = 2 OR c = 3;")
	f.Add("SELECT * FROM t WHERE NOT a = 1;")
	f.Add("SELECT * FROM t WHERE a IN (1, 2, 3, 4, 5);")
	f.Add("SELECT * FROM t WHERE a NOT IN (SELECT id FROM t2);")
	f.Add("SELECT * FROM t WHERE EXISTS (SELECT 1 FROM t2 WHERE t2.id = t.id);")
	f.Add("SELECT * FROM t WHERE NOT EXISTS (SELECT 1 FROM t2);")
	f.Add("SELECT * FROM t WHERE a BETWEEN 1 AND 10;")
	f.Add("SELECT * FROM t WHERE a NOT BETWEEN 1 AND 10;")
	f.Add("SELECT * FROM t WHERE a LIKE '%pattern%';")
	f.Add("SELECT * FROM t WHERE a ILIKE '%PATTERN%';")
	f.Add("SELECT * FROM t WHERE a IS NULL;")
	f.Add("SELECT * FROM t WHERE a IS NOT NULL;")
	f.Add("SELECT * FROM t WHERE (a > 1 OR b < 2) AND (c = 3 OR d = 4);")
	f.Add("SELECT * FROM t WHERE a > ANY (SELECT id FROM t2);")
	f.Add("SELECT * FROM t WHERE a > ALL (SELECT id FROM t2);")

	// --- JOIN types ---
	f.Add("SELECT * FROM t1 INNER JOIN t2 ON t1.id = t2.id;")
	f.Add("SELECT * FROM t1 LEFT JOIN t2 ON t1.id = t2.id;")
	f.Add("SELECT * FROM t1 RIGHT JOIN t2 ON t1.id = t2.id;")
	f.Add("SELECT * FROM t1 FULL JOIN t2 ON t1.id = t2.id;")
	f.Add("SELECT * FROM t1 CROSS JOIN t2;")
	f.Add("SELECT * FROM t1 JOIN t2 USING (id);")
	f.Add("SELECT * FROM t1 LEFT JOIN t2 ON t1.id = t2.id INNER JOIN t3 ON t2.id = t3.id;")
	f.Add("SELECT * FROM t1 AS a INNER JOIN t2 AS b ON a.id = b.id;")

	// --- Window functions ---
	f.Add("SELECT ROW_NUMBER() OVER (ORDER BY id) FROM t;")
	f.Add("SELECT RANK() OVER (PARTITION BY dept ORDER BY salary DESC) FROM t;")
	f.Add("SELECT DENSE_RANK() OVER (PARTITION BY dept ORDER BY salary) FROM t;")
	f.Add("SELECT SUM(amount) OVER (PARTITION BY dept ORDER BY date ROWS BETWEEN UNBOUNDED PRECEDING AND CURRENT ROW) FROM t;")
	f.Add("SELECT COUNT(*) OVER (PARTITION BY a ORDER BY b RANGE BETWEEN UNBOUNDED PRECEDING AND UNBOUNDED FOLLOWING) FROM t;")
	f.Add("SELECT LAG(a) OVER (ORDER BY b) FROM t;")
	f.Add("SELECT LEAD(a) OVER (ORDER BY b) FROM t;")

	// --- CTEs ---
	f.Add("WITH cte AS (SELECT id FROM t WHERE id > 10) SELECT * FROM cte;")
	f.Add("WITH cte1 AS (SELECT id FROM t), cte2 AS (SELECT id FROM t2) SELECT * FROM cte1 JOIN cte2 ON cte1.id = cte2.id;")
	f.Add("WITH RECURSIVE cte AS (SELECT 1 AS n UNION ALL SELECT n + 1 FROM cte WHERE n < 10) SELECT * FROM cte;")

	// --- SHOW statements ---
	f.Add("SHOW DATABASES;")
	f.Add("SHOW TABLES;")
	f.Add("SHOW ENCRYPTION STATUS;")
	f.Add("SHOW INDEXES;")
	f.Add("SHOW INDEXES FROM users;")

	// --- ENCRYPTION-related syntax ---
	f.Add("CREATE TABLE t (id INT, secret TEXT ENCRYPTED);")
	f.Add("SHOW ENCRYPTION STATUS;")
	f.Add("ALTER TABLE t ADD COLUMN enc_col TEXT ENCRYPTED;")

	// --- UPSERT / MERGE syntax ---
	f.Add("INSERT INTO t (id, val) VALUES (1, 'a') ON CONFLICT DO NOTHING;")
	f.Add("INSERT INTO t (id, val) VALUES (1, 'a') ON CONFLICT (id) DO UPDATE SET val = EXCLUDED.val;")
	f.Add("MERGE INTO t USING s ON t.id = s.id WHEN MATCHED THEN UPDATE SET a = s.a;")
	f.Add("MERGE INTO t USING s ON t.id = s.id WHEN NOT MATCHED THEN INSERT (id, a) VALUES (s.id, s.a);")
	f.Add("MERGE INTO t USING s ON t.id = s.id WHEN MATCHED AND s.del = true THEN DELETE;")

	// --- Time Travel syntax ---
	f.Add("SELECT * FROM t AS OF TIMESTAMP '2024-01-01T00:00:00Z';")
	f.Add("SELECT * FROM t AS OF VERSION 42;")
	f.Add("SELECT * FROM t VERSION 10;")
	f.Add("HISTORY FOR TABLE t;")

	// --- EXPLAIN ---
	f.Add("EXPLAIN SELECT * FROM t;")
	f.Add("EXPLAIN ANALYZE SELECT * FROM t;")

	// --- VACUUM / ANALYZE ---
	f.Add("VACUUM;")
	f.Add("ANALYZE;")

	// --- DESCRIBE ---
	f.Add("DESCRIBE t;")
	f.Add("DESC t;")

	// --- Prepared statement lifecycle ---
	f.Add("PREPARE q AS SELECT * FROM t WHERE id = $1;")
	f.Add("EXECUTE q USING 42;")
	f.Add("DEALLOCATE q;")
	f.Add("DEALLOCATE ALL;")

	// --- CASE expressions ---
	f.Add("SELECT CASE WHEN a > 1 THEN 'big' WHEN a > 0 THEN 'medium' ELSE 'small' END FROM t;")
	f.Add("SELECT CASE a WHEN 1 THEN 'one' WHEN 2 THEN 'two' ELSE 'other' END FROM t;")

	// --- CAST ---
	f.Add("SELECT CAST(a AS INT) FROM t;")
	f.Add("SELECT CAST('123' AS FLOAT) FROM t;")

	// --- COALESCE / NVL ---
	f.Add("SELECT COALESCE(a, b, c, 0) FROM t;")

	// --- JSON operators ---
	f.Add("SELECT data->'key' FROM t;")
	f.Add("SELECT data->>'key' FROM t;")
	f.Add("SELECT * FROM t WHERE data @> '{\"a\": 1}';")
	f.Add("SELECT * FROM t WHERE data <@ '{\"a\": 1}';")
	f.Add("SELECT * FROM t WHERE data ? 'key';")
	f.Add("SELECT data || '{\"b\": 2}' FROM t;")
	f.Add("SELECT * FROM t WHERE data @@ 'query';")

	// --- Parameter references ---
	f.Add("SELECT * FROM t WHERE id = $1 AND name = $2;")
	f.Add("SELECT $1 + $2;")

	// --- DROP statements ---
	f.Add("DROP TABLE IF EXISTS t CASCADE;")
	f.Add("DROP VIEW IF EXISTS v;")
	f.Add("DROP INDEX IF EXISTS idx;")
	f.Add("DROP FUNCTION IF EXISTS fn;")
	f.Add("DROP PROCEDURE IF EXISTS proc;")
	f.Add("DROP TRIGGER IF EXISTS trg ON t;")

	// --- Schema and migration ---
	f.Add("CREATE SCHEMA myschema;")
	f.Add("CREATE MIGRATION m1;")

	// --- ENABLE RLS ---
	f.Add("ENABLE RLS ON t;")

	// --- CREATE POLICY ---
	f.Add("CREATE POLICY p ON t FOR SELECT USING (true);")
	f.Add("CREATE POLICY p ON t FOR INSERT WITH CHECK (true);")

	// --- Interval and special values ---
	f.Add("SELECT INTERVAL '1 day';")
	f.Add("SELECT UUID();")

	// --- Subquery in SELECT ---
	f.Add("SELECT a, (SELECT MAX(b) FROM t2) FROM t;")

	// --- LATERAL joins ---
	f.Add("SELECT * FROM t1, LATERAL (SELECT * FROM t2 WHERE t2.id = t1.id) sub;")

	// --- Subquery in FROM ---
	f.Add("SELECT * FROM (SELECT id FROM t) sub;")

	// --- Multiple ORDER BY ---
	f.Add("SELECT * FROM t ORDER BY a ASC, b DESC, c;")

	// --- GROUP BY / HAVING ---
	f.Add("SELECT a, COUNT(*) FROM t GROUP BY a HAVING COUNT(*) > 5;")

	// --- RETURNING ---
	f.Add("INSERT INTO t (a) VALUES (1) RETURNING id;")
	f.Add("UPDATE t SET a = 1 RETURNING *;")
	f.Add("DELETE FROM t WHERE id = 1 RETURNING id;")

	// --- Random template-based seeds ---
	rng := rand.New(rand.NewSource(42)) // deterministic for reproducibility
	for i := 0; i < 100; i++ {
		f.Add(generateRandomSQL(rng))
	}
	for i := 0; i < 50; i++ {
		f.Add(generateMalformedSQL(rng))
	}

	// --- Fuzz function ---
	f.Fuzz(func(t *testing.T, input string) {
		defer func() {
			if r := recover(); r != nil {
				t.Errorf("parser panicked on input %q: %v", input, r)
			}
		}()
		_, _ = Parse(input)
	})
}
