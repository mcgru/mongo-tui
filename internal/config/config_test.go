package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestDefaultPaths(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, "cfg"))
	t.Setenv("XDG_STATE_HOME", filepath.Join(home, "var"))
	t.Setenv("XDG_RUNTIME_DIR", filepath.Join(home, "run"))

	got, err := DefaultPaths()
	if err != nil {
		t.Fatalf("DefaultPaths() error = %v", err)
	}
	if want := filepath.Join(home, "cfg", "mongo-tui", "config.toml"); got.ConfigFile != want {
		t.Errorf("ConfigFile = %q, want %q", got.ConfigFile, want)
	}
	if want := filepath.Join(home, "var", "mongo-tui", "state.json"); got.StateFile != want {
		t.Errorf("StateFile = %q, want %q", got.StateFile, want)
	}
	if want := filepath.Join(home, "cfg", "psql-tui", "config.toml"); got.LegacyConfigFile != want {
		t.Errorf("LegacyConfigFile = %q, want %q", got.LegacyConfigFile, want)
	}
	if want := filepath.Join(home, "var", "psql-tui", "state.json"); got.LegacyStateFile != want {
		t.Errorf("LegacyStateFile = %q, want %q", got.LegacyStateFile, want)
	}
}

func TestMigrateLegacyCopiesAndKeepsBackups(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, "cfg"))
	t.Setenv("XDG_STATE_HOME", filepath.Join(home, "var"))

	paths, err := DefaultPaths()
	if err != nil {
		t.Fatalf("DefaultPaths() error = %v", err)
	}
	if err := os.MkdirAll(filepath.Dir(paths.LegacyConfigFile), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Dir(paths.LegacyStateFile), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(paths.LegacyConfigFile, []byte("read_only = true\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(paths.LegacyStateFile, []byte(`{"version":1,"profiles":[]}`), 0o600); err != nil {
		t.Fatal(err)
	}

	if err := MigrateLegacy(paths); err != nil {
		t.Fatalf("MigrateLegacy() error = %v", err)
	}
	if data, err := os.ReadFile(paths.ConfigFile); err != nil || !strings.Contains(string(data), "read_only") {
		t.Fatalf("migrated config = %q, err=%v", data, err)
	}
	if data, err := os.ReadFile(paths.StateFile); err != nil || !strings.Contains(string(data), `"version":1`) {
		t.Fatalf("migrated state = %q, err=%v", data, err)
	}
	if _, err := os.Stat(paths.ConfigFile + ".psql-tui.bak"); err != nil {
		t.Fatalf("config backup: %v", err)
	}
	if _, err := os.Stat(paths.StateFile + ".psql-tui.bak"); err != nil {
		t.Fatalf("state backup: %v", err)
	}
	// The old files stay in place.
	if _, err := os.Stat(paths.LegacyConfigFile); err != nil {
		t.Fatalf("legacy config disappeared: %v", err)
	}
}

func TestMigrateLegacyDoesNotOverwriteNewFiles(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, "cfg"))
	t.Setenv("XDG_STATE_HOME", filepath.Join(home, "var"))

	paths, err := DefaultPaths()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Dir(paths.ConfigFile), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(paths.ConfigFile, []byte("read_only = false\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Dir(paths.LegacyConfigFile), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(paths.LegacyConfigFile, []byte("read_only = true\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := MigrateLegacy(paths); err != nil {
		t.Fatalf("MigrateLegacy() error = %v", err)
	}
	if data, err := os.ReadFile(paths.ConfigFile); err != nil || !strings.Contains(string(data), "false") {
		t.Fatalf("new config was overwritten: %q err=%v", data, err)
	}
}

func TestDefaultPathsFallsBackToHome(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_CONFIG_HOME", "")
	t.Setenv("XDG_STATE_HOME", "")
	t.Setenv("XDG_RUNTIME_DIR", "")

	got, err := DefaultPaths()
	if err != nil {
		t.Fatalf("DefaultPaths() error = %v", err)
	}
	if want := filepath.Join(home, ".config", "mongo-tui"); got.ConfigDir != want {
		t.Errorf("ConfigDir = %q, want %q", got.ConfigDir, want)
	}
	if !strings.HasPrefix(got.StateFile, home) {
		t.Errorf("StateFile = %q, want it under %q", got.StateFile, home)
	}
}

func TestLoadMissingFileUsesDefaults(t *testing.T) {
	cfg, err := Load(filepath.Join(t.TempDir(), "absent.toml"))
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	want := Default()
	if cfg.ReadOnly != want.ReadOnly ||
		cfg.ResultLimitRows != want.ResultLimitRows || cfg.ResultLimitBytes != want.ResultLimitBytes ||
		len(cfg.Keys) != 0 {
		t.Fatalf("cfg = %+v, want %+v", cfg, want)
	}
}

func TestLoadMissingMongoSectionKeepsDefaultSample(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.toml")
	if err := os.WriteFile(path, []byte("read_only = true\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if cfg.MongoDB.SampleSize != DefaultMongoSampleSize {
		t.Fatalf("sample size = %d, want default %d", cfg.MongoDB.SampleSize, DefaultMongoSampleSize)
	}
}

func TestLoadMongoSampleSize(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.toml")
	if err := os.WriteFile(path, []byte("[mongodb]\nsample_size = 7\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if cfg.MongoDB.SampleSize != 7 {
		t.Fatalf("sample size = %d, want 7", cfg.MongoDB.SampleSize)
	}
}

func TestLoadRejectsUnknownKeys(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.toml")
	if err := os.WriteFile(path, []byte("unknown_key = 1\nresult_limit_rowz = 5\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	_, err := Load(path)
	if err == nil {
		t.Fatal("Load() = nil, want an error for an unknown key")
	}
	if !strings.Contains(err.Error(), "result_limit_rowz") {
		t.Fatalf("error = %q, want it to name the unknown key", err)
	}
}

func TestLoadRejectsBadLimits(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.toml")
	if err := os.WriteFile(path, []byte("result_limit_rows = 0\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := Load(path); err == nil {
		t.Fatal("Load() = nil, want an error for result_limit_rows = 0")
	}
}

func TestStateRoundTrip(t *testing.T) {
	path := filepath.Join(t.TempDir(), "nested", "state.json")
	st := NewState()
	st.Upsert(Profile{ID: "1", Label: "prod", Host: "db", Port: 5432, Database: "appdb", Username: "u"})

	if err := st.Save(path); err != nil {
		t.Fatalf("Save() error = %v", err)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if perm := info.Mode().Perm(); perm != 0o600 {
		t.Errorf("mode = %04o, want 0600", perm)
	}

	got, err := LoadState(path)
	if err != nil {
		t.Fatalf("LoadState() error = %v", err)
	}
	if got.Version != StateVersion {
		t.Errorf("version = %d, want %d", got.Version, StateVersion)
	}
	if len(got.Profiles) != 1 || got.Profiles[0].Label != "prod" {
		t.Fatalf("profiles = %+v", got.Profiles)
	}
}

func TestLoadStateRejectsOtherVersion(t *testing.T) {
	path := filepath.Join(t.TempDir(), "state.json")
	if err := os.WriteFile(path, []byte(`{"version": 99, "profiles": []}`), 0o600); err != nil {
		t.Fatal(err)
	}
	_, err := LoadState(path)
	if err == nil || !strings.Contains(err.Error(), "unsupported state version") {
		t.Fatalf("err = %v, want an unsupported version error", err)
	}
}

func TestLoadStateMissingIsEmpty(t *testing.T) {
	st, err := LoadState(filepath.Join(t.TempDir(), "none.json"))
	if err != nil {
		t.Fatalf("LoadState() error = %v", err)
	}
	if st.Version != StateVersion || len(st.Profiles) != 0 {
		t.Fatalf("state = %+v", st)
	}
}

func TestStateOperations(t *testing.T) {
	st := NewState()
	st.Upsert(Profile{ID: "a", Label: "alpha"})
	st.Upsert(Profile{ID: "b", Label: "beta"})

	if _, ok := st.Find("b"); !ok {
		t.Fatal("Find(b) not found")
	}
	if !st.Remove("a") {
		t.Fatal("Remove(a) = false")
	}
	if st.Remove("a") {
		t.Fatal("Remove(a) twice = true")
	}
	if _, ok := st.Find("a"); ok {
		t.Fatal("Find(a) still found after removal")
	}
}

func TestUpsertKeepsCreatedAt(t *testing.T) {
	st := NewState()
	st.Upsert(Profile{ID: "a", Label: "one"})
	created := st.Profiles[0].CreatedAt

	time.Sleep(time.Millisecond)
	st.Upsert(Profile{ID: "a", Label: "two"})
	if !st.Profiles[0].CreatedAt.Equal(created) {
		t.Fatal("CreatedAt was rewritten by an update")
	}
	if st.Profiles[0].Label != "two" {
		t.Fatal("update did not replace the profile")
	}
}

func TestSortedPutsFavoritesFirstThenRecent(t *testing.T) {
	old := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	recent := time.Date(2026, 11, 1, 0, 0, 0, 0, time.UTC)

	st := NewState()
	st.Profiles = []Profile{
		{ID: "old", Label: "zulu", LastConnectedAt: &old},
		{ID: "recent", Label: "yankee", LastConnectedAt: &recent},
		{ID: "fav", Label: "xray", Favorite: true},
		{ID: "never", Label: "alpha"},
	}

	got := st.Sorted()
	want := []string{"fav", "recent", "old", "never"}
	for i, id := range want {
		if got[i].ID != id {
			t.Fatalf("order = %v, want %v", ids(got), want)
		}
	}
}

func ids(profiles []Profile) []string {
	out := make([]string, len(profiles))
	for i, p := range profiles {
		out[i] = p.ID
	}
	return out
}

func TestEnsurePrivateDirRejectsLooseDirectory(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "loose")
	if err := os.Mkdir(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	err := EnsurePrivateDir(dir)
	if err == nil || !strings.Contains(err.Error(), "chmod 700") {
		t.Fatalf("err = %v, want a permission hint", err)
	}
}
