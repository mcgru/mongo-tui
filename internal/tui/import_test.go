package tui

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"mongo-tui/internal/config"
)

func TestImportProfilesFromMongoFile(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	content := `connect("mongodb://uuu:pppp@mongod1:27021,mongod2:27022,mongod3:27023/stage?authSource=admin&replicaset=dbrs")` + "\n" +
		`mongosh "mongodb+srv://bob@cluster.example/analytics"` + "\n"
	if err := os.WriteFile(filepath.Join(home, ".mongorc.js"), []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}

	opts := testOptions()
	stateDir := filepath.Join(t.TempDir(), "state")
	if err := os.Mkdir(stateDir, 0o700); err != nil {
		t.Fatal(err)
	}
	opts.Paths.StateFile = filepath.Join(stateDir, "state.json")
	opts.State = config.NewState()
	m := New(opts)
	updated, _ := m.Update(tea.WindowSizeMsg{Width: 120, Height: 40})
	m = updated.(Model)

	m, _ = m.openImportFiles(false)
	if m.scr != screenImport {
		t.Fatalf("screen = %v, want import", m.scr)
	}
	if len(m.importFiles) != 1 || filepath.Base(m.importFiles[0]) != ".mongorc.js" {
		t.Fatalf("import files = %v", m.importFiles)
	}

	m, _ = press(t, m, tea.KeyEnter)
	if m.scr != screenPicker {
		t.Fatalf("screen = %v, want picker after import", m.scr)
	}
	if len(m.opts.State.Profiles) != 2 {
		t.Fatalf("profiles = %+v, want 2 imported", m.opts.State.Profiles)
	}
	wantSource := filepath.Join(home, ".mongorc.js")
	for _, profile := range m.opts.State.Profiles {
		if profile.Engine != "mongo" || profile.URI == "" {
			t.Fatalf("profile = %+v", profile)
		}
		if strings.Contains(profile.URI, "pppp") || strings.Contains(profile.URI, "secret") {
			t.Fatalf("password leaked into profile URI: %q", profile.URI)
		}
		if profile.SourceFile != wantSource {
			t.Fatalf("profile source = %q, want %q", profile.SourceFile, wantSource)
		}
	}

	stored, err := config.LoadState(opts.Paths.StateFile)
	if err != nil {
		t.Fatalf("LoadState() error = %v", err)
	}
	if len(stored.Profiles) != 2 {
		t.Fatalf("stored profiles = %+v, status=%q path=%q", stored.Profiles, m.status, opts.Paths.StateFile)
	}

	raw, err := os.ReadFile(opts.Paths.StateFile)
	if err != nil {
		t.Fatalf("ReadFile(state) error = %v", err)
	}
	if strings.Contains(string(raw), "pppp") {
		t.Fatal("password leaked into the state file")
	}
}

func TestImportSkipsDuplicates(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	if err := os.WriteFile(filepath.Join(home, ".mongo_history"),
		[]byte(`mongodb://db.example/app`), 0o600); err != nil {
		t.Fatal(err)
	}

	opts := testOptions()
	stateDir := filepath.Join(t.TempDir(), "state")
	if err := os.Mkdir(stateDir, 0o700); err != nil {
		t.Fatal(err)
	}
	opts.Paths.StateFile = filepath.Join(stateDir, "state.json")
	opts.State = config.NewState()
	m := New(opts)
	updated, _ := m.Update(tea.WindowSizeMsg{Width: 120, Height: 40})
	m = updated.(Model)

	m, _ = m.openImportFiles(false)
	m, _ = press(t, m, tea.KeyEnter)
	if len(m.opts.State.Profiles) != 1 {
		t.Fatalf("profiles = %+v, want 1", m.opts.State.Profiles)
	}
	m, _ = m.openImportFiles(false)
	m, _ = press(t, m, tea.KeyEnter)
	if len(m.opts.State.Profiles) != 1 {
		t.Fatalf("profiles after duplicate import = %+v, want 1", m.opts.State.Profiles)
	}
}

func TestConnectPasswordFromSourceFile(t *testing.T) {
	dir := t.TempDir()
	source := filepath.Join(dir, ".mongorc.js")
	const raw = `mongodb://ann:secret@db.example:27017/app?replicaSet=rs0`
	if err := os.WriteFile(source, []byte(`connect("`+raw+`")`+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	uri, ok := config.NormalizeMongoURI(raw)
	if !ok {
		t.Fatalf("NormalizeMongoURI() rejected %q", raw)
	}

	opts := testOptions()
	opts.State.Upsert(config.Profile{
		ID:         "with-file",
		Label:      config.MongoLabel(uri),
		Engine:     "mongo",
		URI:        uri,
		SourceFile: source,
	})
	opts.State.Upsert(config.Profile{
		ID:     "no-file",
		Label:  config.MongoLabel(uri),
		Engine: "mongo",
		URI:    uri,
	})
	opts.State.Upsert(config.Profile{
		ID:         "missing-file",
		Label:      config.MongoLabel(uri),
		Engine:     "mongo",
		URI:        uri,
		SourceFile: filepath.Join(dir, "gone"),
	})
	m := New(opts)

	for _, tc := range []struct {
		id   string
		want string
	}{
		{"with-file", "secret"},
		{"no-file", ""},
		{"missing-file", ""},
		{"unknown", ""},
	} {
		m.profileID = tc.id
		if got := m.connectPassword(); got != tc.want {
			t.Fatalf("connectPassword(%q) = %q, want %q", tc.id, got, tc.want)
		}
	}
}
