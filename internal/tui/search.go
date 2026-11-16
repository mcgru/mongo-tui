package tui

import (
	"fmt"
	"regexp"

	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
)

// newSearchPrompt is the one-line "/" prompt shown in the footer.
func newSearchPrompt(width int) textinput.Model {
	ti := textinput.New()
	ti.Prompt = ""
	ti.Placeholder = ""
	ti.CharLimit = 256
	ti.Width = maxInt(20, width-30)
	return ti
}

// openGridSearch starts a regexp search in the column under the cursor. It is
// less-like: the prompt sits in the footer, Enter applies and Esc cancels.
func (m Model) openGridSearch() (tea.Model, tea.Cmd) {
	if m.tab != tabResult {
		m.status = "search works in the RESULT pane"
		return m, nil
	}
	if m.grid == nil || m.grid.empty() {
		m.status = "nothing to search"
		return m, nil
	}
	m.searchOpen = true
	m.searchErr = ""
	m.searchInput = newSearchPrompt(m.width)
	m.status = "search in " + m.grid.res.Columns[m.grid.curCol]
	return m, m.searchInput.Focus()
}

// handleSearchInput owns the keyboard while the "/" prompt is open. q is an
// ordinary character here, so only Esc leaves the prompt.
func (m Model) handleSearchInput(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "esc":
		m.searchOpen = false
		m.searchInput.Blur()
		m.searchErr = ""
		m.status = "search cancelled"
		return m, nil
	case "enter":
		return m.applyGridSearch()
	}
	var cmd tea.Cmd
	m.searchInput, cmd = m.searchInput.Update(msg)
	m.searchErr = ""
	return m, cmd
}

// applyGridSearch compiles the pattern and jumps to the first match on or below
// the cursor. An empty prompt repeats the previous pattern.
func (m Model) applyGridSearch() (tea.Model, tea.Cmd) {
	if m.grid == nil || m.grid.empty() {
		m.searchOpen = false
		m.searchInput.Blur()
		m.status = "nothing to search"
		return m, nil
	}
	pattern := m.searchInput.Value()
	if pattern == "" {
		if m.grid.search == nil {
			m.searchOpen = false
			m.searchInput.Blur()
			m.status = "empty search"
			return m, nil
		}
		pattern = m.grid.search.pattern
	}
	expr := pattern
	if m.searchIgnoreCase {
		expr = "(?i)" + pattern
	}
	re, err := regexp.Compile(expr)
	if err != nil {
		m.searchErr = err.Error()
		return m, nil
	}

	m.searchOpen = false
	m.searchInput.Blur()
	m.searchErr = ""
	found, wrapped, idx, total := m.grid.applySearch(re, pattern)
	if !found {
		m.status = "not found: /" + pattern + "/"
		return m, nil
	}
	m.status = fmt.Sprintf("search /%s/: %d/%d", pattern, idx+1, total)
	if wrapped {
		m.status += " (wrapped)"
	}
	return m, nil
}

// jumpGridSearch moves to the next (n) or previous (N) match.
func (m Model) jumpGridSearch(forward bool) (tea.Model, tea.Cmd) {
	if m.grid == nil || m.grid.search == nil {
		m.status = "no active search"
		return m, nil
	}
	found, wrapped, idx, total := m.grid.jumpMatch(forward)
	if !found {
		m.status = "no matches for /" + m.grid.search.pattern + "/"
		return m, nil
	}
	m.status = fmt.Sprintf("search /%s/: %d/%d", m.grid.search.pattern, idx+1, total)
	if wrapped {
		m.status += " (wrapped)"
	}
	return m, nil
}

// clearGridSearch drops the pattern, the matches and the highlight.
func (m Model) clearGridSearch() (tea.Model, tea.Cmd) {
	if m.grid != nil {
		m.grid.search = nil
	}
	m.searchOpen = false
	m.searchInput.Blur()
	m.searchErr = ""
	m.status = "search cleared"
	return m, nil
}

// toggleSearchCase switches future searches between case-sensitive and
// ignore-case. Ctrl+I cannot be used: terminals send it as Tab, so the grid
// uses i, like less's -i option.
func (m Model) toggleSearchCase() (tea.Model, tea.Cmd) {
	if m.tab != tabResult {
		m.status = "search mode works in the RESULT pane"
		return m, nil
	}
	m.searchIgnoreCase = !m.searchIgnoreCase
	if m.searchIgnoreCase {
		m.status = "search: ignore case"
	} else {
		m.status = "search: case-sensitive"
	}
	return m, nil
}

// searchCaseLabel names the current mode for the prompt and the footer.
func (m Model) searchCaseLabel() string {
	if m.searchIgnoreCase {
		return "ignore case"
	}
	return "case-sensitive"
}

// resetSearch forgets the prompt and any applied search, for disconnects.
func (m *Model) resetSearch() {
	m.searchOpen = false
	m.searchInput.Blur()
	m.searchErr = ""
	if m.grid != nil {
		m.grid.search = nil
	}
}
