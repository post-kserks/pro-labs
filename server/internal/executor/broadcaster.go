package executor

import (
	"sync"
	"vaultdb/internal/parser"
)

type Subscription struct {
	ID    string
	Query *parser.SelectStatement
	DB    string
	Send  chan *Result
}

type Broadcaster struct {
	mu            sync.RWMutex
	subscriptions map[string]*Subscription
}

func NewBroadcaster() *Broadcaster {
	return &Broadcaster{
		subscriptions: make(map[string]*Subscription),
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
		// Re-run the query
		cmd := &SelectCommand{stmt: s.Query}
		res, err := cmd.Execute(ctx)
		if err == nil {
			select {
			case s.Send <- res:
			default:
				// Drop notification if channel is full
			}
		}
	}
}
