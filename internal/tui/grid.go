package tui

import (
	"strings"
	"unicode/utf8"

	"github.com/charmbracelet/lipgloss"

	"mongo-tui/internal/engine"
)

const maxCellWidth = 28

var (
	headerStyle   = lipgloss.NewStyle().Bold(true)
	nullStyle     = lipgloss.NewStyle().Faint(true).Italic(true)
	cursorCell    = lipgloss.NewStyle().Reverse(true)
	cursorRowMark = lipgloss.NewStyle().Bold(true)
)

// grid renders a ResultSet with a movable cursor and independent scrolling.
type grid struct {
	res     *engine.Result
	headers []string
	widths  []int
	colOff  int
	rowOff  int
	curRow  int
	curCol  int
}

func newGrid(res *engine.Result) *grid {
	g := &grid{res: res}
	if res == nil {
		return g
	}
	g.headers = make([]string, len(res.Columns))
	g.widths = make([]int, len(res.Columns))
	for i, col := range res.Columns {
		header := col
		if i < len(res.ColumnTypes) && res.ColumnTypes[i] != "" {
			header += " " + res.ColumnTypes[i]
		}
		g.headers[i] = header
		g.widths[i] = clampLen(displayWidth(header), 3, maxCellWidth)
	}
	for _, row := range res.Rows {
		for i := range res.Columns {
			if i >= len(row) {
				continue
			}
			if w := clampLen(displayWidth(cellText(row[i])), 3, maxCellWidth); w > g.widths[i] {
				g.widths[i] = w
			}
		}
	}
	return g
}

func (g *grid) empty() bool {
	return g == nil || g.res == nil || len(g.res.Columns) == 0
}

// move shifts the cursor. Visible size is passed in so the view can keep the
// cursor on screen.
func (g *grid) move(dRow, dCol, visibleRows, visibleCols int) {
	if g.empty() {
		return
	}
	g.curRow = clampInt(g.curRow+dRow, 0, maxInt(0, len(g.res.Rows)-1))
	g.curCol = clampInt(g.curCol+dCol, 0, maxInt(0, len(g.res.Columns)-1))
	_ = visibleRows
	_ = visibleCols
}

// currentRowTSV renders the cursor row for the clipboard: NULL becomes an empty
// field, as in a spreadsheet paste.
func (g *grid) currentRowTSV() string {
	if g.empty() || g.curRow >= len(g.res.Rows) {
		return ""
	}
	row := g.res.Rows[g.curRow]
	cells := make([]string, 0, len(g.res.Columns))
	for i := range g.res.Columns {
		value := ""
		if i < len(row) {
			value = row[i]
		}
		if value == engine.NullSentinel || value == engine.MissingSentinel {
			value = ""
		}
		cells = append(cells, value)
	}
	return strings.Join(cells, "\t")
}

func (g *grid) currentCell() string {
	if g.empty() || g.curRow >= len(g.res.Rows) {
		return ""
	}
	row := g.res.Rows[g.curRow]
	if g.curCol >= len(row) {
		return ""
	}
	if row[g.curCol] == engine.NullSentinel || row[g.curCol] == engine.MissingSentinel {
		return ""
	}
	return row[g.curCol]
}

// lines renders the grid into at most height lines and width columns.
func (g *grid) lines(width, height int) []string {
	if g.empty() {
		return []string{dimStyle.Render("no result yet — press Ctrl+J to run the statement under the cursor")}
	}
	if height < 2 || width < 4 {
		return nil
	}

	from, to := g.visibleColumns(width - 2)
	total := 0
	for c := from; c < to; c++ {
		total += g.widths[c] + 1
	}

	header := make([]string, 0, to-from)
	for c := from; c < to; c++ {
		header = append(header, pad(g.headers[c], g.widths[c]))
	}
	out := []string{
		headerStyle.Render(" " + strings.Join(header, " ")),
		dimStyle.Render(strings.Repeat("─", clampLen(total, 1, width))),
	}

	visibleRows := height - 2
	g.scrollRows(visibleRows)

	for r := g.rowOff; r < len(g.res.Rows) && len(out) < height; r++ {
		row := g.res.Rows[r]
		cells := make([]string, 0, to-from)
		for c := from; c < to; c++ {
			value := ""
			if c < len(row) {
				value = row[c]
			}
			cells = append(cells, cellView(value, g.widths[c], r == g.curRow && c == g.curCol))
		}
		mark := "  "
		if r == g.curRow {
			mark = cursorRowMark.Render("› ")
		}
		out = append(out, mark+strings.Join(cells, " "))
	}

	if g.res.Truncated && len(out) < height {
		out = append(out, dimStyle.Render("… truncated at the row limit"))
	}
	return out
}

func (g *grid) visibleColumns(width int) (int, int) {
	from := clampInt(g.colOff, 0, maxInt(0, len(g.widths)-1))
	used := 0
	to := from
	for to < len(g.widths) {
		next := g.widths[to] + 1
		if used+next > width && to > from {
			break
		}
		used += next
		to++
	}
	if to == from {
		to = from + 1
	}
	return from, to
}

func (g *grid) scrollRows(visible int) {
	if visible <= 0 {
		return
	}
	if g.curRow < g.rowOff {
		g.rowOff = g.curRow
	}
	if g.curRow >= g.rowOff+visible {
		g.rowOff = g.curRow - visible + 1
	}
	maxOff := maxInt(0, len(g.res.Rows)-visible)
	g.rowOff = clampInt(g.rowOff, 0, maxOff)
}

func cellText(value string) string {
	switch value {
	case engine.NullSentinel:
		return "NULL"
	case engine.MissingSentinel:
		return "—"
	}
	return value
}

func cellView(value string, width int, selected bool) string {
	switch value {
	case engine.NullSentinel:
		text := pad("NULL", width)
		if selected {
			return cursorCell.Render(text)
		}
		return nullStyle.Render(text)
	case engine.MissingSentinel:
		text := pad("—", width)
		if selected {
			return cursorCell.Render(text)
		}
		return nullStyle.Render(text)
	}
	text := pad(truncate(value, width), width)
	if selected {
		return cursorCell.Render(text)
	}
	return text
}

func pad(s string, width int) string {
	w := displayWidth(s)
	if w >= width {
		return s
	}
	return s + strings.Repeat(" ", width-w)
}

func truncate(s string, width int) string {
	if displayWidth(s) <= width {
		return s
	}
	if width <= 1 {
		return "…"
	}
	runes := []rune(s)
	if len(runes) > width-1 {
		runes = runes[:width-1]
	}
	return string(runes) + "…"
}

func displayWidth(s string) int {
	return utf8.RuneCountInString(s)
}

func clampLen(v, lo, hi int) int {
	if v < lo {
		return lo
	}
	if v > hi {
		return hi
	}
	return v
}

func clampInt(v, lo, hi int) int {
	if v < lo {
		return lo
	}
	if v > hi {
		return hi
	}
	return v
}

func maxInt(a, b int) int {
	if a > b {
		return a
	}
	return b
}
