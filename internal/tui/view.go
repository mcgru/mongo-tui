package tui

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"strings"
	"unicode/utf8"

	"github.com/charmbracelet/lipgloss"

	"mongo-tui/internal/config"
	"mongo-tui/internal/engine"
	"mongo-tui/internal/mongouri"
)

var errWriter io.Writer = os.Stderr

// activeBorder and activeTitle mark the pane that has the keyboard. A bright
// cyan survives both dark and light terminal themes better than plain blue.
var (
	activeBorder = lipgloss.Color("14")
	activeTitle  = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("14"))
	// The database badge is always visible so the current database can never be
	// mistaken, and the open object is marked in the tree.
	dbBadge         = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("10"))
	txBadge         = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("11"))
	abortBadge      = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("9"))
	currentDBStyle  = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("10"))
	openObjectStyle = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("13"))
	// pendingDBStyle paints a staged database that is not on the server yet:
	// faded pink, distinct from the magenta open-object marker and the green
	// current database.
	pendingDBStyle = lipgloss.NewStyle().Faint(true).Foreground(lipgloss.Color("176"))
)

var (
	titleStyle = lipgloss.NewStyle().Bold(true)
	dimStyle   = lipgloss.NewStyle().Faint(true)
	errorStyle = lipgloss.NewStyle().Bold(true)
	activeTab  = lipgloss.NewStyle().Bold(true).Underline(true)
	warnStyle  = lipgloss.NewStyle().Bold(true)
)

// View renders the current screen.
func (m Model) View() string {
	if m.width <= 0 || m.height <= 0 {
		return "mongo-tui\n"
	}
	if m.help {
		return m.viewHelp()
	}

	switch m.scr {
	case screenPicker:
		return m.viewPicker()
	case screenImport:
		return m.viewImport()
	case screenImportURIs:
		return m.viewImportURIs()
	case screenImportRoles:
		return m.viewImportRoles()
	case screenInput:
		return m.viewPrompt(
			"New connection",
			"Enter a MongoDB URI, for example mongodb://host:27017/db. The password is asked for separately.",
			m.input.View(),
			"Enter connect    Esc back    Ctrl+Q quit",
		)
	case screenPassword:
		return m.viewPrompt(
			"Password",
			fmt.Sprintf("%s needs a password. It is kept in memory for this session only.", m.connectionEndpoint()),
			m.input.View(),
			"Enter connect    Esc back    Ctrl+Q quit",
		)
	case screenSaveProfile:
		return m.viewPrompt(
			"Save profile",
			"The connection is stored without a password. Enter a name to find it in the picker.",
			m.input.View(),
			"Enter save    Esc cancel",
		)
	case screenNewDatabase:
		return m.viewPrompt(
			"New database",
			"Stage a database with the current name. MongoDB creates it only when a document is written, so it stays faded pink until then.",
			m.input.View(),
			"Enter stage    Esc cancel",
		)
	case screenConnecting:
		return m.viewConnecting()
	case screenMain:
		return m.viewMain()
	case screenError:
		return m.viewError()
	}
	return ""
}

func (m Model) viewPicker() string {
	var b strings.Builder
	b.WriteString(titleStyle.Render("mongo-tui " + m.opts.Version))
	b.WriteString("\n")
	b.WriteString(dimStyle.Render("MongoDB 8.0+ through the official Go driver"))
	b.WriteString("\n\n")
	b.WriteString(titleStyle.Render("Connect to"))
	b.WriteString("\n\n")

	if len(m.profiles) == 1 {
		b.WriteString(dimStyle.Render("  no saved connections yet\n\n"))
	}
	for i, p := range m.profiles {
		cursor := "  "
		line := p.Label
		if i == m.cursor {
			cursor = "> "
			line = titleStyle.Render(line)
		}
		b.WriteString(cursor + line)
		if detail := profileDetail(p); detail != "" {
			b.WriteString(dimStyle.Render("  " + detail))
		}
		b.WriteString("\n")
	}

	b.WriteString("\n")
	if m.confirmDelete != "" {
		label := "this profile"
		if profile, ok := m.opts.State.Find(m.confirmDelete); ok {
			label = profile.Label
		}
		b.WriteString(errorStyle.Render("Delete " + label + "?"))
		b.WriteString(dimStyle.Render("    y yes    n no"))
	} else {
		importKey := m.keys.Get(config.ActionImportProfiles)
		hint := "↑/↓ choose    Enter connect    f pin    d delete"
		if importKey != "" {
			hint += "    " + importKey + " import"
		}
		b.WriteString(dimStyle.Render(hint + "    Ctrl+Q quit"))
	}
	return m.frame(b.String())
}

func profileDetail(p config.Profile) string {
	if p.ID == "" {
		return ""
	}
	if p.Engine == "mongo" {
		var parts []string
		if u, err := mongouri.Parse(p.URI); err == nil {
			if u.Host != "" {
				parts = append(parts, u.Host)
			}
			if db := u.Database(); db != "" {
				parts = append(parts, db)
			}
			if u.User != "" {
				parts = append(parts, u.User)
			}
		}
		if p.Favorite {
			parts = append(parts, "★")
		}
		return strings.Join(parts, " ")
	}
	var parts []string
	if p.Host != "" {
		host := p.Host
		if p.Port > 0 {
			host = fmt.Sprintf("%s:%d", host, p.Port)
		}
		parts = append(parts, host)
	}
	if p.Database != "" {
		parts = append(parts, p.Database)
	}
	if p.Username != "" {
		parts = append(parts, p.Username)
	}
	if p.Favorite {
		parts = append(parts, "★")
	}
	return strings.Join(parts, " ")
}

func (m Model) viewConnecting() string {
	body := fmt.Sprintf("%s connecting to %s", m.spin.View(), m.connectionEndpoint())
	if m.readOnly {
		body += "\n\n" + dimStyle.Render("read-only session")
	}
	return m.frame(titleStyle.Render("mongo-tui") + "\n\n" + body + "\n\n" + dimStyle.Render("Ctrl+Q quit"))
}

func (m Model) viewPrompt(title, help, field, keys string) string {
	var b strings.Builder
	b.WriteString(titleStyle.Render(title))
	b.WriteString("\n\n")
	if help != "" {
		b.WriteString(dimStyle.Render(help))
		b.WriteString("\n\n")
	}
	b.WriteString(field)
	b.WriteString("\n\n")
	b.WriteString(dimStyle.Render(keys))
	return m.frame(b.String())
}

func (m Model) viewError() string {
	var b strings.Builder
	b.WriteString(errorStyle.Render("Connection failed"))
	b.WriteString("\n\n")
	if m.err != nil {
		b.WriteString(m.err.Error())
		b.WriteString("\n\n")
	}
	b.WriteString(dimStyle.Render("r retry    Esc back    Ctrl+Q quit"))
	return m.frame(b.String())
}

// Pane sizing. Ctrl+arrows change the percentages; the values are clamped so no
// pane can collapse.
const (
	defaultLeftPct   = 28
	defaultResultPct = 55
	minLeftPct       = 15
	maxLeftPct       = 60
	minResultPct     = 25
	maxResultPct     = 85
	resizeStep       = 2
)

// layout splits the main screen using the current percentages.
func (m Model) layout() (leftW, rightW, resultH, editorH int) {
	bodyH := m.height - 2
	if bodyH < 8 {
		bodyH = 8
	}
	leftPct := m.leftPct
	if leftPct == 0 {
		leftPct = defaultLeftPct
	}
	leftW = m.width * leftPct / 100
	if leftW < 22 {
		leftW = 22
	}
	if leftW > m.width-32 {
		leftW = m.width - 32
	}
	if leftW < 10 {
		leftW = 10
	}
	rightW = m.width - leftW

	resultPct := m.resultPct
	if resultPct == 0 {
		resultPct = defaultResultPct
	}
	resultH = bodyH * resultPct / 100
	if resultH < 4 {
		resultH = 4
	}
	editorH = bodyH - resultH
	if editorH < 4 {
		editorH = 4
	}
	return leftW, rightW, resultH, editorH
}

func (m Model) viewMain() string {
	leftW, rightW, resultH, editorH := m.layout()

	// The role editor is modal, so the panes behind it lose the focus colour.
	active := m.roles == nil
	left := m.pane(m.objectsTitle(), m.treeLines(leftW-4, resultH+editorH-4), leftW, resultH+editorH, active && m.focus == focusTree)
	result := m.pane(m.tabBar(), m.resultLines(rightW-4, resultH-4), rightW, resultH, active && m.focus == focusGrid)
	editor := m.pane(m.editorTitle(), m.editorLines(rightW-4), rightW, editorH, active && m.focus == focusEditor)
	right := lipgloss.JoinVertical(lipgloss.Left, result, editor)

	body := lipgloss.JoinHorizontal(lipgloss.Top, left, right)
	view := lipgloss.JoinVertical(lipgloss.Left, m.headerLine(), body, m.footerLine())
	if m.roles != nil {
		return m.roleEditorOverlay(view)
	}
	return view
}

// headerLine keeps the database badge visible from every screen of the main
// window: the owner asked to see at a glance which database is in use.
func (m Model) headerLine() string {
	mode := "RW"
	if m.readOnly {
		mode = "RO"
	}

	database := m.currentDatabase()
	if database == "" {
		database = "?"
	}

	parts := []string{
		titleStyle.Render(m.appName() + " " + m.opts.Version),
		dbBadge.Render("db:" + database),
	}
	parts = append(parts,
		titleStyle.Render(m.connectionEndpoint()),
		titleStyle.Render(m.engineLabel()),
		mode,
	)

	switch m.tx {
	case engine.TxInTransaction:
		parts = append(parts, txBadge.Render("TX"))
	case engine.TxAborted:
		parts = append(parts, abortBadge.Render("TX ABORTED"))
	}

	parts = append(parts, dimStyle.Render(m.status))
	return strings.Join(parts, dimStyle.Render(" | "))
}

func (m Model) serverVersion() string {
	if m.sess == nil {
		return "?"
	}
	return m.sess.Summary().ServerVersion
}

func (m Model) tabBar() string {
	tabs := []struct {
		name string
		tab  paneTab
	}{
		{"RESULT", tabResult},
		{"OBJECT", tabObject},
		{"MESSAGES", tabMessages},
	}
	labels := make([]string, 0, len(tabs))
	for _, t := range tabs {
		label := t.name
		if m.tab == t.tab {
			label = activeTab.Render(label)
		}
		if t.tab == tabMessages && len(m.messages) > 0 {
			label += dimStyle.Render(fmt.Sprintf(" (%d)", len(m.messages)))
		}
		labels = append(labels, label)
	}
	return strings.Join(labels, " | ")
}

func (m Model) objectsTitle() string { return "OBJECTS" }

func (m Model) editorTitle() string {
	label := "SQL EDITOR"
	if m.isMongo() {
		label = "MQL / JSON EDITOR"
	}
	if m.focus == focusEditor {
		label += " · Ctrl+J run"
	}
	if m.running {
		label += " · running…"
	}
	return label
}

func (m Model) treeLines(width, height int) []string {
	if m.tree == nil {
		return []string{dimStyle.Render("not connected")}
	}
	return m.tree.lines(width, height, m.openObject)
}

func (m Model) resultLines(width, height int) []string {
	switch m.tab {
	case tabObject:
		return m.cardLines(width, height)
	case tabMessages:
		return m.messageLines(width, height)
	}
	if m.grid == nil {
		return []string{dimStyle.Render("no result yet — Ctrl+J runs the statement under the cursor")}
	}
	return m.grid.lines(width, height)
}

// cardLines renders the MongoDB collection card or the selected document.
func (m Model) cardLines(width, height int) []string {
	if m.mongoCard != nil {
		return m.mongoCardLines(width, height)
	}
	return m.mongoDocumentLines(width, height)
}

// mongoDocumentLines shows the full EJSON document of the selected result row.
// The adapter puts the unflattened document next to the grid rows, so the view
// does not have to guess how dotted columns were nested.
func (m Model) mongoDocumentLines(width, height int) []string {
	if m.grid == nil || m.grid.empty() || len(m.grid.res.Rows) == 0 {
		return []string{dimStyle.Render("run a find or aggregate, then press PgUp/PgDn here to read a document")}
	}
	row := m.grid.curRow
	if row < 0 || row >= len(m.grid.res.Rows) {
		row = 0
	}

	var raw []byte
	if row < len(m.grid.res.Documents) {
		raw = m.grid.res.Documents[row]
	}
	if len(raw) == 0 {
		return []string{dimStyle.Render("the adapter did not return the full document for this row")}
	}

	var pretty bytes.Buffer
	if err := json.Indent(&pretty, raw, "", "  "); err != nil {
		return []string{errorStyle.Render("document is not valid JSON: " + err.Error())}
	}

	lines := []string{
		titleStyle.Render(fmt.Sprintf("DOCUMENT row %d", row+1)),
		"",
	}
	lines = append(lines, strings.Split(pretty.String(), "\n")...)
	return window(lines, m.cardOff, m.colOff, height, width)
}

// mongoCardLines renders a collection card: observed fields, indexes, stats,
// options and the JSON Schema validator. Fields are a sample, not a schema.
func (m Model) mongoCardLines(width, height int) []string {
	card := m.mongoCard
	if card == nil {
		return []string{dimStyle.Render("select a collection in the object tree and press Enter")}
	}
	if !card.Exists {
		return []string{errorStyle.Render(card.Database + "." + card.Collection + " — not found or not visible")}
	}

	headline := card.Database + "." + card.Collection + "  " + card.Type
	lines := []string{titleStyle.Render(headline), ""}

	lines = append(lines, dimStyle.Render(fmt.Sprintf("FIELDS (sample %d docs)", card.SampleSize)))
	switch {
	case card.FieldsError != "":
		lines = append(lines, "  "+errorStyle.Render(card.FieldsError))
	case len(card.Fields) == 0:
		lines = append(lines, dimStyle.Render("  no documents sampled"))
	default:
		for _, field := range card.Fields {
			line := fmt.Sprintf("  %-24s %-10s %d/%d", field.Name, field.Types, field.Present, field.Total)
			lines = append(lines, line)
		}
	}

	lines = append(lines, "", dimStyle.Render("INDEXES"))
	if len(card.Indexes) == 0 {
		lines = append(lines, dimStyle.Render("  none"))
	}
	for _, index := range card.Indexes {
		flags := ""
		if index.Unique {
			flags = " unique"
		}
		lines = append(lines, "  "+index.Name+flags+dimStyle.Render("  "+index.Keys))
	}

	lines = append(lines, "", dimStyle.Render("STATS"))
	if card.StatsError != "" {
		lines = append(lines, "  "+errorStyle.Render(card.StatsError))
	} else {
		lines = append(lines, fmt.Sprintf("  %d documents, %s", card.Count, formatBytes(card.SizeBytes)))
	}

	lines = append(lines, "", dimStyle.Render("OPTIONS"))
	for _, line := range prettyJSONLines(card.Options) {
		lines = append(lines, "  "+line)
	}

	if strings.TrimSpace(card.Validator) != "" {
		lines = append(lines, "", dimStyle.Render("VALIDATOR"))
		for _, line := range prettyJSONLines(card.Validator) {
			lines = append(lines, "  "+line)
		}
	}

	return window(lines, m.cardOff, m.colOff, height, width)
}

func prettyJSONLines(text string) []string {
	if strings.TrimSpace(text) == "" {
		return nil
	}
	var pretty bytes.Buffer
	if err := json.Indent(&pretty, []byte(text), "", "  "); err != nil {
		return []string{text}
	}
	return strings.Split(pretty.String(), "\n")
}

func formatBytes(n int64) string {
	const unit = 1024
	if n < unit {
		return fmt.Sprintf("%d B", n)
	}
	div, exp := int64(unit), 0
	for value := n / unit; value >= unit; value /= unit {
		div *= unit
		exp++
	}
	return fmt.Sprintf("%.1f %ciB", float64(n)/float64(div), "KMGTPE"[exp])
}

// window returns a slice of lines starting at off, kept to width cells from the
// left edge colOff so the right pane can scroll wide output sideways.
func window(lines []string, off, colOff, height, width int) []string {
	if height <= 0 {
		return nil
	}
	if off >= len(lines) {
		off = maxInt(0, len(lines)-height)
	}
	end := off + height
	if end > len(lines) {
		end = len(lines)
	}
	out := make([]string, 0, end-off)
	for _, line := range lines[off:end] {
		out = append(out, hclip(line, colOff, maxInt(1, width)))
	}
	return out
}

// hclip keeps width visible cells of a styled or plain line starting at off.
// ANSI escape sequences are copied through and take no cells, so colours survive
// a sideways scroll of the right pane.
func hclip(line string, off, width int) string {
	if width <= 0 {
		return ""
	}
	if off < 0 {
		off = 0
	}
	if !strings.Contains(line, "\x1b") {
		runes := []rune(line)
		if off >= len(runes) {
			return ""
		}
		return truncate(string(runes[off:]), width)
	}
	var b strings.Builder
	visible := 0
	written := 0
	for i := 0; i < len(line); {
		if line[i] == 0x1b { // ESC … m : copy the whole sequence, no cell
			j := i
			for j < len(line) && line[j] != 'm' {
				j++
			}
			if j < len(line) {
				j++ // include the terminating 'm'
			}
			b.WriteString(line[i:j])
			i = j
			continue
		}
		r, size := utf8.DecodeRuneInString(line[i:])
		if visible >= off && written < width {
			b.WriteRune(r)
			written++
		}
		visible++
		i += size
	}
	return b.String()
}

func (m Model) messageLines(width, height int) []string {
	if len(m.messages) == 0 {
		return []string{dimStyle.Render("no messages")}
	}
	var lines []string
	for _, msg := range m.messages {
		for _, line := range strings.Split(msg, "\n") {
			lines = append(lines, line)
		}
	}
	return window(lines, m.msgOff, m.colOff, height, width)
}

// footerLine shows the status and the keys that work right now, so the hint
// follows the focus instead of listing every binding at once.
func (m Model) footerLine() string {
	if m.searchOpen {
		hint := "   Enter search   Esc cancel   " + m.searchCaseLabel()
		if m.searchErr != "" {
			hint = "   " + m.searchErr
		}
		prompt := strings.TrimRight(m.searchInput.View(), " ")
		return "/" + prompt + dimStyle.Render(hint)
	}
	if m.running {
		return dimStyle.Render(m.status + "   |   " + m.keys.Get(config.ActionCancel) + " cancel")
	}

	status := m.status
	if status == "" {
		status = "ready"
	}
	return dimStyle.Render(truncate(status+"   |   "+m.footerKeys(), maxInt(10, m.width-2)))
}

func (m Model) footerKeys() string {
	key := m.keys.Get

	if m.roles != nil {
		return "↑↓ database  Space cycle  Ctrl+Space all roles  Enter apply  Esc close  " +
			key(config.ActionQuit) + " quit"
	}

	switch m.scr {
	case screenPicker:
		return "↑↓ choose  Enter connect  f pin  d delete  " +
			key(config.ActionImportProfiles) + " import  " + key(config.ActionQuit) + " quit"
	case screenImport:
		return "↑↓ choose  Enter import  Esc back  " + key(config.ActionQuit) + " quit"
	case screenImportURIs:
		return "↑↓ choose  Enter import  Esc back  " + key(config.ActionQuit) + " quit"
	case screenImportRoles:
		return "↑↓ move  Space toggle  Enter apply  Esc back  " + key(config.ActionQuit) + " quit"

	case screenMain:
		help := "  " + key(config.ActionHelp) + " help"
		if m.isMongo() {
			switch m.focus {
			case focusTree:
				base := "↑↓ move  →/Enter open  ← collapse  " +
					key(config.ActionSelectTemplate) + " find  " + key(config.ActionPeek) + " peek  " +
					key(config.ActionCreateTemplate) + " index  " + key(config.ActionDropTemplate) + " drop  " +
					key(config.ActionAlterTemplate) + " collMod  " + key(config.ActionInsertName) + " insert  " +
					key(config.ActionRefresh) + " reload  " + key(config.ActionFocus) + " focus"
				if node := m.currentTreeNode(); node != nil && node.kind == nodeFolder && node.title == "Users" {
					base += "  " + key(config.ActionImportProfiles) + " import"
				}
				return base + help
			case focusGrid:
				hint := "↑↓ move  PgUp/PgDn document"
				if m.tab == tabResult {
					hint = "↑↓←→ move cell  PgUp/PgDn page"
					if m.grid != nil && m.grid.search != nil {
						hint += "  n/N match  Esc clear"
					}
					caseHint := "i case"
					if m.searchIgnoreCase {
						caseHint = "i ignore-case"
					}
					hint += "  " + key(config.ActionSelectTemplate) + " search  " + caseHint
					if m.pageMore {
						hint += "  " + key(config.ActionLoadMore) + " more  " +
							key(config.ActionLoadAll) + " all"
					}
				} else {
					hint = "↑↓ scroll  ←→ sideways"
				}
				return hint + "  " + key(config.ActionCopyCell) +
					" cell  " + key(config.ActionCopyRow) + " row  " +
					key(config.ActionPanesBack) + "/" + key(config.ActionPanes) + " panes  " +
					key(config.ActionFocus) + " focus" + help
			default:
				return key(config.ActionRun) + " run request  " + key(config.ActionHistory) +
					" history  " + key(config.ActionEditStatement) + " edit  " +
					key(config.ActionDeleteStatement) + " del  " +
					key(config.ActionFocus) + " focus" + help
			}
		}
		switch m.focus {
		case focusTree:
			return "↑↓ move  →/Enter open  ← collapse  " +
				key(config.ActionSelectTemplate) + " select  " + key(config.ActionInsertName) + " insert  " +
				key(config.ActionPeek) + " peek  " + key(config.ActionEditObject) + " edit  " +
				key(config.ActionCreateTemplate) + key(config.ActionDropTemplate) +
				key(config.ActionAlterTemplate) + " ddl  " +
				key(config.ActionRefresh) + " reload  " + key(config.ActionFocus) + " focus" + help
		case focusGrid:
			return "↑↓←→ move  " + key(config.ActionCopyCell) + " cell  " + key(config.ActionCopyRow) +
				" row  " + key(config.ActionPanes) + " panes  " + key(config.ActionFocus) + " focus" + help
		default:
			return key(config.ActionRun) + " run  " + key(config.ActionComplete) + " complete  " +
				key(config.ActionHistory) + " history  " + key(config.ActionEditStatement) + " edit  " +
				key(config.ActionDeleteStatement) + " del  " +
				key(config.ActionFocus) + " focus" + help
		}

	case screenSaveProfile, screenInput, screenPassword, screenNewDatabase:
		return "Enter confirm  Esc cancel"
	case screenConnecting:
		return key(config.ActionQuit) + " quit"
	case screenError:
		return "r retry  Esc back  " + key(config.ActionQuit) + " quit"
	}
	return ""
}

// pane draws a titled box. Lines are expected to fit the inner width already.
func (m Model) pane(title string, lines []string, width, height int, active bool) string {
	if width < 8 {
		width = 8
	}
	if height < 3 {
		height = 3
	}
	inner := width - 4
	if inner < 1 {
		inner = 1
	}

	titleText := titleStyle.Render(title)
	if active {
		titleText = activeTitle.Render(title)
	}

	// lipgloss counts padding inside Width, so the text area is two cells
	// narrower than inner. Clip every line here: a single overlong row would
	// otherwise wrap, make this pane one line taller than its neighbours and
	// scroll the header off the screen.
	textWidth := maxInt(1, inner-2)
	content := []string{hclip(titleText, 0, textWidth), ""}
	for _, line := range lines {
		if len(content) >= height-2 {
			break
		}
		content = append(content, hclip(line, 0, textWidth))
	}

	style := lipgloss.NewStyle().
		Border(lipgloss.NormalBorder()).
		Width(inner).
		Height(height-2).
		Padding(0, 1)
	if active {
		style = style.BorderForeground(activeBorder)
	}
	return style.Render(strings.Join(content, "\n"))
}

func (m Model) frame(content string) string {
	return lipgloss.NewStyle().
		Width(maxInt(1, m.width-2)).
		Height(maxInt(1, m.height-1)).
		Padding(0, 1).
		Render(content)
}
