package config

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// Guaranteed keys are the ones that stay put: they are context navigation, not
// commands, and mean the same thing in every pane.
const guaranteedKeys = `Arrows, Home/End, PgUp/PgDn, Enter/Space and Esc are fixed: they are
context navigation (a tree row, a grid cell, a completion popup), not commands.
Every command above is rebindable, and the help screen always shows the
bindings that are actually in effect.`

// SampleFile renders config.toml with every setting commented out, so the file
// documents itself and the built-in defaults stay in force until a line is
// uncommented.
func SampleFile() string {
	cfg := Default()
	var b strings.Builder

	b.WriteString("# mongo-tui configuration.\n")
	b.WriteString("#\n")
	b.WriteString("# Everything below is commented out: the built-in defaults apply until you\n")
	b.WriteString("# uncomment a line. Unknown keys, an empty key or two actions on one key\n")
	b.WriteString("# stop the client at startup instead of being silently ignored.\n")
	b.WriteString("\n")
	fmt.Fprintf(&b, "# read_only = %t\n", cfg.ReadOnly)
	fmt.Fprintf(&b, "# result_limit_rows = %d\n", cfg.ResultLimitRows)
	fmt.Fprintf(&b, "# result_limit_bytes = %d\n", cfg.ResultLimitBytes)
	fmt.Fprintf(&b, "# mouse = %t  # middle click inserts the selected name; takes text selection away from the terminal\n", cfg.Mouse)
	b.WriteString("\n")
	b.WriteString("# [mongodb]\n")
	fmt.Fprintf(&b, "# sample_size = %d  # documents sampled for collection fields and completion\n", cfg.MongoDB.SampleSize)
	b.WriteString("\n")
	b.WriteString("# Keys use the names the terminal reports: ctrl+j, shift+tab, f2, c, /\n")
	b.WriteString("#\n")
	b.WriteString("# [keys]\n")
	b.WriteString("# Every command below is rebindable. " +
		strings.ReplaceAll(guaranteedKeys, "\n", "\n# ") + "\n")
	b.WriteString("\n")

	for _, action := range Actions() {
		comment := actionDoc[action]
		key := defaultKeys[action]
		if comment == "" {
			fmt.Fprintf(&b, "# %s = %q\n", action, key)
			continue
		}
		fmt.Fprintf(&b, "# %s = %q  # %s\n", action, key, comment)
	}
	return b.String()
}

// actionDoc is a one-line reminder next to each key in the sample file.
var actionDoc = map[Action]string{
	ActionRun:               "apply: run the selection, or the statement under the cursor",
	ActionComplete:          "complete a keyword, relation, column or alias",
	ActionHistory:           "query history",
	ActionHelp:              "help; F1 works too, unless the terminal keeps it",
	ActionSaveProfile:       "save the current connection as a profile",
	ActionImportProfiles:    "import from a ~/.mongo* file: picker — URIs as profiles; tree on Users — create/update a user",
	ActionConnect:           "back to the connection picker",
	ActionRefresh:           "reload the selected tree node",
	ActionFocus:             "focus next: tree, result, editor",
	ActionFocusBack:         "focus previous",
	ActionPanes:             "next pane: RESULT, OBJECT, MESSAGES",
	ActionPanesBack:         "previous pane",
	ActionLeftWider:         "wider object tree",
	ActionLeftNarrower:      "narrower object tree",
	ActionResultTaller:      "taller result pane",
	ActionResultShorter:     "shorter result pane",
	ActionQuit:              "quit",
	ActionCancel:            "cancel the running statement, then quit",
	ActionDeleteStatement:   "delete the statement under the cursor",
	ActionEditStatement:     "make an executed statement editable again",
	ActionCopyCell:          "copy the cell under the cursor",
	ActionCopyRow:           "copy the whole row as TSV",
	ActionLoadMore:          "RESULT: append the next page of the peeked query",
	ActionLoadAll:           "RESULT: load until the result row/byte limits",
	ActionServerSearch:      "RESULT: find documents by the cursor cell value",
	ActionServerSearchRegex: "RESULT: regexp find by the cursor cell value",
	ActionSelectTemplate:    "table or view: insert a SELECT template (like / in less)",
	ActionPeek:              "table or view: peek 30 rows; load_more/load_all extend it",
	ActionEditObject:        "table, view or role: put an editable definition in the editor",
	ActionInsertName:        "insert the selected name into the editor, focus stays in the tree",
	ActionCreateTemplate:    "RESULT: insertOne template for the open collection; tree: create skeleton",
	ActionDropTemplate:      "DROP skeleton for the selected object",
	ActionAlterTemplate:     "ALTER skeleton for the selected object",
}

// EnsureFile writes the commented sample next to the state file when the user
// has no config yet, so the keys are discoverable without reading the source.
func EnsureFile(path string) error {
	if _, err := os.Stat(path); err == nil {
		return nil
	} else if !errors.Is(err, os.ErrNotExist) {
		return err
	}
	if err := EnsurePrivateDir(filepath.Dir(path)); err != nil {
		return err
	}
	return os.WriteFile(path, []byte(SampleFile()), 0o600)
}
