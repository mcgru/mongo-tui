package config

import (
	"fmt"
	"sort"
	"strings"
)

// Action names a command that can be bound to a key.
type Action string

const (
	ActionRun             Action = "run"
	ActionComplete        Action = "complete"
	ActionHistory         Action = "history"
	ActionHelp            Action = "help"
	ActionSaveProfile     Action = "save_profile"
	ActionImportProfiles  Action = "import_profiles"
	ActionConnect         Action = "connect"
	ActionRefresh         Action = "refresh"
	ActionFocus           Action = "focus"
	ActionFocusBack       Action = "focus_back"
	ActionPanes           Action = "panes"
	ActionPanesBack       Action = "panes_back"
	ActionLeftWider       Action = "left_wider"
	ActionLeftNarrower    Action = "left_narrower"
	ActionResultTaller    Action = "result_taller"
	ActionResultShorter   Action = "result_shorter"
	ActionQuit            Action = "quit"
	ActionCancel          Action = "cancel"
	ActionDeleteStatement Action = "delete_statement"
	ActionEditStatement   Action = "edit_statement"
	ActionCopyCell        Action = "copy_cell"
	ActionCopyRow         Action = "copy_row"

	// Tree actions. They carry the same meaning as in less: / searches an
	// object, * edits it.
	ActionSelectTemplate Action = "select_template"
	ActionPeek           Action = "peek"
	ActionEditObject     Action = "edit_object"
	ActionInsertName     Action = "insert_name"
	ActionCreateTemplate Action = "create_template"
	ActionDropTemplate   Action = "drop_template"
	ActionAlterTemplate  Action = "alter_template"
)

// defaultKeys is the built-in binding of every action.
var defaultKeys = map[Action]string{
	ActionRun:            "ctrl+j",
	ActionComplete:       "ctrl+space",
	ActionHistory:        "ctrl+r",
	ActionHelp:           "ctrl+f1",
	ActionSaveProfile:    "ctrl+s",
	ActionImportProfiles: "i",
	ActionConnect:        "f2",
	ActionRefresh:        "f5",
	ActionFocus:          "tab",
	ActionFocusBack:      "shift+tab",
	ActionPanes:          "]",
	ActionPanesBack:      "[",
	ActionLeftWider:      "ctrl+right",
	ActionLeftNarrower:   "ctrl+left",
	// Ctrl+Up moves the divider up: the editor grows and the result pane
	// shrinks, which is what the hand expects when moving a divider.
	ActionResultTaller:    "ctrl+down",
	ActionResultShorter:   "ctrl+up",
	ActionQuit:            "ctrl+q",
	ActionCancel:          "ctrl+c",
	ActionDeleteStatement: "ctrl+k",
	ActionEditStatement:   "ctrl+e",
	ActionCopyCell:        "c",
	ActionCopyRow:         "C",

	// / searches like less does: it turns the object into a SELECT.
	ActionSelectTemplate: "/",
	// p looks inside, * edits: the definition lands in the editor, and Ctrl+J
	// applies it. . and > paste the selected name into the statement.
	ActionPeek:           "p",
	ActionEditObject:     "*",
	ActionInsertName:     ".,>",
	ActionCreateTemplate: "+",
	ActionDropTemplate:   "-",
	ActionAlterTemplate:  "=",
}

// Keymap maps an action to the key that triggers it. Keys use the names
// bubbletea reports: "ctrl+j", "f1", "shift+left", "c". A value may list
// several keys separated by commas; the first one is the primary binding and
// the one the help screen shows.
type Keymap map[Action]string

// DefaultKeymap returns a fresh copy of the built-in bindings.
func DefaultKeymap() Keymap {
	out := make(Keymap, len(defaultKeys))
	for action, key := range defaultKeys {
		out[action] = key
	}
	return out
}

// Actions lists every action in a stable order, for help and validation.
func Actions() []Action {
	out := make([]Action, 0, len(defaultKeys))
	for action := range defaultKeys {
		out = append(out, action)
	}
	sort.Slice(out, func(i, j int) bool { return out[i] < out[j] })
	return out
}

// Keymap resolves the [keys] section over the defaults.
func (c Config) Keymap() (Keymap, error) {
	out := DefaultKeymap()

	for name, key := range c.Keys {
		action := Action(name)
		if _, known := defaultKeys[action]; !known {
			return nil, fmt.Errorf("unknown key action %q, known actions: %s", name, actionList())
		}
		if strings.TrimSpace(key) == "" {
			return nil, fmt.Errorf("action %q has an empty key", name)
		}
		out[action] = key
	}
	if err := out.Validate(); err != nil {
		return nil, err
	}
	return out, nil
}

func actionList() string {
	names := make([]string, 0, len(defaultKeys))
	for _, action := range Actions() {
		names = append(names, string(action))
	}
	return strings.Join(names, ", ")
}

// Validate rejects two actions on one key, which would make one of them
// unreachable.
func (k Keymap) Validate() error {
	owner := make(map[string]Action, len(k))
	for _, action := range Actions() {
		keys := k.Keys(action)
		if len(keys) == 0 {
			return fmt.Errorf("action %q has no key", action)
		}
		for _, key := range keys {
			if other, taken := owner[key]; taken && other != action {
				return fmt.Errorf("actions %q and %q are both bound to %q", other, action, key)
			}
			owner[key] = action
		}
	}
	return nil
}

// Get returns the primary binding of an action, falling back to the default.
func (k Keymap) Get(action Action) string {
	keys := k.Keys(action)
	if len(keys) == 0 {
		return ""
	}
	return keys[0]
}

// Keys returns every binding of an action, primary first.
func (k Keymap) Keys(action Action) []string {
	raw, ok := k[action]
	if !ok || strings.TrimSpace(raw) == "" {
		raw = defaultKeys[action]
	}
	parts := strings.Split(raw, ",")
	out := make([]string, 0, len(parts))
	for _, part := range parts {
		part = strings.TrimSpace(part)
		if part != "" {
			out = append(out, part)
		}
	}
	return out
}

// Is reports whether key is bound to the action.
func (k Keymap) Is(action Action, key string) bool {
	for _, bound := range k.Keys(action) {
		if bound == key {
			return true
		}
	}
	return false
}
