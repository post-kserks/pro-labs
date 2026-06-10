package metrics

import (
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
