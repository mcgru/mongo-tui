package tui

import (
	"errors"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"mongo-tui/internal/engine"
)

func roleFixture() *fakeMongoCatalog {
	return &fakeMongoCatalog{
		fakeSession: &fakeSession{info: engine.Info{ServerVersion: "8.0.38", Database: "app"}},
		databases: []engine.Item{
			{Name: "admin"},
			{Name: "app"},
			{Name: "analytics"},
			{Name: "shop"},
		},
		userCard: engine.UserCard{
			Username:   "ann",
			Database:   "app",
			Exists:     true,
			Mechanisms: []string{"SCRAM-SHA-256"},
			Roles: []engine.RoleRef{
				{Role: "read", DB: "app"},
				{Role: "readWrite", DB: "analytics"},
				{Role: "root", DB: "admin"},
			},
		},
	}
}

func openRoleEditor(t *testing.T, fake *fakeMongoCatalog) Model {
	t.Helper()
	m := mongoModel(t, fake)
	node := &treeNode{kind: nodeUser, schema: "app", name: "ann"}
	updated, _ := m.Update(m.userCardCmd(node)())
	return updated.(Model)
}

func roleRowFor(t *testing.T, m Model, database string) roleRow {
	t.Helper()
	if m.roles == nil {
		t.Fatal("role editor is not open")
	}
	for _, row := range m.roles.rows {
		if row.database == database {
			return row
		}
	}
	t.Fatalf("database %q not in rows: %+v", database, m.roles.rows)
	return roleRow{}
}

func selectDatabase(t *testing.T, m Model, database string) Model {
	t.Helper()
	if m.roles == nil {
		t.Fatal("role editor is not open")
	}
	for i, row := range m.roles.rows {
		if row.database == database {
			m.roles.cursor = i
			return m
		}
	}
	t.Fatalf("database %q not in rows", database)
	return m
}

func pressKey(t *testing.T, m Model, key tea.KeyType) Model {
	t.Helper()
	updated, _ := m.Update(tea.KeyMsg{Type: key})
	model, ok := updated.(Model)
	if !ok {
		t.Fatalf("Update returned %T, want Model", updated)
	}
	return model
}

func TestRoleEditorOpensWithDatabaseRows(t *testing.T) {
	m := openRoleEditor(t, roleFixture())
	if m.roles == nil {
		t.Fatal("Enter on a user should open the role editor")
	}
	if m.roles.user.Username != "ann" || m.roles.currentDB != "app" {
		t.Fatalf("editor user=%+v current=%q", m.roles.user, m.roles.currentDB)
	}

	wantOrder := []string{"admin", "app", "analytics", "shop"}
	if len(m.roles.rows) != len(wantOrder) {
		t.Fatalf("rows = %+v, want %v", m.roles.rows, wantOrder)
	}
	for i, db := range wantOrder {
		if m.roles.rows[i].database != db {
			t.Fatalf("row %d = %q, want %q (%+v)", i, m.roles.rows[i].database, db, m.roles.rows)
		}
	}
	if got := roleRowFor(t, m, "app").level; got != roleRead {
		t.Fatalf("app level = %v, want read", got)
	}
	if got := roleRowFor(t, m, "analytics").level; got != roleReadWrite {
		t.Fatalf("analytics level = %v, want readWrite", got)
	}
	if got := roleRowFor(t, m, "shop").level; got != roleNone {
		t.Fatalf("shop level = %v, want none", got)
	}

	// root is outside the read/readWrite model and must be preserved visibly.
	if len(m.roles.others) != 1 || m.roles.others[0].Role != "root" || m.roles.others[0].DB != "admin" {
		t.Fatalf("others = %+v, want admin:root", m.roles.others)
	}

	lines := strings.Join(m.roleEditorLines(60, 30), "\n")
	for _, want := range []string{"app", "analytics", "OTHER ROLES", "admin:root", "readWrite"} {
		if !strings.Contains(lines, want) {
			t.Fatalf("modal lines missing %q:\n%s", want, lines)
		}
	}
}

func TestRoleEditorSpaceCyclesShortList(t *testing.T) {
	m := openRoleEditor(t, roleFixture())
	m = selectDatabase(t, m, "shop")

	m = pressKey(t, m, tea.KeySpace)
	if got := roleRowFor(t, m, "shop").level; got != roleRead {
		t.Fatalf("after Space = %v, want read", got)
	}
	m = pressKey(t, m, tea.KeySpace)
	if got := roleRowFor(t, m, "shop").level; got != roleReadWrite {
		t.Fatalf("after second Space = %v, want readWrite", got)
	}
	m = pressKey(t, m, tea.KeySpace)
	if got := roleRowFor(t, m, "shop").level; got != roleNone {
		t.Fatalf("after third Space = %v, want none", got)
	}
}

func TestRoleEditorCtrlSpaceCyclesAllRoles(t *testing.T) {
	m := openRoleEditor(t, roleFixture())
	m = selectDatabase(t, m, "shop")

	want := []roleLevel{roleRead, roleReadWrite, roleDBOwner, roleDBAdmin, roleUserAdmin, roleNone}
	for i, level := range want {
		m = pressKey(t, m, tea.KeyCtrlAt)
		if got := roleRowFor(t, m, "shop").level; got != level {
			t.Fatalf("Ctrl+Space %d = %v, want %v", i+1, got, level)
		}
	}

	// A plain Space on a full-only role drops it to empty, then walks the
	// short cycle again.
	m = pressKey(t, m, tea.KeyCtrlAt) // empty -> read
	m = pressKey(t, m, tea.KeyCtrlAt) // read -> readWrite
	m = pressKey(t, m, tea.KeyCtrlAt) // readWrite -> dbOwner
	if got := roleRowFor(t, m, "shop").level; got != roleDBOwner {
		t.Fatalf("level = %v, want dbOwner", got)
	}
	m = pressKey(t, m, tea.KeySpace)
	if got := roleRowFor(t, m, "shop").level; got != roleNone {
		t.Fatalf("Space on dbOwner = %v, want none", got)
	}
	m = pressKey(t, m, tea.KeySpace)
	if got := roleRowFor(t, m, "shop").level; got != roleRead {
		t.Fatalf("Space after reset = %v, want read", got)
	}
}

func TestRoleEditorEnterAppliesAllChanges(t *testing.T) {
	m := openRoleEditor(t, roleFixture())
	m = selectDatabase(t, m, "shop")
	m = pressKey(t, m, tea.KeySpace) // shop -> read

	updated, cmd := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	m = updated.(Model)
	if cmd == nil {
		t.Fatal("Enter with changes should save")
	}
	msg, ok := cmd().(userRolesMsg)
	if !ok {
		t.Fatalf("save returned %T, want userRolesMsg", cmd())
	}
	if msg.err != nil {
		t.Fatalf("save error = %v", msg.err)
	}
	if !msg.closeEditor {
		t.Fatal("apply should close the editor")
	}
	if len(msg.card.Roles) == 0 {
		t.Fatal("the fresh card should carry the saved roles")
	}

	updated, _ = m.Update(msg)
	m = updated.(Model)
	if m.roles != nil {
		t.Fatal("the editor should close after a successful save")
	}
	if m.status != "roles saved for ann" {
		t.Fatalf("status = %q", m.status)
	}
}

func TestRoleEditorEnterSendsStagedSet(t *testing.T) {
	fake := roleFixture()
	m := openRoleEditor(t, fake)
	m = selectDatabase(t, m, "shop")
	m = pressKey(t, m, tea.KeySpace)

	_, cmd := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	if cmd == nil {
		t.Fatal("Enter with changes should save")
	}
	msg := cmd().(userRolesMsg)
	if msg.err != nil {
		t.Fatalf("save error = %v", msg.err)
	}

	want := []engine.RoleRef{
		{Role: "read", DB: "app"},
		{Role: "readWrite", DB: "analytics"},
		{Role: "root", DB: "admin"},
		{Role: "read", DB: "shop"},
	}
	got := fake.updateRoles.roles
	if len(got) != len(want) {
		t.Fatalf("roles = %+v, want %+v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("roles[%d] = %+v, want %+v (all: %+v)", i, got[i], want[i], got)
		}
	}
}

func TestRoleEditorEscDiscardsChanges(t *testing.T) {
	fake := roleFixture()
	m := openRoleEditor(t, fake)
	m = selectDatabase(t, m, "shop")
	m = pressKey(t, m, tea.KeySpace)

	updated, cmd := m.Update(tea.KeyMsg{Type: tea.KeyEsc})
	m = updated.(Model)
	if cmd != nil {
		t.Fatal("Esc should not write")
	}
	if m.roles != nil {
		t.Fatal("Esc should close the editor")
	}
	if fake.updateRoles.roles != nil {
		t.Fatalf("UpdateUserRoles called: %+v", fake.updateRoles.roles)
	}
}

func TestRoleEditorKeepsUntouchedMultipleGrants(t *testing.T) {
	fake := roleFixture()
	fake.userCard.Roles = []engine.RoleRef{
		{Role: "read", DB: "app"},
		{Role: "dbOwner", DB: "app"},
	}
	m := openRoleEditor(t, fake)
	// The row shows the highest managed role, but leaving it untouched must
	// keep both grants.
	if got := roleRowFor(t, m, "app").level; got != roleDBOwner {
		t.Fatalf("app level = %v, want dbOwner", got)
	}
	m = selectDatabase(t, m, "analytics")
	m = pressKey(t, m, tea.KeySpace) // analytics -> read

	_, cmd := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	if cmd == nil {
		t.Fatal("Enter with changes should save")
	}
	cmd()

	want := []engine.RoleRef{
		{Role: "read", DB: "app"},
		{Role: "dbOwner", DB: "app"},
		{Role: "read", DB: "analytics"},
	}
	got := fake.updateRoles.roles
	if len(got) != len(want) {
		t.Fatalf("roles = %+v, want %+v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("roles[%d] = %+v, want %+v (all: %+v)", i, got[i], want[i], got)
		}
	}
}

func TestRoleEditorFallsBackWithoutDatabaseList(t *testing.T) {
	fake := roleFixture()
	fake.databasesErr = errors.New("not authorized on admin to execute listDatabases")
	m := openRoleEditor(t, fake)

	got := []string{}
	for _, row := range m.roles.rows {
		got = append(got, row.database)
	}
	want := []string{"app", "analytics", "admin"}
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Fatalf("rows = %v, want %v", got, want)
	}
}

func TestTreePlusGrantsReadAndOpensEditor(t *testing.T) {
	fake := roleFixture()
	fake.userCard.Roles = []engine.RoleRef{{Role: "readWrite", DB: "analytics"}}
	m := mongoModel(t, fake)
	node := &treeNode{kind: nodeUser, schema: "app", name: "ann"}

	updated, cmd := m.grantUserReadOnCurrentDatabase(node)
	m = updated.(Model)
	if cmd == nil {
		t.Fatal("+ on a user should grant read")
	}
	msg, ok := cmd().(userRolesMsg)
	if !ok {
		t.Fatalf("grant returned %T, want userRolesMsg", cmd())
	}
	if msg.err != nil {
		t.Fatalf("grant error = %v", msg.err)
	}
	if msg.closeEditor {
		t.Fatal("the tree grant should open the editor, not close it")
	}

	got := fake.updateRoles.roles
	want := []engine.RoleRef{{Role: "readWrite", DB: "analytics"}, {Role: "read", DB: "app"}}
	if len(got) != len(want) || got[0] != want[0] || got[1] != want[1] {
		t.Fatalf("UpdateUserRoles roles = %+v, want %+v", got, want)
	}

	updated, _ = m.Update(msg)
	m = updated.(Model)
	if m.roles == nil {
		t.Fatal("the fresh role editor should open")
	}
	if got := roleRowFor(t, m, "app").level; got != roleRead {
		t.Fatalf("app level = %v, want read", got)
	}
}

func TestRoleEditorOverlaysFocusedWindow(t *testing.T) {
	m := openRoleEditor(t, roleFixture())
	view := m.View()
	if !strings.Contains(view, "ROLES · ann @ app") {
		t.Fatalf("view has no role window:\n%s", view)
	}
	if !strings.Contains(view, "OTHER ROLES (kept as is)") || !strings.Contains(view, "admin:root") {
		t.Fatalf("view does not preserve unmanaged roles:\n%s", view)
	}
}
