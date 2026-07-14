package storage

import (
	"fmt"
	"hash/fnv"
	"strings"
	"time"

	"vaultdb/internal/core/parser"
)

// PartitionedTable wraps table metadata with partition routing logic.
type PartitionedTable struct {
	Spec       *PartitionSpec
	Schema     *TableSchema
	Partitions []Partition
}

// Partition represents one physical partition (a separate heap-backed table).
type Partition struct {
	Name      string
	TableName string      // logical name for storage access: "{parent_table}_{partition_name}"
	Bound     interface{} // upper bound for RANGE partitions, nil for HASH
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
// based on a WHERE clause expression. For RANGE partitions, it extracts comparison
// predicates on the partition key column and performs binary search to skip
// irrelevant partitions. For HASH partitions, simple equality on the key routes
// to a single partition; complex expressions fall back to all partitions.
func (pt *PartitionedTable) PrunePartitions(where parser.Expression) []Partition {
	if where == nil {
		return pt.Partitions
	}

	switch pt.Spec.Type {
	case "RANGE":
		return pt.pruneRange(where)
	case "HASH":
		return pt.pruneHash(where)
	default:
		return pt.Partitions
	}
}

// comparison holds an extracted comparison predicate on the partition key column.
type comparison struct {
	op  string // =, <, >, <=, >=
	val interface{}
}

// partitionBound holds an index and bound value for a partition.
type partitionBound struct {
	index int
	bound interface{} // nil = MAXVALUE
}

// extractComparisons extracts comparison predicates targeting the partition key
// column from a WHERE expression. Returns nil for complex expressions.
func (pt *PartitionedTable) extractComparisons(expr parser.Expression) []comparison {
	switch e := expr.(type) {
	case *parser.BinaryExpr:
		if e.Left == nil || e.Right == nil {
			return nil
		}
		switch e.Operator {
		case "=", "!=", "<", ">", "<=", ">=":
			return pt.checkBinaryComparison(e)
		}
		// Nested binary expression not directly on partition key — can't simplify
		return nil
	case *parser.AndExpr:
		left := pt.extractComparisons(e.Left)
		right := pt.extractComparisons(e.Right)
		if left != nil && right != nil {
			return append(left, right...)
		}
		// If either side can't be extracted, we can't safely prune
		if left != nil || right != nil {
			return nil
		}
		return nil
	case *parser.OrExpr:
		// OR is tricky: we'd need to union partition sets. For simplicity, return
		// nil to indicate we can't prune.
		return nil
	default:
		return nil
	}
}

// checkBinaryComparison extracts a comparison if one side is the partition key
// column and the other is a literal value.
func (pt *PartitionedTable) checkBinaryComparison(e *parser.BinaryExpr) []comparison {
	val, isKeyLeft := pt.matchKeyColumn(e)
	if !isKeyLeft {
		return nil
	}
	rawVal := valueToComparable(val)
	if rawVal == nil {
		return nil
	}
	return []comparison{{op: e.Operator, val: rawVal}}
}

// matchKeyColumn checks if e.Left or e.Right is the partition key column and the
// other is a literal value. Returns the column name, the value, and whether the
// key is on the left side.
func (pt *PartitionedTable) matchKeyColumn(e *parser.BinaryExpr) (val interface{}, keyOnLeft bool) {
	if len(pt.Spec.Columns) == 0 {
		return nil, false
	}
	key := strings.ToLower(pt.Spec.Columns[0])

	if leftCol, ok := e.Left.(*parser.ColumnRef); ok {
		if strings.ToLower(leftCol.Name) == key {
			if leftVal, ok := pt.literalValue(e.Right); ok {
				return leftVal, true
			}
		}
	}
	if rightCol, ok := e.Right.(*parser.ColumnRef); ok {
		if strings.ToLower(rightCol.Name) == key {
			if rightVal, ok := pt.literalValue(e.Left); ok {
				return rightVal, false
			}
		}
	}
	return nil, false
}

// literalValue extracts a raw value from a parser expression literal.
func (pt *PartitionedTable) literalValue(expr parser.Expression) (interface{}, bool) {
	switch v := expr.(type) {
	case parser.Value:
		return pt.parserValueToRaw(v), true
	case *parser.Value:
		return pt.parserValueToRaw(*v), true
	default:
		return nil, false
	}
}

// parserValueToRaw converts a parser Value to its raw Go representation.
func (pt *PartitionedTable) parserValueToRaw(v parser.Value) interface{} {
	switch v.Type {
	case "int":
		return v.IntVal
	case "float":
		return v.FltVal
	case "string":
		return v.StrVal
	case "bool":
		return v.BoolVal
	default:
		return nil
	}
}

// invertOp flips a comparison operator (e.g., "left < right" → "right > left").
func invertOp(op string) string {
	switch op {
	case "<":
		return ">"
	case ">":
		return "<"
	case "<=":
		return ">="
	case ">=":
		return "<="
	default:
		return op
	}
}

// pruneRange filters RANGE partitions based on comparison predicates.
func (pt *PartitionedTable) pruneRange(where parser.Expression) []Partition {
	comps := pt.extractComparisons(where)
	if len(comps) == 0 {
		return pt.Partitions
	}

	// Build bounds from partition definitions.
	// RANGE partitions are ordered by their bound (ascending).
	// Partition[i] contains values < Bound[i] (for non-MAXVALUE).
	// MAXVALUE partition (nil bound) always matches.

	bounds := make([]partitionBound, 0, len(pt.Partitions))
	for i, p := range pt.Partitions {
		var b interface{}
		if p.Bound != nil {
			b = valueToComparable(p.Bound)
		}
		bounds = append(bounds, partitionBound{index: i, bound: b})
	}

	// Determine which partitions can be eliminated.
	// Start with all candidates, then narrow based on each comparison.
	candidateMask := make([]bool, len(pt.Partitions))
	for i := range candidateMask {
		candidateMask[i] = true
	}

	for _, c := range comps {
		pt.applyComparison(candidateMask, bounds, c)
	}

	var result []Partition
	for i, keep := range candidateMask {
		if keep {
			result = append(result, pt.Partitions[i])
		}
	}

	if len(result) == 0 {
		return pt.Partitions
	}
	return result
}

// applyComparison narrows candidateMask based on a single comparison.
func (pt *PartitionedTable) applyComparison(mask []bool, bounds []partitionBound, c comparison) {
	for i, b := range bounds {
		if !mask[i] {
			continue
		}

		// Get the lower bound of this partition (previous partition's upper bound)
		var lowerBound interface{}
		if i > 0 && bounds[i-1].bound != nil {
			lowerBound = bounds[i-1].bound
		}

		// For MAXVALUE (nil bound), the range is [lowerBound, +infinity)
		// For non-MAXVALUE, the range is [lowerBound, upperBound)
		match := pt.rangeOverlapsComp(lowerBound, b.bound, c)
		mask[i] = match
	}
}

// rangeOverlapsComp checks if a partition range [lower, upper) overlaps with
// the set of values satisfying `key <op> c.val`.
//
// RANGE partition semantics: partition i contains rows where
//
//	lower_bound <= key < upper_bound (upper_bound is nil = +infinity for MAXVALUE)
//
// For MAXVALUE partitions (nil upper), the range is [lower_bound, +infinity).
func (pt *PartitionedTable) rangeOverlapsComp(lower, upper interface{}, c comparison) bool {
	switch c.op {
	case "=":
		// Need: lower <= c.val < upper (or lower <= c.val if upper is nil)
		// For MAXVALUE partition (upper=nil): need lower <= c.val
		if upper != nil && compareValues(c.val, upper) >= 0 {
			return false
		}
		if lower != nil && compareValues(c.val, lower) < 0 {
			return false
		}
		return true

	case "<":
		// Need: some x in range with x < c.val
		// Miss if: lower >= c.val (all values in range are >= c.val)
		// For non-MAXVALUE: range is [lower, upper), so all values < upper.
		//   Miss if lower >= c.val
		// For MAXVALUE (upper=nil): range is [lower, +inf)
		//   Miss if lower >= c.val
		if lower != nil && compareValues(lower, c.val) >= 0 {
			return false
		}
		return true

	case "<=":
		// Need: some x in range with x <= c.val
		// Miss if: lower > c.val
		if lower != nil && compareValues(lower, c.val) > 0 {
			return false
		}
		return true

	case ">":
		// Need: some x in range with x > c.val
		// Miss if: upper != nil && upper <= c.val (all values in range are <= c.val)
		//   or upper == nil (MAXVALUE) → always has values > c.val unless lower > c.val
		// For MAXVALUE (upper=nil): range is [lower, +inf)
		//   Miss if: impossible (always has values > c.val)
		if upper != nil && compareValues(upper, c.val) <= 0 {
			return false
		}
		return true

	case ">=":
		// Need: some x in range with x >= c.val
		// For range [lower, upper), all values < upper.
		// Miss if: upper != nil && upper <= c.val (all values < upper <= c.val, so x < c.val)
		if upper != nil && compareValues(upper, c.val) <= 0 {
			return false
		}
		return true

	default:
		return true
	}
}

// pruneHash filters HASH partitions based on equality predicates.
func (pt *PartitionedTable) pruneHash(where parser.Expression) []Partition {
	comps := pt.extractComparisons(where)
	if len(comps) == 0 {
		return pt.Partitions
	}

	// Find an equality comparison on the partition key
	for _, c := range comps {
		if c.op == "=" {
			// Compute hash of the value and return the target partition
			h := fnv.New32a()
			fmt.Fprintf(h, "%v", c.val)
			hash := h.Sum32()
			idx := int(hash) % len(pt.Partitions)
			return []Partition{pt.Partitions[idx]}
		}
	}

	// No equality — can't narrow down for HASH
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
