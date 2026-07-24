package vector

import (
	"testing"
)

func TestVectorizedFilter(t *testing.T) {
	batch := NewRecordBatch()
	batch.AddInt64Column(0) // age

	// Populate batch
	batch.Count = 5
	batch.Int64Cols[0][0] = 10
	batch.Int64Cols[0][1] = 20
	batch.Int64Cols[0][2] = 30
	batch.Int64Cols[0][3] = 40
	batch.Int64Cols[0][4] = 50

	// Filter age > 25
	err := batch.FilterInt64GreaterThan(0, 25)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if batch.Selected != 3 {
		t.Fatalf("expected 3 rows selected, got %d", batch.Selected)
	}

	if batch.Selection[0] != 2 || batch.Selection[1] != 3 || batch.Selection[2] != 4 {
		t.Fatalf("unexpected selection vector: %v", batch.Selection[:batch.Selected])
	}

	// Chain filter: age == 40
	err = batch.FilterInt64Equals(0, 40)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if batch.Selected != 1 {
		t.Fatalf("expected 1 row selected, got %d", batch.Selected)
	}

	if batch.Selection[0] != 3 {
		t.Fatalf("unexpected selection vector: %v", batch.Selection[:batch.Selected])
	}
}
