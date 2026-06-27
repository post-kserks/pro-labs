package parser

import (
	"testing"
)

func FuzzParseSQL(f *testing.F) {
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

	f.Fuzz(func(t *testing.T, input string) {
		_, _ = Parse(input)
	})
}
