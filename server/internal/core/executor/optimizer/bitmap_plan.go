package optimizer

// BitmapScanPlan represents an execution plan that uses multiple index scans
// and combines their results using bitmap operations (AND/OR).
type BitmapScanPlan struct {
	TableName string
	Indexes   []IndexScanDef
	Operation string // "AND" or "OR"
}

// IndexScanDef defines a single index scan in a BitmapScanPlan.
type IndexScanDef struct {
	IndexName string
	Column    string
	Operator  string
	Value     string
}
