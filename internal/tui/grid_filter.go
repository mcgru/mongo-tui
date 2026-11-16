package tui

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"mongo-tui/internal/config"
	"mongo-tui/internal/engine"
)

var (
	errFilterGrid   = errors.New("server filter is for the RESULT grid")
	errFilterSource = errors.New("run a find on a collection first")
	errFilterCell   = errors.New("no cell under the cursor")
)

// filterSpec is a ready filter statement and where the editor cursor goes.
type filterSpec struct {
	statement string
	cursor    int // rune offset right after the inserted value
	column    string
	scope     string // db.collection, fed to completion
	warn      string
}

// openServerFilter turns the cursor cell into a find for the same collection:
// Ctrl+F keeps the value as it is, Ctrl+Alt+F wraps it in a quoted regexp.
func (m Model) openServerFilter(regex bool) (tea.Model, tea.Cmd) {
	spec, err := m.buildServerFilter(regex)
	if err != nil {
		m.status = err.Error()
		return m, nil
	}

	m.editor.SetValue(spec.statement)
	m.editor.ClearMarks()
	m.editor.SetCursorOffset(spec.cursor)
	m.focus = focusEditor
	m.editor.Focus()
	m.scopeCollection = spec.scope

	kind := "filter"
	if regex {
		kind = "regexp"
	}
	m.status = kind + " " + spec.column + " · " + m.keys.Get(config.ActionRun) + " to run"
	if spec.warn != "" {
		m.status += " · " + spec.warn
	}
	return m, nil
}

// buildServerFilter validates the context and assembles the MQL statement. The
// cursor lands right after the value, so the command is runnable as inserted.
func (m Model) buildServerFilter(regex bool) (filterSpec, error) {
	if m.focus != focusGrid || m.tab != tabResult {
		return filterSpec{}, errFilterGrid
	}
	if m.grid == nil || m.grid.empty() || len(m.grid.res.Documents) == 0 {
		return filterSpec{}, errFilterSource
	}
	res := m.grid.res
	if res.Collection == "" {
		return filterSpec{}, errFilterSource
	}

	database := res.Database
	if database == "" {
		database = m.currentDatabase()
	}
	if database != m.currentDatabase() {
		return filterSpec{}, fmt.Errorf("switch to %s first", database)
	}

	column, typ, ok := m.grid.currentColumn()
	if !ok {
		return filterSpec{}, errFilterCell
	}
	raw, ok := m.grid.currentCellText()
	if !ok {
		return filterSpec{}, errFilterCell
	}

	literal, cursor, warn := filterLiteral(raw, typ, m.grid.currentDocument(), column, regex, m.searchIgnoreCase)
	prefix := fmt.Sprintf("%s.find({ %s: ", mongoCollectionRef(res.Collection), filterFieldKey(column))
	statement := prefix + literal + " })"
	return filterSpec{
		statement: statement,
		cursor:    len([]rune(prefix)) + cursor,
		column:    column,
		scope:     database + "." + res.Collection,
		warn:      warn,
	}, nil
}

// filterLiteral renders one cell as an MQL literal. It prefers the typed value
// from the full document and falls back to the visible text and column type.
// The returned offset is the rune distance into the literal where the cursor
// belongs.
func filterLiteral(raw, typ string, document []byte, column string, regex, ignoreCase bool) (string, int, string) {
	text := raw
	switch raw {
	case engine.NullSentinel:
		text = "NULL"
	case engine.MissingSentinel:
		text = ""
	}

	if regex {
		pattern := text
		if raw == engine.NullSentinel || raw == engine.MissingSentinel {
			pattern = ""
		}
		quoted := strconv.Quote(regexp.QuoteMeta(pattern))
		literal := "{$regex: " + quoted
		if ignoreCase {
			literal += `, $options: "i"`
		}
		literal += "}"
		cursor := len([]rune(literal))
		if raw == engine.MissingSentinel {
			cursor = len([]rune("{$regex: "))
		}
		return literal, cursor, regexWarn(typ, raw)
	}

	switch raw {
	case engine.NullSentinel:
		return "null", len("null"), ""
	case engine.MissingSentinel:
		// No value to insert: leave the cursor between the quotes to type one.
		return `""`, 1, "cell has no value"
	}

	if value, ok := documentValue(document, column); ok {
		if literal, ok := literalFromValue(value); ok {
			return literal, len([]rune(literal)), ""
		}
	}
	literal := literalFromText(text, typ)
	return literal, len([]rune(literal)), ""
}

// documentValue walks a dotted column path through the row's EJSON document.
func documentValue(raw []byte, path string) (any, bool) {
	if len(raw) == 0 {
		return nil, false
	}
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.UseNumber()
	var document any
	if err := dec.Decode(&document); err != nil {
		return nil, false
	}
	current := document
	for _, part := range strings.Split(path, ".") {
		object, ok := current.(map[string]any)
		if !ok {
			return nil, false
		}
		current, ok = object[part]
		if !ok {
			return nil, false
		}
	}
	return current, true
}

// literalFromValue renders a value decoded from relaxed EJSON. Values the MQL
// parser cannot express (binary, timestamps, regexes, plain subdocuments) are
// rejected so the caller can fall back to the visible text.
func literalFromValue(value any) (string, bool) {
	switch v := value.(type) {
	case nil:
		return "null", true
	case string:
		return strconv.Quote(v), true
	case bool:
		return strconv.FormatBool(v), true
	case json.Number:
		return v.String(), true
	case []any:
		parts := make([]string, 0, len(v))
		for _, item := range v {
			literal, ok := literalFromValue(item)
			if !ok {
				return "", false
			}
			parts = append(parts, literal)
		}
		return "[" + strings.Join(parts, ", ") + "]", true
	case map[string]any:
		if len(v) != 1 {
			return "", false
		}
		for key, val := range v {
			switch key {
			case "$oid":
				if s, ok := val.(string); ok && s != "" {
					return "ObjectId(" + strconv.Quote(s) + ")", true
				}
			case "$numberDecimal":
				if s, ok := val.(string); ok {
					return "NumberDecimal(" + strconv.Quote(s) + ")", true
				}
			case "$numberLong":
				if s, ok := val.(string); ok {
					return "NumberLong(" + strconv.Quote(s) + ")", true
				}
			case "$date":
				switch date := val.(type) {
				case string:
					return "ISODate(" + strconv.Quote(date) + ")", true
				case map[string]any:
					if ms, ok := date["$numberLong"].(string); ok {
						if n, err := strconv.ParseInt(ms, 10, 64); err == nil {
							stamp := time.UnixMilli(n).UTC().Format(time.RFC3339Nano)
							return "ISODate(" + strconv.Quote(stamp) + ")", true
						}
					}
				}
			}
		}
	}
	return "", false
}

// literalFromText is the fallback when the full document is unavailable or holds
// a type the MQL parser cannot express. Complex values stay quoted, so the user
// can adjust the filter instead of getting an invalid template.
func literalFromText(text, typ string) string {
	switch typ {
	case "string":
		return strconv.Quote(text)
	case "int", "double":
		if isNumberLiteral(text) {
			return text
		}
		return strconv.Quote(text)
	case "long":
		return "NumberLong(" + strconv.Quote(text) + ")"
	case "decimal":
		return "NumberDecimal(" + strconv.Quote(text) + ")"
	case "bool":
		if text == "true" || text == "false" {
			return text
		}
		return strconv.Quote(text)
	case "objectId":
		if hex, ok := objectIDHex(text); ok {
			return "ObjectId(" + strconv.Quote(hex) + ")"
		}
		return strconv.Quote(text)
	case "date":
		return "ISODate(" + strconv.Quote(text) + ")"
	}
	return strconv.Quote(text)
}

func isNumberLiteral(text string) bool {
	if text == "" {
		return false
	}
	_, err := strconv.ParseFloat(text, 64)
	return err == nil
}

// objectIDHex unwraps the grid rendering ObjectID("hex") back to its hex.
func objectIDHex(text string) (string, bool) {
	const prefix = `ObjectID("`
	if !strings.HasPrefix(text, prefix) || !strings.HasSuffix(text, `")`) {
		return "", false
	}
	hex := text[len(prefix) : len(text)-2]
	if len(hex) != 24 {
		return "", false
	}
	return hex, true
}

// filterFieldKey leaves plain ASCII identifiers bare and quotes everything else,
// including dotted paths such as "meta.color".
func filterFieldKey(name string) string {
	if name == "" {
		return strconv.Quote(name)
	}
	for i, r := range name {
		if i == 0 {
			if r != '_' && !isASCIILetter(r) {
				return strconv.Quote(name)
			}
			continue
		}
		if r != '_' && !isASCIILetter(r) && !(r >= '0' && r <= '9') {
			return strconv.Quote(name)
		}
	}
	return name
}

func isASCIILetter(r rune) bool {
	return r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z'
}

func regexWarn(typ, raw string) string {
	if raw == engine.NullSentinel || raw == engine.MissingSentinel {
		return "no value to search"
	}
	switch typ {
	case "", "string", "mixed":
		return ""
	}
	return "regexp on a non-string column"
}
