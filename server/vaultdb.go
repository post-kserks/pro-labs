package vaultdb

import (
	"fmt"

	"vaultdb/internal/ai"
	"vaultdb/internal/executor"
	"vaultdb/internal/metrics"
	"vaultdb/internal/parser"
	"vaultdb/internal/storage"
	"vaultdb/internal/txmanager"
)

// VaultDB is the high-level embedded database engine.
type VaultDB struct {
	Storage     storage.StorageEngine
	Metrics     *metrics.Collector
	TxManager   *txmanager.Manager
	Broadcaster *executor.Broadcaster
	// Embedder, if set, enables SEMANTIC_MATCH and AI_EMBED.
	Embedder ai.Embedder
}

// Open creates a new embedded database instance.
func Open(dataDir string) (*VaultDB, error) {
	m := metrics.New()
	txm := txmanager.NewManager()
	s, err := storage.NewPageStorageEngine(dataDir, nil, txm)
	if err != nil {
		return nil, fmt.Errorf("open vaultdb: %w", err)
	}
	return &VaultDB{
		Storage:     s,
		Metrics:     m,
		TxManager:   txm,
		Broadcaster: executor.NewBroadcaster(),
	}, nil
}

// Close closes the database.
func (db *VaultDB) Close() error {
	return db.Storage.Close()
}

// Query executes a single SQL query.
func (db *VaultDB) Query(dbName, sql string) (*executor.Result, error) {
	stmt, err := parser.Parse(sql)
	if err != nil {
		return nil, err
	}

	session := executor.NewSession(db.Storage, db.Metrics, db.TxManager, db.Broadcaster)
	if db.Embedder != nil {
		session.SetEmbedder(db.Embedder)
	}
	if dbName != "" {
		session.SetCurrentDatabase(dbName)
	}

	return session.Execute(stmt)
}
