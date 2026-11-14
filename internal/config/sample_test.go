package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestSampleFileIsAllDefaults(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.toml")
	if err := os.WriteFile(path, []byte(SampleFile()), 0o600); err != nil {
		t.Fatal(err)
	}

	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("Load(sample) error = %v", err)
	}
	if len(cfg.Keys) != 0 {
		t.Fatalf("keys = %v, the sample must not override anything", cfg.Keys)
	}
	def := Default()
	if cfg.ReadOnly != def.ReadOnly ||
		cfg.ResultLimitRows != def.ResultLimitRows || cfg.ResultLimitBytes != def.ResultLimitBytes ||
		cfg.MongoDB.SampleSize != def.MongoDB.SampleSize {
		t.Fatalf("cfg = %+v, want the defaults %+v", cfg, def)
	}
}

func TestSampleFileListsEveryAction(t *testing.T) {
	sample := SampleFile()
	for _, action := range Actions() {
		if !strings.Contains(sample, "\n# "+string(action)+" = ") {
			t.Errorf("the sample does not document %q", action)
		}
	}
	for _, want := range []string{"select_template", "peek", "edit_object", "create_template"} {
		if !strings.Contains(sample, want) {
			t.Errorf("the sample does not mention %q", want)
		}
	}
	if !strings.Contains(sample, "fixed") {
		t.Error("the sample does not say which keys stay fixed")
	}
}

func TestEnsureFileWritesOnceAndKeepsEdits(t *testing.T) {
	path := filepath.Join(t.TempDir(), "nested", "config.toml")

	if err := EnsureFile(path); err != nil {
		t.Fatalf("EnsureFile() error = %v", err)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if mode := info.Mode().Perm(); mode != 0o600 {
		t.Fatalf("mode = %o, want 0600", mode)
	}

	edited := []byte("[keys]\nrun = \"f9\"\n")
	if err := os.WriteFile(path, edited, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := EnsureFile(path); err != nil {
		t.Fatalf("EnsureFile() on an existing file error = %v", err)
	}
	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != string(edited) {
		t.Fatalf("file = %q, want the user's own config untouched", got)
	}
}
