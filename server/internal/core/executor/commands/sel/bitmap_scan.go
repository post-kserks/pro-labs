package sel

import (
	"fmt"
	"strings"

	"vaultdb/internal/core/executor/types"
	"vaultdb/internal/core/storage"
)

// IndexQueryDef holds the information necessary to query a single index.
type IndexQueryDef struct {
	IndexName string
	Column    string
	Operator  string
	Value     string
}

// ExecuteBitmapScan executes a bitmap index scan using multiple indexes.
// It queries the indexes to get row positions, converts them to storage.PageBitmap,
// merges them using And() or Or(), and then reads the resulting rows from the heap.
func ExecuteBitmapScan(ctx *types.ExecutionContext, tableName string, queries []IndexQueryDef, op string) ([]storage.Row, error) {
	if len(queries) == 0 {
		return nil, fmt.Errorf("no index queries provided for bitmap scan")
	}

	dbName, err := types.RequireCurrentDB(ctx)
	if err != nil {
		return nil, err
	}

	var finalBitmap *storage.PageBitmap

	for i, q := range queries {
		var positions []int
		var ok bool

		switch q.Operator {
		case "=":
			positions, ok = ctx.Storage.IndexLookup(dbName, tableName, q.Column, q.Value)
		default:
			// Fallback to exact match for base implementation
			positions, ok = ctx.Storage.IndexLookup(dbName, tableName, q.Column, q.Value)
		}

		if !ok {
			positions = []int{}
		}

		// Convert []int to PageBitmap.
		// Assuming RowPosition can be represented as PageID = pos/10000, Slot = pos%10000
		currentBitmap := storage.NewPageBitmap()
		for _, pos := range positions {
			currentBitmap.Add(uint64(pos/10000), uint16(pos%10000))
		}

		if i == 0 {
			finalBitmap = currentBitmap
		} else {
			if strings.ToUpper(op) == "OR" {
				finalBitmap = finalBitmap.Or(currentBitmap)
			} else {
				// Default to AND
				finalBitmap = finalBitmap.And(currentBitmap)
			}
		}
	}

	if finalBitmap == nil {
		return []storage.Row{}, nil
	}

	// Extract merged positions
	rowPositions := finalBitmap.ToPositions()

	// Convert back to []int for ReadRowsByPositions
	var mergedPositions []int
	for _, rp := range rowPositions {
		pos := int(rp.PageID)*10000 + int(rp.Slot)
		mergedPositions = append(mergedPositions, pos)
	}

	if len(mergedPositions) == 0 {
		return []storage.Row{}, nil
	}

	// Sequentially read the resulting rows from the heap table
	rows, err := ctx.Storage.ReadRowsByPositions(dbName, tableName, mergedPositions)
	if err != nil {
		return nil, fmt.Errorf("failed to read rows by positions: %w", err)
	}

	return rows, nil
}
