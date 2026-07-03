package vaultdb

import (
	"vaultdb/internal/executor"
	"vaultdb/internal/storage"
)

// Result is the public result type for query execution.
// This type is accessible to external modules (unlike internal/executor.Result).
type Result struct {
	Type     string
	Columns  []string
	Rows     [][]string
	Schema   *storage.TableSchema
	Affected int
	Message  string
	AsOfNote string
}

// toInternal converts the public Result to the internal executor.Result.
func (r *Result) toInternal() *executor.Result {
	return &executor.Result{
		Type:     r.Type,
		Columns:  r.Columns,
		Rows:     r.Rows,
		Schema:   r.Schema,
		Affected: r.Affected,
		Message:  r.Message,
		AsOfNote: r.AsOfNote,
	}
}

// fromInternal converts an internal executor.Result to the public Result.
func fromInternal(r *executor.Result) *Result {
	if r == nil {
		return nil
	}
	return &Result{
		Type:     r.Type,
		Columns:  r.Columns,
		Rows:     r.Rows,
		Schema:   r.Schema,
		Affected: r.Affected,
		Message:  r.Message,
		AsOfNote: r.AsOfNote,
	}
}
