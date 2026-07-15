package executor

import (
	"context"
	"sort"
	"sync"
	"time"

	"vaultdb/internal/core/executor/types"
)

func init() {
	types.KillSessionFunc = GlobalRegistry.KillSession
}

// SessionState represents the current execution state of a session.
type SessionState string

const (
	StateRunning  SessionState = "RUNNING"
	StateIdle     SessionState = "IDLE"
	StateLockWait SessionState = "LOCK_WAIT"
	StateIOWait   SessionState = "IO_WAIT"
)

// SessionInfo holds live metadata for a connected session.
type SessionInfo struct {
	ID        uint64
	User      string
	DBName    string
	Query     string
	State     SessionState
	StartedAt time.Time
	TxID      uint64
	cancelCtx context.CancelFunc
}

// SessionRegistry manages active session metadata safely across goroutines.
type SessionRegistry struct {
	mu       sync.RWMutex
	sessions map[uint64]*SessionInfo
}

// GlobalRegistry is the singleton registry for all active sessions in VaultDB.
var GlobalRegistry = NewSessionRegistry()

// NewSessionRegistry creates a new SessionRegistry instance.
func NewSessionRegistry() *SessionRegistry {
	return &SessionRegistry{
		sessions: make(map[uint64]*SessionInfo),
	}
}

// RegisterSession registers or updates a session in the registry and returns its info.
func (r *SessionRegistry) RegisterSession(id uint64, user, db, query string, txID uint64, cancel context.CancelFunc) *SessionInfo {
	r.mu.Lock()
	defer r.mu.Unlock()

	info := &SessionInfo{
		ID:        id,
		User:      user,
		DBName:    db,
		Query:     query,
		State:     StateRunning,
		StartedAt: time.Now(),
		TxID:      txID,
		cancelCtx: cancel,
	}
	r.sessions[id] = info
	return info
}

// UnregisterSession removes a session from the registry.
func (r *SessionRegistry) UnregisterSession(id uint64) {
	r.mu.Lock()
	defer r.mu.Unlock()
	delete(r.sessions, id)
}

// UpdateQuery updates a session's current query, state, and transaction ID.
func (r *SessionRegistry) UpdateQuery(id uint64, query string, state SessionState, txID uint64) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if info, exists := r.sessions[id]; exists && info != nil {
		info.Query = query
		info.State = state
		info.TxID = txID
		if state == StateIdle {
			info.cancelCtx = nil
		}
	}
}

// GetActiveSessions returns a sorted snapshot copy of all active sessions.
func (r *SessionRegistry) GetActiveSessions() []SessionInfo {
	r.mu.RLock()
	defer r.mu.RUnlock()

	list := make([]SessionInfo, 0, len(r.sessions))
	for _, info := range r.sessions {
		if info != nil {
			list = append(list, *info)
		}
	}
	sort.Slice(list, func(i, j int) bool {
		return list[i].ID < list[j].ID
	})
	return list
}

// KillSession attempts to cancel the running query of the given session ID.
// Returns true if the session was found and had a valid cancellation context, false otherwise.
func (r *SessionRegistry) KillSession(id uint64) bool {
	r.mu.RLock()
	info, exists := r.sessions[id]
	if !exists || info == nil || info.cancelCtx == nil {
		r.mu.RUnlock()
		return false
	}
	cancel := info.cancelCtx
	r.mu.RUnlock()

	cancel()
	return true
}
