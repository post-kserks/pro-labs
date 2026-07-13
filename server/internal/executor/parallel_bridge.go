package executor

import (
	"vaultdb/internal/executor/parallel"
	"vaultdb/internal/parser"
	"vaultdb/internal/storage"
)

// executorEvaluator adapts executor's internal functions to the parallel.Evaluator interface.
type executorEvaluator struct{}

func (e *executorEvaluator) EvalExpr(expr parser.Expression, row storage.Row, schema *storage.TableSchema, ctx interface{}) (bool, error) {
	return evalExpr(expr, row, schema, ctx.(*ExecutionContext))
}

func (e *executorEvaluator) EvalOperand(expr parser.Expression, row storage.Row, schema *storage.TableSchema, ctx interface{}) (interface{}, error) {
	return evalOperand(expr, row, schema, ctx.(*ExecutionContext))
}

func (e *executorEvaluator) ValueToString(val interface{}) string {
	return valueToString(val)
}

func (e *executorEvaluator) CollectAggregates(columns []parser.SelectColumn) []*parser.AggregateExpr {
	return collectAggregates(columns)
}

func (e *executorEvaluator) CompareValues(a, b interface{}) int {
	return CompareValues(a, b)
}

func (e *executorEvaluator) NewAggregator(name string, distinct bool, args ...interface{}) parallel.Aggregator {
	return NewAggregator(name, distinct, args...)
}

// sharedEvaluator is a package-level singleton used by all parallel coordinators.
var sharedEvaluator parallel.Evaluator = &executorEvaluator{}
