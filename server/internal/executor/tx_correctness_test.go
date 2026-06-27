package executor

// Тесты корректности транзакций: read-your-own-writes (Bug #1), OCC-конфликт
// при autocommit-записи (Bug #2), стабильность NOW() (Bug #3), spill при COMMIT
// (Bug #4) и savepoint'ы.

import (
	"strconv"
	"strings"
	"testing"

	"vaultdb/internal/storage"
	"vaultdb/internal/txmanager"
)

// newTxStore создаёт общий storage+txmanager и регистрирует таблицу items.
// Возвращает фабрику сессий, разделяющих один и тот же движок.
func newTxStore(t *testing.T) (func() *Session, *txmanager.Manager, storage.StorageEngine) {
	t.Helper()
	dir := t.TempDir()
	txm := txmanager.NewManager()
	store, err := storage.NewPageStorageEngine(dir, nil, txm)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })

	bootstrap := NewSession(store, nil, txm, nil)
	executeSQL(t, bootstrap, "CREATE DATABASE txdb;")
	executeSQL(t, bootstrap, "USE txdb;")
	executeSQL(t, bootstrap, "CREATE TABLE items (id INT, name TEXT, qty INT);")

	newSession := func() *Session {
		s := NewSession(store, nil, txm, nil)
		executeSQL(t, s, "USE txdb;")
		return s
	}
	return newSession, txm, store
}

// TestReadYourOwnWrites — Bug #1: внутри транзакции INSERT/UPDATE/DELETE видны
// своей же транзакции, а ROLLBACK всё откатывает.
func TestReadYourOwnWrites(t *testing.T) {
	newSession, _, _ := newTxStore(t)
	s := newSession()

	executeSQL(t, s, "INSERT INTO items VALUES (1, 'apple', 10);")

	executeSQL(t, s, "BEGIN;")

	// INSERT виден внутри tx.
	executeSQL(t, s, "INSERT INTO items VALUES (2, 'banana', 20);")
	res := executeSQL(t, s, "SELECT id FROM items ORDER BY id;")
	if len(res.Rows) != 2 {
		t.Fatalf("after INSERT: expected 2 rows, got %d: %v", len(res.Rows), res.Rows)
	}

	// UPDATE виден внутри tx.
	executeSQL(t, s, "UPDATE items SET qty = 99 WHERE id = 2;")
	res = executeSQL(t, s, "SELECT qty FROM items WHERE id = 2;")
	if len(res.Rows) != 1 || res.Rows[0][0] != "99" {
		t.Fatalf("after UPDATE: expected qty=99, got %v", res.Rows)
	}

	// DELETE виден внутри tx.
	executeSQL(t, s, "DELETE FROM items WHERE id = 1;")
	res = executeSQL(t, s, "SELECT id FROM items ORDER BY id;")
	if len(res.Rows) != 1 || res.Rows[0][0] != "2" {
		t.Fatalf("after DELETE: expected only id=2, got %v", res.Rows)
	}

	// ROLLBACK всё отменяет.
	executeSQL(t, s, "ROLLBACK;")
	res = executeSQL(t, s, "SELECT id, qty FROM items ORDER BY id;")
	if len(res.Rows) != 1 || res.Rows[0][0] != "1" || res.Rows[0][1] != "10" {
		t.Fatalf("after ROLLBACK: expected only (1,10), got %v", res.Rows)
	}
}

// TestCommitConflictWithAutocommit — Bug #2: пока транзакция A держит снимок
// таблицы, конкурирующая autocommit-запись бьёт версию таблицы под общим
// commit-локом, и COMMIT транзакции A падает с конфликтом.
func TestCommitConflictWithAutocommit(t *testing.T) {
	newSession, _, _ := newTxStore(t)
	sessA := newSession()
	sessB := newSession()

	executeSQL(t, sessA, "INSERT INTO items VALUES (1, 'apple', 10);")

	// A открывает tx и буферизует UPDATE — снимок версии зафиксирован.
	executeSQL(t, sessA, "BEGIN;")
	executeSQL(t, sessA, "UPDATE items SET qty = 1 WHERE id = 1;")

	// B (autocommit) модифицирует ту же таблицу и коммитит — версия растёт.
	executeSQL(t, sessB, "UPDATE items SET qty = 2 WHERE id = 1;")

	// COMMIT A должен упасть с конфликтом.
	executeSQLExpectError(t, sessA, "COMMIT;")
}

// TestCommitConflictReadOnlyAccess — Bug #2a: даже только-читающая транзакция,
// которая зафиксировала снимок при чтении, конфликтует с конкурентной записью.
func TestCommitConflictReadOnlyAccess(t *testing.T) {
	newSession, _, _ := newTxStore(t)
	sessA := newSession()
	sessB := newSession()

	executeSQL(t, sessA, "INSERT INTO items VALUES (1, 'apple', 10);")

	executeSQL(t, sessA, "BEGIN;")
	// Только чтение — но снимок версии таблицы фиксируется (RecordAccess).
	executeSQL(t, sessA, "SELECT * FROM items;")

	// Конкурентная autocommit-запись.
	executeSQL(t, sessB, "INSERT INTO items VALUES (2, 'banana', 20);")

	executeSQLExpectError(t, sessA, "COMMIT;")
}

// TestNowStableInTx — Bug #3: значение NOW() внутри транзакции (видимое через
// overlay) совпадает с тем, что персистится после COMMIT.
func TestNowStableInTx(t *testing.T) {
	newSession, _, _ := newTxStore(t)
	s := newSession()

	executeSQL(t, s, "BEGIN;")
	executeSQL(t, s, "INSERT INTO items (id, name, qty) VALUES (1, NOW(), 0);")

	inTx := executeSQL(t, s, "SELECT name FROM items WHERE id = 1;")
	if len(inTx.Rows) != 1 {
		t.Fatalf("expected 1 row in tx, got %d", len(inTx.Rows))
	}
	valInTx := inTx.Rows[0][0]
	if valInTx == "" {
		t.Fatalf("NOW() produced empty value")
	}

	executeSQL(t, s, "COMMIT;")

	after := executeSQL(t, s, "SELECT name FROM items WHERE id = 1;")
	if len(after.Rows) != 1 {
		t.Fatalf("expected 1 row after commit, got %d", len(after.Rows))
	}
	if after.Rows[0][0] != valInTx {
		t.Fatalf("NOW() diverged: in-tx=%q persisted=%q", valInTx, after.Rows[0][0])
	}
}

// TestSpilledCommitApplied — Bug #4: при принудительном spill'е (низкий
// SpillThreshold) операция с большим payload не теряется и применяется при COMMIT.
func TestSpilledCommitApplied(t *testing.T) {
	dir := t.TempDir()
	txm := txmanager.NewManager()
	txm.SpillThreshold = 1 // спиллим сразу
	txm.SpillDir = t.TempDir()
	store, err := storage.NewPageStorageEngine(dir, nil, txm)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })

	s := NewSession(store, nil, txm, nil)
	executeSQL(t, s, "CREATE DATABASE sdb;")
	executeSQL(t, s, "USE sdb;")
	executeSQL(t, s, "CREATE TABLE items (id INT, name TEXT, qty INT);")

	// Один INSERT с большим количеством строк: сериализованная операция (одна
	// строка spill-файла) заведомо превышает 64KB — старый bufio.Scanner с
	// дефолтным лимитом потерял бы её молча (Bug #4).
	const nRows = 5000
	var b strings.Builder
	b.WriteString("INSERT INTO items VALUES ")
	for i := 0; i < nRows; i++ {
		if i > 0 {
			b.WriteByte(',')
		}
		b.WriteString("(")
		b.WriteString(itoa(i))
		b.WriteString(", 'name', ")
		b.WriteString(itoa(i))
		b.WriteString(")")
	}
	b.WriteString(";")

	executeSQL(t, s, "BEGIN;")
	executeSQL(t, s, b.String())

	// read-your-writes поверх spill'а тоже должен работать.
	inTx := executeSQL(t, s, "SELECT COUNT(*) FROM items;")
	if len(inTx.Rows) != 1 || inTx.Rows[0][0] != itoa(nRows) {
		t.Fatalf("expected %d rows in tx over spill, got %v", nRows, inTx.Rows)
	}

	executeSQL(t, s, "COMMIT;")

	res := executeSQL(t, s, "SELECT COUNT(*) FROM items;")
	if len(res.Rows) != 1 || res.Rows[0][0] != itoa(nRows) {
		t.Fatalf("expected %d rows after spilled commit (no silent loss), got %v", nRows, res.Rows)
	}
}

func itoa(n int) string {
	return strconv.Itoa(n)
}

// TestSavepoints — savepoint'ы поверх буферной модели.
func TestSavepoints(t *testing.T) {
	newSession, _, _ := newTxStore(t)
	s := newSession()

	executeSQL(t, s, "BEGIN;")
	executeSQL(t, s, "INSERT INTO items VALUES (1, 'a', 1);")
	executeSQL(t, s, "SAVEPOINT s1;")
	executeSQL(t, s, "INSERT INTO items VALUES (2, 'b', 2);")

	// До отката оба видны.
	res := executeSQL(t, s, "SELECT id FROM items ORDER BY id;")
	if len(res.Rows) != 2 {
		t.Fatalf("expected 2 rows before rollback to savepoint, got %d", len(res.Rows))
	}

	executeSQL(t, s, "ROLLBACK TO SAVEPOINT s1;")

	// После отката к s1 виден только 'a'.
	res = executeSQL(t, s, "SELECT id FROM items ORDER BY id;")
	if len(res.Rows) != 1 || res.Rows[0][0] != "1" {
		t.Fatalf("expected only id=1 after rollback to savepoint, got %v", res.Rows)
	}

	executeSQL(t, s, "COMMIT;")

	res = executeSQL(t, s, "SELECT id FROM items ORDER BY id;")
	if len(res.Rows) != 1 || res.Rows[0][0] != "1" {
		t.Fatalf("expected only id=1 persisted, got %v", res.Rows)
	}
}

// TestSavepointReleaseThenRollbackErrors — RELEASE удаляет маркер, после чего
// ROLLBACK TO к нему обязан вернуть ошибку.
func TestSavepointReleaseThenRollbackErrors(t *testing.T) {
	newSession, _, _ := newTxStore(t)
	s := newSession()

	executeSQL(t, s, "BEGIN;")
	executeSQL(t, s, "INSERT INTO items VALUES (1, 'a', 1);")
	executeSQL(t, s, "SAVEPOINT s1;")
	executeSQL(t, s, "RELEASE SAVEPOINT s1;")
	executeSQLExpectError(t, s, "ROLLBACK TO SAVEPOINT s1;")
	executeSQL(t, s, "COMMIT;")
}

// TestSavepointOutsideTxErrors — savepoint вне транзакции запрещён.
func TestSavepointOutsideTxErrors(t *testing.T) {
	newSession, _, _ := newTxStore(t)
	s := newSession()
	executeSQLExpectError(t, s, "SAVEPOINT s1;")
	executeSQLExpectError(t, s, "ROLLBACK TO SAVEPOINT s1;")
	executeSQLExpectError(t, s, "RELEASE SAVEPOINT nope;")
}
