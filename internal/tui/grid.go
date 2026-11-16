package tui

import (
	"regexp"
	"strings"
	"unicode/utf8"

	"github.com/charmbracelet/lipgloss"

	"mongo-tui/internal/engine"
)

const maxCellWidth = 28

var (
	headerStyle      = lipgloss.NewStyle().Bold(true)
	nullStyle        = lipgloss.NewStyle().Faint(true).Italic(true)
	cursorCell       = lipgloss.NewStyle().Reverse(true)
	cursorRowMark    = lipgloss.NewStyle().Bold(true)
	searchMatchStyle = lipgloss.NewStyle().Bold(true).
				Foreground(lipgloss.Color("0")).
				Background(lipgloss.Color("11"))
)

// gridSearch is an applied search: it is scoped to one column and keeps the
// matching row numbers in order.
type gridSearch struct {
	pattern string
	re      *regexp.Regexp
	col     int
	matches []int
	cur     int
	center  bool
}

// grid renders a ResultSet with a movable cursor and independent scrolling.
type grid struct {
	res     *engine.Result
	headers []string
	widths  []int
	colOff  int
	rowOff  int
	curRow  int
	curCol  int
	search  *gridSearch
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

// appendResult merges one more page into the grid. Columns first seen in the
// new page are appended, rows are remapped to the grid's column order, and a
// live search is re-evaluated over the grown set without moving the cursor.
func (g *grid) appendResult(res *engine.Result) {
	if res == nil || len(res.Columns) == 0 {
		return
	}
	if g.res == nil {
		*g = *newGrid(res)
		return
	}

	index := make(map[string]int, len(g.res.Columns))
	for i, name := range g.res.Columns {
		index[name] = i
	}
	for i, name := range res.Columns {
		if _, ok := index[name]; ok {
			continue
		}
		typ := ""
		if i < len(res.ColumnTypes) {
			typ = res.ColumnTypes[i]
		}
		header := name
		if typ != "" {
			header += " " + typ
		}
		index[name] = len(g.res.Columns)
		g.res.Columns = append(g.res.Columns, name)
		g.res.ColumnTypes = append(g.res.ColumnTypes, typ)
		g.headers = append(g.headers, header)
		g.widths = append(g.widths, clampLen(displayWidth(header), 3, maxCellWidth))
		// Rows loaded before this column existed mark it as missing, not as an
		// empty string.
		for r := range g.res.Rows {
			g.res.Rows[r] = append(g.res.Rows[r], engine.MissingSentinel)
		}
	}

	for r, src := range res.Rows {
		row := make([]string, len(g.res.Columns))
		for i := range row {
			row[i] = engine.MissingSentinel
		}
		for i, name := range res.Columns {
			idx, ok := index[name]
			if !ok {
				continue
			}
			value := engine.MissingSentinel
			if i < len(src) {
				value = src[i]
			}
			row[idx] = value
			if w := clampLen(displayWidth(cellText(value)), 3, maxCellWidth); w > g.widths[idx] {
				g.widths[idx] = w
			}
		}
		g.res.Rows = append(g.res.Rows, row)
		var raw []byte
		if r < len(res.Documents) {
			raw = res.Documents[r]
		}
		g.res.Documents = append(g.res.Documents, raw)
	}

	g.res.RowCount = len(g.res.Rows)
	g.res.Truncated = g.res.Truncated || res.Truncated
	if g.search != nil {
		g.search.matches = g.searchRows(g.search.re, g.search.col)
		g.search.cur = -1
		g.search.center = false
	}
}

// move shifts the cursor. width is the cell budget of the RESULT pane; the
// horizontal window follows the cursor only at its edges, so the highlight
// travels across the visible columns like a spreadsheet cursor.
func (g *grid) move(dRow, dCol, width int) {
	if g.empty() {
		return
	}
	g.curRow = clampInt(g.curRow+dRow, 0, maxInt(0, len(g.res.Rows)-1))
	g.curCol = clampInt(g.curCol+dCol, 0, maxInt(0, len(g.res.Columns)-1))
	if dCol != 0 {
		g.scrollColIntoView(width)
	}
}

// scrollColIntoView keeps the cursor column inside the horizontal window by
// moving the left column offset. width is the cell budget of the RESULT pane,
// including the two-cell row marker rendered in front of every row.
func (g *grid) scrollColIntoView(width int) {
	if len(g.widths) == 0 {
		return
	}
	budget := maxInt(1, width-2)
	if g.colOff < 0 {
		g.colOff = 0
	}
	for g.curCol < g.colOff {
		g.colOff--
	}
	if g.colOff < 0 {
		g.colOff = 0
	}
	for g.colOff < len(g.widths)-1 && g.curCol >= g.columnEnd(g.colOff, budget) {
		g.colOff++
	}
}

// columnEnd returns the first column index that no longer fits after `from`,
// counting each cell width plus a one-space separator.
func (g *grid) columnEnd(from, width int) int {
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
	return to
}

// searchRows returns the rows whose visible text in col matches re. Matching
// runs on the truncated text, so "found" always means visible in the cell.
func (g *grid) searchRows(re *regexp.Regexp, col int) []int {
	if g.empty() || col < 0 || col >= len(g.res.Columns) {
		return nil
	}
	rows := make([]int, 0)
	for r, row := range g.res.Rows {
		value := ""
		if col < len(row) {
			value = row[col]
		}
		if re.MatchString(truncate(cellText(value), g.widths[col])) {
			rows = append(rows, r)
		}
	}
	return rows
}

// applySearch scopes a search to the cursor column and jumps to the first match
// at or below the cursor row, wrapping to the top when needed.
func (g *grid) applySearch(re *regexp.Regexp, pattern string) (found, wrapped bool, idx, total int) {
	if g.empty() {
		return false, false, 0, 0
	}
	s := &gridSearch{
		pattern: pattern,
		re:      re,
		col:     g.curCol,
		matches: g.searchRows(re, g.curCol),
		cur:     -1,
	}
	g.search = s
	if len(s.matches) == 0 {
		return false, false, 0, 0
	}
	target := 0
	for i, row := range s.matches {
		if row >= g.curRow {
			target = i
			break
		}
	}
	wrapped = s.matches[target] < g.curRow
	g.gotoMatch(target)
	return true, wrapped, target, len(s.matches)
}

// jumpMatch moves to the next (forward) or previous match, wrapping around.
func (g *grid) jumpMatch(forward bool) (found, wrapped bool, idx, total int) {
	s := g.search
	if s == nil || len(s.matches) == 0 {
		return false, false, 0, 0
	}
	target := -1
	if forward {
		for i, row := range s.matches {
			if row > g.curRow {
				target = i
				break
			}
		}
		if target < 0 {
			target = 0
			wrapped = true
		}
	} else {
		for i := len(s.matches) - 1; i >= 0; i-- {
			if s.matches[i] < g.curRow {
				target = i
				break
			}
		}
		if target < 0 {
			target = len(s.matches) - 1
			wrapped = true
		}
	}
	g.gotoMatch(target)
	return true, wrapped, target, len(s.matches)
}

// gotoMatch puts the cursor on a match and asks the renderer to centre the row.
func (g *grid) gotoMatch(i int) {
	s := g.search
	if s == nil || i < 0 || i >= len(s.matches) {
		return
	}
	s.cur = i
	g.curRow = s.matches[i]
	g.curCol = s.col
	s.center = true
}

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

	if g.search != nil && g.search.center {
		g.scrollColIntoView(width)
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
	if g.search != nil && g.search.center {
		maxOff := maxInt(0, len(g.res.Rows)-visibleRows)
		g.rowOff = clampInt(g.curRow-visibleRows/2, 0, maxOff)
		g.search.center = false
	}

	for r := g.rowOff; r < len(g.res.Rows) && len(out) < height; r++ {
		row := g.res.Rows[r]
		cells := make([]string, 0, to-from)
		for c := from; c < to; c++ {
			value := ""
			if c < len(row) {
				value = row[c]
			}
			cells = append(cells, g.cellViewAt(c, value, r == g.curRow && c == g.curCol))
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
	return from, g.columnEnd(from, width)
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

// cellViewAt renders one cell, highlighting regexp matches in the searched
// column. A matching cell under the cursor keeps the reverse cursor style.
func (g *grid) cellViewAt(col int, value string, selected bool) string {
	width := g.widths[col]
	if g.search != nil && col == g.search.col && g.search.re != nil {
		text := truncate(cellText(value), width)
		if g.search.re.MatchString(text) {
			return highlightCell(text, width, selected, g.search.re)
		}
	}
	return cellView(value, width, selected)
}

// cellSegment is a piece of a cell: match says whether the search regexp
// matched it.
type cellSegment struct {
	text  string
	match bool
}

// matchSegments splits text into matched and unmatched pieces.
func matchSegments(text string, re *regexp.Regexp) []cellSegment {
	spans := re.FindAllStringIndex(text, -1)
	if len(spans) == 0 {
		return []cellSegment{{text: text}}
	}
	segments := make([]cellSegment, 0, len(spans)*2+1)
	last := 0
	for _, span := range spans {
		if span[0] > last {
			segments = append(segments, cellSegment{text: text[last:span[0]]})
		}
		if span[1] > span[0] {
			segments = append(segments, cellSegment{text: text[span[0]:span[1]], match: true})
		}
		last = span[1]
	}
	if last < len(text) {
		segments = append(segments, cellSegment{text: text[last:]})
	}
	return segments
}

// highlightCell marks every regexp match inside already truncated text and pads
// the result to the cell width.
func highlightCell(text string, width int, selected bool, re *regexp.Regexp) string {
	if selected {
		return cursorCell.Render(pad(text, width))
	}
	var b strings.Builder
	for _, segment := range matchSegments(text, re) {
		if segment.match {
			b.WriteString(searchMatchStyle.Render(segment.text))
		} else {
			b.WriteString(segment.text)
		}
	}
	if used := displayWidth(text); used < width {
		b.WriteString(strings.Repeat(" ", width-used))
	}
	return b.String()
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

func minInt(a, b int) int {
	if a < b {
		return a
	}
	return b
}
