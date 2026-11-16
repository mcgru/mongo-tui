package tui

import (
	"os"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
)

const (
	layoutUS = "us"
	layoutRU = "ru"
)

// ruToUS maps a rune produced by the Russian layout to the character the same
// physical key produces on a US keyboard. Only command matching uses it: text
// inputs and the editor always receive the original rune.
var ruToUS = map[rune]string{
	'й': "q", 'ц': "w", 'у': "e", 'к': "r", 'е': "t", 'н': "y", 'г': "u",
	'ш': "i", 'щ': "o", 'з': "p", 'х': "[", 'ъ': "]",
	'ф': "a", 'ы': "s", 'в': "d", 'а': "f", 'п': "g", 'р': "h", 'о': "j",
	'л': "k", 'д': "l", 'ж': ";", 'э': "'",
	'я': "z", 'ч': "x", 'с': "c", 'м': "v", 'и': "b", 'т': "n", 'ь': "m",
	'б': ",", 'ю': ".", 'ё': "`",

	'Й': "Q", 'Ц': "W", 'У': "E", 'К': "R", 'Е': "T", 'Н': "Y", 'Г': "U",
	'Ш': "I", 'Щ': "O", 'З': "P", 'Х': "{", 'Ъ': "}",
	'Ф': "A", 'Ы': "S", 'В': "D", 'А': "F", 'П': "G", 'Р': "H", 'О': "J",
	'Л': "K", 'Д': "L", 'Ж': ":", 'Э': "\"",
	'Я': "Z", 'Ч': "X", 'С': "C", 'М': "V", 'И': "B", 'Т': "N", 'Ь': "M",
	'Б': "<", 'Ю': ">", 'Ё': "~",

	// ASCII punctuation that the Russian layout produces from other physical
	// keys. "." is the big one: it is the physical "/" key, which opens the
	// search. InsertName's "." stays reachable through the physical "." key
	// (which produces "ю").
	'.': "/",
	',': "?",
	';': "$",
	':': "^",
}

// resolveKeyboardLayout turns the stored value into an effective layout.
// Empty and unknown values fall back to the locale.
func resolveKeyboardLayout(value string) string {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case layoutUS:
		return layoutUS
	case layoutRU:
		return layoutRU
	}
	return detectKeyboardLayout()
}

// detectKeyboardLayout guesses the layout from the locale. The terminal never
// reports the active keyboard group, so this is only a default; the profile or
// the Ctrl+L toggle can override it.
func detectKeyboardLayout() string {
	for _, name := range []string{"LC_ALL", "LC_CTYPE", "LANG"} {
		value := strings.ToLower(strings.TrimSpace(os.Getenv(name)))
		if value == "" {
			continue
		}
		if value == "c" || value == "posix" || strings.HasPrefix(value, "c.") {
			return layoutUS
		}
		if strings.HasPrefix(value, "ru") {
			return layoutRU
		}
		return layoutUS
	}
	return layoutUS
}

// normalizeCommandKey maps a Cyrillic command key to the US key on the same
// physical position. Cyrillic runes are mapped whatever the stored layout says:
// the locale can be en_US while the user still types Russian, and text widgets
// always receive the raw rune anyway. The ASCII punctuation aliases of the
// Russian layout apply only when the layout is ru, because otherwise they would
// steal ordinary "." and ",". Control names with a Cyrillic letter are mapped
// too, so ctrl+а reaches ctrl+f on terminals that report the rune rather than
// the control byte; other multi-rune names (ctrl+j, tab, esc, f1) stay as is.
func normalizeCommandKey(layout, raw string) string {
	if prefix, rest, ok := splitControlPrefix(raw); ok {
		runes := []rune(rest)
		if len(runes) == 1 && isCyrillic(runes[0]) {
			if mapped, ok := ruToUS[runes[0]]; ok && len(mapped) == 1 && isASCIILetter(rune(mapped[0])) {
				return prefix + strings.ToLower(mapped)
			}
		}
		return raw
	}

	runes := []rune(raw)
	if len(runes) != 1 {
		return raw
	}
	r := runes[0]
	if !isCyrillic(r) && layout != layoutRU {
		return raw
	}
	if mapped, ok := ruToUS[r]; ok {
		return mapped
	}
	return raw
}

// splitControlPrefix separates a control-key name from its key, longest prefix
// first so ctrl+alt+f is not read as ctrl+ with "alt+f" left in the key.
func splitControlPrefix(raw string) (prefix, rest string, ok bool) {
	for _, candidate := range []string{"ctrl+shift+", "ctrl+alt+", "ctrl+"} {
		if strings.HasPrefix(raw, candidate) {
			return candidate, raw[len(candidate):], true
		}
	}
	return "", "", false
}

// isCyrillic reports whether the rune belongs to the Cyrillic script; the
// Russian layout's letters all do.
func isCyrillic(r rune) bool {
	return r >= '\u0400' && r <= '\u04FF'
}

// commandKey is what the keymap and the fixed context keys are matched
// against; text widgets still get the raw tea.KeyMsg.
func (m Model) commandKey(raw string) string {
	return normalizeCommandKey(m.keyboardLayout, raw)
}

// keyboardLayoutForProfile prefers the profile's layout, then the global one,
// then the locale.
func (m Model) keyboardLayoutForProfile(profileID string) string {
	if profileID != "" {
		if p, ok := m.opts.State.Find(profileID); ok && p.KeyboardLayout != "" {
			return resolveKeyboardLayout(p.KeyboardLayout)
		}
	}
	return resolveKeyboardLayout(m.opts.State.KeyboardLayout)
}

// toggleKeyboardLayout switches us↔ru, remembers it in the active profile or
// globally and reports the result. Ctrl+L is layout-independent.
func (m Model) toggleKeyboardLayout() (tea.Model, tea.Cmd) {
	if m.keyboardLayout == layoutRU {
		m.keyboardLayout = layoutUS
	} else {
		m.keyboardLayout = layoutRU
	}
	err := m.saveKeyboardLayout()
	if err != nil {
		m.status = "keyboard layout: " + m.keyboardLayout + " (not saved: " + err.Error() + ")"
	} else {
		m.status = "keyboard layout: " + m.keyboardLayout
	}
	return m, nil
}

// saveKeyboardLayout writes the choice to the active profile, or globally when
// the session has no saved profile.
func (m *Model) saveKeyboardLayout() error {
	if m.opts.CLI.NoHistory {
		return nil
	}
	stored := false
	if m.profileID != "" {
		if p, ok := m.opts.State.Find(m.profileID); ok {
			p.KeyboardLayout = m.keyboardLayout
			m.opts.State.Upsert(p)
			stored = true
		}
	}
	if !stored {
		m.opts.State.KeyboardLayout = m.keyboardLayout
	}
	return m.opts.State.Save(m.opts.Paths.StateFile)
}
