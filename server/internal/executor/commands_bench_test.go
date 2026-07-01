package executor

import "testing"

func BenchmarkValueToString(b *testing.B) {
	benchmarks := []struct {
		name  string
		input interface{}
	}{
		{"int", int(42)},
		{"int64", int64(123456789)},
		{"float64", float64(3.14159265)},
		{"string", "hello world"},
		{"bool", true},
	}
	for _, bm := range benchmarks {
		b.Run(bm.name, func(b *testing.B) {
			for i := 0; i < b.N; i++ {
				valueToString(bm.input)
			}
		})
	}
}
