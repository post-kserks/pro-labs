package pool

import (
	"io"
	"net"
	"sync"
	"testing"
	"time"
)

func testFactory() func() (net.Conn, error) {
	return func() (net.Conn, error) {
		l, err := net.Listen("tcp", "127.0.0.1:0")
		if err != nil {
			return nil, err
		}
		go func() {
			defer l.Close()
			c, _ := l.Accept()
			if c != nil {
				c.Close()
			}
		}()
		return net.Dial("tcp", l.Addr().String())
	}
}

func echoFactory() func() (net.Conn, error) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		panic(err)
	}
	go func() {
		for {
			conn, err := ln.Accept()
			if err != nil {
				return
			}
			go func(c net.Conn) {
				defer c.Close()
				buf := make([]byte, 1024)
				for {
					n, err := c.Read(buf)
					if err != nil {
						return
					}
					c.Write(buf[:n])
				}
			}(conn)
		}
	}()
	return func() (net.Conn, error) {
		return net.Dial("tcp", ln.Addr().String())
	}
}

func TestConnectionLimiter(t *testing.T) {
	tests := []struct {
		name string
		fn   func(t *testing.T)
	}{
		{"new_pool_defaults", testNewConnectionLimiterDefaults},
		{"acquire_release", testAcquireRelease},
		{"acquire_max", testAcquireMax},
		{"stats", testStats},
		{"close", testClose},
		{"concurrent_acquire_release", testConcurrentAcquireRelease},
		{"health_check", testHealthCheck},
		{"connection_reuse", testConnectionReuse},
		{"connection_timeout", testConnectionTimeout},
		{"factory_error", testFactoryError},
		{"read_write", testReadWrite},
	}
	for _, tt := range tests {
		t.Run(tt.name, tt.fn)
	}
}

func testNewConnectionLimiterDefaults(t *testing.T) {
	p := NewConnectionLimiter(0, 0, 0, testFactory())
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
	p := NewConnectionLimiter(1, 10, time.Minute, testFactory())
	defer p.Close()

	conn, err := p.Acquire()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
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

	conn2, err := p.Acquire()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if conn2 == nil {
		t.Fatal("expected reusable connection, got nil")
	}
	if conn2.ID != conn.ID {
		t.Error("expected same connection ID after release and re-acquire")
	}
}

func testAcquireMax(t *testing.T) {
	p := NewConnectionLimiter(1, 3, time.Minute, testFactory())
	defer p.Close()

	var conns []*Connection
	for i := 0; i < 3; i++ {
		c, err := p.Acquire()
		if err != nil {
			t.Fatalf("unexpected error at index %d: %v", i, err)
		}
		if c == nil {
			t.Fatalf("expected connection at index %d, got nil", i)
		}
		conns = append(conns, c)
	}

	_, err := p.Acquire()
	if err != io.ErrShortBuffer {
		t.Errorf("expected io.ErrShortBuffer when pool is full, got %v", err)
	}

	for _, c := range conns {
		p.Release(c)
	}
}

func testStats(t *testing.T) {
	p := NewConnectionLimiter(1, 10, time.Minute, testFactory())
	defer p.Close()

	stats := p.Stats()
	if stats.Active != 0 || stats.Idle != 0 || stats.Total != 0 {
		t.Errorf("expected empty pool stats, got %+v", stats)
	}

	c1, _ := p.Acquire()
	c2, _ := p.Acquire()

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
	p := NewConnectionLimiter(1, 10, time.Minute, testFactory())

	conn, err := p.Acquire()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
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

func TestConnectionLimiterCleanupEdgeCase(t *testing.T) {
	t.Run("keeps_minSize_when_all_expired", func(t *testing.T) {
		p := NewConnectionLimiter(2, 10, time.Millisecond, testFactory())
		defer p.Close()

		var conns []*Connection
		for i := 0; i < 5; i++ {
			conn, err := p.Acquire()
			if err != nil {
				t.Fatalf("unexpected error at index %d: %v", i, err)
			}
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
		p := NewConnectionLimiter(2, 10, time.Millisecond, testFactory())
		defer p.Close()

		var conns []*Connection
		for i := 0; i < 5; i++ {
			conn, err := p.Acquire()
			if err != nil {
				t.Fatalf("unexpected error at index %d: %v", i, err)
			}
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
		p := NewConnectionLimiter(2, 10, time.Millisecond, testFactory())
		defer p.Close()

		active1, _ := p.Acquire()
		active2, _ := p.Acquire()

		var idleConns []*Connection
		for i := 0; i < 3; i++ {
			conn, err := p.Acquire()
			if err != nil {
				t.Fatalf("unexpected error at index %d: %v", i, err)
			}
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
	p := NewConnectionLimiter(1, 20, time.Minute, testFactory())
	defer p.Close()

	var wg sync.WaitGroup
	for i := 0; i < 10; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < 50; j++ {
				conn, err := p.Acquire()
				if err != nil {
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

func testHealthCheck(t *testing.T) {
	f := echoFactory()
	p := NewConnectionLimiter(1, 5, time.Minute, f)
	defer p.Close()

	conn, err := p.Acquire()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	p.Release(conn)

	conn2, err := p.Acquire()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if conn2.ID != conn.ID {
		t.Error("expected same connection to be reused after health check")
	}
}

func testConnectionReuse(t *testing.T) {
	f := echoFactory()
	p := NewConnectionLimiter(2, 5, time.Minute, f)
	defer p.Close()

	conn1, err := p.Acquire()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	conn2, err := p.Acquire()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	id1, id2 := conn1.ID, conn2.ID
	p.Release(conn1)
	p.Release(conn2)

	conn3, err := p.Acquire()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	conn4, err := p.Acquire()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if conn3.ID != id1 && conn3.ID != id2 {
		t.Error("expected conn3 to be a reused connection")
	}
	if conn4.ID != id1 && conn4.ID != id2 {
		t.Error("expected conn4 to be a reused connection")
	}
	if conn3.ID == conn4.ID {
		t.Error("expected conn3 and conn4 to be different connections")
	}

	p.Release(conn3)
	p.Release(conn4)
}

func testConnectionTimeout(t *testing.T) {
	f := echoFactory()
	p := NewConnectionLimiter(1, 5, time.Millisecond, f)
	defer p.Close()

	var conns []*Connection
	for i := 0; i < 3; i++ {
		conn, err := p.Acquire()
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		conns = append(conns, conn)
	}

	for _, conn := range conns {
		p.Release(conn)
		conn.LastUsed = time.Now().Add(-time.Second)
	}

	p.cleanup()

	stats := p.Stats()
	if stats.Total > p.minSize {
		t.Errorf("expected at most %d connections after timeout cleanup, got %d", p.minSize, stats.Total)
	}
	if stats.Active != 0 {
		t.Errorf("expected 0 active connections after cleanup, got %d", stats.Active)
	}
}

func testFactoryError(t *testing.T) {
	errFactory := func() (net.Conn, error) {
		return nil, io.ErrUnexpectedEOF
	}

	p := NewConnectionLimiter(0, 5, time.Minute, errFactory)
	defer p.Close()

	conn, err := p.Acquire()
	if err != io.ErrUnexpectedEOF {
		t.Errorf("expected io.ErrUnexpectedEOF, got err=%v conn=%v", err, conn)
	}
}

func testReadWrite(t *testing.T) {
	f := echoFactory()
	p := NewConnectionLimiter(1, 5, time.Minute, f)
	defer p.Close()

	conn, err := p.Acquire()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	testData := []byte("hello world")
	n, err := conn.Write(testData)
	if err != nil {
		t.Fatalf("write error: %v", err)
	}
	if n != len(testData) {
		t.Errorf("expected to write %d bytes, wrote %d", len(testData), n)
	}

	buf := make([]byte, 1024)
	conn.conn.SetReadDeadline(time.Now().Add(time.Second))
	n, err = conn.Read(buf)
	if err != nil {
		t.Fatalf("read error: %v", err)
	}
	if string(buf[:n]) != string(testData) {
		t.Errorf("expected %q, got %q", testData, buf[:n])
	}

	p.Release(conn)
}
