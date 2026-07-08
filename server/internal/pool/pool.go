// Package pool provides connection limiting and session pooling utilities.
//
// ConnectionLimiter tracks active TCP connections and enforces a maximum
// concurrency limit. It is NOT a connection pool in the traditional sense —
// connections are not reused or returned to a shared cache. Each Acquire
// creates a new connection (or wraps an accepted one) and Release marks it
// as no longer active. Idle connections are cleaned up periodically.
//
// SessionPool provides true pooling for executor.Session objects, allowing
// HTTP handlers to reuse sessions across requests.
package pool

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"io"
	"net"
	"sync"
	"sync/atomic"
	"time"

	"vaultdb/internal/executor"
)

// ConnectionLimiter tracks active connections and enforces a maximum concurrency limit.
// Despite the historical "Pool" name, this does NOT pool or reuse connections — it
// merely counts active connections and rejects new ones when the limit is reached.
type ConnectionLimiter struct {
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

// NewConnectionLimiter creates a new connection limiter.
func NewConnectionLimiter(minSize, maxSize int, idleTimeout time.Duration, factory func() (net.Conn, error)) *ConnectionLimiter {
	if minSize <= 0 {
		minSize = 1
	}
	if maxSize <= 0 {
		maxSize = 100
	}
	if idleTimeout <= 0 {
		idleTimeout = 5 * time.Minute
	}

	p := &ConnectionLimiter{
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
func (p *ConnectionLimiter) Acquire() (*Connection, error) {
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
func (p *ConnectionLimiter) Release(conn *Connection) {
	p.mu.Lock()
	defer p.mu.Unlock()

	conn.InUse = false
	conn.LastUsed = time.Now()
}

// AcquireConn оборачивает существующее соединение в пул.
// Используется когда соединение уже принято (listener.Accept),
// а не создаётся через factory.
func (p *ConnectionLimiter) AcquireConn(raw net.Conn) (*Connection, error) {
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
func (p *ConnectionLimiter) Close() {
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
func (p *ConnectionLimiter) Stats() ConnectionLimiterStats {
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

	return ConnectionLimiterStats{
		Active: active,
		Idle:   idle,
		Total:  len(p.connections),
	}
}

// ConnectionLimiterStats статистика пула соединений.
type ConnectionLimiterStats struct {
	Active int
	Idle   int
	Total  int
}

// isHealthy
func (p *ConnectionLimiter) isHealthy(conn *Connection) bool {
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
func (p *ConnectionLimiter) removeConnLocked(conn *Connection) {
	for i, c := range p.connections {
		if c == conn {
			p.connections = append(p.connections[:i], p.connections[i+1:]...)
			return
		}
	}
}

func (p *ConnectionLimiter) cleanupLoop() {
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

func (p *ConnectionLimiter) cleanup() {
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

// SessionPool — пул сессий для повторного использования executor.Session.
// Аналогично тому, как PostgreSQL переиспользует соединения через пул,
// SessionPool позволяет HTTP-хендлерам переиспользовать сессии между запросами.
type SessionPool struct {
	sessions    chan *sessionEntry
	factory     func() *executor.Session
	idleTimeout time.Duration
	stopCh      chan struct{}
	closed      bool
	mu          sync.Mutex
	wg          sync.WaitGroup
	active      int32
	maxOpen     int
}

type sessionEntry struct {
	session  *executor.Session
	lastUsed time.Time
}

// NewSessionPool создаёт новый пул сессий.
// factory — функция создания новой сессии.
// maxIdle — максимальное количество безделовых сессий в пуле.
// maxOpen — максимальное количество одновременно активных сессий.
// idleTimeout — максимальное время простоя сессии перед закрытием.
func NewSessionPool(factory func() *executor.Session, maxIdle, maxOpen int, idleTimeout time.Duration) *SessionPool {
	if maxIdle <= 0 {
		maxIdle = 10
	}
	if maxOpen <= 0 {
		maxOpen = 100
	}
	if idleTimeout <= 0 {
		idleTimeout = 5 * time.Minute
	}

	p := &SessionPool{
		sessions:    make(chan *sessionEntry, maxIdle),
		factory:     factory,
		idleTimeout: idleTimeout,
		stopCh:      make(chan struct{}),
		maxOpen:     maxOpen,
	}

	p.wg.Add(1)
	go p.cleanupLoop()

	return p
}

// Get получает сессию из пула или создаёт новую.
func (p *SessionPool) Get() (*executor.Session, error) {
	// Попытка взять из пула (non-blocking)
	select {
	case entry := <-p.sessions:
		if entry != nil {
			atomic.AddInt32(&p.active, 1)
			entry.session.Reset()
			return entry.session, nil
		}
	default:
	}

	// Проверяем лимит
	if atomic.LoadInt32(&p.active) >= int32(p.maxOpen) {
		return nil, fmt.Errorf("session pool exhausted: %d/%d active", atomic.LoadInt32(&p.active), p.maxOpen)
	}

	// Создаём новую сессию
	sess := p.factory()
	atomic.AddInt32(&p.active, 1)
	return sess, nil
}

// Put возвращает сессию в пул для повторного использования.
func (p *SessionPool) Put(sess *executor.Session) {
	if sess == nil {
		return
	}

	atomic.AddInt32(&p.active, -1)

	// Попытка вернуть в пул (non-blocking)
	select {
	case p.sessions <- &sessionEntry{
		session:  sess,
		lastUsed: time.Now(),
	}:
	default:
		// Пул полон — закрываем сессию
		sess.Close()
	}
}

// Close закрывает пул и все сессии в нём.
func (p *SessionPool) Close() {
	p.mu.Lock()
	if p.closed {
		p.mu.Unlock()
		return
	}
	p.closed = true
	p.mu.Unlock()

	close(p.stopCh)
	p.wg.Wait()

	// Закрываем все оставшиеся сессии
	for {
		select {
		case entry := <-p.sessions:
			if entry != nil {
				entry.session.Close()
			}
		default:
			return
		}
	}
}

// Stats возвращает статистику пула сессий.
func (p *SessionPool) Stats() SessionConnectionLimiterStats {
	return SessionConnectionLimiterStats{
		Active: int(atomic.LoadInt32(&p.active)),
		Idle:   len(p.sessions),
		Max:    p.maxOpen,
	}
}

// SessionConnectionLimiterStats статистика пула сессий.
type SessionConnectionLimiterStats struct {
	Active int
	Idle   int
	Max    int
}

func (p *SessionPool) cleanupLoop() {
	defer p.wg.Done()
	ticker := time.NewTicker(30 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-p.stopCh:
			return
		case <-ticker.C:
			p.cleanIdleSessions()
		}
	}
}

func (p *SessionPool) cleanIdleSessions() {
	now := time.Now()
	for {
		select {
		case entry := <-p.sessions:
			if entry == nil {
				continue
			}
			if now.Sub(entry.lastUsed) >= p.idleTimeout {
				entry.session.Close()
			} else {
				// Сессия ещё жива — возвращаем в пул
				select {
				case p.sessions <- entry:
				default:
					entry.session.Close()
				}
			}
		default:
			return
		}
	}
}
