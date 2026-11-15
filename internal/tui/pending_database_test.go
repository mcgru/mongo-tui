package tui

import (
	"testing"
	"time"

	"mongo-tui/internal/engine"
)

func TestPlusOnDatabasesOpensNewDatabasePrompt(t *testing.T) {
	fake := &fakeMongoCatalog{
		fakeSession: &fakeSession{info: engine.Info{ServerVersion: "8.0.38", Database: "app"}},
		databases:   []engine.Item{{Name: "app", Current: true}},
	}
	m := mongoModel(t, fake)
	m.focus = focusTree
	m.tree.cursor = 1 // root, Databases, Users, Roles

	updated, _ := m.handleTreeKey("+")
	if got := updated.(Model); got.scr != screenNewDatabase {
		t.Fatalf("scr = %v, want the new-database prompt", got.scr)
	}
}

func TestCreatePendingDatabaseStagesAndAppendsNode(t *testing.T) {
	fake := &fakeMongoCatalog{
		fakeSession: &fakeSession{info: engine.Info{ServerVersion: "8.0.38", Database: "app"}},
		databases:   []engine.Item{{Name: "app", Current: true}},
	}
	m := mongoModel(t, fake)

	updated, cmd := m.createPendingDatabase("newdb")
	m = updated.(Model)
	if m.scr != screenMain || m.pendingDatabase != "newdb" {
		t.Fatalf("scr=%v pending=%q, want main with staged newdb", m.scr, m.pendingDatabase)
	}
	if m.sess.Summary().Database != "newdb" {
		t.Fatalf("session db = %q, want newdb", m.sess.Summary().Database)
	}
	if cmd == nil {
		t.Fatal("no reload command issued")
	}

	msg, ok := cmd().(loadNodeMsg)
	if !ok {
		t.Fatalf("reload produced %T, want loadNodeMsg", msg)
	}
	after, _ := m.Update(msg)
	m = after.(Model)

	if m.pendingDatabase != "newdb" {
		t.Fatalf("pending cleared too early: %q", m.pendingDatabase)
	}
	staged := findDatabaseNode(m.treeChild("Databases"), "newdb")
	if staged == nil || !staged.pending || !staged.current {
		t.Fatalf("staged node = %+v, want pending+current", staged)
	}
}

func TestCreatePendingDatabaseResolvesWhenServerListsIt(t *testing.T) {
	fake := &fakeMongoCatalog{
		fakeSession: &fakeSession{info: engine.Info{ServerVersion: "8.0.38", Database: "app"}},
		databases:   []engine.Item{{Name: "app"}, {Name: "newdb", Current: true}},
	}
	m := mongoModel(t, fake)

	updated, cmd := m.createPendingDatabase("newdb")
	m = updated.(Model)
	after, _ := m.Update(cmd().(loadNodeMsg))
	m = after.(Model)

	if m.pendingDatabase != "" {
		t.Fatalf("pending = %q, want resolved once the server lists it", m.pendingDatabase)
	}
	if staged := findDatabaseNode(m.treeChild("Databases"), "newdb"); staged != nil && staged.pending {
		t.Fatal("newdb should no longer be marked pending")
	}
}

func TestCreatePendingDatabaseRejectsInvalidName(t *testing.T) {
	fake := &fakeMongoCatalog{
		fakeSession: &fakeSession{info: engine.Info{ServerVersion: "8.0.38", Database: "app"}},
	}
	m := mongoModel(t, fake)

	updated, cmd := m.createPendingDatabase("bad name")
	m = updated.(Model)
	if m.pendingDatabase != "" || cmd != nil {
		t.Fatalf("pending=%q cmd=%v, want nothing staged for an invalid name", m.pendingDatabase, cmd != nil)
	}
	if m.status != "invalid database name" {
		t.Fatalf("status = %q", m.status)
	}
}

func TestExecDoneReloadsWhilePending(t *testing.T) {
	fake := &fakeMongoCatalog{
		fakeSession: &fakeSession{info: engine.Info{ServerVersion: "8.0.38", Database: "app"}},
		databases:   []engine.Item{{Name: "app", Current: true}},
	}
	m := mongoModel(t, fake)
	m = m.createPendingDatabaseNoCmd(t)

	updated, cmd := m.Update(execDoneMsg{statement: `db.c.insertOne({a:1})`, result: engine.Result{}, duration: time.Millisecond})
	if cmd == nil {
		t.Fatal("expected a databases reload command after a write while staging")
	}
	_ = updated
}

func TestSwitchDatabaseClearsPending(t *testing.T) {
	fake := &fakeMongoCatalog{
		fakeSession: &fakeSession{info: engine.Info{ServerVersion: "8.0.38", Database: "newdb"}},
	}
	m := mongoModel(t, fake)
	m.pendingDatabase = "newdb"

	updated, _ := m.switchDatabase("app")
	if got := updated.(Model); got.pendingDatabase != "" {
		t.Fatalf("pending = %q, want cleared when switching to a real database", got.pendingDatabase)
	}
}

func TestValidMongoDatabaseName(t *testing.T) {
	good := []string{"app", "my_db1", "NewDb"}
	bad := []string{"", "a b", "a.b", "a$b", "a/b", "a\\b", "a\"b", "a*b"}
	for _, name := range good {
		if !validMongoDatabaseName(name) {
			t.Fatalf("validMongoDatabaseName(%q) = false, want true", name)
		}
	}
	for _, name := range bad {
		if validMongoDatabaseName(name) {
			t.Fatalf("validMongoDatabaseName(%q) = true, want false", name)
		}
	}
}

// createPendingDatabaseNoCmd stages a database and drops the returned reload cmd,
// leaving a model with a pending database for tests that only need the state.
func (m Model) createPendingDatabaseNoCmd(t *testing.T) Model {
	t.Helper()
	updated, cmd := m.createPendingDatabase("newdb")
	m = updated.(Model)
	if cmd != nil {
		after, _ := m.Update(cmd())
		m = after.(Model)
	}
	return m
}

func findDatabaseNode(folder *treeNode, title string) *treeNode {
	if folder == nil {
		return nil
	}
	for _, child := range folder.children {
		if child.kind == nodeDatabase && child.title == title {
			return child
		}
	}
	return nil
}
