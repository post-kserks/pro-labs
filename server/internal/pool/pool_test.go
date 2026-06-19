package pool

import (
	"sync"
	"testing"
	"time"
)

func TestPool(t *testing.T) {
	tests := []struct {
		name string
		fn   func(t *testing.T)
	}{
		{"new_pool_defaults", testNewPoolDefaults},
		{"acquire_release", testAcquireRelease},
		{"acquire_max", testAcquireMax},
		{"stats", testStats},
		{"close", testClose},
		{"concurrent_acquire_release", testConcurrentAcquireRelease},
	}
	for _, tt := range tests {
		t.Run(tt.name, tt.fn)
	}
}

func testNewPoolDefaults(t *testing.T) {
	p := NewPool(0, 0, 0)
	defer p.Close()

	if p.minSize != 1 {
		t.Errorf("expected minSize=1, got %d", p.minSize)
	}
	if p.maxSize != 100 {
		t.Errorf("expected maxSize=100, got %d", p.maxSize)
	}
	if p.idleTimeout != 5*time.Minute {
		t.Errorf("expected idleTimeout=5m, got %v", p.idleTimeout)
	}
}

func testAcquireRelease(t *testing.T) {
	p := NewPool(1, 10, time.Minute)
	defer p.Close()

	conn := p.Acquire()
	if conn == nil {
		t.Fatal("expected connection, got nil")
	}
	if !conn.InUse {
		t.Error("expected InUse=true after acquire")
	}

	p.Release(conn)
	if conn.InUse {
		t.Error("expected InUse=false after release")
	}

	conn2 := p.Acquire()
	if conn2 == nil {
		t.Fatal("expected reusable connection, got nil")
	}
	if conn2.ID != conn.ID {
		t.Error("expected same connection ID after release and re-acquire")
	}
}

func testAcquireMax(t *testing.T) {
	p := NewPool(1, 3, time.Minute)
	defer p.Close()

	var conns []*Connection
	for i := 0; i < 3; i++ {
		c := p.Acquire()
		if c == nil {
			t.Fatalf("expected connection at index %d, got nil", i)
		}
		conns = append(conns, c)
	}

	extra := p.Acquire()
	if extra != nil {
		t.Error("expected nil when pool is full")
		p.Release(extra)
	}

	for _, c := range conns {
		p.Release(c)
	}
}

func testStats(t *testing.T) {
	p := NewPool(1, 10, time.Minute)
	defer p.Close()

	stats := p.Stats()
	if stats.Active != 0 || stats.Idle != 0 || stats.Total != 0 {
		t.Errorf("expected empty pool stats, got %+v", stats)
	}

	c1 := p.Acquire()
	c2 := p.Acquire()

	stats = p.Stats()
	if stats.Active != 2 {
		t.Errorf("expected Active=2, got %d", stats.Active)
	}
	if stats.Total != 2 {
		t.Errorf("expected Total=2, got %d", stats.Total)
	}

	p.Release(c1)

	stats = p.Stats()
	if stats.Active != 1 {
		t.Errorf("expected Active=1, got %d", stats.Active)
	}
	if stats.Idle != 1 {
		t.Errorf("expected Idle=1, got %d", stats.Idle)
	}
	if stats.Total != 2 {
		t.Errorf("expected Total=2, got %d", stats.Total)
	}

	p.Release(c2)
}

func testClose(t *testing.T) {
	p := NewPool(1, 10, time.Minute)

	conn := p.Acquire()
	if conn == nil {
		t.Fatal("expected connection, got nil")
	}
	p.Release(conn)

	p.Close()

	stats := p.Stats()
	if stats.Total != 0 {
		t.Errorf("expected Total=0 after close, got %d", stats.Total)
	}
}

func TestPoolCleanupEdgeCase(t *testing.T) {
	t.Run("keeps_minSize_when_all_expired", func(t *testing.T) {
		p := NewPool(2, 10, time.Millisecond)
		defer p.Close()

		var conns []*Connection
		for i := 0; i < 5; i++ {
			conn := p.Acquire()
			if conn == nil {
				t.Fatalf("expected connection at index %d, got nil", i)
			}
			conns = append(conns, conn)
		}

		for _, conn := range conns {
			p.Release(conn)
			conn.LastUsed = time.Now().Add(-time.Second)
		}

		p.cleanup()

		stats := p.Stats()
		if stats.Total < p.minSize {
			t.Errorf("expected at least %d connections after cleanup, got %d", p.minSize, stats.Total)
		}
	})

	t.Run("removes_expired_beyond_minSize", func(t *testing.T) {
		p := NewPool(2, 10, time.Millisecond)
		defer p.Close()

		var conns []*Connection
		for i := 0; i < 5; i++ {
			conn := p.Acquire()
			if conn == nil {
				t.Fatalf("expected connection at index %d, got nil", i)
			}
			conns = append(conns, conn)
		}

		for _, conn := range conns {
			p.Release(conn)
			conn.LastUsed = time.Now().Add(-time.Second)
		}

		p.cleanup()

		stats := p.Stats()
		if stats.Total != p.minSize {
			t.Errorf("expected exactly %d connections after cleanup (all expired), got %d", p.minSize, stats.Total)
		}
	})

	t.Run("preserves_active_connections", func(t *testing.T) {
		p := NewPool(2, 10, time.Millisecond)
		defer p.Close()

		active1 := p.Acquire()
		active2 := p.Acquire()

		var idleConns []*Connection
		for i := 0; i < 3; i++ {
			conn := p.Acquire()
			if conn == nil {
				t.Fatalf("expected connection at index %d, got nil", i)
			}
			p.Release(conn)
			conn.LastUsed = time.Now().Add(-time.Second)
			idleConns = append(idleConns, conn)
		}

		_ = idleConns
		p.cleanup()

		stats := p.Stats()
		if stats.Active != 2 {
			t.Errorf("expected 2 active connections after cleanup, got %d", stats.Active)
		}
		if stats.Total < 2 {
			t.Errorf("expected at least 2 total connections after cleanup, got %d", stats.Total)
		}

		p.Release(active1)
		p.Release(active2)
	})
}

func testConcurrentAcquireRelease(t *testing.T) {
	p := NewPool(1, 20, time.Minute)
	defer p.Close()

	var wg sync.WaitGroup
	for i := 0; i < 10; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < 50; j++ {
				conn := p.Acquire()
				if conn == nil {
					return
				}
				time.Sleep(time.Microsecond)
				p.Release(conn)
			}
		}()
	}
	wg.Wait()

	stats := p.Stats()
	if stats.Active != 0 {
		t.Errorf("expected Active=0 after concurrent test, got %d", stats.Active)
	}
	if stats.Idle != stats.Total {
		t.Errorf("expected all connections idle, got Active=%d Idle=%d Total=%d",
			stats.Active, stats.Idle, stats.Total)
	}
}
