package tui

import (
	"crypto/rand"
	"encoding/base64"
	"encoding/hex"
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"mongo-tui/internal/config"
)

// clipboardWriter is where clipboard escape sequences go. Tests replace it.
var clipboardWriter io.Writer = os.Stdout

// osc52 copies text through the terminal, which also works over ssh and inside
// tmux (with set-clipboard on). No external utility is needed.
func osc52(text string) string {
	return "\x1b]52;c;" + base64.StdEncoding.EncodeToString([]byte(text)) + "\a"
}

func copyToClipboard(text string) error {
	_, err := io.WriteString(clipboardWriter, osc52(text))
	return err
}

// historyEntry is one executed statement.
type historyEntry struct {
	SQL    string
	At     time.Time
	Status string
	Failed bool
}

// histState is the open history picker.
type histState struct {
	open   bool
	items  []historyEntry
	cursor int
}

const historyLimit = 200

func (m Model) rememberStatement(sql string, status string, failed bool) []historyEntry {
	history := append(m.history, historyEntry{SQL: strings.TrimSpace(sql), At: time.Now(), Status: status, Failed: failed})
	if len(history) > historyLimit {
		history = history[len(history)-historyLimit:]
	}
	return history
}

func (m Model) openHistory() (tea.Model, tea.Cmd) {
	if len(m.history) == 0 {
		m.status = "no query history yet"
		return m, nil
	}
	items := make([]historyEntry, 0, len(m.history))
	for i := len(m.history) - 1; i >= 0; i-- {
		items = append(items, m.history[i])
	}
	m.hist = histState{open: true, items: items}
	return m, nil
}

// handleHistoryKey drives the history picker.
func (m Model) handleHistoryKey(key string) (Model, tea.Cmd, bool) {
	switch key {
	case "esc":
		m.hist = histState{}
		return m, nil, true
	case "up", "ctrl+p":
		if m.hist.cursor > 0 {
			m.hist.cursor--
		}
		return m, nil, true
	case "down", "ctrl+n":
		if m.hist.cursor < len(m.hist.items)-1 {
			m.hist.cursor++
		}
		return m, nil, true
	case "enter", "tab":
		if len(m.hist.items) > 0 {
			m.editor.SetValue(m.hist.items[m.hist.cursor].SQL)
			m.hist = histState{}
			return m, m.editor.Focus(), true
		}
		return m, nil, true
	}
	m.hist = histState{}
	return m, nil, false
}

// historyLines renders the picker over the editor, like the completion popup.
func (m Model) historyLines(width int) []string {
	const maxItems = 8

	items := m.hist.items
	start := 0
	if m.hist.cursor >= maxItems {
		start = m.hist.cursor - maxItems + 1
	}
	end := start + maxItems
	if end > len(items) {
		end = len(items)
	}

	out := make([]string, 0, end-start)
	for i := start; i < end; i++ {
		mark := "  "
		if i == m.hist.cursor {
			mark = "› "
		}
		sql := strings.Join(strings.Fields(items[i].SQL), " ")
		line := mark + truncate(sql, maxInt(10, width-24))

		suffix := "  " + items[i].Status
		if avail := width - displayWidth(line) - 1; avail > 4 {
			style := dimStyle
			if items[i].Failed {
				style = warnStyle
			}
			line += style.Render(truncate(suffix, avail))
		}
		out = append(out, line)
	}
	return out
}

// copyCurrent copies the cell or the row under the grid cursor.
func (m Model) copyCurrent(wholeRow bool) (tea.Model, tea.Cmd) {
	if m.tab != tabResult || m.grid.empty() {
		m.status = "nothing to copy"
		return m, nil
	}

	var text, what string
	if wholeRow {
		text = m.grid.currentRowTSV()
		what = fmt.Sprintf("row %d (%d columns)", m.grid.curRow+1, len(m.grid.res.Columns))
	} else {
		text = m.grid.currentCell()
		if text == "" {
			text = "NULL"
		}
		what = "cell"
	}

	if err := copyToClipboard(text); err != nil {
		m.status = "copy failed: " + err.Error()
		return m, nil
	}
	m.status = "copied " + what
	return m, nil
}

// helpActions drives the key map screen, so a rebound key is shown as rebound.
var helpActions = []struct {
	action config.Action
	text   string
}{
	{config.ActionRun, "run the selection, or the statement under the cursor"},
	{config.ActionComplete, "complete a keyword, relation, column or alias"},
	{config.ActionHistory, "query history"},
	{config.ActionEditStatement, "make an executed statement editable again"},
	{config.ActionDeleteStatement, "delete the statement under the cursor"},
	{config.ActionHelp, "this help"},
	{config.ActionSaveProfile, "save the current connection as a profile"},
	{config.ActionConnect, "back to the connection picker"},
	{config.ActionRefresh, "reload the selected tree node"},
	{config.ActionFocus, "focus next: tree, result, editor"},
	{config.ActionFocusBack, "focus previous"},
	{config.ActionPanes, "next pane: RESULT, OBJECT, MESSAGES"},
	{config.ActionPanesBack, "previous pane"},
	{config.ActionLeftWider, "wider object tree"},
	{config.ActionLeftNarrower, "narrower object tree"},
	{config.ActionResultTaller, "taller result pane"},
	{config.ActionResultShorter, "shorter result pane"},
	{config.ActionCopyCell, "copy the cell under the cursor"},
	{config.ActionCopyRow, "copy the whole row as TSV"},
	{config.ActionSelectTemplate, "table or view: insert a SELECT template, the way / works in less"},
	{config.ActionPeek, "table or view: run SELECT *, limited to two result screens"},
	{config.ActionInsertName, "insert the selected name where the editor cursor was left"},
	{config.ActionEditObject, "table, view or role: put an editable definition in the editor"},
	{config.ActionCreateTemplate, "CREATE skeleton for the selected group or object"},
	{config.ActionDropTemplate, "DROP skeleton for the selected object"},
	{config.ActionAlterTemplate, "ALTER skeleton for the selected object"},
	{config.ActionCancel, "cancel the running statement"},
	{config.ActionQuit, "quit"},
}

const helpFixed = `  Tab / Enter     accept a completion
  Esc             close a popup
  Esc, Esc        remove the template that was just inserted (before any edit)
  Shift+arrows    select text in the editor
  Ctrl+A          select everything in the editor
  arrows          tree navigation or grid movement
  Right           expand a tree node
  Left            collapse a tree node
  Enter / Space   database: switch the session
                  schema: default search_path and completion scope
                  role: SET ROLE for this session
                  tablespace: default_tablespace for this session
                  table or view: open the object card
                  pressing again on a default clears it
  Home            first row; pressed again it folds one level up
  End             last row
  PgUp / PgDn     page through results, cards and messages

A statement that already ran stays in the buffer, dimmed and marked in the
gutter: ✓ when it succeeded, ✗ when it failed. The cursor cannot move inside it
— only onto its first character — and its text cannot be edited until the edit
key above unlocks it. New statements go to the end on a fresh line.

In the object tree the object keys read like this: the select key turns the
object into a query, the peek key runs a two-screen SELECT *, and the edit key
puts the object's own definition into the editor — ALTER TABLE for a table,
CREATE OR REPLACE for a view, ALTER ROLE plus commented grants for a role.
Nothing is applied until the run key.

The insert key pastes the selected name into the editor where its cursor was
left and keeps the focus in the tree, adding a space after the name — so a
column list or a join can be collected row by row. The middle mouse button does
the same when mouse reporting is on (mouse = true in the config). Arrows, Enter,
Home/End, PgUp/PgDn and Esc stay fixed: they are context navigation, not
commands.`

func (m Model) viewHelp() string {
	var b strings.Builder
	b.WriteString(titleStyle.Render(m.appName() + " " + m.opts.Version))
	b.WriteString("\n\n")
	if m.isMongo() {
		b.WriteString("MongoDB workbench: one JSON envelope or a restricted MQL command per run.\n")
		b.WriteString("The tree, collection cards, completion and MQL templates are available;\n")
		b.WriteString("arbitrary JavaScript is out of scope.\n\n")
	}
	b.WriteString("KEYS\n\n")
	for _, entry := range helpActions {
		fmt.Fprintf(&b, "  %-15s %s\n", m.keys.Get(entry.action), entry.text)
	}
	b.WriteString("\n")
	b.WriteString(helpFixed)
	b.WriteString("\n\n")
	b.WriteString(dimStyle.Render(m.keys.Get(config.ActionHelp) + " or Esc closes this screen"))
	return m.frame(b.String())
}

// newProfileID is random enough for a local state file.
func newProfileID() string {
	buf := make([]byte, 8)
	if _, err := rand.Read(buf); err != nil {
		return fmt.Sprintf("%d", time.Now().UnixNano())
	}
	return hex.EncodeToString(buf)
}

// saveProfile stores the current connection and selects it.
func (m *Model) saveProfile(label string) {
	label = strings.TrimSpace(label)
	if label == "" {
		label = m.connectionDisplay()
	}

	profile := config.Profile{
		ID:              newProfileID(),
		Label:           label,
		Engine:          "mongo",
		URI:             m.mongoURI,
		ReadOnlyDefault: m.readOnly,
	}
	m.opts.State.Upsert(profile)
	if err := m.opts.State.Save(m.opts.Paths.StateFile); err != nil {
		m.status = "profile not saved: " + err.Error()
		return
	}
	m.profileID = profile.ID
	m.rebuildProfiles()
	m.status = "profile saved as " + label
}

// toggleFavorite flips the favorite flag of a profile.
func (m *Model) toggleFavorite(id string) {
	profile, ok := m.opts.State.Find(id)
	if !ok {
		return
	}
	profile.Favorite = !profile.Favorite
	m.opts.State.Upsert(profile)
	if err := m.opts.State.Save(m.opts.Paths.StateFile); err != nil {
		m.status = "favourite not saved: " + err.Error()
		return
	}
	m.rebuildProfiles()
	if profile.Favorite {
		m.status = "pinned " + profile.Label
	} else {
		m.status = "unpinned " + profile.Label
	}
}

// deleteProfile removes a profile from the state file.
func (m *Model) deleteProfile(id string) {
	profile, ok := m.opts.State.Find(id)
	if !ok {
		return
	}
	m.opts.State.Remove(id)
	if err := m.opts.State.Save(m.opts.Paths.StateFile); err != nil {
		m.status = "profile not deleted: " + err.Error()
		return
	}
	if m.profileID == id {
		m.profileID = ""
	}
	m.rebuildProfiles()
	m.status = "deleted " + profile.Label
}

// rebuildProfiles refreshes the picker list after a state change.
func (m *Model) rebuildProfiles() {
	m.profiles = m.profiles[:0]
	for _, p := range m.opts.State.Sorted() {
		m.profiles = append(m.profiles, p)
	}
	m.profiles = append(m.profiles, config.Profile{Label: "New connection…"})
	m.cursor = clampInt(m.cursor, 0, len(m.profiles)-1)
}
