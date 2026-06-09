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
	b.mu.RLock()
	defer b.mu.RUnlock()

	for _, s := range b.subscriptions {
		if s.DB == dbName && s.Query.TableName == tableName {
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
}
