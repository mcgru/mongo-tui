package tui

import (
	"os"
	"path/filepath"
	"testing"

	"mongo-tui/internal/engine"
)

func TestTreeImportKeyOnUsersOpensUserImport(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	if err := os.WriteFile(filepath.Join(home, ".mongorc.js"),
		[]byte(`connect("mongodb://ann:secret@db.example/app")`), 0o600); err != nil {
		t.Fatal(err)
	}

	fake := &fakeMongoCatalog{fakeSession: &fakeSession{info: engine.Info{ServerVersion: "8.0.38", Database: "app"}}}
	m := mongoModel(t, fake)
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

func TestTreeImportKeyOutsideUsersIsNoop(t *testing.T) {
	fake := &fakeMongoCatalog{fakeSession: &fakeSession{info: engine.Info{ServerVersion: "8.0.38", Database: "app"}}}
	m := mongoModel(t, fake)
	m.focus = focusTree
	m.tree.cursor = 0 // root, not Users

	updated, _ := m.handleTreeKey("i")
	if got := updated.(Model); got.scr != screenMain {
		t.Fatalf("scr = %v, want main (no-op outside Users)", got.scr)
	}
}

func TestImportUserFileSingleURICreatesUser(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, ".mongorc.js")
	if err := os.WriteFile(path,
		[]byte(`connect("mongodb://ann:secret@db.example:27017/app?authSource=admin")`), 0o600); err != nil {
		t.Fatal(err)
	}

	fake := &fakeMongoCatalog{fakeSession: &fakeSession{info: engine.Info{ServerVersion: "8.0.38", Database: "app"}}}
	m := mongoModel(t, fake)

	m, cmd := m.importUserFile(path)
	if m.scr != screenMain {
		t.Fatalf("scr = %v, want main after a single-URI import", m.scr)
	}
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

	updated, revealCmd := m.Update(msg)
	if revealCmd == nil {
		t.Fatal("no reveal command after a successful import")
	}
	if got := updated.(Model); got.status != "user ann created" {
		t.Fatalf("status = %q", got.status)
	}
}

func TestImportUserFileMultipleURIsShowsChooser(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, ".mongo_history")
	if err := os.WriteFile(path, []byte(
		"mongodb://ann:pw@host1/app\nmongodb://bob:pw@host2/stage\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	fake := &fakeMongoCatalog{fakeSession: &fakeSession{info: engine.Info{ServerVersion: "8.0.38", Database: "app"}}}
	m := mongoModel(t, fake)

	m, cmd := m.importUserFile(path)
	if cmd != nil {
		t.Fatalf("unexpected command before choosing a URI")
	}
	if m.scr != screenImportURIs || len(m.importURIs) != 2 {
		t.Fatalf("scr=%v uris=%v, want the URI chooser with 2 entries", m.scr, m.importURIs)
	}
}

func TestCreateUserFromURIUsesCurrentDatabaseAndAdminAuthSource(t *testing.T) {
	fake := &fakeMongoCatalog{fakeSession: &fakeSession{info: engine.Info{ServerVersion: "8.0.38", Database: "app"}}}
	m := mongoModel(t, fake)

	m, cmd := m.createUserFromURI("mongodb://bob:pw@host:27017/")
	if cmd == nil {
		t.Fatal("no upsert command issued")
	}
	cmd()
	u := fake.upsertUser
	if u.username != "bob" || u.password != "pw" || u.database != "app" || u.authSource != "admin" {
		t.Fatalf("upsert fields = %+v, want bob/pw/app/admin", u)
	}
}
