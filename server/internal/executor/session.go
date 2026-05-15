package executor

import (
	"vaultdb/internal/parser"
	"vaultdb/internal/storage"
)

type Session struct {
	executor  *Executor
	currentDB string
}

func NewSession(store storage.StorageEngine) *Session {
	return &Session{executor: New(store)}
}

func (s *Session) Execute(stmt parser.Statement) (*Result, error) {
	return s.executor.Run(stmt, &s.currentDB)
}

func (s *Session) CurrentDatabase() string {
	return s.currentDB
}

func (s *Session) SetCurrentDatabase(name string) {
	s.currentDB = name
}
