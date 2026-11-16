package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestDefaultKeymapCoversEveryAction(t *testing.T) {
	km := DefaultKeymap()
	if err := km.Validate(); err != nil {
		t.Fatalf("default keymap is invalid: %v", err)
	}
	for _, action := range Actions() {
		if km.Get(action) == "" {
			t.Errorf("action %q has no default key", action)
		}
	}
	if got := km.Get(ActionRun); got != "ctrl+j" {
		t.Errorf("run = %q", got)
	}
}

func TestKeymapOverride(t *testing.T) {
	cfg := Default()
	cfg.Keys = map[string]string{"run": "f9", "copy_cell": "y"}

	km, err := cfg.Keymap()
	if err != nil {
		t.Fatalf("Keymap() error = %v", err)
	}
	if got := km.Get(ActionRun); got != "f9" {
		t.Errorf("run = %q, want f9", got)
	}
	if !km.Is(ActionCopyCell, "y") {
		t.Error("copy_cell override was not applied")
	}
	if got := km.Get(ActionQuit); got != "ctrl+q" {
		t.Errorf("unrelated action changed: %q", got)
	}
}

func TestOneActionMayHaveAliases(t *testing.T) {
	km := DefaultKeymap()

	keys := km.Keys(ActionInsertName)
	if len(keys) != 2 || keys[0] != "." || keys[1] != ">" {
		t.Fatalf("insert_name keys = %q, want . and >", keys)
	}
	if got := km.Get(ActionInsertName); got != "." {
		t.Fatalf("primary key = %q, want the first one", got)
	}
	for _, key := range []string{".", ">"} {
		if !km.Is(ActionInsertName, key) {
			t.Fatalf("%q does not trigger insert_name", key)
		}
	}
	if km.Is(ActionInsertName, "p") {
		t.Fatal("p triggers insert_name")
	}
}

func TestAliasListIsParsedAndTrimmed(t *testing.T) {
	cfg := Default()
	cfg.Keys = map[string]string{"insert_name": " x , ins "}

	km, err := cfg.Keymap()
	if err != nil {
		t.Fatalf("Keymap() error = %v", err)
	}
	if got := km.Get(ActionInsertName); got != "x" {
		t.Fatalf("primary key = %q", got)
	}
	if !km.Is(ActionInsertName, "ins") {
		t.Fatal("the alias was dropped")
	}
}

func TestPeekHasBackslashAlias(t *testing.T) {
	km := DefaultKeymap()
	keys := km.Keys(ActionPeek)
	if len(keys) != 2 || keys[0] != "p" || keys[1] != "\\" {
		t.Fatalf("peek keys = %q, want p and backslash", keys)
	}
	if !km.Is(ActionPeek, "\\") {
		t.Fatal("backslash does not trigger peek")
	}
	if got := km.Get(ActionPeek); got != "p" {
		t.Fatalf("primary peek key = %q, want p", got)
	}
}

func TestAliasCollisionIsAnError(t *testing.T) {
	cfg := Default()
	cfg.Keys = map[string]string{"insert_name": ".,/"}

	_, err := cfg.Keymap()
	if err == nil {
		t.Fatal("Keymap() = nil, want a collision error")
	}
	if !strings.Contains(err.Error(), "insert_name") || !strings.Contains(err.Error(), "select_template") {
		t.Fatalf("error = %q, want both actions named", err)
	}
}

func TestAliasListOfCommasIsAnError(t *testing.T) {
	cfg := Default()
	cfg.Keys = map[string]string{"insert_name": " , "}

	if _, err := cfg.Keymap(); err == nil {
		t.Fatal("Keymap() = nil, want an error for an empty key list")
	}
}

func TestKeymapRejectsUnknownAction(t *testing.T) {
	cfg := Default()
	cfg.Keys = map[string]string{"format_everything": "f9"}

	_, err := cfg.Keymap()
	if err == nil {
		t.Fatal("Keymap() = nil, want an error")
	}
	if !strings.Contains(err.Error(), "unknown key action") || !strings.Contains(err.Error(), "run") {
		t.Fatalf("error = %q, want the unknown action and the known list", err)
	}
}

func TestKeymapRejectsDuplicateBinding(t *testing.T) {
	cfg := Default()
	cfg.Keys = map[string]string{"run": "tab"} // tab moves focus

	_, err := cfg.Keymap()
	if err == nil {
		t.Fatal("Keymap() = nil, want a conflict error")
	}
	if !strings.Contains(err.Error(), "both bound") {
		t.Fatalf("error = %q", err)
	}
}

func TestKeymapRejectsEmptyKey(t *testing.T) {
	cfg := Default()
	cfg.Keys = map[string]string{"run": "   "}

	if _, err := cfg.Keymap(); err == nil {
		t.Fatal("Keymap() = nil, want an error for an empty key")
	}
}

func TestLoadRejectsBrokenKeysSection(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.toml")
	body := "result_limit_rows = 100\n[keys]\nnope = \"f9\"\n"
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}

	if _, err := Load(path); err == nil {
		t.Fatal("Load() = nil, want a key-validation error")
	}
}

func TestLoadAcceptsKeysSection(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.toml")
	body := "[keys]\nrun = \"f9\"\ncopy_row = \"Y\"\n"
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}

	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	km, err := cfg.Keymap()
	if err != nil {
		t.Fatalf("Keymap() error = %v", err)
	}
	if !km.Is(ActionRun, "f9") || !km.Is(ActionCopyRow, "Y") {
		t.Fatalf("keymap = %+v", km)
	}
}
