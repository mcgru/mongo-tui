package tui

import (
	"path/filepath"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"mongo-tui/internal/config"
	"mongo-tui/internal/engine"
)

func layoutOptions(t *testing.T) Options {
	t.Helper()
	opts := testOptions()
	opts.Paths.StateFile = filepath.Join(t.TempDir(), "state", "state.json")
	opts.CLI.MongoURI = "mongodb://db.example:27017/app?replicaSet=rs0"
	return opts
}

func layoutSession() *fakeSession {
	return &fakeSession{info: engine.Info{ServerVersion: "8.0.38", Database: "app"}}
}

func connectLayoutModel(t *testing.T, opts Options, profileID string) Model {
	t.Helper()
	m := New(opts)
	updated, _ := m.Update(tea.WindowSizeMsg{Width: 140, Height: 44})
	m = updated.(Model)
	updated, _ = m.Update(connectedMsg{
		sess: layoutSession(), mongoURI: opts.CLI.MongoURI, profileID: profileID,
	})
	m = updated.(Model)
	if m.scr != screenMain {
		t.Fatalf("screen = %v, want main", m.scr)
	}
	return m
}

func TestLayoutRestoredFromProfile(t *testing.T) {
	opts := layoutOptions(t)
	p := config.Profile{ID: "p1", Label: "app", Engine: "mongo", URI: opts.CLI.MongoURI}
	p.Layout = &config.Layout{LeftPct: 34, ResultPct: 70}
	opts.State.Upsert(p)

	m := connectLayoutModel(t, opts, "p1")
	if m.leftPct != 34 || m.resultPct != 70 {
		t.Fatalf("layout = %d/%d, want 34/70", m.leftPct, m.resultPct)
	}
}

func TestLayoutFallsBackToGlobal(t *testing.T) {
	opts := layoutOptions(t)
	opts.State.Layout = &config.Layout{LeftPct: 40, ResultPct: 60}

	m := New(opts)
	if m.leftPct != 40 || m.resultPct != 60 {
		t.Fatalf("layout before connect = %d/%d, want 40/60", m.leftPct, m.resultPct)
	}
	m = connectLayoutModel(t, opts, "")
	if m.leftPct != 40 || m.resultPct != 60 {
		t.Fatalf("layout after connect = %d/%d, want 40/60", m.leftPct, m.resultPct)
	}
}

func TestLayoutIgnoresOutOfRangeValues(t *testing.T) {
	opts := layoutOptions(t)
	opts.State.Layout = &config.Layout{LeftPct: 5, ResultPct: 99}

	m := New(opts)
	if m.leftPct != defaultLeftPct || m.resultPct != defaultResultPct {
		t.Fatalf("layout = %d/%d, want defaults %d/%d",
			m.leftPct, m.resultPct, defaultLeftPct, defaultResultPct)
	}
}

func TestLayoutSavedPerProfile(t *testing.T) {
	opts := layoutOptions(t)
	p := config.Profile{ID: "p1", Label: "app", Engine: "mongo", URI: opts.CLI.MongoURI}
	p.Layout = &config.Layout{LeftPct: 30, ResultPct: 50}
	opts.State.Upsert(p)
	m := connectLayoutModel(t, opts, "p1")

	m, _ = press(t, m, tea.KeyCtrlRight) // tree 30 -> 32
	m, _ = press(t, m, tea.KeyCtrlDown)  // result 50 -> 52
	if m.leftPct != 32 || m.resultPct != 52 {
		t.Fatalf("layout in memory = %d/%d, want 32/52", m.leftPct, m.resultPct)
	}
	if m.layoutSeq != 2 {
		t.Fatalf("layoutSeq = %d, want 2", m.layoutSeq)
	}
	saved, ok := m.opts.State.Find("p1")
	if !ok || saved.Layout == nil || saved.Layout.LeftPct != 32 || saved.Layout.ResultPct != 52 {
		t.Fatalf("profile layout in state = %+v", saved.Layout)
	}

	m.saveLayoutNow()
	st, err := config.LoadState(opts.Paths.StateFile)
	if err != nil {
		t.Fatalf("LoadState: %v", err)
	}
	saved, _ = st.Find("p1")
	if saved.Layout == nil || saved.Layout.LeftPct != 32 || saved.Layout.ResultPct != 52 {
		t.Fatalf("persisted profile layout = %+v, want 32/52", saved.Layout)
	}
}

func TestLayoutSavedGloballyWithoutProfile(t *testing.T) {
	opts := layoutOptions(t)
	m := connectLayoutModel(t, opts, "")

	m, _ = press(t, m, tea.KeyCtrlLeft) // tree 28 -> 26
	if m.opts.State.Layout == nil || m.opts.State.Layout.LeftPct != 26 || m.opts.State.Layout.ResultPct != 55 {
		t.Fatalf("global layout = %+v, want 26/55", m.opts.State.Layout)
	}

	m.saveLayoutNow()
	st, err := config.LoadState(opts.Paths.StateFile)
	if err != nil {
		t.Fatalf("LoadState: %v", err)
	}
	if st.Layout == nil || st.Layout.LeftPct != 26 || st.Layout.ResultPct != 55 {
		t.Fatalf("persisted global layout = %+v, want 26/55", st.Layout)
	}
}

func TestQuitSavesLayoutImmediately(t *testing.T) {
	opts := layoutOptions(t)
	p := config.Profile{ID: "p1", Label: "app", Engine: "mongo", URI: opts.CLI.MongoURI}
	opts.State.Upsert(p)
	m := connectLayoutModel(t, opts, "p1")
	m, _ = press(t, m, tea.KeyCtrlRight)

	updated, cmd := m.Update(tea.KeyMsg{Type: tea.KeyCtrlQ})
	m = updated.(Model)
	if cmd == nil {
		t.Fatal("Ctrl+Q should quit")
	}
	if _, ok := cmd().(tea.QuitMsg); !ok {
		t.Fatalf("quit command returned %T", cmd())
	}

	st, err := config.LoadState(opts.Paths.StateFile)
	if err != nil {
		t.Fatalf("LoadState: %v", err)
	}
	saved, _ := st.Find("p1")
	if saved.Layout == nil || saved.Layout.LeftPct != defaultLeftPct+resizeStep {
		t.Fatalf("layout at quit = %+v, want left %d", saved.Layout, defaultLeftPct+resizeStep)
	}
}

func TestLayoutDebouncedSave(t *testing.T) {
	old := layoutSaveDelay
	layoutSaveDelay = time.Millisecond
	defer func() { layoutSaveDelay = old }()

	opts := layoutOptions(t)
	p := config.Profile{ID: "p1", Label: "app", Engine: "mongo", URI: opts.CLI.MongoURI}
	opts.State.Upsert(p)
	m := connectLayoutModel(t, opts, "p1")

	updated, cmd := m.Update(tea.KeyMsg{Type: tea.KeyCtrlRight})
	m = updated.(Model)
	if cmd == nil {
		t.Fatal("resize should schedule a state write")
	}
	msg, ok := cmd().(layoutSaveMsg)
	if !ok || msg.seq != m.layoutSeq {
		t.Fatalf("tick = %#v, want layoutSaveMsg seq %d", msg, m.layoutSeq)
	}

	// A stale tick must not write an older layout.
	updated, _ = m.Update(layoutSaveMsg{seq: msg.seq - 1})
	m = updated.(Model)
	if st, err := config.LoadState(opts.Paths.StateFile); err == nil {
		if saved, _ := st.Find("p1"); saved.Layout != nil {
			t.Fatalf("stale tick wrote a layout: %+v", saved.Layout)
		}
	}

	updated, _ = m.Update(msg)
	m = updated.(Model)
	st, err := config.LoadState(opts.Paths.StateFile)
	if err != nil {
		t.Fatalf("LoadState: %v", err)
	}
	saved, _ := st.Find("p1")
	if saved.Layout == nil || saved.Layout.LeftPct != defaultLeftPct+resizeStep {
		t.Fatalf("debounced layout = %+v, want left %d", saved.Layout, defaultLeftPct+resizeStep)
	}
}

func TestSaveProfileCapturesLayout(t *testing.T) {
	opts := layoutOptions(t)
	m := connectLayoutModel(t, opts, "")
	m.leftPct = 37
	m.resultPct = 63

	m.saveProfile("custom")

	p, ok := m.opts.State.Find(m.profileID)
	if !ok || p.Layout == nil || p.Layout.LeftPct != 37 || p.Layout.ResultPct != 63 {
		t.Fatalf("saved profile layout = %+v (found=%v), want 37/63", p.Layout, ok)
	}
}

func TestNoHistoryDoesNotPersistLayout(t *testing.T) {
	opts := layoutOptions(t)
	opts.CLI.NoHistory = true
	m := connectLayoutModel(t, opts, "")

	updated, cmd := m.Update(tea.KeyMsg{Type: tea.KeyCtrlRight})
	m = updated.(Model)
	if cmd != nil {
		t.Fatal("--no-history must not schedule a state write")
	}
	m.saveLayoutNow()
	st, err := config.LoadState(opts.Paths.StateFile)
	if err != nil {
		t.Fatalf("LoadState: %v", err)
	}
	if st.Layout != nil {
		t.Fatalf("layout persisted with --no-history: %+v", st.Layout)
	}
}
