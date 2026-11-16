package tui

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"mongo-tui/internal/engine"
)

// roleLevel is the staged role of one database row in the role editor. The
// order is the cycle order: plain Space walks empty/read/readWrite, while
// Ctrl+Space walks every builtin database role.
type roleLevel int

const (
	roleNone roleLevel = iota
	roleRead
	roleReadWrite
	roleDBOwner
	roleDBAdmin
	roleUserAdmin
)

var (
	roleShortCycle = []roleLevel{roleNone, roleRead, roleReadWrite}
	roleFullCycle  = []roleLevel{roleNone, roleRead, roleReadWrite, roleDBOwner, roleDBAdmin, roleUserAdmin}
)

func (l roleLevel) role() string {
	switch l {
	case roleRead:
		return "read"
	case roleReadWrite:
		return "readWrite"
	case roleDBOwner:
		return "dbOwner"
	case roleDBAdmin:
		return "dbAdmin"
	case roleUserAdmin:
		return "userAdmin"
	}
	return ""
}

func roleLevelFor(name string) roleLevel {
	switch name {
	case "read":
		return roleRead
	case "readWrite":
		return roleReadWrite
	case "dbOwner":
		return roleDBOwner
	case "dbAdmin":
		return roleDBAdmin
	case "userAdmin":
		return roleUserAdmin
	}
	return roleNone
}

// managedRole reports whether the role is one the editor can stage. Other
// grants (root, clusterAdmin, custom roles) are kept untouched.
func managedRole(name string) bool { return roleLevelFor(name) != roleNone }

// roleRow is one database in the role editor. level is what the user staged;
// current is what the server reported. A row with level == current is
// untouched and keeps its original grants, even if a database carries more
// than one managed role.
type roleRow struct {
	database string
	level    roleLevel
	current  roleLevel
}

// roleEditor is the modal role window. It replaces updateUser's whole grant
// set, so unmanaged roles are preserved separately and only builtin database
// roles are staged per database.
type roleEditor struct {
	user      engine.UserCard
	rows      []roleRow
	others    []engine.RoleRef
	currentDB string
	cursor    int
	err       string
}

// newRoleEditor lists every database the user may touch: the visible ones, the
// databases where they already have roles, their home database and the current
// one. The visible list can be empty when the session may not call
// listDatabases; the role databases still show up.
func newRoleEditor(card engine.UserCard, databases []string, currentDB string) *roleEditor {
	grants := make(map[string][]engine.RoleRef, len(card.Roles))
	for _, role := range card.Roles {
		grants[role.DB] = append(grants[role.DB], role)
	}

	seen := make(map[string]bool, len(databases)+len(card.Roles)+2)
	order := make([]string, 0, len(databases)+len(card.Roles)+2)
	add := func(db string) {
		if db == "" || seen[db] {
			return
		}
		seen[db] = true
		order = append(order, db)
	}
	for _, db := range databases {
		add(db)
	}
	for _, role := range card.Roles {
		add(role.DB)
	}
	add(card.Database)
	add(currentDB)

	rows := make([]roleRow, 0, len(order))
	others := make([]engine.RoleRef, 0, len(card.Roles))
	for _, db := range order {
		level := roleNone
		for _, role := range grants[db] {
			if !managedRole(role.Role) {
				others = append(others, role)
				continue
			}
			if lv := roleLevelFor(role.Role); lv > level {
				level = lv
			}
		}
		rows = append(rows, roleRow{database: db, level: level, current: level})
	}
	return &roleEditor{user: card, rows: rows, others: others, currentDB: currentDB}
}

// changes is how many database rows differ from what the server reported.
func (e *roleEditor) changes() int {
	n := 0
	for _, row := range e.rows {
		if row.level != row.current {
			n++
		}
	}
	return n
}

// cycle advances the highlighted row. The short cycle is empty/read/readWrite;
// the full one also visits the other builtin database roles. A row staged with
// a full-only role drops to empty on the next plain Space.
func (e *roleEditor) cycle(full bool) {
	if e.cursor < 0 || e.cursor >= len(e.rows) {
		return
	}
	row := &e.rows[e.cursor]
	cycle := roleShortCycle
	if full {
		cycle = roleFullCycle
	}
	for i, level := range cycle {
		if level == row.level {
			row.level = cycle[(i+1)%len(cycle)]
			return
		}
	}
	row.level = roleNone
}

// grants builds the updateUser role set: untouched databases keep every grant
// they had, changed databases drop their managed roles and take the staged one,
// and unmanaged roles stay as they are.
func (e *roleEditor) grants() []engine.RoleRef {
	changed := make(map[string]bool, len(e.rows))
	for _, row := range e.rows {
		if row.level != row.current {
			changed[row.database] = true
		}
	}

	out := make([]engine.RoleRef, 0, len(e.user.Roles)+len(e.rows))
	for _, role := range e.user.Roles {
		if changed[role.DB] && managedRole(role.Role) {
			continue
		}
		out = append(out, role)
	}
	for _, row := range e.rows {
		if changed[row.database] && row.level != roleNone {
			out = append(out, engine.RoleRef{Role: row.level.role(), DB: row.database})
		}
	}
	return out
}

// catalogDatabaseNames lists the databases the session can see. A session
// without listDatabases gets nothing; the role editor then falls back to the
// databases found in roles and the current one.
func catalogDatabaseNames(ctx context.Context, cat engine.Catalog) []string {
	items, err := cat.Databases(ctx)
	if err != nil {
		return nil
	}
	out := make([]string, 0, len(items))
	for _, item := range items {
		if item.Name != "" {
			out = append(out, item.Name)
		}
	}
	return out
}

// openRoleEditor installs the modal window and focuses it.
func (m *Model) openRoleEditor(card engine.UserCard, databases []string) {
	if !card.Exists {
		m.messages = []string{"user " + card.Username + " not found in " + card.Database}
		m.tab = tabMessages
		m.status = "user not found"
		return
	}
	current := m.currentDatabase()
	m.roles = newRoleEditor(card, databases, current)
	m.status = card.Username + " @ " + card.Database
}

// closeRoleEditor dismisses the modal without touching the server.
func (m *Model) closeRoleEditor() {
	m.roles = nil
}

// handleRoleEditorKey owns the keyboard while the modal is open: arrows move,
// Space cycles empty/read/readWrite, Ctrl+Space cycles every builtin database
// role, Enter applies the staged set, Esc discards it.
func (m Model) handleRoleEditorKey(key string) (tea.Model, tea.Cmd) {
	e := m.roles
	if e == nil {
		return m, nil
	}
	last := len(e.rows) - 1
	switch key {
	case "esc":
		m.closeRoleEditor()
		m.status = "role changes discarded"
		return m, nil
	case "enter":
		if e.changes() == 0 {
			m.closeRoleEditor()
			return m, nil
		}
		m.status = "saving roles…"
		return m, m.applyRoleEditorCmd()
	case "up", "k":
		if e.cursor > 0 {
			e.cursor--
		}
	case "down", "j":
		if e.cursor < last {
			e.cursor++
		}
	case "home":
		e.cursor = 0
	case "end":
		if last >= 0 {
			e.cursor = last
		}
	case "pgup":
		e.cursor = maxInt(0, e.cursor-rolePageStep)
	case "pgdown":
		e.cursor = minInt(last, e.cursor+rolePageStep)
	case " ":
		e.cycle(false)
	case "ctrl+space", "ctrl+@":
		e.cycle(true)
	}
	return m, nil
}

const rolePageStep = 10

// applyRoleEditorCmd writes the staged grants once, re-reads the card and
// refreshes the database list so the modal can close on fresh state.
func (m Model) applyRoleEditorCmd() tea.Cmd {
	e := m.roles
	if e == nil {
		return nil
	}
	user := e.user
	roles := e.grants()
	sess := m.sess
	return func() tea.Msg {
		cat, ok := sess.(engine.Catalog)
		if !ok {
			return userRolesMsg{err: errors.New("user management is not available"), closeEditor: true}
		}
		ctx, cancel := context.WithTimeout(context.Background(), 25*time.Second)
		defer cancel()
		if err := cat.UpdateUserRoles(ctx, user.Database, user.Username, roles); err != nil {
			return userRolesMsg{err: err, closeEditor: true}
		}
		fresh, err := cat.UserCard(ctx, user.Database, user.Username)
		if err != nil {
			return userRolesMsg{err: err, closeEditor: true}
		}
		return userRolesMsg{
			card:        fresh,
			databases:   catalogDatabaseNames(ctx, cat),
			notice:      "roles saved for " + user.Username,
			closeEditor: true,
		}
	}
}

// roleEditorLines renders the content of the modal. rows are windowed around
// the cursor so the tail (other roles and keys) always stays visible.
func (m Model) roleEditorLines(width, maxLines int) []string {
	e := m.roles
	if e == nil {
		return nil
	}

	tail := make([]string, 0, len(e.others)+4)
	if len(e.others) > 0 {
		tail = append(tail, "", dimStyle.Render("OTHER ROLES (kept as is)"))
		for _, role := range e.others {
			tail = append(tail, "  "+role.DB+":"+role.Role)
		}
	}
	changeText := "no changes"
	if n := e.changes(); n > 0 {
		changeText = fmt.Sprintf("%d change(s)", n)
	}
	tail = append(tail, "", changeText+"   Enter apply   Esc close")
	if e.err != "" {
		tail = append(tail, errorStyle.Render(e.err))
	}

	head := []string{
		dimStyle.Render("↑↓ database"),
		dimStyle.Render("Space: — → read → readWrite    Ctrl+Space: all roles"),
	}
	visible := maxLines - len(head) - len(tail)
	if visible < 1 {
		visible = 1
	}
	start := 0
	if e.cursor >= visible {
		start = e.cursor - visible + 1
	}
	end := minInt(len(e.rows), start+visible)

	lines := append([]string(nil), head...)
	for i := start; i < end; i++ {
		lines = append(lines, m.roleRowLine(e.rows[i], i == e.cursor))
	}
	lines = append(lines, tail...)
	for i := range lines {
		lines[i] = hclip(lines[i], 0, maxInt(1, width)) + "\x1b[0m"
	}
	return lines
}

// roleRowLine is one database: cursor, name, staged role and markers. The name
// is padded before styling so the coloured current database keeps its column.
func (m Model) roleRowLine(row roleRow, selected bool) string {
	e := m.roles
	cursor := "  "
	if selected {
		cursor = "› "
	}

	drawn := row.database
	if row.database == e.currentDB {
		drawn = dbBadge.Render(drawn)
	}
	pad := maxInt(1, 26-displayWidth(row.database))

	state := dimStyle.Render("—")
	switch row.level {
	case roleRead:
		state = "read"
	case roleReadWrite:
		state = titleStyle.Render("readWrite")
	case roleDBOwner, roleDBAdmin, roleUserAdmin:
		state = titleStyle.Render(row.level.role())
	}

	notes := ""
	if row.database == e.user.Database {
		notes += dimStyle.Render("  home")
	}
	if row.level != row.current {
		notes += warnStyle.Render("  changed")
	}
	return cursor + drawn + strings.Repeat(" ", pad) + state + notes
}

// roleEditorOverlay draws the focused role window centred over the main view.
func (m Model) roleEditorOverlay(base string) string {
	e := m.roles
	if e == nil {
		return base
	}
	width := clampInt(68, 24, maxInt(24, m.width-6))
	height := clampInt(11+len(e.rows)+len(e.others), 10, maxInt(10, m.height-4))
	title := "ROLES · " + e.user.Username + " @ " + e.user.Database
	box := m.pane(title, m.roleEditorLines(width-4, height-4), width, height, true)
	return overlayCenter(base, strings.Split(box, "\n"), m.width, m.height)
}

// overlayCenter draws the box over the base view, keeping the content around
// it. hclip is ANSI-aware, so pane colours survive the splicing.
func overlayCenter(base string, box []string, width, height int) string {
	lines := strings.Split(base, "\n")
	boxW := 0
	for _, line := range box {
		boxW = maxInt(boxW, displayWidth(line))
	}
	x := maxInt(0, (width-boxW)/2)
	y := maxInt(0, (height-len(box))/2)
	for i := range lines {
		if i < y || i >= y+len(box) || i >= height {
			continue
		}
		left := hclip(lines[i], 0, x)
		right := hclip(lines[i], x+boxW, maxInt(0, width-x-boxW))
		lines[i] = left + "\x1b[0m" + box[i-y] + "\x1b[0m" + right
	}
	return strings.Join(lines, "\n")
}
