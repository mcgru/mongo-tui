package config

import (
	"path/filepath"
	"testing"
)

func TestStateLayoutRoundTrip(t *testing.T) {
	path := filepath.Join(t.TempDir(), "state", "state.json")
	st := NewState()
	st.Layout = &Layout{LeftPct: 34, ResultPct: 66}
	st.KeyboardLayout = "ru"
	p := Profile{ID: "p1", Label: "app", Engine: "mongo", URI: "mongodb://db/app"}
	p.Layout = &Layout{LeftPct: 44, ResultPct: 70}
	p.KeyboardLayout = "us"
	st.Upsert(p)

	if err := st.Save(path); err != nil {
		t.Fatalf("Save: %v", err)
	}
	got, err := LoadState(path)
	if err != nil {
		t.Fatalf("LoadState: %v", err)
	}
	if got.Layout == nil || got.Layout.LeftPct != 34 || got.Layout.ResultPct != 66 {
		t.Fatalf("global layout = %+v, want 34/66", got.Layout)
	}
	if got.KeyboardLayout != "ru" {
		t.Fatalf("global keyboard layout = %q, want ru", got.KeyboardLayout)
	}
	loaded, ok := got.Find("p1")
	if !ok || loaded.Layout == nil || loaded.Layout.LeftPct != 44 || loaded.Layout.ResultPct != 70 {
		t.Fatalf("profile layout = %+v (found=%v), want 44/70", loaded.Layout, ok)
	}
	if loaded.KeyboardLayout != "us" {
		t.Fatalf("profile keyboard layout = %q, want us", loaded.KeyboardLayout)
	}
}

func TestStateWithoutLayoutStillLoads(t *testing.T) {
	path := filepath.Join(t.TempDir(), "state", "state.json")
	st := NewState()
	st.Upsert(Profile{ID: "p1", Label: "old", Engine: "mongo", URI: "mongodb://db/app"})
	if err := st.Save(path); err != nil {
		t.Fatalf("Save: %v", err)
	}

	got, err := LoadState(path)
	if err != nil {
		t.Fatalf("LoadState: %v", err)
	}
	if got.Layout != nil {
		t.Fatalf("global layout = %+v, want nil", got.Layout)
	}
	p, _ := got.Find("p1")
	if p.Layout != nil {
		t.Fatalf("profile layout = %+v, want nil", p.Layout)
	}
	if got.KeyboardLayout != "" || p.KeyboardLayout != "" {
		t.Fatalf("keyboard layouts = %q/%q, want empty", got.KeyboardLayout, p.KeyboardLayout)
	}
}
