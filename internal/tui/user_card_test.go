package tui

import (
	"strings"
	"testing"

	"mongo-tui/internal/engine"
)

func userCardFixture() *fakeMongoCatalog {
	return &fakeMongoCatalog{
		fakeSession: &fakeSession{info: engine.Info{ServerVersion: "8.0.38", Database: "app"}},
		userCard: engine.UserCard{
			Username:   "ann",
			Database:   "app",
			Exists:     true,
			Mechanisms: []string{"SCRAM-SHA-256"},
			Roles: []engine.RoleRef{
				{Role: "read", DB: "app"},
				{Role: "readWrite", DB: "analytics"},
			},
		},
	}
}

func openUserCard(t *testing.T) (Model, *fakeMongoCatalog) {
	t.Helper()
	fake := userCardFixture()
	m := mongoModel(t, fake)
	node := &treeNode{kind: nodeUser, schema: "app", name: "ann"}

	msg := m.userCardCmd(node)()
	cardMsg, ok := msg.(userCardMsg)
	if !ok {
		t.Fatalf("userCardCmd returned %T, want userCardMsg", msg)
	}
	updated, _ := m.Update(cardMsg)
	return updated.(Model), fake
}

func TestUserCardOpensWithEditableRoles(t *testing.T) {
	m, _ := openUserCard(t)
	if m.mongoUser == nil || m.tab != tabObject || m.mongoCard != nil {
		t.Fatalf("mongoUser=%v tab=%v mongoCard=%v", m.mongoUser != nil, m.tab, m.mongoCard != nil)
	}

	// Two current grants (checked) plus the addable builtin roles on "app"
	// (read is already granted there, so 4 builtin roles remain addable).
	if len(m.userRows) != 6 {
		t.Fatalf("userRows = %d, want 6: %+v", len(m.userRows), m.userRows)
	}
	if !m.userRows[0].checked || !m.userRows[0].current {
		t.Fatalf("row 0 = %+v, want current+checked", m.userRows[0])
	}
	if m.userRows[2].checked {
		t.Fatalf("addable row = %+v, want unchecked", m.userRows[2])
	}

	lines := strings.Join(m.cardLines(80, 40), "\n")
	if !strings.Contains(lines, "USER ann") || !strings.Contains(lines, "ROLES") ||
		!strings.Contains(lines, "[x] app:read") || !strings.Contains(lines, "(add)") {
		t.Fatalf("card lines:\n%s", lines)
	}
}

func TestUserCardRevokeAndAddThenSave(t *testing.T) {
	m, fake := openUserCard(t)

	// Revoke app:read (row 0).
	toggled, _ := m.handleUserCardKey(" ")
	m = toggled.(Model)
	// Move to the dbOwner@app addable row (index 3) and check it.
	for i := 0; i < 3; i++ {
		step, _ := m.handleUserCardKey("down")
		m = step.(Model)
	}
	added, _ := m.handleUserCardKey(" ")
	m = added.(Model)

	if !m.userDirty {
		t.Fatal("expected dirty after toggling roles")
	}
	saved := m.saveUserRolesCmd()()
	msg, ok := saved.(userRolesMsg)
	if !ok {
		t.Fatalf("save returned %T, want userRolesMsg", saved)
	}
	if msg.err != nil {
		t.Fatalf("save error = %v", msg.err)
	}

	// The adapter received the new set: analytics:readWrite kept, app:dbOwner
	// added, app:read removed.
	got := fake.updateRoles.roles
	if len(got) != 2 {
		t.Fatalf("UpdateUserRoles roles = %+v, want 2", got)
	}
	if got[0].Role != "readWrite" || got[0].DB != "analytics" {
		t.Fatalf("first grant = %+v, want analytics:readWrite", got[0])
	}
	if got[1].Role != "dbOwner" || got[1].DB != "app" {
		t.Fatalf("second grant = %+v, want app:dbOwner", got[1])
	}

	// Applying the message reloads the card and clears the dirty flag.
	updated, _ := m.Update(msg)
	m = updated.(Model)
	if m.userDirty {
		t.Fatal("dirty should clear after save")
	}
	if len(m.mongoUser.Roles) != 2 {
		t.Fatalf("reloaded roles = %+v, want 2", m.mongoUser.Roles)
	}
}

func TestUserCardEscCloses(t *testing.T) {
	m, _ := openUserCard(t)
	closed, _ := m.handleUserCardKey("esc")
	if got := closed.(Model); got.mongoUser != nil {
		t.Fatal("esc should close the user card")
	}
}
