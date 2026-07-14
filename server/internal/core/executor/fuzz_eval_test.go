package executor

import (
	"fmt"
	"math/rand"
	"os"
	"strings"
	"sync"
	"testing"

	"vaultdb/internal/core/parser"
	"vaultdb/internal/core/storage"
	"vaultdb/internal/core/txmanager"
)

func generateRandomExpr(rng *rand.Rand) string {
	templates := []func(rng *rand.Rand) string{
		func(rng *rand.Rand) string {
			nums := []string{"1", "2", "0", "-1", "999", "0.5", "-0.5"}
			ops := []string{"+", "-", "*", "/"}
			return fmt.Sprintf("%s %s %s", nums[rng.Intn(len(nums))], ops[rng.Intn(len(ops))], nums[rng.Intn(len(nums))])
		},
		func(rng *rand.Rand) string {
			vals := []string{"'hello'", "'world'", "''", "'a b c'"}
			return fmt.Sprintf("%s || %s", vals[rng.Intn(len(vals))], vals[rng.Intn(len(vals))])
		},
		func(rng *rand.Rand) string {
			return fmt.Sprintf("CASE WHEN %d > %d THEN 'yes' ELSE 'no' END", rng.Intn(100), rng.Intn(100))
		},
		func(rng *rand.Rand) string {
			return fmt.Sprintf("COALESCE(%d, %d)", rng.Intn(10), rng.Intn(10))
		},
		func(rng *rand.Rand) string {
			return fmt.Sprintf("CAST(%d AS INT)", rng.Intn(1000))
		},
		func(rng *rand.Rand) string {
			funcs := []string{"COUNT(*)", "SUM(1)", "AVG(1)", "MIN(1)", "MAX(1)"}
			return funcs[rng.Intn(len(funcs))]
		},
		func(rng *rand.Rand) string {
			return fmt.Sprintf("%d > %d", rng.Intn(100), rng.Intn(100))
		},
		func(rng *rand.Rand) string {
			return fmt.Sprintf("%d = %d", rng.Intn(100), rng.Intn(100))
		},
		func(rng *rand.Rand) string {
			return fmt.Sprintf("%d AND %d", rng.Intn(2), rng.Intn(2))
		},
		func(rng *rand.Rand) string {
			return fmt.Sprintf("%d OR %d", rng.Intn(2), rng.Intn(2))
		},
		func(rng *rand.Rand) string {
			return fmt.Sprintf("NOT %d", rng.Intn(2))
		},
		func(rng *rand.Rand) string {
			return fmt.Sprintf("CASE WHEN %d = 1 THEN %d WHEN %d = 2 THEN %d ELSE %d END",
				rng.Intn(5), rng.Intn(100), rng.Intn(5), rng.Intn(100), rng.Intn(100))
		},
	}
	return templates[rng.Intn(len(templates))](rng)
}

var (
	fuzzSessionOnce sync.Once
	fuzzSession     *Session
	fuzzSessionDir  string
)

func getFuzzSession(t *testing.T) *Session {
	fuzzSessionOnce.Do(func() {
		dir, err := os.MkdirTemp("", "fuzz-exec-*")
		if err != nil {
			t.Fatalf("failed to create temp dir: %v", err)
			return
		}
		fuzzSessionDir = dir

		txm := txmanager.NewManager()
		store, err := storage.NewPageStorageEngine(dir, nil, txm)
		if err != nil {
			t.Fatalf("failed to create storage engine: %v", err)
			return
		}

		s := NewSession(store, nil, txm, nil)
		stmt, _ := parser.Parse("CREATE DATABASE mydb;")
		s.Execute(stmt)
		stmt, _ = parser.Parse("USE mydb;")
		s.Execute(stmt)
		stmt, _ = parser.Parse("CREATE TABLE t (a INT, b TEXT, c BOOL);")
		s.Execute(stmt)
		stmt, _ = parser.Parse("INSERT INTO t VALUES (1, 'x', TRUE), (2, 'y', FALSE);")
		s.Execute(stmt)
		fuzzSession = s
	})
	return fuzzSession
}

func FuzzExpressionEval(f *testing.F) {
	// Seed with valid expressions that reference known columns
	f.Add("1 + 2")
	f.Add("a + 2")
	f.Add("'hello' || 'world'")
	f.Add("a || b")
	f.Add("CASE WHEN 1=1 THEN 'yes' ELSE 'no' END")
	f.Add("CASE WHEN a > 1 THEN 'big' ELSE 'small' END")
	f.Add("COALESCE(NULL, 42)")
	f.Add("COALESCE(a, 0)")
	f.Add("CAST(123 AS INT)")
	f.Add("CAST(a AS TEXT)")
	f.Add("1 > 2")
	f.Add("a > 1")
	f.Add("1 = 1")
	f.Add("a = 1")
	f.Add("TRUE AND FALSE")
	f.Add("TRUE OR FALSE")
	f.Add("NOT TRUE")
	f.Add("a > 0 AND b = 'x'")
	f.Add("a > 0 OR c = TRUE")
	f.Add("CASE WHEN a > 0 THEN 1 WHEN a > 5 THEN 2 ELSE 3 END")
	f.Add("COUNT(*)")
	f.Add("SUM(a)")
	f.Add("AVG(a)")
	f.Add("MIN(a)")
	f.Add("MAX(a)")

	// Edge cases
	f.Add("")
	f.Add("   ")
	f.Add("\t\n\r")
	f.Add("\x00")
	f.Add("1 + + 2")
	f.Add("'hello' || || 'world'")
	f.Add("CASE END")
	f.Add("CASE WHEN THEN ELSE END")
	f.Add("()")
	f.Add("(((())))")
	f.Add("1 * 2 + 3 / 4 - 5")
	f.Add("'a' = 'b' AND 'c' != 'd' OR NOT TRUE")
	f.Add("CASE WHEN TRUE THEN CASE WHEN TRUE THEN 1 ELSE 2 END ELSE 3 END")

	// Unicode and binary
	f.Add("'🎉' || '😀'")

	// Very long expressions (referencing valid columns)
	f.Add(strings.Repeat("a + ", 1000) + "1")

	// Random template-based seeds
	rng := rand.New(rand.NewSource(42))
	for i := 0; i < 100; i++ {
		f.Add(generateRandomExpr(rng))
	}

	f.Fuzz(func(t *testing.T, expr string) {
		defer func() {
			if r := recover(); r != nil {
				t.Fatalf("eval panicked on %q: %v", expr, r)
			}
		}()

		sess := getFuzzSession(t)
		if sess == nil {
			return
		}

		// Try to parse as a SELECT with the expression
		query := fmt.Sprintf("SELECT %s FROM t", expr)
		stmt, err := parser.Parse(query)
		if err != nil {
			return // parse error is expected
		}

		// Execute the expression — should not panic
		_, _ = sess.Execute(stmt)
	})
}
