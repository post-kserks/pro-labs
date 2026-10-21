package pgwire

import (
	"context"
	"crypto/tls"
	"fmt"
	"log/slog"
	"net"
	"sync"
	"sync/atomic"
	"time"

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

	// TLSConfig, when non-nil, enables TLS: SSLRequest is answered with 'S'
	// and the connection is upgraded before the StartupMessage.
	TLSConfig *tls.Config
	// MaxConnections caps concurrent client connections. Connections beyond
	// the limit are rejected with SQLSTATE 53300. <= 0 uses DefaultMaxConnections.
	MaxConnections int
	// HandshakeTimeout bounds the startup/TLS/authentication phase.
	// <= 0 uses DefaultHandshakeTimeout.
	HandshakeTimeout time.Duration

	activeConns atomic.Int64

	listener net.Listener
	wg       sync.WaitGroup
	ctx      context.Context
	cancel   context.CancelFunc

	mu          sync.Mutex
	connections map[net.Conn]struct{}
}

const (
	// DefaultMaxConnections is the default concurrent connection limit.
	DefaultMaxConnections = 1000
	// DefaultHandshakeTimeout is the default deadline for the startup and
	// authentication phase of a connection.
	DefaultHandshakeTimeout = 15 * time.Second

	// rejectWriteTimeout bounds the write of a rejection message so a
	// non-reading client cannot stall the accept loop.
	rejectWriteTimeout = time.Second
)

// LoadTLSConfig builds a server TLS configuration from PEM certificate and key
// files. It returns (nil, nil) when either path is empty (TLS disabled).
func LoadTLSConfig(certFile, keyFile string) (*tls.Config, error) {
	if certFile == "" || keyFile == "" {
		return nil, nil
	}
	cert, err := tls.LoadX509KeyPair(certFile, keyFile)
	if err != nil {
		return nil, fmt.Errorf("failed to load TLS key pair: %w", err)
	}
	return &tls.Config{
		Certificates: []tls.Certificate{cert},
		MinVersion:   tls.VersionTLS12,
	}, nil
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

		MaxConnections:   DefaultMaxConnections,
		HandshakeTimeout: DefaultHandshakeTimeout,
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
	maxConns := int64(s.MaxConnections)
	if maxConns <= 0 {
		maxConns = DefaultMaxConnections
	}
	s.Logger.Info("PGWireServer started", "addr", s.Addr, "tls", s.TLSConfig != nil, "max_connections", maxConns)

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

			if s.activeConns.Add(1) > maxConns {
				s.activeConns.Add(-1)
				s.Logger.Warn("PGWire connection rejected: too many connections",
					"remote", conn.RemoteAddr().String(), "max_connections", maxConns)
				_ = conn.SetWriteDeadline(time.Now().Add(rejectWriteTimeout))
				_ = writeFatalError(conn, "53300", "sorry, too many clients already")
				_ = conn.Close()
				continue
			}

			s.mu.Lock()
			s.connections[conn] = struct{}{}
			s.mu.Unlock()

			s.wg.Add(1)
			go func() {
				defer s.wg.Done()
				defer func() {
					s.activeConns.Add(-1)
					_ = conn.Close()
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
		_ = s.listener.Close()
	}

	s.mu.Lock()
	for conn := range s.connections {
		_ = conn.Close()
	}
	s.mu.Unlock()

	s.wg.Wait()
	s.Logger.Info("PGWireServer stopped")
}
