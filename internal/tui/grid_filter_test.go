package tui

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"mongo-tui/internal/engine"
)

const filterDoc = `{
	"_id": {"$oid": "507f1f77bcf86cd799439011"},
	"name": "Ann",
	"qty": 3,
	"price": {"$numberDecimal": "19.99"},
	"when": {"$date": "2026-11-16T10:00:00Z"},
	"big": {"$numberLong": "9007199254740993"},
	"ok": true,
	"note": null,
	"tags": ["a", "b"],
	"meta": {"color": "red"}
}`

func TestFilterLiteralFromDocument(t *testing.T) {
	cases := []struct {
		column string
		raw    string
		typ    string
		want   string
	}{
		{"name", "Ann", "string", `"Ann"`},
		{"qty", "3", "int", `3`},
		{"price", "19.99", "decimal", `NumberDecimal("19.99")`},
		{"when", "2026-11-16T10:00:00Z", "date", `ISODate("2026-11-16T10:00:00Z")`},
		{"big", "9007199254740993", "long", `NumberLong("9007199254740993")`},
		{"ok", "true", "bool", `true`},
		{"note", engine.NullSentinel, "null", `null`},
		{"tags", `["a","b"]`, "array", `["a", "b"]`},
		{"meta.color", "red", "string", `"red"`},
		{"_id", `ObjectID("507f1f77bcf86cd799439011")`, "objectId", `ObjectId("507f1f77bcf86cd799439011")`},
	}
	for _, tc := range cases {
		got, _, _ := filterLiteral(tc.raw, tc.typ, []byte(filterDoc), tc.column, false, false)
		if got != tc.want {
			t.Errorf("filterLiteral(%s) = %s, want %s", tc.column, got, tc.want)
		}
	}
}

func TestFilterLiteralWithoutDocument(t *testing.T) {
	cases := []struct {
		raw  string
		typ  string
		want string
	}{
		{"Ann", "string", `"Ann"`},
		{"3", "int", `3`},
		{"3", "string", `"3"`},
		{"1.5", "double", `1.5`},
		{"true", "bool", `true`},
		{`ObjectID("507f1f77bcf86cd799439011")`, "objectId", `ObjectId("507f1f77bcf86cd799439011")`},
		{"2026-11-16T10:00:00Z", "date", `ISODate("2026-11-16T10:00:00Z")`},
		{"19.99", "decimal", `NumberDecimal("19.99")`},
		{"9007199254740993", "long", `NumberLong("9007199254740993")`},
	}
	for _, tc := range cases {
		got, _, _ := filterLiteral(tc.raw, tc.typ, nil, "field", false, false)
		if got != tc.want {
			t.Errorf("filterLiteral(%q, %s) = %s, want %s", tc.raw, tc.typ, got, tc.want)
		}
	}
}

func TestFilterLiteralMissingAndRegex(t *testing.T) {
	got, cursor, warn := filterLiteral(engine.MissingSentinel, "string", nil, "name", false, false)
	if got != `""` || cursor != 1 || !strings.Contains(warn, "no value") {
		t.Fatalf("missing exact = %q cursor=%d warn=%q", got, cursor, warn)
	}

	got, _, _ = filterLiteral("a.b", "string", nil, "name", true, false)
	if got != `{$regex: "a\\.b"}` {
		t.Fatalf("regexp escape = %q", got)
	}

	got, _, _ = filterLiteral("Ann", "string", nil, "name", true, true)
	if got != `{$regex: "Ann", $options: "i"}` {
		t.Fatalf("regexp ignore-case = %q", got)
	}

	_, _, warn = filterLiteral("42", "int", nil, "qty", true, false)
	if !strings.Contains(warn, "non-string") {
		t.Fatalf("regexp warning = %q, want a non-string hint", warn)
	}
}

func serverFilterModel(t *testing.T) Model {
	t.Helper()
	fake := &fakeSession{info: engine.Info{ServerVersion: "8.0.38", Database: "app"}}
	m := mongoModel(t, fake)
	m.focus = focusGrid
	m.tab = tabResult
	m.grid = newGrid(&engine.Result{
		Columns:     []string{"_id", "name", "meta.color"},
		ColumnTypes: []string{"objectId", "string", "string"},
		Rows:        [][]string{{`ObjectID("507f1f77bcf86cd799439011")`, "Ann", "red"}},
		Documents:   [][]byte{[]byte(filterDoc)},
		Database:    "app",
		Collection:  "items",
	})
	return m
}

func TestBuildServerFilterStatementAndCursor(t *testing.T) {
	m := serverFilterModel(t)
	m.grid.curCol = 1

	spec, err := m.buildServerFilter(false)
	if err != nil {
		t.Fatalf("buildServerFilter: %v", err)
	}
	if spec.statement != `db.items.find({ name: "Ann" })` {
		t.Fatalf("statement = %q", spec.statement)
	}
	before := string([]rune(spec.statement)[:spec.cursor])
	if !strings.HasSuffix(before, `"Ann"`) {
		t.Fatalf("cursor lands at %q, want right after the value", before)
	}
	if spec.scope != "app.items" {
		t.Fatalf("scope = %q, want app.items", spec.scope)
	}

	m.grid.curCol = 2
	spec, err = m.buildServerFilter(false)
	if err != nil {
		t.Fatalf("nested buildServerFilter: %v", err)
	}
	if spec.statement != `db.items.find({ "meta.color": "red" })` {
		t.Fatalf("nested statement = %q", spec.statement)
	}
}

func TestBuildServerFilterRegex(t *testing.T) {
	m := serverFilterModel(t)
	m.grid.curCol = 1
	m.searchIgnoreCase = false

	spec, err := m.buildServerFilter(true)
	if err != nil {
		t.Fatalf("buildServerFilter: %v", err)
	}
	if spec.statement != `db.items.find({ name: {$regex: "Ann"} })` {
		t.Fatalf("regexp statement = %q", spec.statement)
	}

	m.searchIgnoreCase = true
	spec, err = m.buildServerFilter(true)
	if err != nil {
		t.Fatalf("ignore-case buildServerFilter: %v", err)
	}
	if spec.statement != `db.items.find({ name: {$regex: "Ann", $options: "i"} })` {
		t.Fatalf("ignore-case statement = %q", spec.statement)
	}

	m.searchIgnoreCase = false
	m.grid.curCol = 2
	m.grid.res.Rows[0][2] = "a.b"
	spec, err = m.buildServerFilter(true)
	if err != nil {
		t.Fatalf("nested regexp buildServerFilter: %v", err)
	}
	if spec.statement != `db.items.find({ "meta.color": {$regex: "a\\.b"} })` {
		t.Fatalf("escaped nested statement = %q", spec.statement)
	}
}

func TestServerFilterGuards(t *testing.T) {
	m := serverFilterModel(t)
	m.tab = tabObject
	if _, err := m.buildServerFilter(false); err != errFilterGrid {
		t.Fatalf("tabObject error = %v, want errFilterGrid", err)
	}

	m = serverFilterModel(t)
	m.grid.res.Collection = ""
	if _, err := m.buildServerFilter(false); err != errFilterSource {
		t.Fatalf("no collection error = %v, want errFilterSource", err)
	}

	m = serverFilterModel(t)
	m.grid.res.Database = "other"
	if _, err := m.buildServerFilter(false); err == nil || !strings.Contains(err.Error(), "switch to other first") {
		t.Fatalf("database guard error = %v", err)
	}
}

func TestServerFilterClearsEditorAndFocuses(t *testing.T) {
	m := serverFilterModel(t)
	m.grid.curCol = 1
	m.editor.SetValue("db.old.find({})")
	m.editor.AddMarks(map[string]execMark{"db.old.find({})": markOK})

	updated, _ := m.Update(tea.KeyMsg{Type: tea.KeyCtrlF})
	m = updated.(Model)
	if m.focus != focusEditor {
		t.Fatalf("focus = %v, want focusEditor", m.focus)
	}
	if m.editor.Value() != `db.items.find({ name: "Ann" })` {
		t.Fatalf("editor = %q", m.editor.Value())
	}
	if m.editor.marks != nil {
		t.Fatalf("marks survived the rewrite: %v", m.editor.marks)
	}
	if !strings.Contains(m.status, "filter name") || !strings.Contains(m.status, "to run") {
		t.Fatalf("status = %q", m.status)
	}
	if m.scopeCollection != "app.items" {
		t.Fatalf("scopeCollection = %q", m.scopeCollection)
	}
}

func TestServerFilterRegexKey(t *testing.T) {
	m := serverFilterModel(t)
	m.grid.curCol = 1
	updated, _ := m.handleMainKey(tea.KeyMsg{}, "ctrl+alt+f")
	m = updated.(Model)
	if !strings.Contains(m.editor.Value(), `{$regex: "Ann"`) {
		t.Fatalf("editor = %q, want a regexp filter", m.editor.Value())
	}
}
