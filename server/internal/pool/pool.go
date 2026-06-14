package pool

import (
	"crypto/rand"
	"encoding/hex"
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
	stopCh      chan struct{}
}

// Connection — соединение в пуле.
type Connection struct {
	ID        string
	CreatedAt time.Time
	LastUsed  time.Time
	InUse     bool
}

// NewPool создаёт новый пул соединений.
func NewPool(minSize, maxSize int, idleTimeout time.Duration) *Pool {
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
		stopCh:      make(chan struct{}),
	}

	// Запускаем фоновую горутину для очистки неиспользуемых соединений
	go p.cleanupLoop()

	return p
}

// Acquire получает соединение из пула.
// Если пул пуст и не достигнут maxSize — создаёт новое соединение.
// Если пул полон — возвращает nil.
func (p *Pool) Acquire() *Connection {
	p.mu.Lock()
	defer p.mu.Unlock()

	// Ищем свободное соединение
	for _, conn := range p.connections {
		if !conn.InUse {
			conn.InUse = true
			conn.LastUsed = time.Now()
			return conn
		}
	}

	// Если пул не полон — создаём новое соединение
	if len(p.connections) < p.maxSize {
		conn := &Connection{
			ID:        generateID(),
			CreatedAt: time.Now(),
			LastUsed:  time.Now(),
			InUse:     true,
		}
		p.connections = append(p.connections, conn)
		return conn
	}

	// Пул полон
	return nil
}

// Release возвращает соединение в пул.
func (p *Pool) Release(conn *Connection) {
	p.mu.Lock()
	defer p.mu.Unlock()

	conn.InUse = false
	conn.LastUsed = time.Now()
}

// Close закрывает пул и все соединения.
func (p *Pool) Close() {
	p.mu.Lock()
	defer p.mu.Unlock()

	close(p.stopCh)
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

// cleanupLoop periodically cleans up idle connections.
// It exits when the pool's stopCh is closed via Close().
func (p *Pool) cleanupLoop() {
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

// cleanup удаляет неиспользуемые соединения старше idleTimeout.
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
		}
	}

	p.connections = remaining
}

// generateID генерирует уникальный ID для соединения.
func generateID() string {
	b := make([]byte, 8)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}

// PoolStats статистика пула соединений.
type PoolStats struct {
	Active int
	Idle   int
	Total  int
}
