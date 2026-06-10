package executor

import (
	"vaultdb/internal/ai"
	"vaultdb/internal/metrics"
	"vaultdb/internal/parser"
	"vaultdb/internal/storage"
	"vaultdb/internal/txmanager"
)

type Session struct {
	executor  *Executor
	currentDB string

	ActiveTx    *txmanager.Transaction
	TxManager   *txmanager.Manager
	Broadcaster *Broadcaster

	PreparedStatements map[string]*PreparedStatement
}

type PreparedStatement struct {
	Name  string
	Query parser.Statement
}

func NewSession(store storage.StorageEngine, m *metrics.Collector, txm *txmanager.Manager, b *Broadcaster) *Session {
	return &Session{
		executor:           New(store, m, txm, b),
		TxManager:          txm,
		Broadcaster:        b,
		PreparedStatements: make(map[string]*PreparedStatement),
	}
}

// SetEmbedder подключает embedding-провайдер для SEMANTIC_MATCH/AI_EMBED.
func (s *Session) SetEmbedder(emb ai.Embedder) {
	s.executor.SetEmbedder(emb)
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
