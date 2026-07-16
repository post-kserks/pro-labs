package pgwire

import (
	"context"
	"fmt"
	"log/slog"
	"net"
	"sync"

	"vaultdb/internal/auth"
	"vaultdb/internal/core/ai"
	"vaultdb/internal/core/executor"
	"vaultdb/internal/core/metrics"
	"vaultdb/internal/core/storage"
	"vaultdb/internal/core/txmanager"
	"vaultdb/internal/core/wal"
)

// PGWireServer implements a PostgreSQL wire protocol server.
type PGWireServer struct {
	Addr        string
	Store       storage.StorageEngine
	Metrics     *metrics.Collector
	TxManager   *txmanager.Manager
	Broadcaster *executor.Broadcaster
	Embedder    ai.Embedder
	WAL         *wal.WAL
	AuthManager *auth.Manager
	Logger      *slog.Logger

	listener net.Listener
	wg       sync.WaitGroup
	ctx      context.Context
	cancel   context.CancelFunc

	mu          sync.Mutex
	connections map[net.Conn]struct{}
}

// NewServer creates a new instance of PGWireServer.
func NewServer(
	addr string,
	store storage.StorageEngine,
	metricsCollector *metrics.Collector,
	txm *txmanager.Manager,
	br *executor.Broadcaster,
	w *wal.WAL,
	authMgr *auth.Manager,
	logger *slog.Logger,
) *PGWireServer {
	if logger == nil {
		logger = slog.Default()
	}
	return &PGWireServer{
		Addr:        addr,
		Store:       store,
		Metrics:     metricsCollector,
		TxManager:   txm,
		Broadcaster: br,
		WAL:         w,
		AuthManager: authMgr,
		Logger:      logger,
		connections: make(map[net.Conn]struct{}),
	}
}

// Start starts the PGWire TCP listener.
func (s *PGWireServer) Start(ctx context.Context) error {
	s.ctx, s.cancel = context.WithCancel(ctx)

	l, err := net.Listen("tcp", s.Addr)
	if err != nil {
		return fmt.Errorf("failed to listen on %s: %w", s.Addr, err)
	}
	s.listener = l
	s.Logger.Info("PGWireServer started", "addr", s.Addr)

	s.wg.Add(1)
	go func() {
		defer s.wg.Done()
		for {
			conn, err := s.listener.Accept()
			if err != nil {
				select {
				case <-s.ctx.Done():
					return
				default:
					s.Logger.Error("failed to accept connection", "error", err)
					continue
				}
			}

			s.mu.Lock()
			s.connections[conn] = struct{}{}
			s.mu.Unlock()

			s.wg.Add(1)
			go func() {
				defer s.wg.Done()
				defer func() {
					conn.Close()
					s.mu.Lock()
					delete(s.connections, conn)
					s.mu.Unlock()
				}()
				s.handleConn(conn)
			}()
		}
	}()

	return nil
}

// Stop stops the server and waits for all active connections to close.
func (s *PGWireServer) Stop() {
	if s.cancel != nil {
		s.cancel()
	}
	if s.listener != nil {
		s.listener.Close()
	}

	s.mu.Lock()
	for conn := range s.connections {
		conn.Close()
	}
	s.mu.Unlock()

	s.wg.Wait()
	s.Logger.Info("PGWireServer stopped")
}
