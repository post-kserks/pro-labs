package executor

import (
	"testing"
	"vaultdb/internal/core/executor/vector"
)

// BenchmarkVectorizedVSVolcano compares simulated Vectorized engine vs Volcano
func BenchmarkVectorizedVSVolcano(b *testing.B) {
	b.Run("Volcano", func(b *testing.B) {
		data := make([]int64, 1024)
		for i := 0; i < 1024; i++ {
			data[i] = int64(i)
		}

		b.ResetTimer()
		for i := 0; i < b.N; i++ {
			count := 0
			for j := 0; j < 1024; j++ {
				if data[j] > 500 {
					count++
				}
			}
		}
	})

	b.Run("Vectorized", func(b *testing.B) {
		batch := vector.NewRecordBatch()
		batch.Count = 1024
		batch.AddInt64Column(0)
		for i := 0; i < 1024; i++ {
			batch.Int64Cols[0][i] = int64(i)
		}

		b.ResetTimer()
		for i := 0; i < b.N; i++ {
			batch.Selected = 0
			_ = batch.FilterInt64GreaterThan(0, 500)
		}
	})
}
