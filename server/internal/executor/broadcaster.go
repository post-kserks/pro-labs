package executor

import (
	"log/slog"
	"sync"
	"sync/atomic"
	"time"

	"vaultdb/internal/parser"
)

// DropPolicy определяет поведение при переполнении канала подписки.
type DropPolicy int

const (
	// PolicyDrop — отбросить обновление с логированием (поведение по умолчанию).
	PolicyDrop DropPolicy = iota
	// PolicyBlock — блокировать до освобождения места; по таймауту клиент отписывается.
	PolicyBlock
	// PolicyEvict — вытеснить старейшее обновление и положить новое.
	PolicyEvict
)

// ParseDropPolicy разбирает значение drop_policy из конфигурации.
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

	// notifyMu сериализует notify для одной подписки: NotifyTableChanged может
	// запустить несколько горутин для одной подписки (на быстрых подряд
	// изменениях таблицы), а политика PolicyEvict (drain + insert) корректна
	// только при единственном писателе в канал. Мьютекс это гарантирует.
	notifyMu     sync.Mutex
	closed       atomic.Bool
	snapshotTxID uint64
}

// Close закрывает канал подписки ровно один раз.
func (s *Subscription) Close() {
	if s.closed.CompareAndSwap(false, true) {
		close(s.Send)
	}
}

// notify доставляет обновление согласно политике подписки.
// Возвращает false, если клиент должен быть отписан (block-таймаут).
func (s *Subscription) notify(res *Result, logger *slog.Logger) bool {
	// Только один notify на подписку одновременно — иначе drain+insert в
	// PolicyEvict гоняется между параллельными писателями.
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
			// Клиент слишком медленный — отписываем
			logger.Warn("live query subscription timed out, unsubscribing",
				"subscription", s.ID)
			return false
		}

	case PolicyEvict:
		// Один select: попытка отправить, при провале — дренировать и повторить.
		// Это O(1), не цикл.
		select {
		case s.Send <- res:
			// успех с первой попытки
		default:
			// Канал полон: дренируем одно старое сообщение и вставляем новое.
			select {
			case <-s.Send: // discard oldest
			default: // канал уже пуст (race condition — ok)
			}
			// Теперь место точно есть (мы единственный writer для этой подписки)
			select {
			case s.Send <- res:
			default:
				// Если всё равно не влезло — кто-то ещё пишет параллельно.
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

// Configure задаёт политику доставки Live Queries (вызывается при старте).
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

// NewSubscription создаёт подписку с настроенными политикой и буфером.
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

// NotifyTableChanged переоценивает и рассылает live-query подписки, чья базовая
// таблица совпала с изменившейся.
//
// ОГРАНИЧЕНИЕ: совпадение идёт только по основной таблице запроса
// (s.Query.TableName) в той же БД. Live query с JOIN или подзапросом по другой
// таблице НЕ будет переоценён при изменении этой другой таблицы — обновится
// только при изменении своей основной таблицы. Полноценное отслеживание
// зависимостей по всем читаемым таблицам пока не реализовано.
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
