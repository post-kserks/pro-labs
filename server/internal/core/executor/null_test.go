package executor

import (
	"testing"
)

func setupNullTestSession(t *testing.T) *Session {
	t.Helper()
	session := setupSession(t)
	executeSQL(t, session, "CREATE TABLE users (id INT, name VARCHAR(100), age INT);")
	executeSQL(t, session, "INSERT INTO users VALUES (1, 'Alice', 30);")
	executeSQL(t, session, "INSERT INTO users VALUES (2, 'Bob', 25);")
	executeSQL(t, session, "INSERT INTO users (id, name) VALUES (3, 'Unknown');")
	return session
}

func TestIsNullOperator(t *testing.T) {
	session := setupNullTestSession(t)

	tests := []struct {
		name     string
		query    string
		expected [][]string
	}{
		{
			name:     "IS NULL match",
			query:    "SELECT name FROM users WHERE age IS NULL;",
			expected: [][]string{{"Unknown"}},
		},
		{
			name:     "IS NOT NULL match",
			query:    "SELECT name FROM users WHERE age IS NOT NULL;",
			expected: [][]string{{"Alice"}, {"Bob"}},
		},
		{
			name:     "IS NULL no match",
			query:    "SELECT name FROM users WHERE age IS NULL AND name = 'Alice';",
			expected: [][]string{},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			res := executeSQL(t, session, tt.query)
			if len(res.Rows) != len(tt.expected) {
				t.Fatalf("expected %d rows, got %d: %v", len(tt.expected), len(res.Rows), res.Rows)
			}
			for i, row := range res.Rows {
				if len(row) != len(tt.expected[i]) {
					t.Fatalf("row %d: expected %d columns, got %d", i, len(tt.expected[i]), len(row))
				}
				for j, val := range row {
					if val != tt.expected[i][j] {
						t.Fatalf("row %d col %d: expected %q, got %q", i, j, tt.expected[i][j], val)
					}
				}
			}
		})
	}
}

func TestCoalesceFunction(t *testing.T) {
	session := setupNullTestSession(t)

	tests := []struct {
		name     string
		query    string
		expected [][]string
	}{
		{
			name:     "COALESCE with NULL first",
			query:    "SELECT COALESCE(NULL, 'default') FROM users WHERE id = 3;",
			expected: [][]string{{"default"}},
		},
		{
			name:     "COALESCE with non-NULL first",
			query:    "SELECT COALESCE('value', 'default') FROM users WHERE id = 3;",
			expected: [][]string{{"value"}},
		},
		{
			name:     "COALESCE all NULL",
			query:    "SELECT COALESCE(NULL, NULL, NULL) FROM users WHERE id = 3;",
			expected: [][]string{{""}},
		},
		{
			name:     "COALESCE column with NULLs",
			query:    "SELECT COALESCE(age, 0) FROM users ORDER BY id;",
			expected: [][]string{{"30"}, {"25"}, {"0"}},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			res := executeSQL(t, session, tt.query)
			if len(res.Rows) != len(tt.expected) {
				t.Fatalf("expected %d rows, got %d: %v", len(tt.expected), len(res.Rows), res.Rows)
			}
			for i, row := range res.Rows {
				if len(row) != len(tt.expected[i]) {
					t.Fatalf("row %d: expected %d columns, got %d", i, len(tt.expected[i]), len(row))
				}
				for j, val := range row {
					if val != tt.expected[i][j] {
						t.Fatalf("row %d col %d: expected %q, got %q", i, j, tt.expected[i][j], val)
					}
				}
			}
		})
	}
}

func TestAggregateWithNulls(t *testing.T) {
	session := setupNullTestSession(t)

	tests := []struct {
		name     string
		query    string
		expected [][]string
	}{
		{
			name:     "COUNT ignores NULLs",
			query:    "SELECT COUNT(age) FROM users;",
			expected: [][]string{{"2"}},
		},
		{
			name:     "SUM ignores NULLs",
			query:    "SELECT SUM(age) FROM users;",
			expected: [][]string{{"55"}},
		},
		{
			name:     "AVG ignores NULLs",
			query:    "SELECT AVG(age) FROM users;",
			expected: [][]string{{"27.5"}},
		},
		{
			name:     "MIN ignores NULLs",
			query:    "SELECT MIN(age) FROM users;",
			expected: [][]string{{"25"}},
		},
		{
			name:     "MAX ignores NULLs",
			query:    "SELECT MAX(age) FROM users;",
			expected: [][]string{{"30"}},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			res := executeSQL(t, session, tt.query)
			if len(res.Rows) != len(tt.expected) {
				t.Fatalf("expected %d rows, got %d: %v", len(tt.expected), len(res.Rows), res.Rows)
			}
			for i, row := range res.Rows {
				if row[0] != tt.expected[i][0] {
					t.Fatalf("row %d: expected %q, got %q", i, tt.expected[i][0], row[0])
				}
			}
		})
	}
}

func TestNullInComparisons(t *testing.T) {
	session := setupNullTestSession(t)

	tests := []struct {
		name     string
		query    string
		expected [][]string
	}{
		{
			name:     "NULL + 1 returns NULL",
			query:    "SELECT NULL + 1;",
			expected: [][]string{{""}},
		},
		{
			name:     "column with NULL compared with < in WHERE filters NULL rows",
			query:    "SELECT name FROM users WHERE age < 30;",
			expected: [][]string{{"Bob"}},
		},
		{
			name:     "column with NULL compared with > in WHERE filters NULL rows",
			query:    "SELECT name FROM users WHERE age > 30;",
			expected: [][]string{},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			res := executeSQL(t, session, tt.query)
			if len(res.Rows) != len(tt.expected) {
				t.Fatalf("expected %d rows, got %d: %v", len(tt.expected), len(res.Rows), res.Rows)
			}
			for i, row := range res.Rows {
				if row[0] != tt.expected[i][0] {
					t.Fatalf("row %d: expected %q, got %q", i, tt.expected[i][0], row[0])
				}
			}
		})
	}
}

func TestNullInOrderBy(t *testing.T) {
	session := setupNullTestSession(t)

	tests := []struct {
		name     string
		query    string
		expected [][]string
	}{
		{
			name:     "ORDER BY nullable column ASC",
			query:    "SELECT name FROM users ORDER BY age ASC;",
			expected: [][]string{{"Unknown"}, {"Bob"}, {"Alice"}},
		},
		{
			name:     "ORDER BY nullable column DESC",
			query:    "SELECT name FROM users ORDER BY age DESC;",
			expected: [][]string{{"Alice"}, {"Bob"}, {"Unknown"}},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			res := executeSQL(t, session, tt.query)
			if len(res.Rows) != len(tt.expected) {
				t.Fatalf("expected %d rows, got %d: %v", len(tt.expected), len(res.Rows), res.Rows)
			}
			for i, row := range res.Rows {
				if row[0] != tt.expected[i][0] {
					t.Fatalf("row %d: expected %q, got %q", i, tt.expected[i][0], row[0])
				}
			}
		})
	}
}

func TestNullInGroupBy(t *testing.T) {
	session := setupNullTestSession(t)

	res := executeSQL(t, session, "SELECT age, COUNT(*) as cnt FROM users GROUP BY age ORDER BY age ASC;")
	if len(res.Rows) != 3 {
		t.Fatalf("expected 3 groups, got %d: %v", len(res.Rows), res.Rows)
	}
	if res.Rows[0][0] != "" {
		t.Fatalf("expected NULL group first, got %q", res.Rows[0][0])
	}
	if res.Rows[0][1] != "1" {
		t.Fatalf("expected NULL group count 1, got %q", res.Rows[0][1])
	}
	if res.Rows[1][0] != "25" {
		t.Fatalf("expected age 25 group, got %q", res.Rows[1][0])
	}
	if res.Rows[1][1] != "1" {
		t.Fatalf("expected age 25 group count 1, got %q", res.Rows[1][1])
	}
	if res.Rows[2][0] != "30" {
		t.Fatalf("expected age 30 group, got %q", res.Rows[2][0])
	}
	if res.Rows[2][1] != "1" {
		t.Fatalf("expected age 30 group count 1, got %q", res.Rows[2][1])
	}
}

func TestNullifWithNulls(t *testing.T) {
	session := setupNullTestSession(t)

	tests := []struct {
		name     string
		query    string
		expected [][]string
	}{
		{
			name:     "NULLIF same values returns NULL",
			query:    "SELECT NULLIF(1, 1);",
			expected: [][]string{{""}},
		},
		{
			name:     "NULLIF different values returns first",
			query:    "SELECT NULLIF(1, 2);",
			expected: [][]string{{"1"}},
		},
		{
			name:     "NULLIF with column NULL value",
			query:    "SELECT NULLIF(age, 30) FROM users WHERE id = 1;",
			expected: [][]string{{""}},
		},
		{
			name:     "NULLIF with column non-matching value",
			query:    "SELECT NULLIF(age, 99) FROM users WHERE id = 1;",
			expected: [][]string{{"30"}},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			res := executeSQL(t, session, tt.query)
			if len(res.Rows) != len(tt.expected) {
				t.Fatalf("expected %d rows, got %d: %v", len(tt.expected), len(res.Rows), res.Rows)
			}
			if res.Rows[0][0] != tt.expected[0][0] {
				t.Fatalf("expected %q, got %q", tt.expected[0][0], res.Rows[0][0])
			}
		})
	}
}

func TestCountStarIncludesNulls(t *testing.T) {
	session := setupNullTestSession(t)

	res := executeSQL(t, session, "SELECT COUNT(*) FROM users;")
	if res.Rows[0][0] != "3" {
		t.Fatalf("COUNT(*) should include NULLs, expected 3, got %s", res.Rows[0][0])
	}
}

func TestNullArithmetic(t *testing.T) {
	session := setupNullTestSession(t)

	tests := []struct {
		name     string
		query    string
		expected [][]string
	}{
		{
			name:     "NULL + number returns NULL",
			query:    "SELECT age + 1 FROM users WHERE id = 3;",
			expected: [][]string{{""}},
		},
		{
			name:     "NULL * number returns NULL",
			query:    "SELECT age * 2 FROM users WHERE id = 3;",
			expected: [][]string{{""}},
		},
		{
			name:     "NULL - number returns NULL",
			query:    "SELECT age - 1 FROM users WHERE id = 3;",
			expected: [][]string{{""}},
		},
		{
			name:     "non-NULL arithmetic works",
			query:    "SELECT age + 1 FROM users WHERE id = 1;",
			expected: [][]string{{"31"}},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			res := executeSQL(t, session, tt.query)
			if len(res.Rows) != len(tt.expected) {
				t.Fatalf("expected %d rows, got %d: %v", len(tt.expected), len(res.Rows), res.Rows)
			}
			if res.Rows[0][0] != tt.expected[0][0] {
				t.Fatalf("expected %q, got %q", tt.expected[0][0], res.Rows[0][0])
			}
		})
	}
}

func TestNullWithLogicalOperators(t *testing.T) {
	session := setupNullTestSession(t)

	tests := []struct {
		name     string
		query    string
		expected [][]string
	}{
		{
			name:     "NULL column in AND condition",
			query:    "SELECT name FROM users WHERE age IS NOT NULL AND name = 'Alice';",
			expected: [][]string{{"Alice"}},
		},
		{
			name:     "NULL column in OR condition",
			query:    "SELECT name FROM users WHERE age IS NULL OR name = 'Alice';",
			expected: [][]string{{"Alice"}, {"Unknown"}},
		},
		{
			name:     "NOT with IS NULL",
			query:    "SELECT name FROM users WHERE NOT (age IS NULL);",
			expected: [][]string{{"Alice"}, {"Bob"}},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			res := executeSQL(t, session, tt.query)
			if len(res.Rows) != len(tt.expected) {
				t.Fatalf("expected %d rows, got %d: %v", len(tt.expected), len(res.Rows), res.Rows)
			}
			for i, row := range res.Rows {
				if row[0] != tt.expected[i][0] {
					t.Fatalf("row %d: expected %q, got %q", i, tt.expected[i][0], row[0])
				}
			}
		})
	}
}
