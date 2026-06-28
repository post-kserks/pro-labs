package parser

import "testing"

func TestRecursiveCTEUnion(t *testing.T) {
	queries := []string{
		"WITH RECURSIVE cte AS (SELECT 1 AS n) SELECT * FROM cte;",
		"WITH RECURSIVE cte AS (SELECT 1 AS n UNION ALL SELECT n + 1 FROM cte WHERE n < 5) SELECT * FROM cte;",
		"WITH RECURSIVE tree AS (SELECT id, name, parent_id, 0 AS depth FROM org WHERE parent_id = 0 UNION ALL SELECT o.id, o.name, o.parent_id, t.depth + 1 FROM org o JOIN tree t ON o.parent_id = t.id) SELECT name, depth FROM tree;",
	}
	for _, q := range queries {
		t.Run(q[:40], func(t *testing.T) {
			_, err := Parse(q)
			if err != nil {
				t.Errorf("Parse failed: %v\nQuery: %s", err, q)
			}
		})
	}
}
