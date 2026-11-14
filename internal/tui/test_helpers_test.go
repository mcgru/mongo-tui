package tui

import (
	"context"
	"sync"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"mongo-tui/internal/cli"
	"mongo-tui/internal/config"
	"mongo-tui/internal/engine"
)

type fakeSession struct {
	mu        sync.Mutex
	results   []engine.Result
	err       error
	executed  []string
	info      engine.Info
	readOnly  bool
	tx        engine.TxState
	closed    bool
	cancels   int
	switches  int
	switchErr error
	byQuery   func(string) (engine.Result, bool)
}

func (f *fakeSession) Execute(_ context.Context, statement string) (engine.Result, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.executed = append(f.executed, statement)
	if f.byQuery != nil {
		if res, ok := f.byQuery(statement); ok {
			return res, nil
		}
		return engine.Result{}, nil
	}
	if f.err != nil {
		return engine.Result{}, f.err
	}
	if len(f.results) == 0 {
		return engine.Result{}, nil
	}
	res := f.results[0]
	if len(f.results) > 1 {
		f.results = f.results[1:]
	}
	return res, nil
}

func (f *fakeSession) Cancel(context.Context) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.cancels++
	return nil
}

func (f *fakeSession) TxState(context.Context) engine.TxState { return f.tx }

func (f *fakeSession) SwitchDatabase(_ context.Context, database string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.switches++
	f.info.Database = database
	return f.switchErr
}

func (f *fakeSession) Summary() engine.Info { return f.info }
func (f *fakeSession) ReadOnly() bool       { return f.readOnly }

func (f *fakeSession) Close() error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.closed = true
	return nil
}

func (f *fakeSession) statements() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]string(nil), f.executed...)
}

func testOptions() Options {
	return Options{
		CLI:     cli.Options{},
		Config:  config.Default(),
		State:   config.NewState(),
		Paths:   config.Paths{StateFile: "/tmp/mongo-tui-test/state.json"},
		Version: "0.1.0-test",
	}
}

func sized(m Model) Model {
	updated, _ := m.Update(tea.WindowSizeMsg{Width: 120, Height: 40})
	return updated.(Model)
}

func mainModel(t *testing.T, sess Executor) Model {
	t.Helper()
	opts := testOptions()
	m := New(opts)
	updated, _ := m.Update(tea.WindowSizeMsg{Width: 140, Height: 44})
	m = updated.(Model)
	updated, _ = m.Update(connectedMsg{
		sess: sess, mongoURI: "mongodb://db.example:27017/app?replicaSet=rs0",
	})
	m = updated.(Model)
	if m.scr != screenMain {
		t.Fatalf("screen = %v, want main", m.scr)
	}
	return m
}

func press(t *testing.T, m Model, key tea.KeyType) (Model, tea.Cmd) {
	t.Helper()
	updated, cmd := m.Update(tea.KeyMsg{Type: key})
	model, ok := updated.(Model)
	if !ok {
		t.Fatalf("Update returned %T, want Model", updated)
	}
	return model, cmd
}
