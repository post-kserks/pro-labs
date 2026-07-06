package storage

import (
	"fmt"
	"hash/fnv"
	"strings"
	"time"
)

// PartitionedTable wraps table metadata with partition routing logic.
type PartitionedTable struct {
	Spec       *PartitionSpec
	Schema     *TableSchema
	Partitions []Partition
}

// Partition represents one physical partition (a separate heap-backed table).
type Partition struct {
	Name     string
	TableName string // logical name for storage access: "{parent_table}_{partition_name}"
	Bound    interface{} // upper bound for RANGE partitions, nil for HASH
}

// NewPartitionedTable creates a PartitionedTable from a schema's partition spec.
func NewPartitionedTable(schema *TableSchema) *PartitionedTable {
	if schema.PartitionBy == nil {
		return nil
	}

	pt := &PartitionedTable{
		Spec:   schema.PartitionBy,
		Schema: schema,
	}

	switch schema.PartitionBy.Type {
	case "RANGE":
		for _, def := range schema.PartitionBy.Partitions {
			pt.Partitions = append(pt.Partitions, Partition{
				Name:      def.Name,
				TableName: schema.Name + "_" + def.Name,
				Bound:     def.Bound,
			})
		}
	case "HASH":
		n := schema.PartitionBy.NumParts
		if n <= 0 {
			n = 1
		}
		for i := 0; i < n; i++ {
			pt.Partitions = append(pt.Partitions, Partition{
				Name:      fmt.Sprintf("p%d", i),
				TableName: fmt.Sprintf("%s_p%d", schema.Name, i),
				Bound:     nil,
			})
		}
	}

	return pt
}

// InsertRoute determines which partition a row should be inserted into.
func (pt *PartitionedTable) InsertRoute(row Row) (string, error) {
	switch pt.Spec.Type {
	case "RANGE":
		return pt.findRangePartition(row)
	case "HASH":
		return pt.findHashPartition(row)
	default:
		return "", fmt.Errorf("unsupported partition type: %s", pt.Spec.Type)
	}
}

// findRangePartition finds the partition whose bound encompasses the row's key value.
func (pt *PartitionedTable) findRangePartition(row Row) (string, error) {
	keyIdx := pt.findKeyColumnIndex()
	if keyIdx < 0 {
		return "", fmt.Errorf("partition key column '%s' not found", pt.Spec.Columns[0])
	}
	if keyIdx >= len(row) {
		return "", fmt.Errorf("row has fewer columns than partition key")
	}

	val := row[keyIdx]
	for i, p := range pt.Partitions {
		if p.Bound == nil {
			// MAXVALUE partition — always matches
			return pt.Partitions[i].TableName, nil
		}
		boundVal := extractValue(p.Bound)
		if compareValues(val, boundVal) < 0 {
			return pt.Partitions[i].TableName, nil
		}
	}

	return "", fmt.Errorf("no partition found for value %v", val)
}

// findHashPartition computes hash of partition key and returns the target partition.
func (pt *PartitionedTable) findHashPartition(row Row) (string, error) {
	keyIdx := pt.findKeyColumnIndex()
	if keyIdx < 0 {
		return "", fmt.Errorf("partition key column '%s' not found", pt.Spec.Columns[0])
	}
	if keyIdx >= len(row) {
		return "", fmt.Errorf("row has fewer columns than partition key")
	}

	val := row[keyIdx]
	h := fnv.New32a()
	fmt.Fprintf(h, "%v", val)
	hash := h.Sum32()

	idx := int(hash) % len(pt.Partitions)
	return pt.Partitions[idx].TableName, nil
}

// findKeyColumnIndex returns the index of the first partition key column in the schema.
func (pt *PartitionedTable) findKeyColumnIndex() int {
	if len(pt.Spec.Columns) == 0 {
		return -1
	}
	key := strings.ToLower(pt.Spec.Columns[0])
	for i, col := range pt.Schema.Columns {
		if strings.ToLower(col.Name) == key {
			return i
		}
	}
	return -1
}

// PrunePartitions returns a subset of partitions that might contain matching rows
// based on a simple WHERE clause. For now, returns all partitions (conservative).
func (pt *PartitionedTable) PrunePartitions(where interface{}) []Partition {
	// Conservative: return all partitions.
	// A smarter implementation would extract key comparisons from the WHERE clause.
	return pt.Partitions
}

// extractValue unwraps a parser.Value or returns the raw interface{}.
func extractValue(v interface{}) interface{} {
	return v
}

// compareValues compares two values for partition routing.
// Returns -1 if a < b, 0 if equal, 1 if a > b.
func compareValues(a, b interface{}) int {
	// Convert both to comparable strings for date/string types
	av := valueToComparable(a)
	bv := valueToComparable(b)

	switch aVal := av.(type) {
	case int64:
		bVal, ok := bv.(int64)
		if !ok {
			return 0
		}
		if aVal < bVal {
			return -1
		}
		if aVal > bVal {
			return 1
		}
		return 0
	case float64:
		bVal, ok := bv.(float64)
		if !ok {
			return 0
		}
		if aVal < bVal {
			return -1
		}
		if aVal > bVal {
			return 1
		}
		return 0
	case string:
		bVal, ok := bv.(string)
		if !ok {
			return 0
		}
		if aVal < bVal {
			return -1
		}
		if aVal > bVal {
			return 1
		}
		return 0
	case time.Time:
		bVal, ok := bv.(time.Time)
		if !ok {
			return 0
		}
		if aVal.Before(bVal) {
			return -1
		}
		if aVal.After(bVal) {
			return 1
		}
		return 0
	}
	return 0
}

func valueToComparable(v interface{}) interface{} {
	switch val := v.(type) {
	case string:
		// Try to parse as date
		if t, err := time.Parse("2006-01-02", val); err == nil {
			return t
		}
		return val
	case int64:
		return val
	case float64:
		return val
	case time.Time:
		return val
	default:
		return fmt.Sprintf("%v", v)
	}
}
