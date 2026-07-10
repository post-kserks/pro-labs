package pool

import (
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"vaultdb/internal/executor"
	"vaultdb/internal/metrics"
	"vaultdb/internal/txmanager"
)

func newMockSessionFactory() func() *executor.Session {
	return func() *executor.Session {
		return executor.NewSession(nil, metrics.New(), txmanager.NewManager(), nil)
	}
}

func TestSessionPoolReuse(t *testing.T) {
	factory := newMockSessionFactory()
	p := NewSessionPool(factory, 5, 10, time.Minute)
	defer p.Close()

	// Get a session
	sess1, err := p.Get()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if sess1 == nil {
		t.Fatal("expected session, got nil")
	}

	// Return it to pool
	p.Put(sess1)

	// Get another session - should reuse the same one
	sess2, err := p.Get()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if sess2 == nil {
		t.Fatal("expected session, got nil")
	}

	// Stats should show 1 active
	stats := p.Stats()
	if stats.Active != 1 {
		t.Errorf("expected Active=1, got %d", stats.Active)
	}

	p.Put(sess2)
}

func TestSessionPoolExhaustion(t *testing.T) {
	factory := newMockSessionFactory()
	p := NewSessionPool(factory, 2, 3, time.Minute)
	defer p.Close()

	// Get max sessions
	var sessions []*executor.Session
	for i := 0; i < 3; i++ {
		sess, err := p.Get()
		if err != nil {
			t.Fatalf("unexpected error at index %d: %v", i, err)
		}
		sessions = append(sessions, sess)
	}

	// Try to get one more - should fail
	_, err := p.Get()
	if err == nil {
		t.Error("expected error when pool exhausted, got nil")
	}

	// Return all sessions
	for _, sess := range sessions {
		p.Put(sess)
	}

	// Now should work again
	sess, err := p.Get()
	if err != nil {
		t.Fatalf("unexpected error after returning sessions: %v", err)
	}
	p.Put(sess)
}

func TestSessionPoolConcurrentAccess(t *testing.T) {
	factory := newMockSessionFactory()
	p := NewSessionPool(factory, 10, 20, time.Minute)
	defer p.Close()

	var wg sync.WaitGroup
	for i := 0; i < 10; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < 50; j++ {
				sess, err := p.Get()
				if err != nil {
					return
				}
				time.Sleep(time.Microsecond)
				p.Put(sess)
			}
		}()
	}
	wg.Wait()

	stats := p.Stats()
	if stats.Active != 0 {
		t.Errorf("expected Active=0 after concurrent test, got %d", stats.Active)
	}
}

func TestSessionPoolIdleCleanup(t *testing.T) {
	factory := newMockSessionFactory()
	p := NewSessionPool(factory, 5, 10, time.Millisecond)
	defer p.Close()

	// Get and return sessions
	for i := 0; i < 3; i++ {
		sess, err := p.Get()
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		p.Put(sess)
	}

	// Wait for cleanup
	time.Sleep(10 * time.Millisecond)
	p.cleanIdleSessions()

	// Pool should be empty after cleanup (idleTimeout is 1ms)
	stats := p.Stats()
	if stats.Idle != 0 {
		t.Errorf("expected Idle=0 after cleanup, got %d", stats.Idle)
	}
}

func TestSessionPoolClose(t *testing.T) {
	factory := newMockSessionFactory()
	p := NewSessionPool(factory, 5, 10, time.Minute)

	// Get a session
	sess, err := p.Get()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	p.Put(sess)

	// Close the pool
	p.Close()

	// Stats should show 0 everything
	stats := p.Stats()
	if stats.Active != 0 || stats.Idle != 0 {
		t.Errorf("expected empty pool after close, got Active=%d Idle=%d", stats.Active, stats.Idle)
	}
}

func TestSessionPoolPutNil(t *testing.T) {
	factory := newMockSessionFactory()
	p := NewSessionPool(factory, 5, 10, time.Minute)
	defer p.Close()

	// Putting nil should not panic
	p.Put(nil)

	stats := p.Stats()
	if stats.Active != 0 {
		t.Errorf("expected Active=0 after putting nil, got %d", stats.Active)
	}
}

func TestSessionPoolReset(t *testing.T) {
	factory := newMockSessionFactory()
	p := NewSessionPool(factory, 5, 10, time.Minute)
	defer p.Close()

	sess, err := p.Get()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Set some state
	sess.SetCurrentDatabase("testdb")

	p.Put(sess)

	// Get again - should be reset
	sess2, err := p.Get()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Database should be reset (empty)
	if sess2.CurrentDatabase() != "" {
		t.Errorf("expected empty database after reset, got %q", sess2.CurrentDatabase())
	}

	p.Put(sess2)
}

func TestSessionPoolStats(t *testing.T) {
	factory := newMockSessionFactory()
	p := NewSessionPool(factory, 5, 10, time.Minute)
	defer p.Close()

	stats := p.Stats()
	if stats.Active != 0 || stats.Idle != 0 || stats.Max != 10 {
		t.Errorf("expected initial stats Active=0 Idle=0 Max=10, got %+v", stats)
	}

	sess1, _ := p.Get()
	sess2, _ := p.Get()

	stats = p.Stats()
	if stats.Active != 2 {
		t.Errorf("expected Active=2, got %d", stats.Active)
	}

	p.Put(sess1)

	stats = p.Stats()
	if stats.Active != 1 || stats.Idle != 1 {
		t.Errorf("expected Active=1 Idle=1, got Active=%d Idle=%d", stats.Active, stats.Idle)
	}

	p.Put(sess2)
}

func TestSessionPoolDefaultValues(t *testing.T) {
	factory := newMockSessionFactory()
	p := NewSessionPool(factory, 0, 0, 0)
	defer p.Close()

	if p.maxOpen != 100 {
		t.Errorf("expected default maxOpen=100, got %d", p.maxOpen)
	}
	if p.idleTimeout != 5*time.Minute {
		t.Errorf("expected default idleTimeout=5m, got %v", p.idleTimeout)
	}
}

func BenchmarkSessionPoolGetPut(b *testing.B) {
	factory := newMockSessionFactory()
	p := NewSessionPool(factory, 100, 1000, time.Minute)
	defer p.Close()

	b.ResetTimer()
	b.RunParallel(func(pb *testing.PB) {
		for pb.Next() {
			sess, _ := p.Get()
			if sess != nil {
				p.Put(sess)
			}
		}
	})
}

func BenchmarkSessionPoolVsNew(b *testing.B) {
	factory := newMockSessionFactory()
	p := NewSessionPool(factory, 100, 1000, time.Minute)
	defer p.Close()

	b.Run("Pool", func(b *testing.B) {
		b.ResetTimer()
		b.RunParallel(func(pb *testing.PB) {
			for pb.Next() {
				sess, _ := p.Get()
				if sess != nil {
					p.Put(sess)
				}
			}
		})
	})

	b.Run("NewSession", func(b *testing.B) {
		b.ResetTimer()
		b.RunParallel(func(pb *testing.PB) {
			for pb.Next() {
				sess := factory()
				sess.Close()
			}
		})
	})
}

func BenchmarkSessionPoolConcurrency(b *testing.B) {
	factory := newMockSessionFactory()
	p := NewSessionPool(factory, 50, 100, time.Minute)
	defer p.Close()

	var counter int64
	b.ResetTimer()
	b.RunParallel(func(pb *testing.PB) {
		for pb.Next() {
			sess, _ := p.Get()
			if sess != nil {
				atomic.AddInt64(&counter, 1)
				p.Put(sess)
			}
		}
	})
}
