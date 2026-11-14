package tui

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"mongo-tui/internal/engine"
)

func TestEditorSelectionAndSelectAll(t *testing.T) {
	e := newEditor()
	e.SetValue("db.items.find({});\ndb.items.countDocuments({})")
	e.SetCursorOffset(0)

	e, _ = e.Update(tea.KeyMsg{Type: tea.KeyShiftRight})
	e, _ = e.Update(tea.KeyMsg{Type: tea.KeyShiftRight})
	if got := e.SelectedText(); got != "db" {
		t.Fatalf("selection = %q, want db", got)
	}
	e, _ = e.Update(tea.KeyMsg{Type: tea.KeyCtrlA})
	if got := e.SelectedText(); got != e.Value() {
		t.Fatalf("Ctrl+A selection = %q, want the whole buffer", got)
	}
}

func TestEditorLocksExecutedStatements(t *testing.T) {
	e := newEditor()
	e.SetValue("db.items.find({});\ndb.items.countDocuments({})")
	e.AddMarks(map[string]execMark{"db.items.find({})": markOK})

	if _, ok := e.lockedSpanAt(0); !ok {
		t.Fatal("executed statement is not locked")
	}
	if !e.executedLines()[0] {
		t.Fatal("executed line is not dimmed")
	}
	if e.lineMarks()[0] != markOK {
		t.Fatalf("line mark = %v, want ok", e.lineMarks()[0])
	}
	st, ok := e.StatementAtLine(0)
	if !ok || !e.Unlock(st.Text) {
		t.Fatal("unlock failed")
	}
	if _, ok := e.lockedSpanAt(0); ok {
		t.Fatal("statement is still locked after Unlock")
	}
}

func TestEditorDeleteProtectsExecutedStatements(t *testing.T) {
	e := newEditor()
	e.SetValue("db.items.find({});\ndb.items.countDocuments({})")
	e.SetCursorOffset(0)

	deleted, locked := e.DeleteStatementAtCursor()
	if !deleted || locked {
		t.Fatalf("delete = %v/%v, want deleted and unlocked", deleted, locked)
	}
	if strings.Contains(e.Value(), "find({})") || !strings.Contains(e.Value(), "countDocuments") {
		t.Fatalf("value after delete = %q", e.Value())
	}

	e.SetValue("db.items.find({});\ndb.items.countDocuments({})")
	e.SetCursorOffset(0)
	e.AddMarks(map[string]execMark{"db.items.find({})": markOK})
	deleted, locked = e.DeleteStatementAtCursor()
	if deleted || !locked {
		t.Fatalf("protected delete = %v/%v, want refused and locked", deleted, locked)
	}
}

func TestModelUnlockAndDeleteStatement(t *testing.T) {
	m := mongoModel(t, &fakeSession{info: engine.Info{Database: "app"}})
	m.editor.SetValue("db.items.find({});\ndb.items.countDocuments({})")
	m.editor.SetCursorOffset(0)
	m.editor.AddMarks(map[string]execMark{"db.items.find({})": markOK})

	updated, _ := m.unlockStatement()
	m = updated.(Model)
	if _, ok := m.editor.lockedSpanAt(0); ok {
		t.Fatal("Ctrl+E did not unlock the executed statement")
	}

	updated, _ = m.deleteStatement()
	m = updated.(Model)
	if strings.Contains(m.editor.Value(), "find({})") {
		t.Fatalf("Ctrl+K did not delete the statement: %q", m.editor.Value())
	}
}

func TestMongoDoubleEscRemovesTemplate(t *testing.T) {
	m := mongoModel(t, &fakeSession{info: engine.Info{Database: "app"}})
	m.editor.SetValue("")
	m = m.insertStatement("db.items.find({})")

	m, _ = press(t, m, tea.KeyEsc)
	if !m.editor.AutoTemplatePending() {
		t.Fatal("first Esc removed the template immediately")
	}
	if !strings.Contains(m.status, "Esc again") {
		t.Fatalf("status = %q, want the second-Esc hint", m.status)
	}
	m, _ = press(t, m, tea.KeyEsc)
	if strings.TrimSpace(m.editor.Value()) != "" {
		t.Fatalf("editor = %q, want the inserted template removed", m.editor.Value())
	}
}
