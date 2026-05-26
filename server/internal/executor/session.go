package executor

import (
	"vaultdb/internal/metrics"
	"vaultdb/internal/parser"
	"vaultdb/internal/storage"
	"vaultdb/internal/txmanager"
)

type Session struct {
	executor  *Executor
	currentDB string

	ActiveTx  *txmanager.Transaction
	TxManager *txmanager.Manager

	PreparedStatements map[string]*PreparedStatement
}

type PreparedStatement struct {
	Name  string
	Query parser.Statement
}

func NewSession(store storage.StorageEngine, m *metrics.Collector, txm *txmanager.Manager) *Session {
	return &Session{
		executor:           New(store, m),
		TxManager:          txm,
		PreparedStatements: make(map[string]*PreparedStatement),
	}
}

func (s *Session) IsInTx() bool {
	return s.ActiveTx != nil && s.ActiveTx.State == txmanager.TxActive
}

func (s *Session) Execute(stmt parser.Statement) (*Result, error) {
	return s.executor.Run(stmt, s)
}

func (s *Session) CurrentDatabase() string {
	return s.currentDB
}

func (s *Session) SetCurrentDatabase(name string) {
	s.currentDB = name
}
