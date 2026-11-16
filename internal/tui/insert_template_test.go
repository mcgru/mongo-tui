package tui

import (
	"strings"
	"testing"
	"unicode/utf8"

	tea "github.com/charmbracelet/bubbletea"

	"mongo-tui/internal/engine"
)

func insertTemplateModel(t *testing.T) Model {
	t.Helper()
	fake := &fakeSession{info: engine.Info{ServerVersion: "8.0.38", Database: "app"}}
	m := mongoModel(t, fake)
	m.focus = focusGrid
	m.tab = tabResult
	m.grid = newGrid(&engine.Result{
		Columns:     []string{"_id", "createdAt", "name", "qty", "meta.color", "tags"},
		ColumnTypes: []string{"objectId", "date", "string", "int", "string", "array"},
		Rows: [][]string{{
			`ObjectID("507f1f77bcf86cd799439011")`, "2026-11-16T10:00:00Z",
			"Ann", "3", "red", `["a"]`,
		}},
		Database:   "app",
		Collection: "items",
	})
	return m
}

func TestInsertResultDocumentTemplate(t *testing.T) {
	m := insertTemplateModel(t)
	m = m.insertResultDocument()

	value := m.editor.Value()
	if !strings.HasPrefix(value, "db.items.insertOne({createdAt: ISODate(") {
		t.Fatalf("template = %q, want insertOne with createdAt first", value)
	}
	if !strings.Contains(value, `name: "", qty: 0, meta: {color: ""}, tags: []`) {
		t.Fatalf("template = %q, want typed placeholders and the nested object", value)
	}
	if !strings.HasSuffix(value, "});") {
		t.Fatalf("template = %q, want a terminated statement", value)
	}
	if !strings.Contains(m.status, "5 fields") || !strings.Contains(m.status, "_id server-assigned") {
		t.Fatalf("status = %q", m.status)
	}
	if m.focus != focusEditor {
		t.Fatalf("focus = %v, want focusEditor", m.focus)
	}

	// createdAt is auto-filled, so the first empty value is name.
	idx := strings.Index(value, `name: "`)
	want := utf8.RuneCountInString(value[:idx]) + utf8.RuneCountInString(`name: "`)
	if m.editor.cur != want {
		t.Fatalf("cursor = %d, want %d (between the name quotes)", m.editor.cur, want)
	}
	runes := []rune(value)
	if runes[m.editor.cur-1] != '"' || runes[m.editor.cur] != '"' {
		t.Fatalf("cursor is not between quotes: %q", string(runes[m.editor.cur-1:m.editor.cur+1]))
	}
}

func TestInsertTemplateNestedFields(t *testing.T) {
	fake := &fakeSession{info: engine.Info{Database: "app"}}
	m := mongoModel(t, fake)
	m.focus = focusGrid
	m.tab = tabResult
	m.grid = newGrid(&engine.Result{
		Columns:     []string{"meta.color", "meta.size"},
		ColumnTypes: []string{"string", "int"},
		Rows:        [][]string{{"red", "2"}},
		Database:    "app",
		Collection:  "items",
	})

	m = m.insertResultDocument()
	value := m.editor.Value()
	if !strings.Contains(value, `meta: {color: "", size: 0}`) {
		t.Fatalf("template = %q, want one nested meta object", value)
	}
	if strings.Count(value, "meta:") != 1 {
		t.Fatalf("template = %q, want meta exactly once", value)
	}
}

func TestInsertTemplateCustomID(t *testing.T) {
	fake := &fakeSession{info: engine.Info{Database: "app"}}
	m := mongoModel(t, fake)
	m.focus = focusGrid
	m.tab = tabResult
	m.grid = newGrid(&engine.Result{
		Columns:     []string{"_id", "name"},
		ColumnTypes: []string{"string", "string"},
		Rows:        [][]string{{"order-1", "Ann"}},
		Database:    "app",
		Collection:  "items",
	})

	m = m.insertResultDocument()
	value := m.editor.Value()
	if !strings.Contains(value, `insertOne({_id: "", name: ""})`) {
		t.Fatalf("template = %q, want a custom string _id", value)
	}
	if strings.Contains(m.status, "server-assigned") {
		t.Fatalf("status = %q, custom _id must not be server-assigned", m.status)
	}
	idx := strings.Index(value, `_id: "`)
	want := utf8.RuneCountInString(value[:idx]) + utf8.RuneCountInString(`_id: "`)
	if m.editor.cur != want {
		t.Fatalf("cursor = %d, want %d at the custom _id", m.editor.cur, want)
	}
}

func TestInsertTemplateNoFields(t *testing.T) {
	fake := &fakeSession{info: engine.Info{Database: "app"}}
	m := mongoModel(t, fake)
	m.focus = focusGrid
	m.tab = tabResult
	m.grid = newGrid(&engine.Result{Database: "app", Collection: "items"})

	m = m.insertResultDocument()
	if !strings.Contains(m.editor.Value(), "db.items.insertOne({});") {
		t.Fatalf("template = %q, want an empty document", m.editor.Value())
	}
	if !strings.Contains(m.status, "no fields") {
		t.Fatalf("status = %q", m.status)
	}
}

func TestInsertTemplateGuards(t *testing.T) {
	m := insertTemplateModel(t)
	m.grid.res.Collection = ""
	m = m.insertResultDocument()
	if !strings.Contains(m.status, "run a find") {
		t.Fatalf("status = %q, want the source guard", m.status)
	}

	m = insertTemplateModel(t)
	m.grid.res.Database = "other"
	m = m.insertResultDocument()
	if !strings.Contains(m.status, "switch to other first") {
		t.Fatalf("status = %q, want the database guard", m.status)
	}
}

func TestPlusInResultInsertsTemplate(t *testing.T) {
	m := insertTemplateModel(t)
	updated, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'+'}})
	m = updated.(Model)
	if !strings.Contains(m.editor.Value(), "insertOne(") || m.focus != focusEditor {
		t.Fatalf("editor = %q focus=%v, + did not insert the template", m.editor.Value(), m.focus)
	}
}
