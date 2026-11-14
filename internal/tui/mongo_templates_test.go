package tui

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"mongo-tui/internal/engine"
)

func TestMongoTemplatesAndInsertName(t *testing.T) {
	fake := &fakeSession{info: engine.Info{ServerVersion: "8.0.38", Database: "app"}}
	m := mongoModel(t, fake)
	collection := &treeNode{kind: nodeCollection, title: "items", schema: "app", name: "items", openable: true}

	m = m.insertSelectTemplate(collection)
	if !strings.Contains(m.editor.Value(), "db.items.find({}).limit(100)") {
		t.Fatalf("select template = %q", m.editor.Value())
	}

	m.editor.SetValue("")
	m = m.insertDdl(collection, ddlCreate)
	if !strings.Contains(m.editor.Value(), "db.items.createIndex") {
		t.Fatalf("create template = %q", m.editor.Value())
	}

	m.editor.SetValue("")
	m = m.insertDdl(collection, ddlDrop)
	if !strings.Contains(m.editor.Value(), "db.items.drop()") {
		t.Fatalf("drop template = %q", m.editor.Value())
	}

	m.editor.SetValue("")
	m = m.insertDdl(collection, ddlAlter)
	if !strings.Contains(m.editor.Value(), "db.items.collMod") {
		t.Fatalf("alter template = %q", m.editor.Value())
	}

	index := &treeNode{kind: nodeIndex, title: "name_1", schema: "app", name: "items"}
	m.editor.SetValue("")
	m = m.insertDdl(index, ddlDrop)
	if !strings.Contains(m.editor.Value(), `db.items.dropIndex("name_1")`) {
		t.Fatalf("drop index template = %q", m.editor.Value())
	}

	database := &treeNode{kind: nodeDatabase, title: "app"}
	m.editor.SetValue("")
	m = m.insertDdl(database, ddlCreate)
	if !strings.Contains(m.editor.Value(), `db.createCollection("new_collection", {})`) {
		t.Fatalf("create collection template = %q", m.editor.Value())
	}
	wantCursor := strings.Index(m.editor.Value(), "new_collection") + len("new_collection")
	if got := m.editor.CursorOffset(); got != wantCursor {
		t.Fatalf("cursor = %d, want %d after the suggested name", got, wantCursor)
	}

	other := &treeNode{kind: nodeCollection, title: "items", schema: "other", name: "items", openable: true}
	m.editor.SetValue("")
	m = m.insertSelectTemplate(other)
	if !strings.Contains(m.status, "switch to other") {
		t.Fatalf("status = %q, want a switch hint", m.status)
	}

	m.editor.SetValue("")
	m = m.insertName(collection)
	if !strings.Contains(m.editor.Value(), `"items"`) {
		t.Fatalf("inserted name = %q", m.editor.Value())
	}
}

func TestMongoRunValidatesRequest(t *testing.T) {
	fake := &fakeSession{info: engine.Info{ServerVersion: "8.0.38", Database: "app"}}
	m := mongoModel(t, fake)
	m.editor.SetValue("db.items.find({")
	m.focus = focusEditor

	m, cmd := press(t, m, tea.KeyCtrlJ)
	if cmd != nil {
		t.Fatal("Ctrl+J returned a command for an incomplete MQL request")
	}
	if len(fake.statements()) != 0 {
		t.Fatalf("executed = %v, want nothing sent", fake.statements())
	}
	if !strings.Contains(strings.Join(m.messages, "\n"), "mql") {
		t.Fatalf("messages = %v, want an MQL error", m.messages)
	}
}

func TestMongoRunAcceptsMQL(t *testing.T) {
	fake := &fakeSession{
		info: engine.Info{ServerVersion: "8.0.38", Database: "app"},
		results: []engine.Result{{
			Columns:     []string{"_id", "name"},
			ColumnTypes: []string{"int", "string"},
			Rows:        [][]string{{"1", "Ann"}},
		}},
	}
	m := mongoModel(t, fake)
	m.editor.SetValue(`db.items.find({name: "Ann"})`)
	m.focus = focusEditor

	m, cmd := press(t, m, tea.KeyCtrlJ)
	if cmd == nil {
		t.Fatal("Ctrl+J returned no command")
	}
	msg := cmd().(execDoneMsg)
	if msg.err != nil {
		t.Fatalf("exec error = %v", msg.err)
	}
	executed := fake.statements()
	if len(executed) != 1 || executed[0] != `db.items.find({name: "Ann"})` {
		t.Fatalf("executed = %q", executed)
	}
	updated, _ := m.Update(msg)
	m = updated.(Model)
	if m.grid == nil || len(m.grid.res.Rows) != 1 {
		t.Fatalf("grid = %+v, want one row", m.grid)
	}
}

func TestMongoRunMultipleRequests(t *testing.T) {
	fake := &fakeSession{
		info: engine.Info{ServerVersion: "8.0.38", Database: "app"},
		results: []engine.Result{
			{RowCount: 1, Messages: []string{"inserted 1 document"}},
			{Columns: []string{"_id", "name"}, ColumnTypes: []string{"int", "string"}, Rows: [][]string{{"1", "Ann"}}},
		},
	}
	m := mongoModel(t, fake)
	m.editor.SetValue(`db.items.insertOne({_id: 1, name: "Ann"}); db.items.find({})`)
	m.focus = focusEditor

	m, cmd := press(t, m, tea.KeyCtrlJ)
	if cmd == nil {
		t.Fatal("Ctrl+J returned no command")
	}
	msg := cmd().(execDoneMsg)
	if msg.err != nil {
		t.Fatalf("exec error = %v", msg.err)
	}
	executed := fake.statements()
	if len(executed) != 2 {
		t.Fatalf("executed = %d requests, want 2: %#v", len(executed), executed)
	}
	updated, _ := m.Update(msg)
	m = updated.(Model)
	if m.grid == nil || len(m.grid.res.Rows) != 1 {
		t.Fatalf("grid = %+v, want the last result", m.grid)
	}
}
