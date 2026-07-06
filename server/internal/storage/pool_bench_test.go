package storage

import "testing"

func BenchmarkRowAllocMake(b *testing.B) {
	for b.Loop() {
		row := make(Row, 16)
		_ = row
	}
}

func BenchmarkRowAllocPool(b *testing.B) {
	for b.Loop() {
		row := GetRowWithLen(16)
		PutRow(row)
	}
}

func BenchmarkRowAllocPoolParallel(b *testing.B) {
	b.RunParallel(func(pb *testing.PB) {
		for pb.Next() {
			row := GetRowWithLen(16)
			PutRow(row)
		}
	})
}
