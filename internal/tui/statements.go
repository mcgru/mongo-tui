package tui

import (
	"strings"
	"unicode/utf8"
)

// editorStatement is one top-level request in the editor. Start and End are
// byte offsets into the editor value; Text is trimmed.
type editorStatement struct {
	Text  string
	Start int
	End   int
}

// splitEditorStatements is the tolerant splitter used for executed marks,
// locking and cursor movement. It splits on top-level semicolons and keeps
// semicolons inside strings, comments, objects, arrays and calls. An
// unbalanced tail is treated as one final statement; query validation reports
// the real error before sending.
func splitEditorStatements(text string) []editorStatement {
	bytes := []byte(text)
	var out []editorStatement
	start := 0
	depth := 0
	quote := byte(0)
	escaped := false
	lineComment := false
	blockComment := false
	i := 0
	appendPart := func(end int) {
		part := strings.TrimSpace(text[start:end])
		if part == "" {
			start = end + 1
			return
		}
		trimmedStart := start + len(text[start:end]) - len(strings.TrimLeft(text[start:end], " \t\r\n"))
		trimmedEnd := end - (len(text[start:end]) - len(strings.TrimRight(text[start:end], " \t\r\n")))
		out = append(out, editorStatement{Text: part, Start: trimmedStart, End: trimmedEnd})
		start = end + 1
	}
	for i < len(bytes) {
		c := bytes[i]
		if lineComment {
			if c == '\n' {
				lineComment = false
			}
			i++
			continue
		}
		if blockComment {
			if c == '*' && i+1 < len(bytes) && bytes[i+1] == '/' {
				blockComment = false
				i += 2
				continue
			}
			i++
			continue
		}
		if quote != 0 {
			if escaped {
				escaped = false
				i++
				continue
			}
			if c == '\\' {
				escaped = true
				i++
				continue
			}
			if c == quote {
				quote = 0
			}
			i++
			continue
		}
		if c == '"' || c == '\'' {
			quote = c
			i++
			continue
		}
		if c == '/' && i+1 < len(bytes) {
			if bytes[i+1] == '/' {
				lineComment = true
				i += 2
				continue
			}
			if bytes[i+1] == '*' {
				blockComment = true
				i += 2
				continue
			}
		}
		switch c {
		case '{', '[', '(':
			depth++
		case '}', ']', ')':
			if depth > 0 {
				depth--
			}
		case ';':
			if depth == 0 {
				appendPart(i)
			}
		}
		_, size := utf8.DecodeRune(bytes[i:])
		i += size
	}
	appendPart(len(bytes))
	return out
}
