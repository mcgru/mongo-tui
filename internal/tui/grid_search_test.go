package tui

import (
	"regexp"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"mongo-tui/internal/engine"
)

func searchGridModel(t *testing.T, columns []string, rows [][]string) Model {
	t.Helper()
	m := mongoModel(t, &fakeMongoCatalog{
		fakeSession: &fakeSession{info: engine.Info{ServerVersion: "8.0.38", Database: "app"}},
	})
	m.grid = newGrid(&engine.Result{Columns: columns, Rows: rows})
	m.focus = focusGrid
	m.tab = tabResult
	return m
}

func searchFixtures() ([]string, [][]string) {
	return []string{"name", "city"}, [][]string{
		{"ann", "Paris"},
		{"bob", "Rome"},
		{"anna", "Berlin"},
		{"dave", ""},
	}
}

func typeInto(t *testing.T, m Model, text string) Model {
	t.Helper()
	updated, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(text)})
	model, ok := updated.(Model)
	if !ok {
		t.Fatalf("Update returned %T, want Model", updated)
	}
	return model
}

func TestGridSearchScopesToOneColumn(t *testing.T) {
	cols, rows := searchFixtures()
	g := newGrid(&engine.Result{Columns: cols, Rows: rows})
	g.curCol = 0

	found, wrapped, idx, total := g.applySearch(regexp.MustCompile("an"), "an")
	if !found || wrapped || idx != 0 || total != 2 {
		t.Fatalf("applySearch = %v/%v/%d/%d, want found, no wrap, 0, 2", found, wrapped, idx, total)
	}
	if g.search.col != 0 || len(g.search.matches) != 2 || g.search.matches[0] != 0 || g.search.matches[1] != 2 {
		t.Fatalf("search = %+v, want column 0 rows [0 2]", g.search)
	}
	if g.curRow != 0 || g.curCol != 0 || !g.search.center {
		t.Fatalf("cursor = %d/%d center=%v", g.curRow, g.curCol, g.search.center)
	}

	// The same pattern in the city column finds Paris instead of the names.
	g.curCol = 1
	found, _, _, total = g.applySearch(regexp.MustCompile("ar"), "ar")
	if !found || total != 1 || g.search.col != 1 || g.search.matches[0] != 0 {
		t.Fatalf("city search = %+v (found=%v total=%d)", g.search, found, total)
	}
}

func TestGridSearchJumpsAndWraps(t *testing.T) {
	cols, rows := searchFixtures()
	g := newGrid(&engine.Result{Columns: cols, Rows: rows})
	g.applySearch(regexp.MustCompile("an"), "an")

	if g.curRow != 0 {
		t.Fatalf("first match row = %d, want 0", g.curRow)
	}
	if _, wrapped, _, _ := g.jumpMatch(true); wrapped || g.curRow != 2 {
		t.Fatalf("n = row %d wrapped=%v, want row 2", g.curRow, wrapped)
	}
	if _, wrapped, _, _ := g.jumpMatch(true); !wrapped || g.curRow != 0 {
		t.Fatalf("n wrap = row %d wrapped=%v, want row 0", g.curRow, wrapped)
	}
	if _, wrapped, _, _ := g.jumpMatch(false); !wrapped || g.curRow != 2 {
		t.Fatalf("N wrap = row %d wrapped=%v, want row 2", g.curRow, wrapped)
	}
	if _, wrapped, _, _ := g.jumpMatch(false); wrapped || g.curRow != 0 {
		t.Fatalf("N = row %d wrapped=%v, want row 0", g.curRow, wrapped)
	}
}

func TestGridSearchStartsAtOrBelowCursor(t *testing.T) {
	cols, rows := searchFixtures()
	g := newGrid(&engine.Result{Columns: cols, Rows: rows})
	g.curRow = 1

	found, wrapped, idx, _ := g.applySearch(regexp.MustCompile("an"), "an")
	if !found || wrapped || idx != 1 || g.curRow != 2 {
		t.Fatalf("applySearch = found=%v wrapped=%v idx=%d row=%d, want row 2", found, wrapped, idx, g.curRow)
	}

	g.curRow = 3
	found, wrapped, idx, _ = g.applySearch(regexp.MustCompile("an"), "an")
	if !found || !wrapped || idx != 0 || g.curRow != 0 {
		t.Fatalf("wrapped search = found=%v wrapped=%v idx=%d row=%d, want row 0", found, wrapped, idx, g.curRow)
	}
}

func TestGridSearchMatchesVisibleTextOnly(t *testing.T) {
	long := strings.Repeat("a", 40) + "Z"
	g := newGrid(&engine.Result{Columns: []string{"name"}, Rows: [][]string{{long}}})

	g.curCol = 0
	if found, _, _, _ := g.applySearch(regexp.MustCompile("Z"), "Z"); found {
		t.Fatal("a match beyond the truncated cell must not be reported")
	}
	if found, _, _, _ := g.applySearch(regexp.MustCompile("aaa"), "aaa"); !found {
		t.Fatal("a visible match must be reported")
	}
}

func TestGridSearchHighlightsMatches(t *testing.T) {
	re := regexp.MustCompile("an")
	segments := matchSegments("ann", re)
	if len(segments) != 2 || segments[0].text != "an" || !segments[0].match ||
		segments[1].text != "n" || segments[1].match {
		t.Fatalf("segments = %+v, want [an match][n plain]", segments)
	}
	if segments := matchSegments("bob", re); len(segments) != 1 || segments[0].match {
		t.Fatalf("non-matching segments = %+v", segments)
	}

	cols, rows := searchFixtures()
	g := newGrid(&engine.Result{Columns: cols, Rows: rows})
	g.search = &gridSearch{re: re, col: 0, matches: []int{0}}
	if got := lipgloss.Width(g.cellViewAt(0, "ann", false)); got != g.widths[0] {
		t.Fatalf("highlighted cell width = %d, want %d", got, g.widths[0])
	}
}

func TestGridSearchCentersTheMatch(t *testing.T) {
	rows := make([][]string, 40)
	for i := range rows {
		rows[i] = []string{"row"}
	}
	rows[25][0] = "needle"
	g := newGrid(&engine.Result{Columns: []string{"name"}, Rows: rows})
	g.curRow = 30

	g.applySearch(regexp.MustCompile("needle"), "needle")
	g.lines(80, 10) // 8 visible rows

	if g.curRow != 25 {
		t.Fatalf("cursor row = %d, want 25", g.curRow)
	}
	if g.rowOff != 21 {
		t.Fatalf("rowOff = %d, want 21 (25 centred in 8 visible rows)", g.rowOff)
	}
}

func TestSlashOpensResultSearch(t *testing.T) {
	cols, rows := searchFixtures()
	m := searchGridModel(t, cols, rows)

	updated, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'/'}})
	m = updated.(Model)
	if !m.searchOpen {
		t.Fatal("/ on the result grid should open the search prompt")
	}
	if !strings.Contains(m.grid.res.Columns[0], "name") || !strings.Contains(m.status, "search in name") {
		t.Fatalf("status = %q", m.status)
	}

	m = typeInto(t, m, "ann")
	updated, _ = m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	m = updated.(Model)
	if m.searchOpen {
		t.Fatal("Enter should close the prompt")
	}
	if !strings.Contains(m.status, "search /ann/: 1/2") {
		t.Fatalf("status = %q, want search 1/2", m.status)
	}
	if m.grid.curRow != 0 || m.grid.curCol != 0 {
		t.Fatalf("cursor = %d/%d, want 0/0", m.grid.curRow, m.grid.curCol)
	}

	m = typeInto(t, m, "n")
	if m.grid.curRow != 2 {
		t.Fatalf("n row = %d, want 2", m.grid.curRow)
	}
	m = typeInto(t, m, "n")
	if m.grid.curRow != 0 || !strings.Contains(m.status, "wrapped") {
		t.Fatalf("wrapped n row = %d status = %q", m.grid.curRow, m.status)
	}
	m = typeInto(t, m, "N")
	if m.grid.curRow != 2 || !strings.Contains(m.status, "wrapped") {
		t.Fatalf("wrapped N row = %d status = %q", m.grid.curRow, m.status)
	}

	updated, _ = m.Update(tea.KeyMsg{Type: tea.KeyEsc})
	m = updated.(Model)
	if m.grid.search != nil || m.status != "search cleared" {
		t.Fatalf("Esc should clear the search (search=%v status=%q)", m.grid.search, m.status)
	}
}

func TestSearchPromptTreatsQAsText(t *testing.T) {
	cols, rows := searchFixtures()
	m := searchGridModel(t, cols, rows)
	updated, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'/'}})
	m = updated.(Model)

	m = typeInto(t, m, "q")
	if !m.searchOpen || m.searchInput.Value() != "q" {
		t.Fatalf("q should be typed into the pattern (open=%v value=%q)", m.searchOpen, m.searchInput.Value())
	}
}

func TestInvalidRegexpKeepsThePrompt(t *testing.T) {
	cols, rows := searchFixtures()
	m := searchGridModel(t, cols, rows)
	updated, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'/'}})
	m = updated.(Model)

	m = typeInto(t, m, "(")
	updated, _ = m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	m = updated.(Model)
	if !m.searchOpen || m.searchErr == "" {
		t.Fatalf("invalid regexp should keep the prompt with an error (open=%v err=%q)", m.searchOpen, m.searchErr)
	}
}

func TestSearchNotFoundAndEmptyRepeat(t *testing.T) {
	cols, rows := searchFixtures()
	m := searchGridModel(t, cols, rows)
	updated, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'/'}})
	m = updated.(Model)
	m = typeInto(t, m, "zzz")
	updated, _ = m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	m = updated.(Model)
	if !strings.Contains(m.status, "not found: /zzz/") {
		t.Fatalf("status = %q", m.status)
	}

	// An empty prompt repeats the last pattern.
	updated, _ = m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'/'}})
	m = updated.(Model)
	updated, _ = m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	m = updated.(Model)
	if !strings.Contains(m.status, "not found: /zzz/") {
		t.Fatalf("empty Enter should repeat /zzz/, status = %q", m.status)
	}
}

func TestSearchIsResultOnly(t *testing.T) {
	cols, rows := searchFixtures()
	m := searchGridModel(t, cols, rows)
	m.tab = tabMessages

	updated, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'/'}})
	m = updated.(Model)
	if m.searchOpen {
		t.Fatal("the search prompt must not open outside RESULT")
	}
	if m.status != "search works in the RESULT pane" {
		t.Fatalf("status = %q", m.status)
	}
}
