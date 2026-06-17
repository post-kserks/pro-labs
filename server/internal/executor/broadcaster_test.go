package executor

import (
	"log/slog"
	"testing"
	"time"

	"vaultdb/internal/parser"
	"vaultdb/internal/storage"
)

func TestSubscriptionPolicyEvict(t *testing.T) {
	s := &Subscription{
		Send:       make(chan *Result, 2),
		DropPolicy: PolicyEvict,
	}
	logger := slog.Default()

	for i := 0; i < 5; i++ {
		if !s.notify(&Result{Affected: i}, logger) {
			t.Fatal("evict policy must not unsubscribe")
		}
	}

	// В буфере должны остаться два последних обновления (3 и 4)
	first := <-s.Send
	second := <-s.Send
	if first.Affected != 3 || second.Affected != 4 {
		t.Fatalf("expected newest updates kept, got %d and %d", first.Affected, second.Affected)
	}
}

func TestSubscriptionPolicyBlockTimesOut(t *testing.T) {
	s := &Subscription{
		Send:         make(chan *Result, 1),
		DropPolicy:   PolicyBlock,
		BlockTimeout: 30 * time.Millisecond,
	}
	logger := slog.Default()

	if !s.notify(&Result{}, logger) {
		t.Fatal("first notify must succeed")
	}
	// Буфер полон, читателя нет — по таймауту notify должен вернуть false
	start := time.Now()
	if s.notify(&Result{}, logger) {
		t.Fatal("second notify must time out and request unsubscribe")
	}
	if time.Since(start) < 30*time.Millisecond {
		t.Fatal("block policy returned before timeout")
	}
}

func TestSubscriptionPolicyDrop(t *testing.T) {
	s := &Subscription{
		Send:       make(chan *Result, 1),
		DropPolicy: PolicyDrop,
	}
	logger := slog.Default()

	if !s.notify(&Result{Affected: 1}, logger) || !s.notify(&Result{Affected: 2}, logger) {
		t.Fatal("drop policy must never unsubscribe")
	}
	got := <-s.Send
	if got.Affected != 1 {
		t.Fatalf("drop policy must keep oldest update, got %d", got.Affected)
	}
}

func TestSubscriptionCloseIsIdempotent(t *testing.T) {
	s := &Subscription{Send: make(chan *Result)}
	s.Close()
	s.Close() // не должно паниковать
	if s.notify(&Result{}, slog.Default()) {
		t.Fatal("notify after close must request unsubscribe")
	}
}

func TestBroadcasterAsync(t *testing.T) {
	store := NewMockStorage()
	store.databases["testdb"] = true
	store.ensureDB("testdb")
	store.tables["testdb"]["items"] = &storage.TableSchema{
		Name: "items",
		Columns: []storage.ColumnSchema{
			{Name: "id", Type: "INT"},
		},
	}
	store.rows["testdb"]["items"] = []storage.Row{{int64(1)}}

	session := newTestSession(store)
	session.SetCurrentDatabase("testdb")
	ctx := &ExecutionContext{
		Storage: store,
		Session: session,
	}

	b := NewBroadcaster()
	b.Configure(PolicyBlock, 5*time.Second, 1, slog.Default())

	sub := b.NewSubscription("slow", &parser.SelectStatement{
		TableName: "items",
		Columns: []parser.SelectColumn{
			{Expr: &parser.ColumnRef{Name: "id"}},
		},
	}, "testdb")
	b.Subscribe(sub)

	// Fill the channel so the next notify will block waiting for a consumer.
	sub.Send <- &Result{}

	start := time.Now()
	b.NotifyTableChanged("testdb", "items", ctx)
	elapsed := time.Since(start)

	// The call must return quickly because queries run in goroutines.
	// Without goroutines it would block for ~5 seconds.
	if elapsed > 500*time.Millisecond {
		t.Fatalf("NotifyTableChanged blocked for %v, expected async return", elapsed)
	}

	// Allow goroutines to finish before cleanup.
	time.Sleep(100 * time.Millisecond)
	sub.Close()
}
