package executor

import (
	"log/slog"
	"sync"
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
	}, "testdb", 0)
	b.Subscribe(sub)

	sub.Send <- &Result{}

	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		b.NotifyTableChanged("testdb", "items", ctx)
	}()

	time.Sleep(50 * time.Millisecond)

	<-sub.Send

	select {
	case <-sub.Send:
	case <-time.After(2 * time.Second):
		t.Fatal("timeout waiting for async notification to complete")
	}

	wg.Wait()

	b.Unsubscribe(sub.ID)
	sub.Close()
}

// versionedMockStorage tracks rows at different snapshot versions.
type versionedMockStorage struct {
	*MockStorage
	snapshotRows map[string][]storage.Row
	snapshotTxID uint64
}

func newVersionedMockStorage(snapshotTxID uint64) *versionedMockStorage {
	return &versionedMockStorage{
		MockStorage:  NewMockStorage(),
		snapshotRows: make(map[string][]storage.Row),
		snapshotTxID: snapshotTxID,
	}
}

func (v *versionedMockStorage) ReadRowsAsOf(dbName, tableName string, txID uint64) ([]storage.Row, error) {
	if txID <= v.snapshotTxID {
		if rows, ok := v.snapshotRows[dbName+"/"+tableName]; ok {
			return rows, nil
		}
	}
	return v.rows[dbName][tableName], nil
}

func TestLiveQuerySnapshotIsolation(t *testing.T) {
	store := newVersionedMockStorage(5)
	store.databases["mydb"] = true
	store.ensureDB("mydb")
	store.tables["mydb"]["orders"] = &storage.TableSchema{
		Name: "orders",
		Columns: []storage.ColumnSchema{
			{Name: "id", Type: "INT"},
			{Name: "status", Type: "TEXT"},
		},
	}
	// Snapshot data: 2 rows
	store.rows["mydb"]["orders"] = []storage.Row{
		{int64(1), "pending"},
		{int64(2), "shipped"},
	}
	store.snapshotRows["mydb/orders"] = []storage.Row{
		{int64(1), "pending"},
		{int64(2), "shipped"},
	}

	// Simulate a mutation: add a third row
	store.rows["mydb"]["orders"] = append(store.rows["mydb"]["orders"],
		storage.Row{int64(3), "delivered"},
	)

	session := newTestSession(store)
	session.SetCurrentDatabase("testdb")

	b := NewBroadcaster()
	b.Configure(PolicyDrop, 5*time.Second, 256, slog.Default())

	// Create subscription with snapshot txID=5 — should see only the 2 original rows
	sub := b.NewSubscription("snap-test", &parser.SelectStatement{
		TableName: "orders",
		Columns: []parser.SelectColumn{
			{Expr: &parser.ColumnRef{Name: "id"}},
			{Expr: &parser.ColumnRef{Name: "status"}},
		},
	}, "mydb", 5)
	b.Subscribe(sub)
	defer b.Unsubscribe(sub.ID)
	defer sub.Close()

	ctx := &ExecutionContext{
		Storage:      store,
		Session:      session,
		SnapshotTxID: 5,
	}

	b.NotifyTableChanged("mydb", "orders", ctx)

	select {
	case res := <-sub.Send:
		if res == nil {
			t.Fatal("expected non-nil result")
		}
		// Should see only the 2 rows from the snapshot
		if len(res.Rows) != 2 {
			t.Fatalf("expected 2 rows (snapshot), got %d: %v", len(res.Rows), res.Rows)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timeout waiting for live query notification")
	}
}

func TestLiveQueryNoSnapshotSeesCurrent(t *testing.T) {
	store := NewMockStorage()
	store.databases["mydb"] = true
	store.ensureDB("mydb")
	store.tables["mydb"]["orders"] = &storage.TableSchema{
		Name: "orders",
		Columns: []storage.ColumnSchema{
			{Name: "id", Type: "INT"},
		},
	}
	store.rows["mydb"]["orders"] = []storage.Row{
		{int64(1)},
		{int64(2)},
		{int64(3)},
	}

	session := newTestSession(store)
	session.SetCurrentDatabase("mydb")

	b := NewBroadcaster()
	b.Configure(PolicyDrop, 5*time.Second, 256, slog.Default())

	// No snapshot (txID=0) — should see all current rows
	sub := b.NewSubscription("no-snap", &parser.SelectStatement{
		TableName: "orders",
		Columns: []parser.SelectColumn{
			{Expr: &parser.ColumnRef{Name: "id"}},
		},
	}, "mydb", 0)
	b.Subscribe(sub)
	defer b.Unsubscribe(sub.ID)
	defer sub.Close()

	ctx := &ExecutionContext{
		Storage: store,
		Session: session,
	}

	b.NotifyTableChanged("mydb", "orders", ctx)

	select {
	case res := <-sub.Send:
		if res == nil {
			t.Fatal("expected non-nil result")
		}
		if len(res.Rows) != 3 {
			t.Fatalf("expected 3 rows (current), got %d", len(res.Rows))
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timeout waiting for live query notification")
	}
}
