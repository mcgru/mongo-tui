// Package tui is the terminal front end: connection picker, SQL workbench and
// the main window.
package tui

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/charmbracelet/bubbles/spinner"
	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"

	"mongo-tui/internal/cli"
	"mongo-tui/internal/config"
	"mongo-tui/internal/engine"
	"mongo-tui/internal/mongo"
	mongoquery "mongo-tui/internal/mongo/query"
)

const connectTimeout = 25 * time.Second

// Executor is the part of an engine session the UI needs. It is an alias so
// tests can provide a fake session.
type Executor = engine.Session

// Options is everything the TUI needs, prepared by main.
type Options struct {
	CLI     cli.Options
	Config  config.Config
	State   config.State
	Paths   config.Paths
	Version string
}

type screen int

const (
	screenPicker screen = iota
	screenImport
	screenImportURIs
	screenImportRoles
	screenInput
	screenPassword
	screenConnecting
	screenMain
	screenError
	screenSaveProfile
	screenNewDatabase
)

type focus int

const (
	focusEditor focus = iota
	focusGrid
	focusTree
)

type paneTab int

const (
	tabResult paneTab = iota
	tabObject
	tabMessages
)

type connectedMsg struct {
	sess      Executor
	mongoURI  string
	profileID string
	at        time.Time
}

type connectErrMsg struct {
	err       error
	mongoURI  string
	profileID string
	// password is what was already tried; a second prompt would loop forever.
	password string
}

type execDoneMsg struct {
	statement  string
	statements int
	marks      map[string]execMark
	result     engine.Result
	duration   time.Duration
	tx         engine.TxState
	err        error
	// keepCursor means the statement was put in the buffer as already
	// executed, so the cursor stays at cursorAt instead of a fresh line.
	keepCursor bool
	cursorAt   int
	// A successful write can change the current database's collection list.
	// refreshDB is that database and refreshName is the newly created collection
	// (empty for drops/renames), so the tree reloads without a re-login.
	refreshDB, refreshName string
}

type cancelDoneMsg struct {
	err error
}

type switchedMsg struct {
	database string
	err      error
}

type userUpsertMsg struct {
	username string
	err      error
}

type mongoTreePlace struct {
	path       []string
	openObject string
}

type loadNodeMsg struct {
	node     *treeNode
	children []*treeNode
	err      error
}

type mongoCardMsg struct {
	card engine.CollectionCard
	err  error
}

type userCardMsg struct {
	card      engine.UserCard
	databases []string
	err       error
}

// userRolesMsg carries the freshly read card after UpdateUserRoles.
// closeEditor tells the handler to dismiss the modal; the tree's one-step
// grant leaves it false so the result opens in the editor.
type userRolesMsg struct {
	card        engine.UserCard
	databases   []string
	err         error
	notice      string
	closeEditor bool
}

// Model is the bubbletea model for the whole application.
type Model struct {
	opts   Options
	scr    screen
	width  int
	height int

	profiles []config.Profile
	cursor   int
	// importFiles/importCursor drive the ~/.mongo* import picker. importUser
	// marks it as importing a user (from the tree) instead of profiles (from the
	// picker); importURIs is the second level: the URIs found in the chosen file
	// when there is more than one to pick from. importURI/importRoleSel are the
	// third level: the chosen URI awaiting a role selection (Space toggles,
	// Enter applies).
	importFiles      []string
	importURIs       []string
	importURI        string
	importRoleSel    []bool
	importCursor     int
	importRoleCursor int
	importUser       bool

	mongoURI  string
	profileID string
	readOnly  bool
	// leftPct and resultPct size the panes; Ctrl+arrows change them and the
	// result is remembered in state.json. layoutSeq drops stale save ticks.
	leftPct   int
	resultPct int
	layoutSeq int
	// pendingCursor is the tree row to select once its branch has loaded, used
	// after a database switch rebuilt the tree.
	pendingCursor string
	// pendingPath/pendingOpen/pendingWaiting/pendingFinal restore a tree
	// position and an open card across a database switch.
	pendingPath    []string
	pendingOpen    string
	pendingWaiting string
	pendingFinal   string
	dbStates       map[string]mongoTreePlace
	// openObject is "database.collection" of the card displayed.
	openObject string
	// scopeCollection narrows completion to the collection opened from the tree.
	scopeCollection string
	// pendingDatabase is the name of a staged database the session switched to
	// with "+" but that MongoDB has not created yet (no collection with a
	// document). It shows faded pink and clears once listDatabases reports it.
	pendingDatabase string
	// discardPending is the first of the two Esc presses that remove a template.
	discardPending bool

	keys config.Keymap
	sess Executor
	// mongoSnap is the MongoDB completion metadata.
	mongoSnap *mongoSnapshot
	comp      compState
	spin      spinner.Model
	input     textinput.Model
	// searchOpen shows the "/" prompt in the footer; searchInput edits the
	// pattern and searchErr keeps a regexp error visible while typing.
	searchOpen       bool
	searchInput      textinput.Model
	searchErr        string
	searchIgnoreCase bool
	// keyboardLayout is "us" or "ru" for command-key matching; text widgets
	// always receive the original rune.
	keyboardLayout string
	editor         editor
	grid           *grid
	tree           *tree
	// mongoCard is the MongoDB collection card.
	mongoCard *engine.CollectionCard
	// roles is the modal role editor for one user; nil when it is closed.
	roles   *roleEditor
	cardOff int
	msgOff  int
	colOff  int
	focus   focus
	tab     paneTab
	tx      engine.TxState
	running bool

	history       []historyEntry
	hist          histState
	help          bool
	confirmDelete string

	messages   []string
	status     string
	err        error
	pendingURI string
}

// New builds the initial model. It decides the first screen from the parsed
// command line; Init starts the connection when one was requested.
func New(opts Options) Model {
	sp := spinner.New()
	sp.Spinner = spinner.Dot

	editor := newEditor()

	keys, err := opts.Config.Keymap()
	if err != nil {
		keys = config.DefaultKeymap()
	}

	m := Model{
		opts:           opts,
		keys:           keys,
		scr:            screenPicker,
		spin:           sp,
		editor:         editor,
		focus:          focusEditor,
		tx:             engine.TxIdle,
		readOnly:       opts.Config.ReadOnly || opts.CLI.ReadOnly,
		leftPct:        defaultLeftPct,
		resultPct:      defaultResultPct,
		keyboardLayout: resolveKeyboardLayout(opts.State.KeyboardLayout),
	}
	m.applyLayout(opts.State.Layout)

	for _, p := range opts.State.Sorted() {
		m.profiles = append(m.profiles, p)
	}
	m.profiles = append(m.profiles, config.Profile{Label: "New connection…"})

	switch {
	case opts.CLI.Profile != "":
		if p, ok := opts.State.Find(opts.CLI.Profile); ok {
			uri, supported := profileURI(p)
			if !supported {
				m.err = fmt.Errorf("profile %q is a legacy PostgreSQL profile; MongoDB support replaced it in M6", p.Label)
				m.scr = screenError
				break
			}
			m.mongoURI = uri
			m.profileID = p.ID
			m.readOnly = m.readOnly || p.ReadOnlyDefault
			m.scr = screenConnecting
		} else {
			m.err = fmt.Errorf("profile %q not found in %s", opts.CLI.Profile, opts.Paths.StateFile)
			m.scr = screenError
		}
	case opts.CLI.MongoURI != "":
		m.mongoURI = opts.CLI.MongoURI
		m.scr = screenConnecting
	}
	m.pendingURI = m.mongoURI
	return m
}

// Init starts the first connection when the command line asked for one.
func (m Model) Init() tea.Cmd {
	if m.scr == screenConnecting {
		return tea.Batch(m.spin.Tick, m.connectCmd(m.connectPassword()))
	}
	return nil
}

// Update handles messages.
func (m Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width, m.height = msg.Width, msg.Height
		m.resize()
		return m, nil

	case layoutSaveMsg:
		if msg.seq != m.layoutSeq {
			return m, nil
		}
		if err := m.opts.State.Save(m.opts.Paths.StateFile); err != nil {
			m.status = "layout not saved: " + err.Error()
		}
		return m, nil

	case spinner.TickMsg:
		if m.scr != screenConnecting {
			return m, nil
		}
		var cmd tea.Cmd
		m.spin, cmd = m.spin.Update(msg)
		return m, cmd

	case connectedMsg:
		m.sess = msg.sess
		m.mongoURI = msg.mongoURI
		m.profileID = msg.profileID
		m.scr = screenMain
		m.err = nil
		m.focus = focusTree
		m.grid = nil
		m.mongoCard = nil
		m.closeRoleEditor()
		m.openObject = ""
		m.scopeCollection = ""
		m.messages = nil
		m.tab = tabResult
		m.mongoSnap = nil
		m.tx = engine.TxIdle
		m.pendingDatabase = ""
		m.cardOff = 0
		m.msgOff = 0
		m.colOff = 0
		m.dbStates = nil
		m.pendingPath = nil
		m.pendingOpen = ""
		m.pendingWaiting = ""
		m.pendingFinal = ""
		m.applyLayout(m.layoutForProfile(msg.profileID))
		m.keyboardLayout = m.keyboardLayoutForProfile(msg.profileID)
		m.buildTree()
		m.status = m.rememberConnection(msg)
		m.editor.ClearMarks()
		m.resize()
		cmds := []tea.Cmd{m.editor.Focus()}
		if cmd := m.expandMongoDatabases(); cmd != nil {
			cmds = append(cmds, cmd)
		}
		if cmd := m.loadMongoSnapshotCmd(); cmd != nil {
			cmds = append(cmds, cmd)
		}
		return m, tea.Batch(cmds...)

	case connectErrMsg:
		m.mongoURI = msg.mongoURI
		m.pendingURI = msg.mongoURI
		m.pendingURI = msg.mongoURI
		if msg.password == "" && needsPassword(msg.err) && mongoHasUser(m.mongoURI) {
			m.scr = screenPassword
			m.input = newPasswordInput()
			return m, m.input.Focus()
		}
		m.err = msg.err
		m.scr = screenError
		return m, nil

	case execDoneMsg:
		m.running = false
		if msg.err != nil {
			m.history = m.rememberStatement(msg.statement, msg.err.Error(), true)
			m.messages = []string{msg.err.Error()}
			m.tab = tabMessages
			m.status = "error"
			return m, nil
		}
		if len(msg.marks) > 0 {
			m.editor.AddMarks(msg.marks)
		}
		if msg.keepCursor {
			// The statement was inserted as already executed: the cursor is
			// pinned to its first character, not to a new line.
			m.editor.SetCursorOffset(msg.cursorAt)
		} else {
			// The statement just ran, so leave the cursor on a fresh line below it.
			m.editor.AfterRun()
		}
		status := describeResult(msg.result, msg.duration)
		if msg.statements > 1 {
			status += fmt.Sprintf(" · %d statements", msg.statements)
		}
		m.history = m.rememberStatement(msg.statement, status, msg.result.Error)
		m.tx = msg.tx

		m.messages = msg.result.Messages
		if !msg.result.Error && len(msg.result.Columns) == 0 && len(m.messages) == 0 {
			m.messages = []string{fmt.Sprintf("ok, %d ms", msg.duration.Milliseconds())}
		}

		m.grid = newGrid(&msg.result)
		if m.isMongo() {
			// A query result takes over the OBJECT pane; the collection card is
			// one Enter away in the tree.
			m.mongoCard = nil
			m.closeRoleEditor()
			m.openObject = ""
		}

		// Show the output where it actually is: rows in RESULT, anything else in
		// MESSAGES, so a CREATE does not look like a no-op.
		m.colOff = 0
		switch {
		case msg.result.Error:
			m.tab = tabMessages
		case len(msg.result.Columns) > 0:
			m.tab = tabResult
		case len(m.messages) > 0:
			m.tab = tabMessages
		default:
			m.tab = tabResult
		}

		m.status = status
		// A write can materialize a staged database or create a collection; reload
		// the affected tree branch so it appears without a re-login.
		if msg.refreshDB != "" || m.pendingDatabase != "" {
			db := msg.refreshDB
			if db == "" {
				db = m.currentDatabase()
			}
			restored, cmd := m.revealAfterWrite(db, msg.refreshName)
			return restored, cmd
		}
		return m, nil

	case userUpsertMsg:
		if msg.err != nil {
			m.status = "user " + msg.username + " failed"
			m.messages = []string{msg.err.Error()}
			m.tab = tabMessages
			return m, nil
		}
		m.status = "user " + msg.username + " created"
		restored, cmd := m.revealUser(msg.username)
		return restored, cmd

	case switchedMsg:
		if msg.err != nil {
			m.messages = []string{msg.err.Error()}
			m.tab = tabMessages
			m.status = "switch failed"
			return m, nil
		}
		m.grid = nil
		m.mongoCard = nil
		m.closeRoleEditor()
		m.openObject = ""
		m.scopeCollection = ""
		m.tab = tabResult
		m.editor.ClearMarks()
		m.buildTree()
		m.mongoSnap = nil
		place, ok := m.dbStates[strings.ToLower(msg.database)]
		m.pendingPath = place.path
		m.pendingOpen = place.openObject
		m.pendingWaiting = ""
		m.pendingFinal = ""
		if !ok || len(m.pendingPath) == 0 {
			m.pendingPath = []string{"Databases", msg.database}
			m.pendingOpen = ""
		}
		m.status = "connected to " + msg.database
		restored, cmd := m.continuePath()
		m = restored.(Model)
		cmds := []tea.Cmd{m.loadMongoSnapshotCmd()}
		if cmd != nil {
			cmds = append(cmds, cmd)
		}
		return m, tea.Batch(cmds...)

	case cancelDoneMsg:
		if msg.err != nil {
			m.messages = append(m.messages, "cancel failed: "+msg.err.Error())
			m.tab = tabMessages
		} else {
			m.status = "cancel requested"
		}
		return m, nil

	case loadNodeMsg:
		msg.node.loading = false
		if msg.err != nil {
			msg.node.err = msg.err.Error()
			msg.node.expanded = false
		} else {
			msg.node.children = msg.children
			msg.node.loaded = true
			msg.node.expanded = true
			msg.node.err = ""
		}
		// The connected database must stay navigable even if listing failed.
		m.reconcileDatabases(msg.node)
		m.tree.rebuild()

		// Advance the restore chain only when the load it asked for finished;
		// unrelated branches load in parallel and must not consume it.
		if m.pendingWaiting == msg.node.title && m.pendingWaiting != "" {
			m.pendingWaiting = ""
			if m.pendingFinal == msg.node.title {
				finished, cmd := m.finishPath(msg.node)
				m = finished.(Model)
				return m, cmd
			}
			if len(m.pendingPath) > 0 {
				advanced, cmd := m.continuePath()
				m = advanced.(Model)
				if cmd != nil {
					return m, cmd
				}
			}
		}

		if m.pendingCursor != "" {
			// The row can only appear once its branch has loaded.
			if m.treeSelect(m.pendingCursor) || msg.node.title == "Databases" {
				m.pendingCursor = ""
			}
		}
		return m, nil

	case mongoSnapshotMsg:
		if msg.err != nil {
			m.status = "MongoDB completion metadata unavailable: " + msg.err.Error()
			return m, nil
		}
		m.mongoSnap = msg.snap
		return m, nil

	case mongoFieldsMsg:
		if msg.err != nil {
			m.status = "MongoDB fields unavailable: " + msg.err.Error()
			return m, nil
		}
		if m.mongoSnap == nil {
			m.mongoSnap = &mongoSnapshot{
				collections: map[string][]string{},
				fields:      map[string][]string{},
			}
		}
		m.mongoSnap.fields[msg.database+"."+msg.collection] = msg.fields
		return m.startMongoCompletion()

	case mongoCardMsg:
		if msg.err != nil {
			m.messages = []string{msg.err.Error()}
			m.tab = tabMessages
			m.status = "collection card failed"
			return m, nil
		}
		card := msg.card
		m.mongoCard = &card
		if card.Exists {
			m.openObject = card.Database + "." + card.Collection
			m.scopeCollection = m.openObject
		} else {
			m.openObject = ""
			m.scopeCollection = ""
		}
		m.cardOff = 0
		m.colOff = 0
		m.tab = tabObject
		return m, nil

	case userCardMsg:
		if msg.err != nil {
			m.messages = []string{msg.err.Error()}
			m.tab = tabMessages
			m.status = "user card failed"
			return m, nil
		}
		m.openRoleEditor(msg.card, msg.databases)
		return m, nil

	case userRolesMsg:
		if msg.err != nil {
			m.messages = []string{msg.err.Error()}
			m.tab = tabMessages
			m.status = "roles not saved"
			if m.roles != nil {
				m.roles.err = msg.err.Error()
			}
			return m, nil
		}
		if msg.closeEditor {
			m.closeRoleEditor()
		} else {
			m.openRoleEditor(msg.card, msg.databases)
		}
		if msg.notice != "" {
			m.status = msg.notice
		} else {
			m.status = "roles saved for " + msg.card.Username
		}
		return m, nil

	case tea.MouseMsg:
		return m.handleMouse(msg)

	case tea.KeyMsg:
		return m.handleKey(msg)
	}

	if m.scr == screenInput || m.scr == screenPassword {
		var cmd tea.Cmd
		m.input, cmd = m.input.Update(msg)
		return m, cmd
	}
	return m, nil
}

func (m Model) handleKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	// Command keys are matched after layout normalisation; text widgets below
	// still receive the original msg.
	key := m.commandKey(msg.String())

	if m.keys.Is(config.ActionQuit, key) {
		m.saveLayoutNow()
		return m, tea.Quit
	}
	if m.keys.Is(config.ActionCancel, key) {
		if m.scr == screenMain && m.running {
			return m, m.cancelCmd()
		}
		m.saveLayoutNow()
		return m, tea.Quit
	}
	if m.keys.Is(config.ActionKeyboardLayout, key) {
		return m.toggleKeyboardLayout()
	}

	if m.confirmDelete != "" {
		switch key {
		case "y":
			id := m.confirmDelete
			m.confirmDelete = ""
			m.deleteProfile(id)
		case "n", "esc":
			m.confirmDelete = ""
		}
		return m, nil
	}

	if m.help {
		if key == "esc" || key == "q" || key == "f1" || m.keys.Is(config.ActionHelp, key) {
			m.help = false
		}
		return m, nil
	}

	if m.scr == screenMain && m.hist.open {
		model, cmd, handled := m.handleHistoryKey(key)
		m = model
		if handled {
			return m, cmd
		}
	}

	if m.scr == screenMain && m.comp.open {
		model, cmd, handled := m.handleCompletionKey(key)
		m = model
		if handled {
			return m, cmd
		}
	}

	switch m.scr {
	case screenPicker:
		return m.handlePickerKey(key)

	case screenImport:
		return m.handleImportKey(key)

	case screenImportURIs:
		return m.handleImportURIsKey(key)

	case screenImportRoles:
		return m.handleImportRolesKey(key)

	case screenInput:
		switch key {
		case "esc":
			m.scr = screenPicker
			return m, nil
		case "enter":
			return m.connectFromInput()
		}
		return m.updateInput(msg)

	case screenPassword:
		switch key {
		case "esc":
			m.scr = screenPicker
			return m, nil
		case "enter":
			password := m.input.Value()
			m.scr = screenConnecting
			return m, tea.Batch(m.spin.Tick, m.connectCmd(password))
		}
		return m.updateInput(msg)

	case screenSaveProfile:
		switch key {
		case "esc":
			m.scr = screenMain
			return m, nil
		case "enter":
			label := m.input.Value()
			m.scr = screenMain
			m.saveProfile(label)
			return m, nil
		}
		return m.updateInput(msg)

	case screenNewDatabase:
		switch key {
		case "esc":
			m.scr = screenMain
			return m, nil
		case "enter":
			name := m.input.Value()
			return m.createPendingDatabase(name)
		}
		return m.updateInput(msg)

	case screenMain:
		return m.handleMainKey(msg, key)

	case screenError:
		switch key {
		case "r":
			m.mongoURI = m.pendingURI
			m.err = nil
			m.scr = screenConnecting
			return m, tea.Batch(m.spin.Tick, m.connectCmd(m.connectPassword()))
		case "esc", "q":
			m.err = nil
			m.scr = screenPicker
			return m, nil
		}
		return m, nil
	}
	return m, nil
}

func (m Model) handlePickerKey(key string) (tea.Model, tea.Cmd) {
	switch key {
	case "up", "k":
		if m.cursor > 0 {
			m.cursor--
		}
	case "down", "j":
		if m.cursor < len(m.profiles)-1 {
			m.cursor++
		}
	case "enter":
		chosen := m.profiles[m.cursor]
		if chosen.ID == "" {
			m.scr = screenInput
			m.input = newConnInput()
			return m, m.input.Focus()
		}
		uri, supported := profileURI(chosen)
		if !supported {
			m.err = fmt.Errorf("profile %q is a legacy PostgreSQL profile; MongoDB support replaced it in M6", chosen.Label)
			m.scr = screenError
			return m, nil
		}
		m.mongoURI = uri
		m.profileID = chosen.ID
		m.readOnly = m.opts.Config.ReadOnly || m.opts.CLI.ReadOnly || chosen.ReadOnlyDefault
		m.pendingURI = m.mongoURI
		m.scr = screenConnecting
		return m, tea.Batch(m.spin.Tick, m.connectCmd(m.connectPassword()))
	case "d":
		if chosen := m.profiles[m.cursor]; chosen.ID != "" {
			m.confirmDelete = chosen.ID
		}
	case "f":
		if chosen := m.profiles[m.cursor]; chosen.ID != "" {
			m.toggleFavorite(chosen.ID)
		}
	case m.keys.Get(config.ActionImportProfiles):
		return m.openImportFiles(false)
	case "q":
		m.saveLayoutNow()
		return m, tea.Quit
	}
	return m, nil
}

func (m Model) connectFromInput() (tea.Model, tea.Cmd) {
	text := strings.TrimSpace(m.input.Value())
	if !looksLikeMongoURI(text) {
		m.err = errors.New("enter a MongoDB URI, for example mongodb://host:27017/db")
		m.scr = screenError
		return m, nil
	}
	m.mongoURI = text
	m.profileID = ""
	m.pendingURI = m.mongoURI
	m.scr = screenConnecting
	return m, tea.Batch(m.spin.Tick, m.connectCmd(""))
}

func (m Model) handleMainKey(msg tea.KeyMsg, key string) (tea.Model, tea.Cmd) {
	// The role editor is modal: it owns every key while it is open.
	if m.roles != nil {
		return m.handleRoleEditorKey(key)
	}
	// The "/" prompt is modal for the same reason.
	if m.searchOpen {
		return m.handleSearchInput(msg)
	}

	// Esc removes an untouched template in two presses; any other key cancels
	// the pending second press.
	if m.focus == focusEditor && key == "esc" && !m.editor.HasSelection() && m.editor.AutoTemplatePending() {
		if m.discardPending && m.editor.DiscardAutoTemplate() {
			m.discardPending = false
			m.status = "inserted template removed"
			return m, nil
		}
		m.discardPending = true
		m.status = "Esc again removes the inserted template"
		return m, nil
	}
	m.discardPending = false

	switch {
	case m.keys.Is(config.ActionRun, key) || key == "ctrl+enter" || key == "alt+enter":
		return m.startRun()

	case m.keys.Is(config.ActionComplete, key) || key == "ctrl+@":
		return m.startCompletion()

	case m.keys.Is(config.ActionHelp, key) || key == "f1":
		m.help = true
		return m, nil

	case m.keys.Is(config.ActionEditStatement, key):
		return m.unlockStatement()

	case m.keys.Is(config.ActionDeleteStatement, key):
		return m.deleteStatement()

	case m.keys.Is(config.ActionHistory, key):
		return m.openHistory()

	case m.keys.Is(config.ActionSaveProfile, key):
		m.scr = screenSaveProfile
		m.input = newProfileLabelInput(m.connectionDisplay())
		return m, m.input.Focus()

	case m.keys.Is(config.ActionConnect, key):
		m.closeSession()
		m.scr = screenPicker
		return m, nil

	case m.keys.Is(config.ActionFocus, key) || key == "f6":
		return m.focusNext()

	case m.keys.Is(config.ActionFocusBack, key):
		return m.focusPrev()

	// "[" and "]" are the only pane switchers, and never while the editor has
	// focus — there [ and ] must type JSON arrays and pipelines.
	case m.focus != focusEditor && m.keys.Is(config.ActionPanes, key):
		m.tab = nextPane(m.tab)
		m.colOff = 0
		return m, nil

	case m.focus != focusEditor && m.keys.Is(config.ActionPanesBack, key):
		m.tab = prevPane(m.tab)
		m.colOff = 0
		return m, nil

	case m.keys.Is(config.ActionLeftWider, key):
		m.leftPct = clampInt(m.leftPct+resizeStep, minLeftPct, maxLeftPct)
		m.resize()
		cmd := m.rememberLayout()
		return m, cmd

	case m.keys.Is(config.ActionLeftNarrower, key):
		m.leftPct = clampInt(m.leftPct-resizeStep, minLeftPct, maxLeftPct)
		m.resize()
		cmd := m.rememberLayout()
		return m, cmd

	case m.keys.Is(config.ActionResultTaller, key):
		m.resultPct = clampInt(m.resultPct+resizeStep, minResultPct, maxResultPct)
		m.resize()
		cmd := m.rememberLayout()
		return m, cmd

	case m.keys.Is(config.ActionResultShorter, key):
		m.resultPct = clampInt(m.resultPct-resizeStep, minResultPct, maxResultPct)
		m.resize()
		cmd := m.rememberLayout()
		return m, cmd
	}

	switch m.focus {
	case focusEditor:
		var cmd tea.Cmd
		m.editor, cmd = m.editor.Update(msg)
		if m.editor.TakeLockedNotice() {
			m.status = "that statement already ran: " +
				m.keys.Get(config.ActionEditStatement) + " makes it editable again"
		}
		return m, cmd

	case focusTree:
		return m.handleTreeKey(key)
	}

	// In the result grid "/" starts a regexp search in the cursor column;
	// n / N walk the matches and Esc clears the search.
	if m.focus == focusGrid {
		switch {
		case m.keys.Is(config.ActionSelectTemplate, key):
			return m.openGridSearch()
		case m.tab == tabResult && key == "n":
			return m.jumpGridSearch(true)
		case m.tab == tabResult && key == "N":
			return m.jumpGridSearch(false)
		case key == "i":
			return m.toggleSearchCase()
		}
	}
	if m.focus == focusGrid && key == "esc" && m.grid != nil && m.grid.search != nil {
		return m.clearGridSearch()
	}

	if m.keys.Is(config.ActionCopyCell, key) {
		return m.copyCurrent(false)
	}
	if m.keys.Is(config.ActionCopyRow, key) {
		return m.copyCurrent(true)
	}

	switch key {
	case "up", "k":
		m.scrollPane(-1)
	case "down", "j":
		m.scrollPane(1)
	case "left", "shift+left", "h":
		m.scrollPaneX(-1)
	case "right", "shift+right", "l":
		m.scrollPaneX(1)
	case "pgup":
		m.scrollPane(-m.resultPage())
	case "pgdown":
		m.scrollPane(m.resultPage())
	case "home":
		m.scrollPane(-1 << 20)
	case "end":
		m.scrollPane(1 << 20)

	}
	return m, nil
}

// handleMouse turns the middle button into the insert key: the tree row is
// pasted into the editor without moving the focus.
func (m Model) handleMouse(msg tea.MouseMsg) (tea.Model, tea.Cmd) {
	if m.roles != nil || m.scr != screenMain || m.tree == nil {
		return m, nil
	}
	if msg.Action != tea.MouseActionPress || msg.Button != tea.MouseButtonMiddle {
		return m, nil
	}
	return m.insertName(m.tree.current()), nil
}

// insertSelectTemplate puts a ready SELECT for the selected relation into the
// editor, so browsing turns into a query without retyping names.
func (m Model) insertSelectTemplate(node *treeNode) Model {
	if node.kind != nodeCollection {
		m.status = "select templates are for collections"
		return m
	}
	if !m.mongoNodeInCurrentDB(node) {
		m.status = "switch to " + node.schema + " first"
		return m
	}
	return m.insertStatement(fmt.Sprintf("%s.find({}).limit(100)", mongoCollectionRef(node.name)))
}

// starSelectLimit is twice the visible height of the result pane: peeking
// shows about two screens of rows, which is enough to recognise the data
// without dragging a whole table into the terminal.
func (m Model) peekLimit() int {
	_, _, resultH, _ := m.layout()
	rows := resultH - 4
	if rows < 5 {
		rows = 5
	}
	return rows * 2
}

// runPeekSelect puts a limited SELECT * for the relation into the editor and
// runs it at once. The statement lands in the buffer already marked as
// executed, so all that is left for the user is to read the result.
func (m Model) runPeekSelect(node *treeNode) (tea.Model, tea.Cmd) {
	if node.kind != nodeCollection {
		m.status = "peek is for collections"
		return m, nil
	}
	if !m.mongoNodeInCurrentDB(node) {
		m.status = "switch to " + node.schema + " first"
		return m, nil
	}
	if m.sess == nil || m.running {
		return m, nil
	}
	statement := fmt.Sprintf("%s.find({}).limit(%d)", mongoCollectionRef(node.name), m.peekLimit())
	start := m.editor.AppendStatement(statement)
	m.running = true
	m.status = "running " + node.name
	return m, m.execCmd([]string{statement}, start)
}

// insertDdl puts a MongoDB template for the node into the editor.
func (m Model) insertDdl(node *treeNode, op ddlOperation) Model {
	return m.insertMongoTemplate(node, op)
}

// insertStatement appends a skeleton to the editor and moves the focus there.
func (m Model) insertStatement(statement string) Model {
	return m.insertStatementAfter(statement, "")
}

// insertStatementAfter appends a skeleton and puts the cursor right after the
// first occurrence of needle, so the suggested name can be typed over.
func (m Model) insertStatementAfter(statement, needle string) Model {
	// Terminate the statement so several appended templates stay separate
	// requests: the splitter (and the cursor-line run) cut on top-level ";",
	// otherwise two adjacent lines would glue into one malformed request.
	if trimmed := strings.TrimRight(statement, " \t\r\n"); trimmed != "" && !strings.HasSuffix(trimmed, ";") {
		statement = trimmed + ";"
	}
	// A template is new work, so it goes to the end of the buffer on its own
	// line instead of landing in the middle of whatever the cursor was on.
	start := m.editor.AppendStatement(statement)
	if needle != "" {
		if idx := strings.Index(statement, needle); idx >= 0 {
			m.editor.SetCursorOffset(start + utf8.RuneCountInString(statement[:idx+len(needle)]))
		}
	}
	m.focus = focusEditor
	m.editor.Focus()
	m.status = "template inserted"
	return m
}

// insertName drops the selected collection name into the editor as a JSON
// string and keeps the focus in the tree.
func (m Model) insertName(node *treeNode) Model {
	if node == nil || node.kind != nodeCollection {
		m.status = "nothing to insert for this row"
		return m
	}
	name := fmt.Sprintf("%q", node.name)
	m.editor.InsertString(name + " ")
	if m.editor.TakeLockedNotice() {
		m.status = "that statement already ran: " +
			m.keys.Get(config.ActionEditStatement) + " makes it editable again"
		return m
	}
	m.status = "inserted " + name
	return m
}

// deleteStatement removes the statement the cursor is in, unless it already ran.
func (m Model) deleteStatement() (tea.Model, tea.Cmd) {
	deleted, locked := m.editor.DeleteStatementAtCursor()
	switch {
	case locked:
		m.status = "that statement already ran: " +
			m.keys.Get(config.ActionEditStatement) + " makes it editable again"
	case !deleted:
		m.status = "no statement under the cursor"
	default:
		m.discardPending = false
		m.status = "statement removed"
	}
	return m, nil
}

// unlockStatement makes the executed statement under the cursor editable again.
func (m Model) unlockStatement() (tea.Model, tea.Cmd) {
	st, ok := m.editor.StatementAtLine(m.editor.Line())
	if !ok {
		m.status = "no statement here"
		return m, nil
	}
	if !m.editor.Unlock(st.Text) {
		m.status = "that statement is already editable"
		return m, nil
	}
	m.editor.MoveToStatement(st)
	m.status = "statement unlocked, edit and run it again"
	return m, nil
}

// rememberPlace stores the current tree position and card for this database.
func (m *Model) rememberPlace() {
	if m.tree == nil || m.tree.root == nil {
		return
	}
	database := m.currentDatabase()
	if database == "" {
		return
	}
	path := m.tree.pathTo(m.tree.current())
	if len(path) <= 2 {
		// A cursor on Databases or on a database row is not a position inside
		// the database; the default restore selects that database instead.
		path = nil
	}
	if m.dbStates == nil {
		m.dbStates = map[string]mongoTreePlace{}
	}
	m.dbStates[strings.ToLower(database)] = mongoTreePlace{
		path:       path,
		openObject: m.openObject,
	}
}

// continuePath walks pendingPath: each folder is expanded and loaded, the last
// title is selected and its card reopened. It stops when the next title is not
// among the children, which means the object is gone.
func (m Model) continuePath() (tea.Model, tea.Cmd) {
	if m.tree == nil || len(m.pendingPath) == 0 {
		return m, nil
	}

	title := m.pendingPath[0]
	node := m.treeChild(title)
	if node == nil {
		m.pendingPath = nil
		m.pendingOpen = ""
		m.pendingFinal = ""
		return m, nil
	}
	m.pendingPath = m.pendingPath[1:]
	last := len(m.pendingPath) == 0

	if node.load != nil && !node.loaded {
		node.expanded = true
		node.loading = true
		m.pendingWaiting = node.title
		if last {
			m.pendingFinal = node.title
		}
		return m, m.loadNodeCmd(node)
	}

	node.expanded = true
	m.tree.rebuild()
	if !last {
		return m.continuePath()
	}
	return m.finishPath(node)
}

func (m Model) finishPath(node *treeNode) (tea.Model, tea.Cmd) {
	if node == nil {
		return m, nil
	}
	m.treeSelect(node.title)
	m.pendingFinal = ""
	m.pendingPath = nil

	if m.pendingOpen != "" && node.openable && nodeKey(node) == m.pendingOpen {
		m.pendingOpen = ""
		return m, m.mongoCardCmd(node)
	}
	m.pendingOpen = ""
	return m, nil
}

// switchDatabase moves the session to another database of the same cluster.
func (m Model) switchDatabase(database string) (tea.Model, tea.Cmd) {
	if m.sess == nil {
		return m, nil
	}
	if strings.EqualFold(database, m.sess.Summary().Database) {
		m.status = "already connected to " + database
		return m, nil
	}
	// Moving to another (real) database abandons any staged one — it never
	// existed on the server, so it should not linger in the list.
	m.pendingDatabase = ""
	m.rememberPlace()
	m.status = "switching to " + database + "…"
	return m, m.switchCmd(database)
}

func (m Model) switchCmd(database string) tea.Cmd {
	sess := m.sess
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 25*time.Second)
		defer cancel()
		return switchedMsg{database: database, err: sess.SwitchDatabase(ctx, database)}
	}
}

// openNewDatabase asks for a name for the database to stage with "+" on Databases.
func (m Model) openNewDatabase() (tea.Model, tea.Cmd) {
	m.scr = screenNewDatabase
	m.input = newDatabaseNameInput()
	return m, m.input.Focus()
}

// createPendingDatabase stages a database the session switches to right away (a
// client-side "use"), which MongoDB materializes only once a document is written.
// The node stays faded pink until listDatabases reports the database as real.
// Choosing the database already connected to is allowed: it just reveals it.
func (m Model) createPendingDatabase(name string) (tea.Model, tea.Cmd) {
	m.scr = screenMain
	m.focus = focusTree
	name = strings.TrimSpace(name)
	if !validMongoDatabaseName(name) {
		m.status = "invalid database name"
		return m, nil
	}
	if m.sess == nil {
		return m, nil
	}
	if !strings.EqualFold(name, m.sess.Summary().Database) {
		if err := m.sess.SwitchDatabase(context.Background(), name); err != nil {
			m.status = err.Error()
			return m, nil
		}
	}
	m.pendingDatabase = name
	if m.tree != nil && m.tree.root != nil {
		m.tree.root.title = "MongoDB " + name
	}
	// Reuse the pending-cursor mechanism to land on the node after reload.
	m.pendingCursor = name
	m.status = "database " + name + " — a document creates it on the server"
	return m, m.reloadDatabasesCmd()
}

// reloadDatabasesCmd re-lists Databases so a staged database appears and resolves.
func (m Model) reloadDatabasesCmd() tea.Cmd {
	if m.tree == nil {
		return nil
	}
	folder := m.treeChild("Databases")
	if folder == nil {
		return nil
	}
	folder.loaded = false
	folder.children = nil
	folder.expanded = true
	folder.err = ""
	m.tree.rebuild()
	return m.loadNodeCmd(folder)
}

// reconcileDatabases keeps the connected database reachable in the tree even
// when the server does not list it: an empty database is absent from
// listDatabases, and a low-privilege user (e.g. readWrite on one db) cannot call
// listDatabases at all. Without a node there is no way down to Collections and
// the first collection cannot be written. We append the current database, faded
// pink until the server starts reporting it itself.
func (m *Model) reconcileDatabases(node *treeNode) {
	if node.title != "Databases" {
		return
	}
	name := m.currentDatabase()
	if name == "" {
		return
	}
	for _, child := range node.children {
		if child.kind == nodeDatabase && strings.EqualFold(child.title, name) {
			child.current = true
			child.pending = false
			m.pendingDatabase = ""
			return
		}
	}
	staged := &treeNode{
		kind:    nodeDatabase,
		title:   name,
		current: true,
		pending: true,
	}
	if cat, ok := m.sess.(engine.Catalog); ok {
		staged.load = mongoDatabaseLoader(cat, name)
	}
	node.children = append(node.children, staged)
	// Make it reachable even when listDatabases itself failed.
	node.loaded = true
	node.expanded = true
}

// validMongoDatabaseName rejects the characters MongoDB forbids in a database
// name (and empties); the server stays the final authority.
func validMongoDatabaseName(name string) bool {
	if name == "" {
		return false
	}
	return !strings.ContainsAny(name, ` ./"\*<>:|?$`)
}

// upsertUserCmd runs UpsertUser on the live session.
func (m Model) upsertUserCmd(username, password, database, authSource string, roles []string) tea.Cmd {
	sess := m.sess
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 25*time.Second)
		defer cancel()
		cat, ok := sess.(engine.Catalog)
		if !ok {
			return userUpsertMsg{username: username, err: errors.New("user management is not available")}
		}
		return userUpsertMsg{username: username, err: cat.UpsertUser(ctx, username, password, database, authSource, roles)}
	}
}

// revealUser reloads the Users folder and moves the cursor onto username, so
// the freshly imported user is visible without a manual reload.
func (m Model) revealUser(username string) (Model, tea.Cmd) {
	if m.tree == nil {
		return m, nil
	}
	node := m.treeChild("Users")
	if node == nil {
		return m, nil
	}
	node.loaded = false
	node.children = nil
	node.expanded = true
	node.err = ""
	m.pendingPath = []string{"Users", username}
	m.pendingOpen = ""
	m.pendingWaiting = ""
	m.pendingFinal = ""
	m.tree.rebuild()
	restored, cmd := m.continuePath()
	return restored.(Model), cmd
}

// revealAfterWrite reloads the tree branch a write changed so the new
// collection (or a database the write just materialized) shows without a
// re-login, then moves the cursor onto the collection when it is known.
func (m Model) revealAfterWrite(database, collection string) (Model, tea.Cmd) {
	if m.tree == nil || database == "" {
		return m, nil
	}
	// A provisional database that this write materialized must drop its pink
	// marker, so the whole Databases folder is re-listed before we walk into it.
	if m.pendingDatabase != "" {
		if folder := m.treeChild("Databases"); folder != nil {
			folder.loaded = false
			folder.children = nil
			folder.err = ""
		}
	}
	// The Collections folder likely caches the stale list; reset it so the walk
	// refetches it and the new collection appears.
	if db := m.findDatabaseNode(database); db != nil {
		if folder := findNamedChild(db, "Collections"); folder != nil {
			folder.loaded = false
			folder.children = nil
			folder.err = ""
		}
	}
	m.pendingPath = []string{"Databases", database, "Collections"}
	if collection != "" {
		m.pendingPath = append(m.pendingPath, collection)
	}
	m.pendingOpen = ""
	m.pendingWaiting = ""
	m.pendingFinal = ""
	m.tree.rebuild()
	restored, cmd := m.continuePath()
	return restored.(Model), cmd
}

// findDatabaseNode returns the loaded nodeDatabase for a database name, or nil
// when that branch has not been listed yet.
func (m Model) findDatabaseNode(database string) *treeNode {
	folder := m.treeChild("Databases")
	if folder == nil {
		return nil
	}
	for _, db := range folder.children {
		if db.kind == nodeDatabase && strings.EqualFold(db.title, database) {
			return db
		}
	}
	return nil
}

// findNamedChild returns a direct child with this title, or nil.
func findNamedChild(node *treeNode, title string) *treeNode {
	if node == nil {
		return nil
	}
	for _, child := range node.children {
		if child.title == title {
			return child
		}
	}
	return nil
}

// opChangesCollections reports whether a successful write can add or remove a
// collection in the current database.
func opChangesCollections(spec mongoquery.Spec) bool {
	switch spec.Op {
	case mongoquery.InsertOne, mongoquery.InsertMany, mongoquery.CreateCollection,
		mongoquery.DropCollection, mongoquery.RenameCollection:
		return true
	case mongoquery.UpdateOne, mongoquery.UpdateMany, mongoquery.ReplaceOne:
		return spec.Upsert
	}
	return false
}

// opCreatesCollection reports the ops that bring the named collection into
// existence, so the tree can navigate straight to it.
func opCreatesCollection(spec mongoquery.Spec) bool {
	switch spec.Op {
	case mongoquery.InsertOne, mongoquery.InsertMany, mongoquery.CreateCollection:
		return true
	case mongoquery.UpdateOne, mongoquery.UpdateMany, mongoquery.ReplaceOne:
		return spec.Upsert
	}
	return false
}

// treeSelect moves the cursor to the first visible row with this title.
func (m *Model) treeSelect(title string) bool {
	if m.tree == nil {
		return false
	}
	for i, node := range m.tree.visible {
		if node.title == title {
			m.tree.cursor = i
			return true
		}
	}
	return false
}

// treeChild finds a folder by title among the visible rows.
func (m Model) treeChild(title string) *treeNode {
	if m.tree == nil {
		return nil
	}
	for _, node := range m.tree.visible {
		if node.title == title {
			return node
		}
	}
	return nil
}

// currentTreeNode returns the selected tree row, or nil when there is no tree.
func (m Model) currentTreeNode() *treeNode {
	if m.tree == nil {
		return nil
	}
	return m.tree.current()
}

// focusNext moves focus clockwise: tree, result, editor.
func (m Model) focusNext() (tea.Model, tea.Cmd) {
	switch m.focus {
	case focusTree:
		m.focus = focusGrid
	case focusGrid:
		m.focus = focusEditor
		return m, m.editor.Focus()
	default:
		m.focus = focusTree
		m.editor.Blur()
	}
	return m, nil
}

// focusPrev moves focus the other way.
func (m Model) focusPrev() (tea.Model, tea.Cmd) {
	switch m.focus {
	case focusTree:
		m.focus = focusEditor
		return m, m.editor.Focus()
	case focusEditor:
		m.focus = focusGrid
		m.editor.Blur()
	default:
		m.focus = focusTree
		m.editor.Blur()
	}
	return m, nil
}

func nextPane(t paneTab) paneTab {
	switch t {
	case tabResult:
		return tabObject
	case tabObject:
		return tabMessages
	}
	return tabResult
}

func prevPane(t paneTab) paneTab {
	switch t {
	case tabMessages:
		return tabObject
	case tabObject:
		return tabResult
	}
	return tabMessages
}

// handleTreeKey drives the object browser.
func (m Model) handleTreeKey(key string) (tea.Model, tea.Cmd) {
	if m.tree == nil {
		return m, nil
	}

	// Commands come from the keymap; arrows, Enter and Esc below are fixed
	// context navigation and stay where they are in every pane.
	switch {
	case m.keys.Is(config.ActionSelectTemplate, key):
		if node := m.tree.current(); node != nil && node.openable {
			return m.insertSelectTemplate(node), nil
		}
		return m, nil
	case m.keys.Is(config.ActionPeek, key):
		if node := m.tree.current(); node != nil && node.openable {
			return m.runPeekSelect(node)
		}
		return m, nil
	case m.keys.Is(config.ActionInsertName, key):
		node := m.tree.current()
		if node == nil {
			return m, nil
		}
		return m.insertName(node), nil
	case m.keys.Is(config.ActionEditObject, key):
		m.status = "object editing is not available for MongoDB"
		return m, nil
	case m.keys.Is(config.ActionCreateTemplate, key):
		node := m.tree.current()
		// "+" on the Databases folder stages a new database; on the Users
		// folder it imports a user, the same as "i"; on a user it grants read
		// on the current database; anywhere else it is the
		// create-collection/create-index template.
		if node != nil && node.kind == nodeFolder {
			switch node.title {
			case "Databases":
				return m.openNewDatabase()
			case "Users":
				return m.openImportFiles(true)
			}
		}
		if node != nil && node.kind == nodeUser {
			return m.grantUserReadOnCurrentDatabase(node)
		}
		if node != nil {
			return m.insertDdl(node, ddlCreate), nil
		}
		return m, nil
	case m.keys.Is(config.ActionDropTemplate, key):
		if node := m.tree.current(); node != nil {
			return m.insertDdl(node, ddlDrop), nil
		}
		return m, nil
	case m.keys.Is(config.ActionAlterTemplate, key):
		if node := m.tree.current(); node != nil {
			return m.insertDdl(node, ddlAlter), nil
		}
		return m, nil
	case m.keys.Is(config.ActionImportProfiles, key):
		// On the Users folder the same import key creates a user from a file;
		// elsewhere in the tree it is a no-op.
		if node := m.tree.current(); node != nil && node.kind == nodeFolder && node.title == "Users" {
			return m.openImportFiles(true)
		}
		return m, nil
	}

	switch key {
	case "up", "k":
		m.tree.move(-1)
	case "down", "j":
		m.tree.move(1)
	case "home":
		if m.tree.home() {
			m.status = "folded all levels"
		}
	case "end":
		m.tree.end()
	case "pgup":
		m.tree.move(-m.resultPage())
	case "pgdown":
		m.tree.move(m.resultPage())
	case "left", "h":
		m.tree.collapse()
	case "right", "l":
		if m.isMongo() {
			if node := m.tree.current(); node != nil && node.kind == nodeUser {
				return m, m.userCardCmd(node)
			}
		}
		if node := m.tree.toggle(); node != nil {
			return m, m.loadNodeCmd(node)
		}
	case "enter", " ":
		node := m.tree.current()
		if m.isMongo() && node != nil {
			switch node.kind {
			case nodeCollection:
				return m.openNode()
			case nodeIndex:
				m.status = node.detail
				return m, nil
			case nodeUser:
				return m, m.userCardCmd(node)
			}
		}
		if node != nil && node.kind == nodeDatabase {
			return m.switchDatabase(node.title)
		}
		if node := m.tree.toggle(); node != nil {
			return m, m.loadNodeCmd(node)
		}
		return m.openNode()
	default:
		if m.keys.Is(config.ActionRefresh, key) {
			return m.refreshNode()
		}
	}
	return m, nil
}

// scrollPane moves whatever the right-hand pane currently shows.
func (m *Model) scrollPane(delta int) {
	switch m.tab {
	case tabResult:
		m.grid.move(delta, 0, m.gridHeight(), m.gridWidth())
	case tabObject:
		m.cardOff = maxInt(0, m.cardOff+delta)
	case tabMessages:
		m.msgOff = maxInt(0, m.msgOff+delta)
	}
}

// scrollPaneX slides the right-hand pane sideways: the grid by columns, the
// text panes by a horizontal cell offset.
func (m *Model) scrollPaneX(delta int) {
	switch m.tab {
	case tabResult:
		if m.grid != nil {
			m.grid.move(0, delta, m.gridHeight(), m.gridWidth())
		}
	case tabObject, tabMessages:
		m.colOff = maxInt(0, m.colOff+delta)
	}
}

func (m Model) loadNodeCmd(node *treeNode) tea.Cmd {
	if node == nil || node.load == nil {
		return nil
	}
	load := node.load
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
		defer cancel()
		children, err := load(ctx)
		return loadNodeMsg{node: node, children: children, err: err}
	}
}

// openNode shows the object card for the selected relation.
func (m Model) openNode() (tea.Model, tea.Cmd) {
	node := m.tree.current()
	if node == nil || !node.openable {
		return m, nil
	}
	if node.kind != nodeCollection {
		return m, nil
	}
	return m, m.mongoCardCmd(node)
}

// refreshNode reloads the children of the selected node.
func (m Model) refreshNode() (tea.Model, tea.Cmd) {
	node := m.tree.current()
	if node == nil || node.load == nil {
		return m, nil
	}
	node.loaded = false
	node.loading = true
	node.err = ""
	return m, m.loadNodeCmd(node)
}
func (m Model) mongoCardCmd(node *treeNode) tea.Cmd {
	cat, ok := m.sess.(engine.Catalog)
	if !ok {
		return func() tea.Msg {
			return mongoCardMsg{err: errors.New("this session does not expose a MongoDB catalog")}
		}
	}
	database, collection := node.schema, node.name
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 25*time.Second)
		defer cancel()
		card, err := cat.CollectionCard(ctx, database, collection)
		return mongoCardMsg{card: card, err: err}
	}
}

// userCardCmd reads one user's definition and the visible databases for the
// role editor.
func (m Model) userCardCmd(node *treeNode) tea.Cmd {
	cat, ok := m.sess.(engine.Catalog)
	if !ok {
		return func() tea.Msg {
			return userCardMsg{err: errors.New("this session does not expose a MongoDB catalog")}
		}
	}
	database, username := node.schema, node.name
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 25*time.Second)
		defer cancel()
		card, err := cat.UserCard(ctx, database, username)
		if err != nil {
			return userCardMsg{err: err}
		}
		return userCardMsg{card: card, databases: catalogDatabaseNames(ctx, cat)}
	}
}

// addUserReadRoleCmd grants read on roleDB to a user whose editor is not open,
// keeping the roles the server reports. It reads first because updateUser
// replaces the whole grant set.
func (m Model) addUserReadRoleCmd(homeDB, username, roleDB string) tea.Cmd {
	sess := m.sess
	return func() tea.Msg {
		cat, ok := sess.(engine.Catalog)
		if !ok {
			return userRolesMsg{err: errors.New("user management is not available")}
		}
		ctx, cancel := context.WithTimeout(context.Background(), 25*time.Second)
		defer cancel()
		card, err := cat.UserCard(ctx, homeDB, username)
		if err != nil {
			return userRolesMsg{err: err}
		}
		roles := append([]engine.RoleRef(nil), card.Roles...)
		granted := false
		for _, role := range roles {
			if role.DB == roleDB && role.Role == "read" {
				granted = true
				break
			}
		}
		notice := "granted read on " + roleDB
		if granted {
			notice = "read is already granted on " + roleDB
		} else {
			roles = append(roles, engine.RoleRef{Role: "read", DB: roleDB})
			if err := cat.UpdateUserRoles(ctx, homeDB, username, roles); err != nil {
				return userRolesMsg{err: err}
			}
		}
		fresh, err := cat.UserCard(ctx, homeDB, username)
		if err != nil {
			return userRolesMsg{err: err}
		}
		return userRolesMsg{
			card:      fresh,
			databases: catalogDatabaseNames(ctx, cat),
			notice:    notice,
		}
	}
}

// grantUserReadOnCurrentDatabase grants read on the current database to the
// selected user without opening the card first: "+" on a user is a one-step
// grant. The read keeps the roles the server already reports; the fresh card
// opens with the result.
func (m Model) grantUserReadOnCurrentDatabase(node *treeNode) (tea.Model, tea.Cmd) {
	if node.schema == "" || node.name == "" {
		m.status = "select a user first"
		return m, nil
	}
	database := m.currentDatabase()
	if database == "" {
		m.status = "no current database"
		return m, nil
	}
	return m, m.addUserReadRoleCmd(node.schema, node.name, database)
}

// resultPage is how many lines one page step moves.
func (m Model) resultPage() int {
	_, _, resultH, _ := m.layout()
	return maxInt(1, resultH-3)
}

// startRun validates the statement under the cursor and executes it.
func (m Model) startRun() (tea.Model, tea.Cmd) {
	if m.sess == nil {
		return m, nil
	}
	if m.running {
		m.status = "a statement is already running"
		return m, nil
	}

	statements, err := m.requestUnderCursor()
	if err != nil {
		m.messages = []string{err.Error()}
		m.tab = tabMessages
		m.status = "nothing to run"
		return m, nil
	}
	if !m.isMongo() {
		m.messages = []string{"only MongoDB is supported"}
		m.tab = tabMessages
		return m, nil
	}
	for _, statement := range statements {
		if _, _, err := mongoquery.ParseRequest(statement); err != nil {
			m.messages = []string{err.Error()}
			m.tab = tabMessages
			m.status = "not sent"
			return m, nil
		}
	}

	m.running = true
	if len(statements) > 1 {
		m.status = fmt.Sprintf("running %d statements…", len(statements))
	} else {
		m.status = "running…"
	}
	m.tab = tabResult
	return m, m.runCmd(statements)
}

// requestUnderCursor returns the requests to run: the selection when there is
// one, otherwise the single statement under the cursor — never the whole buffer,
// so already-executed (✓) statements are not re-run by accident. Statements are
// split on top-level separators and validated by the adapter.
func (m Model) requestUnderCursor() ([]string, error) {
	text := ""
	if m.editor.HasSelection() {
		text = m.editor.SelectedText()
	} else if st, ok := m.editor.StatementAtLine(m.editor.Line()); ok {
		text = st.Text
	}
	text = strings.TrimSpace(text)
	if text == "" {
		return nil, errors.New("no request under the cursor")
	}
	requests, err := mongoquery.SplitRequests(text)
	if err != nil {
		return nil, err
	}
	if len(requests) == 0 {
		return nil, errors.New("the editor is empty")
	}
	return requests, nil
}

// cursorOffset is the cursor position as a byte offset in the buffer.
func (m Model) cursorOffset() int {
	return m.editor.CursorOffset()
}

// runCmd executes the statements in order and stops at the first failure.
func (m Model) runCmd(statements []string) tea.Cmd {
	return m.execCmd(statements, -1)
}

// execCmd is runCmd with a fixed cursor offset: cursorAt >= 0 keeps the cursor
// on an already inserted statement instead of moving it to a new line.
func (m Model) execCmd(statements []string, cursorAt int) tea.Cmd {
	sess := m.sess
	return func() tea.Msg {
		start := time.Now()
		var last engine.Result
		marks := make(map[string]execMark, len(statements))
		var refreshDB, refreshName string

		for _, statement := range statements {
			res, err := sess.Execute(context.Background(), statement)
			if err != nil {
				marks[normalizeStatement(statement)] = markFailed
				return execDoneMsg{
					statement: statement, err: err, duration: time.Since(start),
					marks: marks, keepCursor: cursorAt >= 0, cursorAt: cursorAt,
				}
			}
			last = res
			if res.Error {
				marks[normalizeStatement(statement)] = markFailed
				break
			}
			marks[normalizeStatement(statement)] = markOK
			// A write that can change the collection list tells the tree to
			// reload; for a creation we also learn which collection to reveal.
			if spec, _, perr := mongoquery.ParseRequest(statement); perr == nil && opChangesCollections(spec) {
				db := spec.Database
				if db == "" {
					db = m.currentDatabase()
				}
				refreshDB, refreshName = db, ""
				if opCreatesCollection(spec) {
					refreshName = spec.Collection
				}
			}
		}

		txCtx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		return execDoneMsg{
			statement:   strings.Join(statements, ";\n"),
			statements:  len(statements),
			marks:       marks,
			result:      last,
			duration:    time.Since(start),
			tx:          sess.TxState(txCtx),
			keepCursor:  cursorAt >= 0,
			cursorAt:    cursorAt,
			refreshDB:   refreshDB,
			refreshName: refreshName,
		}
	}
}

func (m Model) cancelCmd() tea.Cmd {
	sess := m.sess
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		return cancelDoneMsg{err: sess.Cancel(ctx)}
	}
}

func (m Model) connectCmd(password string) tea.Cmd {
	mongoURI := m.mongoURI
	profileID, readOnly := m.profileID, m.readOnly
	opts := m.opts

	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), connectTimeout)
		defer cancel()

		sess, err := mongo.Connect(ctx, mongo.ConnectOptions{
			URI:        mongoURI,
			Password:   password,
			ReadOnly:   readOnly,
			MaxDocs:    opts.Config.ResultLimitRows,
			MaxBytes:   int(opts.Config.ResultLimitBytes),
			SampleSize: opts.Config.MongoDB.SampleSize,
		})
		if err != nil {
			return connectErrMsg{
				err: err, mongoURI: mongoURI,
				profileID: profileID, password: password,
			}
		}
		return connectedMsg{
			sess: sess, mongoURI: mongoURI,
			profileID: profileID, at: time.Now(),
		}
	}
}

// rememberConnection records the profile as used and returns a status note.
func (m Model) rememberConnection(msg connectedMsg) string {
	if m.opts.CLI.NoHistory || msg.profileID == "" {
		return "connected"
	}
	m.opts.State.MarkConnected(msg.profileID, msg.at)
	if err := m.opts.State.Save(m.opts.Paths.StateFile); err != nil {
		return "connected (history not saved: " + err.Error() + ")"
	}
	return "connected"
}

func (m *Model) closeSession() {
	m.saveLayoutNow()
	if m.sess != nil {
		m.sess.Close()
		m.sess = nil
	}
	m.mongoSnap = nil
	m.comp = compState{}
	m.mongoCard = nil
	m.closeRoleEditor()
	m.resetSearch()
	m.pendingDatabase = ""
}

func (m Model) updateInput(msg tea.Msg) (tea.Model, tea.Cmd) {
	var cmd tea.Cmd
	m.input, cmd = m.input.Update(msg)
	return m, cmd
}

// resize propagates the terminal size to the widgets.
func (m *Model) resize() {
	if m.width <= 0 || m.height <= 0 {
		return
	}
	_, rightW, _, editorH := m.layout()
	m.editor.SetWidth(maxInt(10, rightW-4))
	m.editor.SetHeight(maxInt(1, editorH-4))
	if m.searchOpen {
		m.searchInput.Width = maxInt(20, m.width-30)
	}
}

func (m Model) gridHeight() int {
	if m.grid.empty() {
		return 1
	}
	return maxInt(1, len(m.grid.res.Rows))
}

func (m Model) gridWidth() int {
	if m.grid.empty() {
		return 1
	}
	return maxInt(1, len(m.grid.res.Columns))
}

func profileURI(p config.Profile) (string, bool) {
	if p.Engine == "mongo" && p.URI != "" {
		return p.URI, true
	}
	return "", false
}

// connectPassword resolves the password for the selected profile from its
// source file, when it was imported from one. It returns "" when there is no
// profile, no source file or no matching URI; the empty value falls through to
// the normal password prompt.
func (m Model) connectPassword() string {
	if m.profileID == "" {
		return ""
	}
	p, ok := m.opts.State.Find(m.profileID)
	if !ok || p.SourceFile == "" {
		return ""
	}
	pw, _ := config.PasswordFromFile(p.SourceFile, p.URI)
	return pw
}

func needsPassword(err error) bool {
	if err == nil {
		return false
	}
	text := strings.ToLower(err.Error())
	return strings.Contains(text, "no password supplied") ||
		strings.Contains(text, "password authentication failed") ||
		strings.Contains(text, "fe_sendauth") ||
		strings.Contains(text, "authentication failed") ||
		strings.Contains(text, "requires authentication") ||
		strings.Contains(text, "scram")
}

func describeResult(res engine.Result, took time.Duration) string {
	parts := []string{
		fmt.Sprintf("%d rows", len(res.Rows)),
		fmt.Sprintf("%d ms", took.Milliseconds()),
	}
	if res.Truncated {
		parts = append(parts, "truncated")
	}
	if res.Cancelled {
		parts = append(parts, "cancelled")
	}
	if res.Error && res.Code != "" {
		parts = append(parts, "code "+res.Code)
	}
	if !res.Error && len(res.Columns) == 0 {
		parts = append(parts, fmt.Sprintf("%d affected", res.RowCount))
	}
	return strings.Join(parts, " · ")
}

func newConnInput() textinput.Model {
	ti := textinput.New()
	ti.Placeholder = "postgresql://user@host:5432/db"
	ti.CharLimit = 2048
	ti.Width = 64
	return ti
}

func newPasswordInput() textinput.Model {
	ti := textinput.New()
	ti.Placeholder = "password"
	ti.EchoMode = textinput.EchoPassword
	ti.EchoCharacter = '•'
	ti.CharLimit = 512
	ti.Width = 40
	return ti
}

func newDatabaseNameInput() textinput.Model {
	ti := textinput.New()
	ti.Placeholder = "database"
	ti.CharLimit = 128
	ti.Width = 40
	return ti
}

// Run starts the program and returns the process exit code.
func Run(opts Options) int {
	model := New(opts)
	programOpts := []tea.ProgramOption{tea.WithAltScreen()}
	if model.opts.Config.Mouse {
		// Cell motion still reports button presses, which is all the middle
		// button needs; all-motion would flood the model with move events.
		programOpts = append(programOpts, tea.WithMouseCellMotion())
	}
	program := tea.NewProgram(model, programOpts...)

	final, err := program.Run()
	if m, ok := final.(Model); ok {
		m.closeSession()
	}
	if err != nil {
		fmt.Fprintf(errWriter, "mongo-tui: %v\n", err)
		return 1
	}
	return 0
}
