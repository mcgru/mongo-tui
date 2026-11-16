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
	{config.ActionComplete, "complete databases, collections, methods, fields or operators"},
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
	{config.ActionLoadMore, "RESULT: append the next page of a peeked query"},
	{config.ActionLoadAll, "RESULT: load a peeked query up to the row/byte limits"},
	{config.ActionServerSearch, "RESULT: find documents by the cursor cell value"},
	{config.ActionServerSearchRegex, "RESULT: regexp find by the cursor cell value"},
	{config.ActionKeyboardLayout, "toggle the command-key layout: us / ru"},
	{config.ActionSelectTemplate, "collection/view: find template; RESULT grid: regexp search"},
	{config.ActionPeek, "collection or view: run a paged find, 30 rows first (p or the key above Enter)"},
	{config.ActionInsertName, "insert the selected name where the editor cursor was left"},
	{config.ActionCreateTemplate, "database or Collections: new document; collection: createIndex; Users: import user"},
	{config.ActionDropTemplate, "drop the selected collection or index"},
	{config.ActionAlterTemplate, "collMod template for the selected collection"},
	{config.ActionCancel, "cancel the running statement"},
	{config.ActionQuit, "quit"},
}

const helpFixed = `  Tab / Enter     accept a completion
  Esc             close a popup or the role editor
  Esc, Esc        remove the template that was just inserted (before any edit)
  Shift+arrows    select text in the editor
  Ctrl+A          select everything in the editor
  arrows          tree navigation; in the result/object pane ← / → scroll wide
                  output sideways (use [ and ] to change panes)
  Right           expand a tree node
  Left            collapse a tree node
  Enter / Space   database: switch the session
                  collection or view: open the object card
                  index: show its keys
                  user: open the role editor
                  folder: expand it
  Home            first row; pressed again it folds one level up
  End             last row
  PgUp / PgDn     page through results, cards and messages
  Ctrl+←/→        resize the tree; Ctrl+↑/↓ move the result/editor divider
                  (sizes are saved for the profile or globally)

A statement that already ran stays in the buffer, dimmed and marked in the
gutter: ✓ when it succeeded, ✗ when it failed. The cursor cannot move inside it
— only onto its first character — and its text cannot be edited until the edit
key above unlocks it. New statements go to the end on a fresh line.

In the object tree the object keys read like this: the select key turns a
collection or view into a find, the peek key runs a limited find, create adds a
new-document insertOne on a database or its Collections folder (or a new index
on a collection), drop removes a collection or index, and alter inserts a
collMod. On the Users folder the import key adds a user from ~/.mongo*; on a
user the create key grants read on the current database. Nothing is sent until
the run key.

The role editor lists databases: Space cycles — / read / readWrite, Ctrl+Space
cycles every builtin database role, Enter applies the staged set, Esc cancels.
Roles outside that model stay under OTHER ROLES and are sent back untouched.

In the RESULT grid the select key starts a regexp search in the column under the
cursor: Enter applies it, n / N jump to the next / previous match (wrapping),
and Esc clears the highlight. Searches are ignore-case at startup (like less
-i); the i key toggles case sensitivity for future searches. Ctrl+I cannot be
used because terminals send it as Tab. q is an ordinary character in the
pattern, so it does not leave the search.

Peek shows the first 30 rows and keeps the server cursor open: the more key
appends the next page, the all key drains the cursor up to result_limit_rows
and result_limit_bytes. The search covers the rows loaded so far. A new query
or a database switch closes the cursor.

The server-filter keys turn the cell under the cursor into a find for the same
collection: the exact key inserts the value as a typed literal, the regexp key
wraps the visible text in an escaped regexp. The editor is cleared, the value
is inserted, and the cursor lands right after it. Null becomes null; a missing
field leaves an empty string to type into. The filter runs in the current
database, so a result from another database asks to switch first.

Cyrillic command keys always act as the US key on the same physical position
(з is p, т is n, ь is m, Ф is A, х is [), even when the stored layout is us or
the locale is wrong; text fields still type Cyrillic. With layout ru the
punctuation of the Russian layout also works (. is /), because in the US layout
. must stay a period. Ctrl+L toggles us/ru and saves the choice; when no layout
is stored, it is detected from the locale.

The insert key pastes the selected name into the editor where its cursor was
left and keeps the focus in the tree, adding a space after the name — so a
field list or a filter can be collected row by row. The middle mouse button does
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
		Layout:          &config.Layout{LeftPct: m.leftPct, ResultPct: m.resultPct},
		KeyboardLayout:  m.keyboardLayout,
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
