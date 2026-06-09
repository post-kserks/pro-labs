package server

import (
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
}

// Open creates a new embedded database instance.
func Open(dataDir string) (*VaultDB, error) {
	m := metrics.New()
	s := storage.NewFileStorageEngine(dataDir, m)
	return &VaultDB{
		Storage:     s,
		Metrics:     m,
		TxManager:   txmanager.NewManager(),
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
	if dbName != "" {
		session.SetCurrentDatabase(dbName)
	}

	return session.Execute(stmt)
}
