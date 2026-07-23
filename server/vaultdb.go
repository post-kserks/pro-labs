package vaultdb

import (
	"context"
	"fmt"
	"log/slog"
	"path/filepath"

	"vaultdb/internal/core/ai"
	"vaultdb/internal/core/audit"
	"vaultdb/internal/core/executor"
	"vaultdb/internal/core/metrics"
	"vaultdb/internal/core/storage"
	"vaultdb/internal/core/txmanager"
	"vaultdb/internal/core/wal"
)

// VaultDB is the high-level embedded database engine.
type VaultDB struct {
	Storage     storage.StorageEngine
	Metrics     *metrics.Collector
	TxManager   *txmanager.Manager
	Broadcaster *executor.Broadcaster
	// Embedder, if set, enables SEMANTIC_MATCH and AI_EMBED.
	Embedder   ai.Embedder
	AuditTable *audit.TableLog
}

// Open creates a new embedded database instance with WAL for durability.
func Open(dataDir string) (*VaultDB, error) {
	m := metrics.New()
	txm := txmanager.NewManager()

	walPath := filepath.Join(dataDir, "wal", "vaultdb.wal")
	w, err := wal.Open(walPath)
	if err != nil {
		return nil, fmt.Errorf("open vaultdb WAL: %w", err)
	}

	s, err := storage.NewPageStorageEngine(dataDir, w, txm)
	if err != nil {
		w.Close()
		return nil, fmt.Errorf("open vaultdb: %w", err)
	}

	if err := s.RecoverFromWAL(); err != nil {
		s.Close()
		w.Close()
		return nil, fmt.Errorf("vaultdb WAL recovery: %w", err)
	}

	// Create audit log system table if not exists.
	auditLog := audit.NewTableLog(s)
	if err := auditLog.EnsureTable(); err != nil {
		slog.Warn("failed to create audit log table", "error", err)
	}

	return &VaultDB{
		Storage:     s,
		Metrics:     m,
		TxManager:   txm,
		Broadcaster: executor.NewBroadcaster(),
		AuditTable:  auditLog,
	}, nil
}

// Close closes the database.
func (db *VaultDB) Close() error {
	return db.Storage.Close()
}

// Query executes a single SQL query.
func (db *VaultDB) Query(dbName, sql string) (*Result, error) {
	return db.QueryContext(context.Background(), dbName, sql)
}

// QueryContext executes a single SQL query with a context for cancellation.
func (db *VaultDB) QueryContext(ctx context.Context, dbName, sql string) (*Result, error) {
	session := executor.NewSession(db.Storage, db.Metrics, db.TxManager, db.Broadcaster)
	session.SetServerContext(ctx)
	defer session.Close()

	stmt, err := session.Parse(sql)
	if err != nil {
		return nil, err
	}

	if db.Embedder != nil {
		session.SetEmbedder(db.Embedder)
	}
	if db.AuditTable != nil {
		session.AuditTable = db.AuditTable
	}
	if dbName != "" {
		session.SetCurrentDatabase(dbName)
	}

	internalResult, err := session.Execute(stmt)
	if err != nil {
		return nil, err
	}

	return fromInternal(internalResult), nil
}
