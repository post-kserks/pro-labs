package executor

import (
	"testing"
)

func TestForeignKeyInsertReject(t *testing.T) {
	session := setupSession(t)
	executeSQL(t, session, "CREATE TABLE departments (id INT PRIMARY KEY, name TEXT);")
	executeSQL(t, session, "INSERT INTO departments VALUES (1, 'Engineering');")
	executeSQL(t, session, "CREATE TABLE employees (id INT, dept_id INT);")
	executeSQL(t, session, "ALTER TABLE employees ADD CONSTRAINT fk_dept FOREIGN KEY (dept_id) REFERENCES departments(id);")
	executeSQL(t, session, "INSERT INTO employees VALUES (1, 1);")
	executeSQLExpectError(t, session, "INSERT INTO employees VALUES (2, 999);")
}

func TestForeignKeyDeleteReject(t *testing.T) {
	session := setupSession(t)
	executeSQL(t, session, "CREATE TABLE parents (id INT PRIMARY KEY, name TEXT);")
	executeSQL(t, session, "INSERT INTO parents VALUES (1, 'parent1');")
	executeSQL(t, session, "CREATE TABLE children (id INT, parent_id INT);")
	executeSQL(t, session, "ALTER TABLE children ADD CONSTRAINT fk_parent FOREIGN KEY (parent_id) REFERENCES parents(id);")
	executeSQL(t, session, "INSERT INTO children VALUES (1, 1);")
	executeSQLExpectError(t, session, "DELETE FROM parents WHERE id = 1;")
}

func TestForeignKeyUpdateReject(t *testing.T) {
	session := setupSession(t)
	executeSQL(t, session, "CREATE TABLE categories (id INT PRIMARY KEY, name TEXT);")
	executeSQL(t, session, "INSERT INTO categories VALUES (1, 'Cat1');")
	executeSQL(t, session, "INSERT INTO categories VALUES (2, 'Cat2');")
	executeSQL(t, session, "CREATE TABLE items (id INT, cat_id INT);")
	executeSQL(t, session, "ALTER TABLE items ADD CONSTRAINT fk_cat FOREIGN KEY (cat_id) REFERENCES categories(id);")
	executeSQL(t, session, "INSERT INTO items VALUES (1, 1);")
	executeSQLExpectError(t, session, "UPDATE items SET cat_id = 999 WHERE id = 1;")
	executeSQL(t, session, "UPDATE items SET cat_id = 2 WHERE id = 1;")
}

func TestForeignKeyDeleteCascade(t *testing.T) {
	session := setupSession(t)
	executeSQL(t, session, "CREATE TABLE parents2 (id INT PRIMARY KEY, name TEXT);")
	executeSQL(t, session, "INSERT INTO parents2 VALUES (1, 'parent1');")
	executeSQL(t, session, "INSERT INTO parents2 VALUES (2, 'parent2');")
	executeSQL(t, session, "CREATE TABLE children2 (id INT, parent_id INT);")
	executeSQL(t, session, "ALTER TABLE children2 ADD CONSTRAINT fk_parent2 FOREIGN KEY (parent_id) REFERENCES parents2(id) ON DELETE CASCADE;")
	executeSQL(t, session, "INSERT INTO children2 VALUES (10, 1);")
	executeSQL(t, session, "INSERT INTO children2 VALUES (20, 1);")
	executeSQL(t, session, "INSERT INTO children2 VALUES (30, 2);")

	executeSQL(t, session, "DELETE FROM parents2 WHERE id = 1;")

	children := executeSQL(t, session, "SELECT id, parent_id FROM children2;")
	if len(children.Rows) != 1 {
		t.Fatalf("expected 1 child row remaining, got %d", len(children.Rows))
	}
}
