package tui

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"mongo-tui/internal/engine"
)

func TestHclipPlainAndOffset(t *testing.T) {
	if got := hclip("hello world", 6, 5); got != "world" {
		t.Fatalf("hclip offset = %q, want world", got)
	}
	// The plain truncate path keeps the existing ellipsis behaviour at column 0.
	if got := hclip("hello world", 0, 5); got != "hell…" {
		t.Fatalf("hclip truncate = %q, want hell…", got)
	}
	if got := hclip("hello", 10, 5); got != "" {
		t.Fatalf("hclip past end = %q, want empty", got)
	}
}

// hclip keeps styling while cutting a window, so colours survive a sideways
// scroll of the right pane.
func TestHclipPreservesANSI(t *testing.T) {
	styled := "\x1b[2mhello world\x1b[0m"
	got := hclip(styled, 6, 5)
	if !strings.Contains(got, "world") {
		t.Fatalf("hclip styled = %q, want to contain world", got)
	}
	if strings.Contains(got, "hello") {
		t.Fatalf("hclip styled leaked cut text: %q", got)
	}
	if !strings.Contains(got, "\x1b[2m") {
		t.Fatalf("hclip styled dropped the SGR prefix: %q", got)
	}
}

func TestScrollPaneXSidewaysText(t *testing.T) {
	m := Model{tab: tabObject}
	m.scrollPaneX(5)
	if m.colOff != 5 {
		t.Fatalf("colOff after +5 = %d", m.colOff)
	}
	m.scrollPaneX(-3)
	if m.colOff != 2 {
		t.Fatalf("colOff after -3 = %d", m.colOff)
	}
	m.scrollPaneX(-100)
	if m.colOff != 0 {
		t.Fatalf("colOff floored = %d, want 0", m.colOff)
	}
}

func newPaneModel(t *testing.T) Model {
	t.Helper()
	fake := &fakeMongoCatalog{
		fakeSession: &fakeSession{info: engine.Info{ServerVersion: "8.0.38", Database: "app"}},
		databases:   []engine.Item{{Name: "app", Current: true}},
	}
	m := mongoModel(t, fake)
	m.focus = focusGrid
	m.tab = tabResult
	return m
}

func keyPress(t *testing.T, m Model, s string) Model {
	t.Helper()
	var msg tea.Msg
	switch s {
	case "right":
		msg = tea.KeyMsg{Type: tea.KeyRight}
	case "left":
		msg = tea.KeyMsg{Type: tea.KeyLeft}
	case "down":
		msg = tea.KeyMsg{Type: tea.KeyDown}
	case "up":
		msg = tea.KeyMsg{Type: tea.KeyUp}
	default:
		msg = tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(s)}
	}
	updated, _ := m.Update(msg)
	model, ok := updated.(Model)
	if !ok {
		t.Fatalf("Update(%q) returned %T", s, updated)
	}
	return model
}

// In the right pane the arrows scroll horizontally and must not change the tab.
func TestArrowsScrollNotSwitchPanes(t *testing.T) {
	m := newPaneModel(t)
	m.tab = tabObject

	if got := keyPress(t, m, "right"); got.tab != tabObject {
		t.Fatalf("right changed tab to %v, want tabObject", got.tab)
	} else if got.colOff != 1 {
		t.Fatalf("colOff after right = %d, want 1", got.colOff)
	}
	m = keyPress(t, m, "right")
	m = keyPress(t, m, "left")
	if m.colOff != 0 {
		t.Fatalf("colOff after an extra right,left = %d, want 0", m.colOff)
	}
}

// "[" and "]" are the only tab switchers: ] goes RESULT→OBJECT→MESSAGES→RESULT.
func TestBracketsSwitchPanes(t *testing.T) {
	m := newPaneModel(t) // tab = tabResult
	if got := keyPress(t, m, "]"); got.tab != tabObject {
		t.Fatalf("] from RESULT = %v, want OBJECT", got.tab)
	}
	if got := keyPress(t, keyPress(t, m, "]"), "]"); got.tab != tabMessages {
		t.Fatalf("]] from RESULT = %v, want MESSAGES", got.tab)
	}
	if got := keyPress(t, m, "["); got.tab != tabMessages {
		t.Fatalf("[ from RESULT = %v, want MESSAGES (wrap)", got.tab)
	}
}

// In the editor [ and ] must type text, not switch panes.
func TestBracketsTypeInEditor(t *testing.T) {
	m := newPaneModel(t)
	m.focus = focusEditor
	m.editor.SetValue("")
	before := m.tab
	got := keyPress(t, m, "[")
	if got.tab != before {
		t.Fatalf("[ switched pane to %v while editing", got.tab)
	}
	if !strings.Contains(got.editor.Value(), "[") {
		t.Fatalf("editor value = %q, want it to contain [", got.editor.Value())
	}
}
