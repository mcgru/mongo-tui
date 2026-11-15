package tui

import (
	"os"
	"path/filepath"
	"slices"
	"testing"

	"mongo-tui/internal/engine"
)

func newImportTestModel(t *testing.T) (Model, *fakeMongoCatalog) {
	t.Helper()
	fake := &fakeMongoCatalog{fakeSession: &fakeSession{info: engine.Info{ServerVersion: "8.0.38", Database: "app"}}}
	return mongoModel(t, fake), fake
}

func TestTreeImportKeyOnUsersOpensUserImport(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	if err := os.WriteFile(filepath.Join(home, ".mongorc.js"),
		[]byte(`connect("mongodb://ann:secret@db.example/app")`), 0o600); err != nil {
		t.Fatal(err)
	}

	m, _ := newImportTestModel(t)
	m.focus = focusTree
	m.tree.cursor = 2 // root, Databases, Users, Roles

	updated, _ := m.handleTreeKey("i")
	got := updated.(Model)
	if got.scr != screenImport || !got.importUser {
		t.Fatalf("scr=%v importUser=%v, want the import screen in user mode", got.scr, got.importUser)
	}
	if len(got.importFiles) != 1 || filepath.Base(got.importFiles[0]) != ".mongorc.js" {
		t.Fatalf("import files = %v", got.importFiles)
	}
}

func TestTreePlusOnUsersOpensUserImport(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	if err := os.WriteFile(filepath.Join(home, ".mongorc.js"),
		[]byte(`connect("mongodb://ann:secret@db.example/app")`), 0o600); err != nil {
		t.Fatal(err)
	}

	m, _ := newImportTestModel(t)
	m.focus = focusTree
	m.tree.cursor = 2 // Users folder

	updated, _ := m.handleTreeKey("+")
	got := updated.(Model)
	if got.scr != screenImport || !got.importUser {
		t.Fatalf("scr=%v importUser=%v, want user import via +", got.scr, got.importUser)
	}
}

func TestTreeImportKeyOutsideUsersIsNoop(t *testing.T) {
	m, _ := newImportTestModel(t)
	m.focus = focusTree
	m.tree.cursor = 0 // root, not Users

	updated, _ := m.handleTreeKey("i")
	if got := updated.(Model); got.scr != screenMain {
		t.Fatalf("scr = %v, want main (no-op outside Users)", got.scr)
	}
}

func TestImportUserFileSingleURIOpensRolePicker(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, ".mongorc.js")
	if err := os.WriteFile(path,
		[]byte(`connect("mongodb://ann:secret@db.example:27017/app?authSource=admin")`), 0o600); err != nil {
		t.Fatal(err)
	}
	m, _ := newImportTestModel(t)

	m, cmd := m.importUserFile(path)
	if cmd != nil {
		t.Fatal("no command should be issued before choosing roles")
	}
	if m.scr != screenImportRoles || m.importURI == "" {
		t.Fatalf("scr=%v uri=%q, want the role picker holding a URI", m.scr, m.importURI)
	}
	// readWrite (index 1) is checked, the rest are not.
	if !m.importRoleSel[1] || m.importRoleSel[0] || m.importRoleSel[2] {
		t.Fatalf("role selection = %v, want only readWrite checked", m.importRoleSel)
	}
}

func TestImportRolesEnterCreatesWithDefaultReadWrite(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, ".mongorc.js")
	if err := os.WriteFile(path,
		[]byte(`connect("mongodb://ann:secret@db.example:27017/app?authSource=admin")`), 0o600); err != nil {
		t.Fatal(err)
	}
	m, fake := newImportTestModel(t)

	m, _ = m.importUserFile(path) // role picker, readWrite checked
	updated, cmd := m.handleImportRolesKey("enter")
	m = updated.(Model)
	if cmd == nil {
		t.Fatal("no upsert command issued")
	}
	msg := cmd().(userUpsertMsg)
	if msg.err != nil || msg.username != "ann" {
		t.Fatalf("msg = %+v, want username ann", msg)
	}
	u := fake.upsertUser
	if u.username != "ann" || u.password != "secret" || u.database != "app" || u.authSource != "admin" {
		t.Fatalf("upsert fields = %+v, want ann/secret/app/admin", u)
	}
	if !slices.Equal(u.roles, []string{"readWrite"}) {
		t.Fatalf("upsert roles = %v, want [readWrite]", u.roles)
	}

	if m.scr != screenMain {
		t.Fatalf("scr = %v, want main", m.scr)
	}
	updated2, revealCmd := m.Update(msg)
	if revealCmd == nil {
		t.Fatal("no reveal command after a successful import")
	}
	if got := updated2.(Model); got.status != "user ann created" {
		t.Fatalf("status = %q", got.status)
	}
}

func TestImportRolesToggleSelectsMultiple(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, ".mongorc.js")
	if err := os.WriteFile(path,
		[]byte(`connect("mongodb://ann:secret@app")`), 0o600); err != nil {
		t.Fatal(err)
	}
	m, fake := newImportTestModel(t)

	m, _ = m.importUserFile(path) // cursor 0 = read, readWrite checked
	// Toggle read (cursor 0) on, so read + readWrite are selected.
	toggled, _ := m.handleImportRolesKey(" ")
	m = toggled.(Model)
	_, cmd := m.handleImportRolesKey("enter")
	cmd()
	if got := fake.upsertUser.roles; !slices.Equal(got, []string{"read", "readWrite"}) {
		t.Fatalf("upsert roles = %v, want [read readWrite] in list order", got)
	}
}

func TestImportRolesNoneCheckedShowsError(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, ".mongorc.js")
	if err := os.WriteFile(path, []byte(`connect("mongodb://ann:secret@app")`), 0o600); err != nil {
		t.Fatal(err)
	}
	m, _ := newImportTestModel(t)

	m, _ = m.importUserFile(path)
	moved, _ := m.handleImportRolesKey("down") // cursor 1 = readWrite
	m = moved.(Model)
	toggled, _ := m.handleImportRolesKey(" ") // uncheck readWrite
	m = toggled.(Model)

	updated, cmd := m.handleImportRolesKey("enter")
	m = updated.(Model)
	if cmd != nil {
		t.Fatal("no command expected for an empty selection")
	}
	if m.scr != screenImportRoles || m.status != "select at least one role" {
		t.Fatalf("scr=%v status=%q, want to stay on the picker with an error", m.scr, m.status)
	}
}

func TestImportURIsEnterOpensRolePicker(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, ".mongo_history")
	if err := os.WriteFile(path, []byte(
		"mongodb://ann:pw@host1/app\nmongodb://bob:pw@host2/stage\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	m, _ := newImportTestModel(t)

	m, _ = m.importUserFile(path)
	if m.scr != screenImportURIs || len(m.importURIs) != 2 {
		t.Fatalf("scr=%v uris=%v, want the URI chooser with 2 entries", m.scr, m.importURIs)
	}
	updated, cmd := m.handleImportURIsKey("enter")
	if cmd != nil {
		t.Fatal("no direct upsert expected before roles")
	}
	m = updated.(Model)
	if m.scr != screenImportRoles || m.importURI == "" {
		t.Fatalf("scr=%v uri=%q, want the role picker after choosing a URI", m.scr, m.importURI)
	}

	// Esc from the role picker returns to the URI chooser (multiple URIs).
	back, _ := m.handleImportRolesKey("esc")
	if got := back.(Model); got.scr != screenImportURIs {
		t.Fatalf("scr = %v, want back at the URI chooser", got.scr)
	}
}

func TestCreateUserFromURIUsesRolesCurrentDatabaseAndAdminAuthSource(t *testing.T) {
	m, fake := newImportTestModel(t)

	_, cmd := m.createUserFromURI("mongodb://bob:pw@host:27017/", []string{"dbOwner"})
	if cmd == nil {
		t.Fatal("no upsert command issued")
	}
	cmd()
	u := fake.upsertUser
	if u.username != "bob" || u.password != "pw" || u.database != "app" || u.authSource != "admin" {
		t.Fatalf("upsert fields = %+v, want bob/pw/app/admin", u)
	}
	if !slices.Equal(u.roles, []string{"dbOwner"}) {
		t.Fatalf("upsert roles = %v, want [dbOwner]", u.roles)
	}
}
