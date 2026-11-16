package tui

import (
	"strings"
	"unicode"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

// editor is a small SQL editor: a rune buffer, a cursor with column memory,
// selection, scrolling and syntax colouring. It exists because bubbles/textarea
// has no per-token hooks, and highlighting is a requirement.
type editor struct {
	runes []rune
	// cur is the cursor as a rune index in runes.
	cur int
	// sel is the selection anchor, or -1 when nothing is selected.
	sel int

	width, height int
	offLine       int
	offCol        int
	prefCol       int
	focused       bool

	// marks remembers which statements already ran, by their text.
	marks map[string]execMark
	// lockedNotice is set when an edit was refused because the statement already
	// ran; the model turns it into a hint.
	lockedNotice bool
	// autoStart and autoEnd span the statement appended by a template, as long
	// as it stays untouched; Esc Esc uses it to undo the insertion.
	autoStart int
	autoEnd   int
	autoValid bool
}

// execMark is the outcome of the last run of a statement.
type execMark int

const (
	markNone execMark = iota
	markOK
	markFailed
)

// AddMarks records outcomes for statements by their normalised text.
func (e *editor) AddMarks(marks map[string]execMark) {
	if e.marks == nil {
		e.marks = map[string]execMark{}
	}
	for statement, mark := range marks {
		e.marks[normalizeStatement(statement)] = mark
	}
}

// ClearMarks drops every executed-statement marker.
func (e *editor) ClearMarks() { e.marks = nil }

func normalizeStatement(s string) string {
	return strings.TrimSpace(strings.TrimRight(strings.TrimSpace(s), ";"))
}

// lockSpan is the line range of a statement that already ran.
type lockSpan struct {
	start int
	end   int
}

// runeOffset converts a byte offset (as reported by the splitter) into a rune
// index, which is what the cursor uses.
func (e editor) runeOffset(byteOffset int) int {
	if byteOffset <= 0 {
		return 0
	}
	value := string(e.runes)
	if byteOffset >= len(value) {
		return len(e.runes)
	}
	return len([]rune(value[:byteOffset]))
}

func (e editor) lineOfRune(idx int) int {
	idx = clampInt(idx, 0, len(e.runes))
	return strings.Count(string(e.runes[:idx]), "\n")
}

// lockSpans returns the line ranges of executed statements.
func (e editor) lockSpans() []lockSpan {
	if len(e.marks) == 0 {
		return nil
	}
	var out []lockSpan
	for _, st := range splitEditorStatements(e.Value()) {
		if _, ok := e.marks[normalizeStatement(st.Text)]; !ok {
			continue
		}
		out = append(out, lockSpan{
			start: e.lineOfRune(e.runeOffset(st.Start)),
			end:   e.lineOfRune(e.runeOffset(st.End)),
		})
	}
	return out
}

func (e editor) lockedSpanAt(line int) (lockSpan, bool) {
	for _, span := range e.lockSpans() {
		if line >= span.start && line <= span.end {
			return span, true
		}
	}
	return lockSpan{}, false
}

// cursorLocked reports whether the cursor sits where an executed statement can
// be changed. Such a statement is only touchable at the very start of its first
// line: everything else inside is read-only.
func (e editor) cursorLocked() bool {
	line, col := e.position()
	span, ok := e.lockedSpanAt(line)
	if !ok {
		return false
	}
	return line != span.start || col != 0
}

// editingLocked reports whether the cursor's line belongs to an executed
// statement, where no edit is allowed until it is unlocked.
func (e editor) editingLocked() bool {
	line, _ := e.position()
	_, locked := e.lockedSpanAt(line)
	return locked
}

// TakeLockedNotice reports and clears the "edit refused" flag.
func (e *editor) TakeLockedNotice() bool {
	notice := e.lockedNotice
	e.lockedNotice = false
	return notice
}

// SnapToEditable moves the cursor out of a locked statement's body to the start
// of its first line.
func (e *editor) SnapToEditable() {
	line, _ := e.position()
	span, ok := e.lockedSpanAt(line)
	if !ok {
		return
	}
	e.cur = e.indexAtLineCol(span.start, 0)
	e.sel = -1
	e.rememberColumn()
}

// AppendStatement puts a new statement at the end of the buffer, on its own
// line, and leaves the cursor at its end. It returns the statement's start as a
// rune offset, so the caller can move the cursor inside it.
func (e *editor) AppendStatement(text string) int {
	// Close a previous unterminated statement so the splitter does not glue it
	// to the new template: find({}) followed by insertOne(...) would otherwise
	// parse as one malformed request.
	if last := lastNonSpaceRune(e.runes); last >= 0 && e.runes[last] != ';' {
		e.runes = append(e.runes, 0)
		copy(e.runes[last+2:], e.runes[last+1:])
		e.runes[last+1] = ';'
		if e.cur > last {
			e.cur++
		}
		if e.sel > last {
			e.sel++
		}
	}

	// Undo covers the separating newline as well, so it goes before start.
	e.autoStart = len(e.runes)
	if len(e.runes) > 0 && e.runes[len(e.runes)-1] != '\n' {
		e.runes = append(e.runes, '\n')
	}
	start := len(e.runes)
	e.cur = start
	e.sel = -1
	e.InsertString(text)

	// Remember the inserted span, newline included, so Esc Esc can undo it.
	e.autoEnd = len(e.runes)
	e.autoValid = true
	return start
}

// lastNonSpaceRune is the index of the last rune that is not whitespace, or -1.
func lastNonSpaceRune(runes []rune) int {
	for i := len(runes) - 1; i >= 0; i-- {
		if !unicode.IsSpace(runes[i]) {
			return i
		}
	}
	return -1
}

// SetCursorOffset puts the cursor at a rune offset in the buffer.
func (e *editor) SetCursorOffset(idx int) {
	e.cur = clampInt(idx, 0, len(e.runes))
	e.sel = -1
	e.rememberColumn()
}

// AutoTemplatePending reports whether an untouched template is still in place.
func (e editor) AutoTemplatePending() bool { return e.autoValid }

// DiscardAutoTemplate removes the untouched template and returns whether it did.
func (e *editor) DiscardAutoTemplate() bool {
	if !e.autoValid || e.autoEnd > len(e.runes) || e.autoEnd <= e.autoStart {
		return false
	}
	e.runes = append(e.runes[:e.autoStart], e.runes[e.autoEnd:]...)
	e.cur = clampInt(e.autoStart, 0, len(e.runes))
	e.sel = -1
	e.autoValid = false
	e.rememberColumn()
	return true
}

// DeleteStatementAtCursor removes the statement the cursor is in. A statement
// that already ran is protected: it must be unlocked first.
func (e *editor) DeleteStatementAtCursor() (deleted, locked bool) {
	line, _ := e.position()
	st, ok := e.StatementAtLine(line)
	if !ok {
		return false, false
	}
	if _, isLocked := e.lockedSpanAt(line); isLocked {
		return false, true
	}

	from := e.runeOffset(st.Start)
	to := e.runeOffset(st.End)
	if to < len(e.runes) && e.runes[to] == '\n' {
		to++
	}
	e.runes = append(e.runes[:from], e.runes[to:]...)
	e.cur = clampInt(from, 0, len(e.runes))
	e.sel = -1
	e.autoValid = false
	e.rememberColumn()
	return true, false
}

// AfterRun puts the cursor on a fresh line below what just ran.
func (e *editor) AfterRun() {
	e.autoValid = false
	if len(e.runes) == 0 {
		return
	}
	if e.runes[len(e.runes)-1] != '\n' {
		e.runes = append(e.runes, '\n')
	}
	e.cur = len(e.runes)
	e.sel = -1
	e.rememberColumn()
}

// StatementAtLine returns the statement covering the line.
func (e editor) StatementAtLine(line int) (editorStatement, bool) {
	for _, st := range splitEditorStatements(e.Value()) {
		from := e.lineOfRune(e.runeOffset(st.Start))
		to := e.lineOfRune(e.runeOffset(st.End))
		if line >= from && line <= to {
			return st, true
		}
	}
	return editorStatement{}, false
}

// Unlock makes an executed statement editable again and returns whether it was
// locked.
func (e *editor) Unlock(statement string) bool {
	key := normalizeStatement(statement)
	if _, ok := e.marks[key]; !ok {
		return false
	}
	delete(e.marks, key)
	return true
}

// MoveToStatement puts the cursor at the statement's first character.
func (e *editor) MoveToStatement(st editorStatement) {
	e.cur = clampInt(e.runeOffset(st.Start), 0, len(e.runes))
	e.sel = -1
	e.rememberColumn()
}

var (
	synKeyword    = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("12"))
	synString     = lipgloss.NewStyle().Foreground(lipgloss.Color("10"))
	synComment    = lipgloss.NewStyle().Faint(true)
	synNumber     = lipgloss.NewStyle().Foreground(lipgloss.Color("11"))
	synPunct      = lipgloss.NewStyle().Faint(true)
	editorSel     = lipgloss.NewStyle().Reverse(true)
	executedSt    = lipgloss.NewStyle().Faint(true)
	editorCur     = lipgloss.NewStyle().Reverse(true).Bold(true)
	gutterSt      = lipgloss.NewStyle().Faint(true)
	markOKStyle   = lipgloss.NewStyle().Foreground(lipgloss.Color("10")).Bold(true)
	markFailStyle = lipgloss.NewStyle().Foreground(lipgloss.Color("9")).Bold(true)
)

func newEditor() editor {
	return editor{sel: -1}
}

// Value returns the whole buffer.
func (e editor) Value() string { return string(e.runes) }

// SetValue replaces the buffer and moves the cursor to the end.
func (e *editor) SetValue(text string) {
	e.autoValid = false
	e.runes = []rune(text)
	e.cur = len(e.runes)
	e.sel = -1
	e.rememberColumn()
}

// InsertString inserts text at the cursor, replacing a selection.
func (e *editor) InsertString(text string) {
	if e.editingLocked() {
		e.lockedNotice = true
		return
	}
	e.autoValid = false
	e.deleteSelection()
	insert := []rune(text)
	tail := append([]rune(nil), e.runes[e.cur:]...)
	e.runes = append(append(e.runes[:e.cur], insert...), tail...)
	e.cur += len(insert)
	e.rememberColumn()
}

// SetCursor moves the cursor to a column of the current line.
func (e *editor) SetCursor(col int) {
	line, _ := e.position()
	start, end := e.lineSpan(line)
	e.cur = clampInt(start+col, start, e.snapInside(end))
	e.rememberColumn()
}

// CursorStart moves to the beginning of the current line.
func (e *editor) CursorStart() { e.SetCursor(0) }

// CursorUp moves one line up.
func (e *editor) CursorUp() { e.moveVertical(-1, false) }

// CursorDown moves one line down.
func (e *editor) CursorDown() { e.moveVertical(1, false) }

// Line reports the cursor line.
func (e editor) Line() int {
	line, _ := e.position()
	return line
}

// CursorOffset is the cursor position as a byte offset in Value.
func (e editor) CursorOffset() int {
	return len(string(e.runes[:e.cur]))
}

func (e editor) HasSelection() bool { return e.sel >= 0 && e.sel != e.cur }

// SelectedText returns the selected text.
func (e editor) SelectedText() string {
	lo, hi := e.selection()
	if lo >= hi {
		return ""
	}
	return string(e.runes[lo:hi])
}

func (e *editor) SelectAll() {
	e.sel = 0
	e.cur = len(e.runes)
}

func (e *editor) ClearSelection() { e.sel = -1 }

// Focused reports whether the editor has the keyboard.
func (e editor) Focused() bool { return e.focused }

func (e *editor) Focus() tea.Cmd {
	e.focused = true
	return nil
}

func (e *editor) Blur() { e.focused = false }

func (e *editor) SetWidth(w int)  { e.width = maxInt(1, w) }
func (e *editor) SetHeight(h int) { e.height = maxInt(1, h) }

// Update handles one key message.
func (e editor) Update(msg tea.Msg) (editor, tea.Cmd) {
	key, ok := msg.(tea.KeyMsg)
	if !ok {
		return e, nil
	}

	shift := strings.HasPrefix(key.String(), "shift+")
	name := strings.TrimPrefix(key.String(), "shift+")

	switch name {
	case "left":
		e.moveHorizontal(-1, shift)
	case "right":
		e.moveHorizontal(1, shift)
	case "up":
		e.moveVertical(-1, shift)
	case "down":
		e.moveVertical(1, shift)
	case "home":
		e.startSelection(shift)
		line, _ := e.position()
		start, _ := e.lineSpan(line)
		e.cur = start
		e.rememberColumn()
		e.SnapToEditable()
	case "end":
		e.startSelection(shift)
		line, _ := e.position()
		if _, locked := e.lockedSpanAt(line); locked {
			e.SnapToEditable()
			break
		}
		_, end := e.lineSpan(line)
		e.cur = e.snapInside(end)
		e.rememberColumn()
	case "pgup":
		e.moveVertical(-e.height, shift)
	case "pgdown":
		e.moveVertical(e.height, shift)
	case "backspace":
		e.deleteBackward()
	case "delete":
		e.deleteForward()
	case "enter", "ctrl+m":
		e.insertNewline()
	case "ctrl+a":
		e.SelectAll()
	case "esc":
		e.sel = -1
	default:
		if len(key.Runes) > 0 && !hasControlModifier(key) {
			e.InsertString(string(key.Runes))
		}
	}
	return e, nil
}

// insertNewline starts the new line at the same indentation as the current one,
// which matters more than Tab-for-spaces now that Tab switches focus.
func (e *editor) insertNewline() {
	line, _ := e.position()
	start, _ := e.lineSpan(line)

	indent := make([]rune, 0, 8)
	for i := start; i < len(e.runes); i++ {
		if e.runes[i] != ' ' && e.runes[i] != '\t' {
			break
		}
		indent = append(indent, e.runes[i])
	}
	e.InsertString("\n" + string(indent))
}

func hasControlModifier(key tea.KeyMsg) bool {
	// Printable runes arrive without modifiers; ctrl+letter arrives as a named
	// key, so anything with runes here is ordinary typing.
	return key.Alt
}

func (e *editor) startSelection(extend bool) {
	if !extend && e.sel >= 0 {
		e.sel = -1
	}
	if extend && e.sel < 0 {
		e.sel = e.cur
	}
}

func (e *editor) moveHorizontal(delta int, extend bool) {
	if e.editingLocked() {
		// A locked statement is reachable only at its start; horizontal moves
		// must not wander into it.
		e.SnapToEditable()
		return
	}
	e.startSelection(extend)
	e.cur = clampInt(e.cur+delta, 0, len(e.runes))
	e.rememberColumn()
	if !extend {
		e.sel = -1
	}
}

func (e *editor) moveVertical(delta int, extend bool) {
	e.startSelection(extend)

	line, col := e.position()
	target := line + delta

	if span, locked := e.lockedSpanAt(target); locked {
		if delta < 0 {
			target = span.start
		} else {
			target = span.end + 1
		}
		// Land on the statement's very start, not at the remembered column.
		e.cur = e.indexAtLineCol(target, 0)
		e.prefCol = 0
		if !extend {
			e.sel = -1
		}
		return
	}

	e.cur = e.indexAtLineCol(target, e.prefColFor(col))
	e.cur = clampInt(e.cur, 0, len(e.runes))
	e.prefCol = e.prefColFor(col)

	if !extend {
		e.sel = -1
	}
}

func (e *editor) prefColFor(current int) int {
	if e.prefCol > current {
		return e.prefCol
	}
	return current
}

func (e editor) indexAtLineCol(line, col int) int {
	lines := e.lineCount()
	if line < 0 {
		return 0
	}
	if line >= lines {
		return len(e.runes)
	}
	start, end := e.lineSpan(line)
	return clampInt(start+col, start, e.snapInside(end))
}

func (e *editor) deleteSelection() {
	lo, hi := e.selection()
	if lo >= hi {
		return
	}
	e.runes = append(e.runes[:lo], e.runes[hi:]...)
	e.cur = lo
	e.sel = -1
	e.rememberColumn()
}

func (e *editor) deleteBackward() {
	if e.editingLocked() {
		e.lockedNotice = true
		return
	}
	e.autoValid = false
	if e.HasSelection() {
		e.deleteSelection()
		return
	}
	if e.cur == 0 {
		return
	}
	e.runes = append(e.runes[:e.cur-1], e.runes[e.cur:]...)
	e.cur--
	e.rememberColumn()
}

func (e *editor) deleteForward() {
	if e.editingLocked() {
		e.lockedNotice = true
		return
	}
	e.autoValid = false
	if e.HasSelection() {
		e.deleteSelection()
		return
	}
	if e.cur >= len(e.runes) {
		return
	}
	e.runes = append(e.runes[:e.cur], e.runes[e.cur+1:]...)
	e.rememberColumn()
}

func (e editor) selection() (int, int) {
	if e.sel < 0 {
		return e.cur, e.cur
	}
	if e.sel < e.cur {
		return e.sel, e.cur
	}
	return e.cur, e.sel
}

func (e *editor) rememberColumn() {
	_, col := e.position()
	e.prefCol = col
}

// position converts the cursor into line and column.
func (e editor) position() (int, int) {
	line, start := 0, 0
	for i := 0; i < e.cur && i < len(e.runes); i++ {
		if e.runes[i] == '\n' {
			line++
			start = i + 1
		}
	}
	return line, e.cur - start
}

func (e editor) lineCount() int { return strings.Count(string(e.runes), "\n") + 1 }

// lineSpan returns the rune indexes of the line start and of its newline.
func (e editor) lineSpan(line int) (int, int) {
	start, current := 0, 0
	for i, r := range e.runes {
		if current == line {
			start = i
			break
		}
		if r == '\n' {
			current++
			if current == line {
				start = i + 1
			}
		}
	}
	if line == 0 {
		start = 0
	}
	for i := start; i < len(e.runes); i++ {
		if e.runes[i] == '\n' {
			return start, i
		}
	}
	return start, len(e.runes)
}

// snapInside keeps an index before a newline.
func (e editor) snapInside(end int) int {
	if end > len(e.runes) {
		return len(e.runes)
	}
	return end
}

// View renders the visible window with colours.
func (e editor) View() string {
	if e.width <= 0 || e.height <= 0 {
		return ""
	}

	styles := highlightMongoBuffer(e.runes)
	lines := e.lineCount()
	e.adjustViewport()

	marks := e.lineMarks()
	executed := e.executedLines()
	numbers := maxInt(3, len(itoa(lines)))
	var out strings.Builder

	for row := 0; row < e.height; row++ {
		lineIdx := e.offLine + row
		if row > 0 {
			out.WriteByte('\n')
		}
		if lineIdx >= lines {
			continue
		}

		out.WriteString(e.gutterCell(lineIdx, numbers, marks[lineIdx]))
		out.WriteByte(' ')
		out.WriteString(e.renderLine(lineIdx, styles, executed[lineIdx]))
	}
	return out.String()
}

// gutterCell renders the executed marker and the line number.
func (e editor) gutterCell(line, numbers int, mark execMark) string {
	cell := " " + padLeft(itoa(line+1), numbers)
	switch mark {
	case markOK:
		return markOKStyle.Render("✓") + gutterSt.Render(cell[1:])
	case markFailed:
		return markFailStyle.Render("✗") + gutterSt.Render(cell[1:])
	}
	return gutterSt.Render(cell)
}

// executedLines reports which lines belong to statements that already ran, so
// their text can be dimmed and stop drawing the eye.
func (e editor) executedLines() map[int]bool {
	if len(e.marks) == 0 {
		return nil
	}
	out := map[int]bool{}
	for _, st := range splitEditorStatements(e.Value()) {
		if _, ok := e.marks[normalizeStatement(st.Text)]; !ok {
			continue
		}
		from := strings.Count(string(e.runes[:clampInt(st.Start, 0, len(e.runes))]), "\n")
		to := strings.Count(string(e.runes[:clampInt(st.End, 0, len(e.runes))]), "\n")
		for line := from; line <= to; line++ {
			out[line] = true
		}
	}
	return out
}

// lineMarks maps the first line of every executed statement to its outcome.
func (e editor) lineMarks() map[int]execMark {
	if len(e.marks) == 0 {
		return nil
	}
	out := map[int]execMark{}
	for _, st := range splitEditorStatements(e.Value()) {
		mark, ok := e.marks[normalizeStatement(st.Text)]
		if !ok {
			continue
		}
		out[strings.Count(string(e.runes[:st.Start]), "\n")] = mark
	}
	return out
}

func (e *editor) adjustViewport() {
	line, col := e.position()

	if line < e.offLine {
		e.offLine = line
	}
	if line >= e.offLine+e.height {
		e.offLine = line - e.height + 1
	}
	if e.offLine < 0 {
		e.offLine = 0
	}

	visible := e.width - maxInt(3, len(itoa(e.lineCount()))) - 3
	if visible < 8 {
		visible = 8
	}
	if col < e.offCol {
		e.offCol = col
	}
	if col >= e.offCol+visible {
		e.offCol = col - visible + 1
	}
	if e.offCol < 0 {
		e.offCol = 0
	}
}

func (e editor) renderLine(line int, styles []synKind, executed bool) string {
	start, end := e.lineSpan(line)
	lo, hi := e.selection()

	from := clampInt(start+e.offCol, start, end)
	lastStart := -1

	// The cursor may sit on the newline or past the text: show a block there.
	cursorHere := e.cur >= from && e.cur <= end

	var out strings.Builder
	i := from
	for i < end {
		kind := styles[i]
		selected := e.sel >= 0 && i >= lo && i < hi
		isCursor := e.focused && i == e.cur
		j := i + 1
		for j < end {
			nextKind := styles[j]
			nextSelected := e.sel >= 0 && j >= lo && j < hi
			nextCursor := e.focused && j == e.cur
			if nextKind != kind || nextSelected != selected || nextCursor != isCursor {
				break
			}
			j++
		}

		segment := string(e.runes[i:j])
		out.WriteString(styleSegment(kind, selected, isCursor, executed, segment))
		lastStart = i
		i = j
	}

	if e.focused && e.cur == end && (lastStart < 0 || e.cur >= from) {
		if cursorHere {
			out.WriteString(editorCur.Render(" "))
		}
	}
	return out.String()
}

func styleSegment(kind synKind, selected, cursor, executed bool, text string) string {
	switch {
	case cursor:
		return editorCur.Render(text)
	case selected:
		return editorSel.Render(text)
	case executed:
		return executedSt.Render(text)
	}
	switch kind {
	case synKeywordKind:
		return synKeyword.Render(text)
	case synStringKind:
		return synString.Render(text)
	case synCommentKind:
		return synComment.Render(text)
	case synNumberKind:
		return synNumber.Render(text)
	case synPunctKind:
		return synPunct.Render(text)
	}
	return text
}

// synKind is the colour class of one rune.
type synKind int

const (
	synPlain synKind = iota
	synKeywordKind
	synStringKind
	synCommentKind
	synNumberKind
	synPunctKind
)

// highlightMongoBuffer colours the JSON/MQL editor: strings, numbers,
// $operators, method names and comments. It is a tolerant scanner, not a
// parser; the request is validated before it is sent.
func highlightMongoBuffer(runes []rune) []synKind {
	out := make([]synKind, len(runes))
	i, n := 0, len(runes)
	for i < n {
		c := runes[i]
		switch {
		case c == '/' && i+1 < n && runes[i+1] == '/':
			for i < n && runes[i] != '\n' {
				out[i] = synCommentKind
				i++
			}
		case c == '/' && i+1 < n && runes[i+1] == '*':
			out[i] = synCommentKind
			out[i+1] = synCommentKind
			i += 2
			for i < n {
				out[i] = synCommentKind
				if i+1 < n && runes[i] == '*' && runes[i+1] == '/' {
					out[i+1] = synCommentKind
					i += 2
					break
				}
				i++
			}
		case c == '"' || c == '\'':
			quote := c
			out[i] = synStringKind
			i++
			for i < n {
				out[i] = synStringKind
				if runes[i] == '\\' && i+1 < n {
					out[i+1] = synStringKind
					i += 2
					continue
				}
				if runes[i] == quote {
					i++
					break
				}
				i++
			}
		case c == '-' || unicode.IsDigit(c):
			if c == '-' && (i+1 >= n || !unicode.IsDigit(runes[i+1])) {
				out[i] = synPunctKind
				i++
				break
			}
			for i < n {
				if unicode.IsDigit(runes[i]) || strings.ContainsRune(".eE+-", runes[i]) {
					out[i] = synNumberKind
					i++
					continue
				}
				break
			}
		case c == '$' || isWordRune(c):
			start := i
			for i < n && (isWordRune(runes[i]) || runes[i] == '$') {
				i++
			}
			word := string(runes[start:i])
			if c == '$' || mongoKeywords[word] {
				for k := start; k < i; k++ {
					out[k] = synKeywordKind
				}
			}
		case unicode.IsSpace(c):
			i++
		case strings.ContainsRune("{}[](),:.", c):
			out[i] = synPunctKind
			i++
		default:
			i++
		}
	}
	return out
}

var mongoKeywords = map[string]bool{
	"db": true, "true": true, "false": true, "null": true, "undefined": true,
	"ObjectId": true, "ISODate": true, "NumberDecimal": true, "NumberLong": true, "Date": true,
	"find": true, "findOne": true, "aggregate": true, "count": true, "countDocuments": true,
	"distinct": true, "insertOne": true, "insertMany": true, "updateOne": true,
	"updateMany": true, "replaceOne": true, "deleteOne": true, "deleteMany": true,
	"createIndex": true, "dropIndex": true, "drop": true, "collMod": true,
	"rename": true, "createCollection": true, "getCollection": true,
	"sort": true, "project": true, "skip": true, "limit": true,
}

func isWordRune(r rune) bool {
	return r == '_' || unicode.IsLetter(r) || unicode.IsDigit(r)
}

func padLeft(s string, width int) string {
	if len(s) >= width {
		return s
	}
	return strings.Repeat(" ", width-len(s)) + s
}

func itoa(v int) string {
	if v == 0 {
		return "0"
	}
	digits := make([]byte, 0, 8)
	for v > 0 {
		digits = append([]byte{byte('0' + v%10)}, digits...)
		v /= 10
	}
	return string(digits)
}
