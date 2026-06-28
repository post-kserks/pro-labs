package pool

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"io"
	"net"
	"sync"
	"time"
)

// Pool — пул соединений с автоматическим управлением.
type Pool struct {
	mu          sync.Mutex
	connections []*Connection
	minSize     int
	maxSize     int
	idleTimeout time.Duration
	factory     func() (net.Conn, error)
	stopCh      chan struct{}
	closed      bool
	wg          sync.WaitGroup
}

// Connection — соединение в пуле, оборачивает реальное TCP-соединение.
type Connection struct {
	conn      net.Conn
	ID        string
	CreatedAt time.Time
	LastUsed  time.Time
	InUse     bool
	mu        sync.Mutex
}

// Read читает данные из соединения, обновляя LastUsed.
func (c *Connection) Read(b []byte) (int, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.LastUsed = time.Now()
	return c.conn.Read(b)
}

// Write записывает данные в соединение, обновляя LastUsed.
func (c *Connection) Write(b []byte) (int, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.LastUsed = time.Now()
	return c.conn.Write(b)
}

// Close закрывает底层 TCP-соединение.
func (c *Connection) Close() error {
	return c.conn.Close()
}

// RemoteAddr возвращает адрес удалённой стороны.
func (c *Connection) RemoteAddr() net.Addr {
	return c.conn.RemoteAddr()
}

// SetDeadline устанавливает deadline на соединении.
func (c *Connection) SetDeadline(t time.Time) error {
	return c.conn.SetDeadline(t)
}

// SetReadDeadline устанавливает read deadline.
func (c *Connection) SetReadDeadline(t time.Time) error {
	return c.conn.SetReadDeadline(t)
}

// SetWriteDeadline устанавливает write deadline.
func (c *Connection) SetWriteDeadline(t time.Time) error {
	return c.conn.SetWriteDeadline(t)
}

// NewPool создаёт новый пул соединений.
func NewPool(minSize, maxSize int, idleTimeout time.Duration, factory func() (net.Conn, error)) *Pool {
	if minSize <= 0 {
		minSize = 1
	}
	if maxSize <= 0 {
		maxSize = 100
	}
	if idleTimeout <= 0 {
		idleTimeout = 5 * time.Minute
	}

	p := &Pool{
		connections: make([]*Connection, 0, maxSize),
		minSize:     minSize,
		maxSize:     maxSize,
		idleTimeout: idleTimeout,
		factory:     factory,
		stopCh:      make(chan struct{}),
	}

	p.wg.Add(1)
	go p.cleanupLoop()

	return p
}

// Acquire получает соединение из пула.
func (p *Pool) Acquire() (*Connection, error) {
	p.mu.Lock()
	defer p.mu.Unlock()

	for _, conn := range p.connections {
		if !conn.InUse {
			if p.isHealthy(conn) {
				conn.InUse = true
				conn.LastUsed = time.Now()
				return conn, nil
			}
			p.removeConnLocked(conn)
		}
	}

	if len(p.connections) >= p.maxSize {
		return nil, io.ErrShortBuffer
	}

	if p.factory == nil {
		return nil, io.ErrNoProgress
	}

	raw, err := p.factory()
	if err != nil {
		return nil, err
	}

	conn := &Connection{
		conn:      raw,
		ID:        randomID(),
		CreatedAt: time.Now(),
		LastUsed:  time.Now(),
		InUse:     true,
	}
	p.connections = append(p.connections, conn)
	return conn, nil
}

// Release возвращает соединение в пул.
func (p *Pool) Release(conn *Connection) {
	p.mu.Lock()
	defer p.mu.Unlock()

	conn.InUse = false
	conn.LastUsed = time.Now()
}

// AcquireConn оборачивает существующее соединение в пул.
// Используется когда соединение уже принято (listener.Accept),
// а не создаётся через factory.
func (p *Pool) AcquireConn(raw net.Conn) (*Connection, error) {
	p.mu.Lock()
	defer p.mu.Unlock()

	if len(p.connections) >= p.maxSize {
		return nil, io.ErrShortBuffer
	}

	conn := &Connection{
		conn:      raw,
		ID:        randomID(),
		CreatedAt: time.Now(),
		LastUsed:  time.Now(),
		InUse:     true,
	}
	p.connections = append(p.connections, conn)
	return conn, nil
}

func randomID() string {
	b := make([]byte, 8)
	if _, err := rand.Read(b); err != nil {
		return fmt.Sprintf("%d", time.Now().UnixNano())
	}
	return hex.EncodeToString(b)
}

// Close закрывает пул и все соединения.
func (p *Pool) Close() {
	p.mu.Lock()
	if p.closed {
		p.mu.Unlock()
		return
	}
	p.closed = true
	p.mu.Unlock()

	close(p.stopCh)
	p.wg.Wait()

	p.mu.Lock()
	defer p.mu.Unlock()

	for _, conn := range p.connections {
		conn.conn.Close()
	}
	p.connections = nil
}

// Stats возвращает статистику пула.
func (p *Pool) Stats() PoolStats {
	p.mu.Lock()
	defer p.mu.Unlock()

	active := 0
	idle := 0
	for _, conn := range p.connections {
		if conn.InUse {
			active++
		} else {
			idle++
		}
	}

	return PoolStats{
		Active: active,
		Idle:   idle,
		Total:  len(p.connections),
	}
}

// PoolStats статистика пула соединений.
type PoolStats struct {
	Active int
	Idle   int
	Total  int
}

// isHealthy
func (p *Pool) isHealthy(conn *Connection) bool {
	conn.mu.Lock()
	defer conn.mu.Unlock()

	_ = conn.conn.SetReadDeadline(time.Now().Add(1 * time.Second))
	_, err := conn.conn.Read(make([]byte, 0))
	_ = conn.conn.SetReadDeadline(time.Time{})

	if err == nil {
		return true
	}
	if ne, ok := err.(net.Error); ok && ne.Timeout() {
		return true
	}
	// io.EOF означает, что remote side закрыл соединение — оно мёртвое
	return false
}

// removeConnLocked удаляет соединение из списка (должно вызываться с p.mu).
func (p *Pool) removeConnLocked(conn *Connection) {
	for i, c := range p.connections {
		if c == conn {
			p.connections = append(p.connections[:i], p.connections[i+1:]...)
			return
		}
	}
}

func (p *Pool) cleanupLoop() {
	defer p.wg.Done()
	ticker := time.NewTicker(30 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-p.stopCh:
			return
		case <-ticker.C:
			p.cleanup()
		}
	}
}

func (p *Pool) cleanup() {
	p.mu.Lock()
	defer p.mu.Unlock()

	now := time.Now()
	var remaining []*Connection

	idleCount := 0
	for _, conn := range p.connections {
		if !conn.InUse && now.Sub(conn.LastUsed) >= p.idleTimeout {
			idleCount++
		}
	}

	for _, conn := range p.connections {
		if conn.InUse {
			remaining = append(remaining, conn)
		} else if now.Sub(conn.LastUsed) < p.idleTimeout {
			remaining = append(remaining, conn)
		} else if idleCount > len(p.connections)-p.minSize {
			idleCount--
			remaining = append(remaining, conn)
		} else {
			conn.conn.Close()
		}
	}

	p.connections = remaining
}


