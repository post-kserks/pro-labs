package executor

// Wires optimizer predicate pushdown into the executor: runs the optimizer
// to extract per-table predicates and applies them as early filters on table
// scans, reducing rows before JOINs and final WHERE evaluation.

import (
	"vaultdb/internal/core/executor/optimizer"
	"vaultdb/internal/core/parser"
	"vaultdb/internal/core/storage"
)

// cloneSelectStatement creates a shallow clone of a SelectStatement suitable
// for passing to the optimizer. The clone copies all value/slice fields so the
// optimizer can mutate its WHERE and Joins without affecting the original.
func cloneSelectStatement(stmt *parser.SelectStatement) *parser.SelectStatement {
	clone := *stmt
	if len(stmt.Joins) > 0 {
		clone.Joins = make([]parser.JoinClause, len(stmt.Joins))
		copy(clone.Joins, stmt.Joins)
	}
	if len(stmt.Columns) > 0 {
		clone.Columns = make([]parser.SelectColumn, len(stmt.Columns))
		copy(clone.Columns, stmt.Columns)
	}
	if len(stmt.GroupBy) > 0 {
		clone.GroupBy = make([]parser.Expression, len(stmt.GroupBy))
		copy(clone.GroupBy, stmt.GroupBy)
	}
	if len(stmt.OrderBy) > 0 {
		clone.OrderBy = make([]parser.OrderItem, len(stmt.OrderBy))
		copy(clone.OrderBy, stmt.OrderBy)
	}
	if len(stmt.DistinctOn) > 0 {
		clone.DistinctOn = make([]parser.Expression, len(stmt.DistinctOn))
		copy(clone.DistinctOn, stmt.DistinctOn)
	}
	return &clone
}

// applyPushdownFilter runs the optimizer on a cloned statement, extracts
// TablePredicates, and filters the given rows using the predicate for the
// specified table. Returns the filtered rows. If no predicate exists for the
// table, rows are returned unchanged.
func applyPushdownFilter(dbName string, stmt *parser.SelectStatement, tableName string, rows []storage.Row, schema *storage.TableSchema, ctx *ExecutionContext) ([]storage.Row, error) {
	if stmt.Where == nil || len(rows) == 0 {
		return rows, nil
	}

	store := ctx.Storage
	opt := optimizer.NewOptimizer(store)

	clone := cloneSelectStatement(stmt)
	plan, err := opt.OptimizePlan(dbName, clone)
	if err != nil || plan == nil {
		return rows, nil
	}

	pred, ok := plan.TablePredicates[tableName]
	if !ok || pred == nil {
		return rows, nil
	}

	ensureColumnIndex(ctx, schema)

	filtered := make([]storage.Row, 0, len(rows))
	for _, row := range rows {
		ok, err := evalExpr(pred, row, schema, ctx)
		if err != nil {
			return nil, err
		}
		if ok {
			filtered = append(filtered, row)
		}
	}
	return filtered, nil
}
