package executor

import (
	"log/slog"
	"sync"
	"sync/atomic"
	"time"

	"vaultdb/internal/parser"
)

// DropPolicy defines behavior when the subscription channel overflows.
type DropPolicy int

const (
	// PolicyDrop — drop update with logging (default behavior).
	PolicyDrop DropPolicy = iota
	// PolicyBlock — block until space is freed; on timeout, client is unsubscribed.
	PolicyBlock
	// PolicyEvict — evict the oldest update and insert the new one.
	PolicyEvict
)

// ParseDropPolicy parses the drop_policy value from configuration.
func ParseDropPolicy(s string) DropPolicy {
	switch s {
	case "block":
		return PolicyBlock
	case "evict":
		return PolicyEvict
	default:
		return PolicyDrop
	}
}

type Subscription struct {
	ID    string
	Query *parser.SelectStatement
	DB    string
	Send  chan *Result

	DropPolicy   DropPolicy
	BlockTimeout time.Duration

	// notifyMu serializes notify for a single subscription: NotifyTableChanged can
	// launch multiple goroutines for one subscription (on rapid consecutive
	// table changes), and the PolicyEvict (drain + insert) policy is only correct
	// with a single writer to the channel. The mutex guarantees this.
	notifyMu     sync.Mutex
	closed       atomic.Bool
	snapshotTxID uint64
}

// Close closes the subscription channel exactly once.
func (s *Subscription) Close() {
	if s.closed.CompareAndSwap(false, true) {
		close(s.Send)
	}
}

// notify delivers an update according to the subscription policy.
// Returns false if the client should be unsubscribed (block timeout).
func (s *Subscription) notify(res *Result, logger *slog.Logger) bool {
	// Only one notify per subscription at a time — otherwise drain+insert in
	// PolicyEvict races between parallel writers.
	s.notifyMu.Lock()
	defer s.notifyMu.Unlock()

	if s.closed.Load() {
		return false
	}

	switch s.DropPolicy {
	case PolicyBlock:
		timeout := s.BlockTimeout
		if timeout <= 0 {
			timeout = 5 * time.Second
		}
		select {
		case s.Send <- res:
			return true
		case <-time.After(timeout):
			// Client too slow — unsubscribe
			logger.Warn("live query subscription timed out, unsubscribing",
				"subscription", s.ID)
			return false
		}

	case PolicyEvict:
		// Single select: attempt to send; on failure — drain and retry.
		// This is O(1), not a loop.
		select {
		case s.Send <- res:
			// success on first try
		default:
			// Channel full: drain one old message and insert the new one.
			select {
			case <-s.Send: // discard oldest
			default: // channel already empty (race condition — ok)
			}
			// Now there is definitely room (we are the only writer for this subscription)
			select {
			case s.Send <- res:
			default:
				// If it still didn't fit — someone else is writing in parallel.
				logger.Warn("evict policy: could not insert after drain, dropping",
					"session", s.ID)
			}
		}
		return true

	default: // PolicyDrop
		select {
		case s.Send <- res:
		default:
			logger.Warn("live query notification dropped, client too slow",
				"subscription", s.ID)
		}
		return true
	}
}

type Broadcaster struct {
	mu            sync.RWMutex
	subscriptions map[string]*Subscription
	workerPool    chan struct{} // buffered channel = max concurrent live query evaluations

	logger        *slog.Logger
	defaultPolicy DropPolicy
	blockTimeout  time.Duration
	bufferSize    int
}

func NewBroadcaster() *Broadcaster {
	return &Broadcaster{
		subscriptions: make(map[string]*Subscription),
		workerPool:    make(chan struct{}, 64),
		logger:        slog.Default(),
		defaultPolicy: PolicyDrop,
		blockTimeout:  5 * time.Second,
		bufferSize:    256,
	}
}

// Configure sets the Live Queries delivery policy (called at startup).
func (b *Broadcaster) Configure(policy DropPolicy, blockTimeout time.Duration, bufferSize int, logger *slog.Logger) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.defaultPolicy = policy
	if blockTimeout > 0 {
		b.blockTimeout = blockTimeout
	}
	if bufferSize > 0 {
		b.bufferSize = bufferSize
	}
	if logger != nil {
		b.logger = logger
	}
}

// NewSubscription creates a subscription with configured policy and buffer.
func (b *Broadcaster) NewSubscription(id string, query *parser.SelectStatement, db string, snapshotTxID uint64) *Subscription {
	b.mu.RLock()
	defer b.mu.RUnlock()
	return &Subscription{
		ID:           id,
		Query:        query,
		DB:           db,
		Send:         make(chan *Result, b.bufferSize),
		DropPolicy:   b.defaultPolicy,
		BlockTimeout: b.blockTimeout,
		snapshotTxID: snapshotTxID,
	}
}

func (b *Broadcaster) Subscribe(s *Subscription) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.subscriptions[s.ID] = s
}

func (b *Broadcaster) Unsubscribe(id string) {
	b.mu.Lock()
	defer b.mu.Unlock()
	delete(b.subscriptions, id)
}

// NotifyTableChanged re-evaluates and sends live-query subscriptions whose base
// table matched the changed one.
//
// LIMITATION: matching is only by the query's base table
// (s.Query.TableName) in the same database. A live query with JOIN or subquery on another
// table will NOT be re-evaluated when that other table changes — it will only update
// when its own base table changes. Full dependency tracking across all read tables
// is not yet implemented.
func (b *Broadcaster) NotifyTableChanged(dbName, tableName string, ctx *ExecutionContext) {
	// Snapshot matching subscriptions first so subscriber queries do not run
	// while holding the broadcaster lock.
	b.mu.RLock()
	matched := make([]*Subscription, 0, len(b.subscriptions))
	for _, s := range b.subscriptions {
		if s.DB == dbName && s.Query.TableName == tableName {
			matched = append(matched, s)
		}
	}
	b.mu.RUnlock()

	for _, s := range matched {
		sub := s
		// Acquire worker slot (skip notification if pool is full to avoid overload).
		select {
		case b.workerPool <- struct{}{}:
		default:
			b.logger.Warn("live query worker pool exhausted, skipping notification",
				"subscription", sub.ID)
			continue
		}

		go func() {
			defer func() { <-b.workerPool }()
			defer func() {
				if r := recover(); r != nil {
					b.logger.Error("panic in live query notification",
						"db", dbName, "table", tableName, "panic", r)
				}
			}()

			sess := NewSession(ctx.Storage, ctx.Metrics, ctx.TxManager, ctx.Broadcaster)
			sess.SetCurrentDatabase(sub.DB)
			if ctx.WAL != nil {
				sess.SetWAL(ctx.WAL)
			}
			if ctx.Embedder != nil {
				sess.SetEmbedder(ctx.Embedder)
			}
			if sub.snapshotTxID > 0 {
				sess.SetSnapshotTxID(sub.snapshotTxID)
			}

			res, err := sess.Execute(sub.Query)
			sess.Close()

			if err != nil {
				return
			}
			if !sub.notify(res, b.logger) {
				b.Unsubscribe(sub.ID)
				sub.Close()
			}
		}()
	}
}
