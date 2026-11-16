package tui

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"mongo-tui/internal/config"
	"mongo-tui/internal/engine"
)

func TestNormalizeRussianCommandKeys(t *testing.T) {
	cases := map[string]string{
		"з": "p", "т": "n", "Т": "N", "ш": "i", "с": "c", "С": "C",
		"х": "[", "ъ": "]", ".": "/", "ю": ".", "Ю": ">", "ж": ";",
		"ctrl+j": "ctrl+j", "enter": "enter", "f1": "f1", "зз": "зз",
	}
	for raw, want := range cases {
		if got := normalizeCommandKey(layoutRU, raw); got != want {
			t.Errorf("normalize(ru, %q) = %q, want %q", raw, got, want)
		}
	}
	// A Cyrillic command key works even when the stored layout is wrong: the
	// locale may say en_US while the user still types Russian.
	if got := normalizeCommandKey(layoutUS, "з"); got != "p" {
		t.Errorf("normalize(us, з) = %q, want p", got)
	}
	if got := normalizeCommandKey(layoutUS, "ь"); got != "m" {
		t.Errorf("normalize(us, ь) = %q, want m", got)
	}
	if got := normalizeCommandKey(layoutUS, "Ф"); got != "A" {
		t.Errorf("normalize(us, Ф) = %q, want A", got)
	}
	// ASCII punctuation keeps its meaning in the US layout.
	for _, raw := range []string{".", ",", ";", ":"} {
		if got := normalizeCommandKey(layoutUS, raw); got != raw {
			t.Errorf("normalize(us, %q) = %q, want unchanged", raw, got)
		}
	}
	// Control names may arrive with a Cyrillic letter on terminals that report
	// the rune instead of the control byte: ctrl+а is the physical ctrl+f.
	controls := map[string]string{
		"ctrl+а": "ctrl+f", "ctrl+А": "ctrl+f",
		"ctrl+ф": "ctrl+a", "ctrl+alt+а": "ctrl+alt+f",
		"ctrl+j": "ctrl+j", "ctrl+shift+left": "ctrl+shift+left",
	}
	for raw, want := range controls {
		if got := normalizeCommandKey(layoutUS, raw); got != want {
			t.Errorf("normalize(us, %q) = %q, want %q", raw, got, want)
		}
	}
}

func TestDetectKeyboardLayoutFromLocale(t *testing.T) {
	t.Setenv("LC_ALL", "")
	t.Setenv("LC_CTYPE", "")
	t.Setenv("LANG", "ru_RU.UTF-8")
	if got := detectKeyboardLayout(); got != layoutRU {
		t.Fatalf("LANG=ru_RU detect = %q, want ru", got)
	}
	t.Setenv("LANG", "en_US.UTF-8")
	if got := detectKeyboardLayout(); got != layoutUS {
		t.Fatalf("LANG=en_US detect = %q, want us", got)
	}
	t.Setenv("LC_ALL", "C.UTF-8")
	t.Setenv("LANG", "ru_RU.UTF-8")
	if got := detectKeyboardLayout(); got != layoutUS {
		t.Fatalf("LC_ALL=C detect = %q, want us", got)
	}
}

func TestKeyboardLayoutRestoredFromProfile(t *testing.T) {
	opts := layoutOptions(t)
	opts.State.KeyboardLayout = layoutUS
	p := config.Profile{ID: "p1", Label: "app", Engine: "mongo", URI: opts.CLI.MongoURI, KeyboardLayout: layoutRU}
	opts.State.Upsert(p)

	m := connectLayoutModel(t, opts, "p1")
	if m.keyboardLayout != layoutRU {
		t.Fatalf("keyboard layout = %q, want ru", m.keyboardLayout)
	}
}

func TestKeyboardLayoutFallsBackToGlobal(t *testing.T) {
	opts := layoutOptions(t)
	opts.State.KeyboardLayout = layoutRU

	m := New(opts)
	if m.keyboardLayout != layoutRU {
		t.Fatalf("keyboard layout = %q, want ru", m.keyboardLayout)
	}
	m = connectLayoutModel(t, opts, "")
	if m.keyboardLayout != layoutRU {
		t.Fatalf("keyboard layout after connect = %q, want ru", m.keyboardLayout)
	}
}

func TestKeyboardLayoutDetectedWhenUnset(t *testing.T) {
	opts := layoutOptions(t)
	opts.State.KeyboardLayout = ""
	t.Setenv("LC_ALL", "")
	t.Setenv("LC_CTYPE", "")
	t.Setenv("LANG", "ru_RU.UTF-8")

	m := New(opts)
	if m.keyboardLayout != layoutRU {
		t.Fatalf("keyboard layout = %q, want ru from the locale", m.keyboardLayout)
	}
}

func TestCyrillicCommandKeys(t *testing.T) {
	fake := &fakeSession{info: engine.Info{ServerVersion: "8.0.38", Database: "app"}}
	m := mongoModel(t, fake)
	m.keyboardLayout = layoutRU
	m.focus = focusTree
	m.tree.visible = []*treeNode{{kind: nodeCollection, schema: "app", name: "items", openable: true}}
	m.tree.cursor = 0

	// з is the physical p: peek.
	updated, cmd := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'з'}})
	m = updated.(Model)
	if cmd == nil || !m.running {
		t.Fatalf("з did not peek (running=%v status=%q)", m.running, m.status)
	}
}

func TestCyrillicSlashOpensSearch(t *testing.T) {
	cols, rows := searchFixtures()
	m := searchGridModel(t, cols, rows)
	m.keyboardLayout = layoutRU

	// "." is the physical / key on the Russian layout.
	updated, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'.'}})
	m = updated.(Model)
	if !m.searchOpen {
		t.Fatal("physical / (.) did not open the search")
	}

	// The prompt itself keeps the Cyrillic runes.
	updated, _ = m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("привет")})
	m = updated.(Model)
	if m.searchInput.Value() != "привет" {
		t.Fatalf("prompt value = %q, want привет", m.searchInput.Value())
	}
}

func TestCyrillicNextMatchAndCaseToggle(t *testing.T) {
	cols, rows := searchFixtures()
	m := searchGridModel(t, cols, rows)
	m.keyboardLayout = layoutRU

	updated, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'/'}})
	m = updated.(Model)
	m = typeInto(t, m, "ann")
	updated, _ = m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	m = updated.(Model)

	// т is n: next match.
	m = typeInto(t, m, "т")
	if m.grid.curRow != 2 {
		t.Fatalf("т row = %d, want 2", m.grid.curRow)
	}
	// ш is i: switch from the default ignore-case to case-sensitive.
	m = typeInto(t, m, "ш")
	if m.searchIgnoreCase {
		t.Fatal("ш did not toggle the search case")
	}
}

func TestEditorStillTypesCyrillic(t *testing.T) {
	m := mongoModel(t, &fakeSession{info: engine.Info{ServerVersion: "8.0.38", Database: "app"}})
	m.keyboardLayout = layoutRU
	m.focus = focusEditor

	updated, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("привет")})
	m = updated.(Model)
	if !strings.Contains(m.editor.Value(), "привет") {
		t.Fatalf("editor = %q, want the Cyrillic text", m.editor.Value())
	}
}

func TestCtrlLTogglesAndSavesKeyboardLayout(t *testing.T) {
	opts := layoutOptions(t)
	p := config.Profile{ID: "p1", Label: "app", Engine: "mongo", URI: opts.CLI.MongoURI, KeyboardLayout: layoutUS}
	opts.State.Upsert(p)
	m := connectLayoutModel(t, opts, "p1")

	updated, _ := m.Update(tea.KeyMsg{Type: tea.KeyCtrlL})
	m = updated.(Model)
	if m.keyboardLayout != layoutRU {
		t.Fatalf("layout = %q, want ru", m.keyboardLayout)
	}
	if !strings.Contains(m.status, "keyboard layout: ru") {
		t.Fatalf("status = %q", m.status)
	}

	st, err := config.LoadState(opts.Paths.StateFile)
	if err != nil {
		t.Fatalf("LoadState: %v", err)
	}
	saved, _ := st.Find("p1")
	if saved.KeyboardLayout != layoutRU {
		t.Fatalf("saved layout = %q, want ru", saved.KeyboardLayout)
	}
}

func TestSaveProfileCapturesKeyboardLayout(t *testing.T) {
	opts := layoutOptions(t)
	m := connectLayoutModel(t, opts, "")
	m.keyboardLayout = layoutRU

	m.saveProfile("custom")

	p, ok := m.opts.State.Find(m.profileID)
	if !ok || p.KeyboardLayout != layoutRU {
		t.Fatalf("saved profile layout = %q (found=%v), want ru", p.KeyboardLayout, ok)
	}
}
