package tui

import (
	"strings"

	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
)

// suggestionKind labels a completion candidate.
type suggestionKind int

const (
	suggestKeyword suggestionKind = iota
	suggestRelation
	suggestColumn
	suggestSchema
)

func (k suggestionKind) String() string {
	switch k {
	case suggestKeyword:
		return "kw"
	case suggestRelation:
		return "rel"
	case suggestColumn:
		return "col"
	case suggestSchema:
		return "sch"
	}
	return "?"
}

// suggestion is one completion candidate.
type suggestion struct {
	Text   string
	Kind   suggestionKind
	Detail string
}

// compState is the open completion popup.
type compState struct {
	open   bool
	items  []suggestion
	cursor int
	// prefix is how many runes to delete before inserting a candidate.
	prefix int
}

// startCompletion opens the MongoDB completion popup.
func (m Model) startCompletion() (tea.Model, tea.Cmd) {
	m.comp = compState{}
	return m.startMongoCompletion()
}

// handleCompletionKey consumes popup navigation. The bool reports whether the
// key belonged to the popup; otherwise the popup closes and the key is handled
// normally.
func (m Model) handleCompletionKey(key string) (Model, tea.Cmd, bool) {
	switch key {
	case "esc":
		m.comp = compState{}
		return m, nil, true
	case "up", "ctrl+p":
		if m.comp.cursor > 0 {
			m.comp.cursor--
		}
		return m, nil, true
	case "down", "ctrl+n":
		if m.comp.cursor < len(m.comp.items)-1 {
			m.comp.cursor++
		}
		return m, nil, true
	case "tab", "enter":
		return m.acceptCompletion(), nil, true
	}

	m.comp = compState{}
	return m, nil, false
}

// acceptCompletion replaces the typed prefix with the selected candidate.
func (m Model) acceptCompletion() Model {
	if !m.comp.open || len(m.comp.items) == 0 {
		return m
	}
	chosen := m.comp.items[m.comp.cursor]

	for i := 0; i < m.comp.prefix; i++ {
		m.editor, _ = m.editor.Update(tea.KeyMsg{Type: tea.KeyBackspace})
	}
	m.editor.InsertString(chosen.Text)
	m.comp = compState{}
	return m
}

// popupLines renders the candidate list over the bottom of the editor.
func (m Model) popupLines(width int) []string {
	const maxItems = 8

	items := m.comp.items
	start := 0
	if m.comp.cursor >= maxItems {
		start = m.comp.cursor - maxItems + 1
	}
	end := start + maxItems
	if end > len(items) {
		end = len(items)
	}

	out := make([]string, 0, end-start)
	for i := start; i < end; i++ {
		mark := "  "
		if i == m.comp.cursor {
			mark = "› "
		}
		line := mark + items[i].Text
		suffix := "  " + items[i].Kind.String()
		if items[i].Detail != "" {
			suffix += " " + items[i].Detail
		}
		if avail := width - displayWidth(line) - 1; avail > 4 {
			line += dimStyle.Render(truncate(suffix, avail))
		}
		out = append(out, line)
	}
	return out
}

// editorLines is the editor body with the popup laid over its last lines.
func (m Model) editorLines(width int) []string {
	lines := strings.Split(m.editor.View(), "\n")
	for i, line := range lines {
		if strings.TrimRight(line, " ") == "" {
			lines[i] = ""
		}
	}
	var popup []string
	switch {
	case m.hist.open:
		popup = m.historyLines(width)
	case m.comp.open:
		popup = m.popupLines(width)
	}
	if len(popup) == 0 {
		return lines
	}
	start := len(lines) - len(popup)
	if start < 0 {
		start = 0
		for len(lines) < start+len(popup) {
			lines = append(lines, "")
		}
	}
	for i, line := range popup {
		if start+i < len(lines) {
			lines[start+i] = line
		}
	}
	return lines
}

// newProfileLabelInput asks for a name to save the current connection under.
func newProfileLabelInput(placeholder string) textinput.Model {
	ti := textinput.New()
	ti.Placeholder = placeholder
	ti.CharLimit = 120
	ti.Width = 60
	return ti
}
