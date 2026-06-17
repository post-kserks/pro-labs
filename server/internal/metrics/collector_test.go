package metrics

import (
	"fmt"
	"strings"
	"testing"
	"time"
)

func TestRecordQueryAndRender(t *testing.T) {
	c := New()
	c.RecordQuery("SELECT", "ok", 2*time.Millisecond)
	c.RecordQuery("select", "error", 0)
	c.RecordQuery("INSERT", "ok", time.Millisecond)
	c.RecordQuery("CREATE_TABLE", "ok", time.Millisecond)
	c.RecordQuery("BEGIN", "ok", 0)
	c.RecordQuery("SHOW_TABLES", "ok", 0)

	out := c.Render()
	for _, want := range []string{
		`vaultdb_queries_total{type="select",status="ok"} 1`,
		`vaultdb_queries_total{type="select",status="error"} 1`,
		`vaultdb_queries_total{type="insert",status="ok"} 1`,
		`vaultdb_queries_total{type="ddl",status="ok"} 1`,
		`vaultdb_queries_total{type="transaction",status="ok"} 1`,
		`vaultdb_queries_total{type="other",status="ok"} 1`,
	} {
		if !strings.Contains(out, want) {
			t.Errorf("render output missing %q", want)
		}
	}
}

func BenchmarkRecordQuery(b *testing.B) {
	c := New()
	b.RunParallel(func(pb *testing.PB) {
		for pb.Next() {
			c.RecordQuery("select", "ok", time.Millisecond)
		}
	})
}

func TestMetricsCardinalityLimit(t *testing.T) {
	c := New()

	for i := 0; i < 1005; i++ {
		db := "db"
		if i >= 1000 {
			db = "overflow_db"
		}
		c.UpdateStorageRows(db, fmt.Sprintf("table_%d", i), int64(i))
	}

	out := c.Render()

	count := 0
	for _, line := range strings.Split(out, "\n") {
		if strings.HasPrefix(line, "vaultdb_storage_rows{") {
			count++
		}
	}
	if count != maxStorageRowMetrics {
		t.Errorf("expected %d storage_rows metrics, got %d", maxStorageRowMetrics, count)
	}

	if !strings.Contains(out, "vaultdb_storage_rows_overflow 1") {
		t.Error("expected vaultdb_storage_rows_overflow 1 when limit exceeded")
	}
}

func TestMetricsCardinalityNoOverflow(t *testing.T) {
	c := New()

	for i := 0; i < 100; i++ {
		c.UpdateStorageRows("db", fmt.Sprintf("table_%d", i), int64(i))
	}

	out := c.Render()

	count := 0
	for _, line := range strings.Split(out, "\n") {
		if strings.HasPrefix(line, "vaultdb_storage_rows{") {
			count++
		}
	}
	if count != 100 {
		t.Errorf("expected 100 storage_rows metrics, got %d", count)
	}

	if strings.Contains(out, "vaultdb_storage_rows_overflow") {
		t.Error("unexpected vaultdb_storage_rows_overflow when under limit")
	}
}
