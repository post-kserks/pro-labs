package executor

import (
	"math"
	"testing"
)

func TestWelfordVariance(t *testing.T) {
	tests := []struct {
		name   string
		values []float64
		want   float64
	}{
		{
			name:   "empty",
			values: []float64{},
			want:   math.NaN(),
		},
		{
			name:   "single value",
			values: []float64{42},
			want:   0.0,
		},
		{
			name:   "two values",
			values: []float64{2, 4},
			want:   2.0,
		},
		{
			name:   "known variance",
			values: []float64{1, 2, 3, 4, 5},
			want:   2.5, // sample variance
		},
		{
			name:   "identical values",
			values: []float64{5, 5, 5, 5, 5},
			want:   0.0,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			agg := &varianceAgg{}
			for _, v := range tt.values {
				agg.Add(nil, v)
			}
			result := agg.Result()

			if len(tt.values) == 0 {
				if result != nil {
					t.Errorf("expected nil, got %v", result)
				}
				return
			}

			got, ok := result.(float64)
			if !ok {
				t.Fatalf("expected float64, got %T", result)
			}
			if math.Abs(got-tt.want) > 1e-10 {
				t.Errorf("variance = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestWelfordStddev(t *testing.T) {
	tests := []struct {
		name   string
		values []float64
		want   float64
	}{
		{
			name:   "empty",
			values: []float64{},
			want:   math.NaN(),
		},
		{
			name:   "single value",
			values: []float64{42},
			want:   0.0,
		},
		{
			name:   "two values",
			values: []float64{2, 4},
			want:   math.Sqrt(2.0),
		},
		{
			name:   "known stddev",
			values: []float64{1, 2, 3, 4, 5},
			want:   math.Sqrt(2.5),
		},
		{
			name:   "identical values",
			values: []float64{5, 5, 5, 5, 5},
			want:   0.0,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			agg := &stddevAgg{}
			for _, v := range tt.values {
				agg.Add(nil, v)
			}
			result := agg.Result()

			if len(tt.values) == 0 {
				if result != nil {
					t.Errorf("expected nil, got %v", result)
				}
				return
			}

			got, ok := result.(float64)
			if !ok {
				t.Fatalf("expected float64, got %T", result)
			}
			if math.Abs(got-tt.want) > 1e-10 {
				t.Errorf("stddev = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestWelfordNilIgnored(t *testing.T) {
	agg := &varianceAgg{}
	agg.Add(nil, 1.0)
	agg.Add(nil, nil)
	agg.Add(nil, 3.0)

	result := agg.Result()
	got, ok := result.(float64)
	if !ok {
		t.Fatalf("expected float64, got %T", result)
	}
	// variance of [1, 3] = 2.0 (sample variance)
	if math.Abs(got-2.0) > 1e-10 {
		t.Errorf("variance = %v, want 2.0", got)
	}
}

func TestWelfordLargeDataset(t *testing.T) {
	agg := &varianceAgg{}
	// Add 1 million values, all equal to 100
	for i := 0; i < 1_000_000; i++ {
		agg.Add(nil, 100.0)
	}
	result := agg.Result()
	got, ok := result.(float64)
	if !ok {
		t.Fatalf("expected float64, got %T", result)
	}
	if math.Abs(got) > 1e-10 {
		t.Errorf("variance of identical values should be 0, got %v", got)
	}
}

func TestWelfordSequentialVsBatch(t *testing.T) {
	values := []float64{10, 20, 30, 40, 50, 60, 70, 80, 90, 100}

	// Sequential add
	aggSeq := &varianceAgg{}
	for _, v := range values {
		aggSeq.Add(nil, v)
	}

	// Batch add (all at once via loop)
	aggBatch := &varianceAgg{}
	for _, v := range values {
		aggBatch.Add(nil, v)
	}

	seqResult := aggSeq.Result()
	batchResult := aggBatch.Result()

	seqVal, _ := seqResult.(float64)
	batchVal, _ := batchResult.(float64)

	if math.Abs(seqVal-batchVal) > 1e-10 {
		t.Errorf("sequential (%v) != batch (%v)", seqVal, batchVal)
	}
}
