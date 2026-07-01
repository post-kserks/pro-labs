package executor

import (
	"fmt"
	"testing"
)

func TestDistinctRowsCorrectness(t *testing.T) {
	tests := []struct {
		name     string
		input    [][]string
		expected [][]string
	}{
		{
			name:     "empty",
			input:    [][]string{},
			expected: [][]string{},
		},
		{
			name:     "no duplicates",
			input:    [][]string{{"a", "1"}, {"b", "2"}, {"c", "3"}},
			expected: [][]string{{"a", "1"}, {"b", "2"}, {"c", "3"}},
		},
		{
			name:     "all duplicates",
			input:    [][]string{{"a", "1"}, {"a", "1"}, {"a", "1"}},
			expected: [][]string{{"a", "1"}},
		},
		{
			name:     "some duplicates",
			input:    [][]string{{"a", "1"}, {"b", "2"}, {"a", "1"}, {"c", "3"}, {"b", "2"}},
			expected: [][]string{{"a", "1"}, {"b", "2"}, {"c", "3"}},
		},
		{
			name:     "single row",
			input:    [][]string{{"hello"}},
			expected: [][]string{{"hello"}},
		},
		{
			name:     "different columns same value",
			input:    [][]string{{"a", "b"}, {"a,b", ""}},
			expected: [][]string{{"a", "b"}, {"a,b", ""}},
		},
		{
			name:     "empty strings",
			input:    [][]string{{"", ""}, {"", ""}, {"a", ""}},
			expected: [][]string{{"", ""}, {"a", ""}},
		},
		{
			name:     "null byte in values",
			input:    [][]string{{"a\x00b", "c"}, {"a\x00b", "c"}, {"a", "b\x00c"}},
			expected: [][]string{{"a\x00b", "c"}, {"a", "b\x00c"}},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := distinctRows(tt.input)
			if len(result) != len(tt.expected) {
				t.Fatalf("expected %d rows, got %d", len(tt.expected), len(result))
			}
			for i := range result {
				if len(result[i]) != len(tt.expected[i]) {
					t.Fatalf("row %d: expected %d cols, got %d", i, len(tt.expected[i]), len(result[i]))
				}
				for j := range result[i] {
					if result[i][j] != tt.expected[i][j] {
						t.Fatalf("row %d col %d: expected %q, got %q", i, j, tt.expected[i][j], result[i][j])
					}
				}
			}
		})
	}
}

func BenchmarkDistinctRows(b *testing.B) {
	for _, size := range []int{100, 1000, 10000} {
		b.Run(fmt.Sprintf("rows=%d", size), func(b *testing.B) {
			rows := make([][]string, size)
			for i := range rows {
				rows[i] = []string{fmt.Sprintf("col1_%d", i%100), fmt.Sprintf("col2_%d", i%50)}
			}
			b.ResetTimer()
			for b.Loop() {
				_ = distinctRows(rows)
			}
		})
	}
}
