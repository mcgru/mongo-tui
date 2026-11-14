package query

import (
	"fmt"
	"strings"
)

// SplitRequests splits a Mongo editor buffer into requests on top-level
// semicolons. Semicolons inside strings, comments, objects, arrays and calls
// stay part of the current request. The caller validates each request with
// ParseRequest before sending it.
func SplitRequests(text string) ([]string, error) {
	runes := []rune(text)
	var (
		parts                     []string
		start                     int
		braces, brackets, parens  int
		quote                     rune
		escaped                   bool
		lineComment, blockComment bool
	)
	flush := func(end int) {
		part := strings.TrimSpace(string(runes[start:end]))
		if part != "" {
			parts = append(parts, part)
		}
		start = end + 1
	}

	for i := 0; i < len(runes); i++ {
		c := runes[i]
		if lineComment {
			if c == '\n' {
				lineComment = false
			}
			continue
		}
		if blockComment {
			if c == '*' && i+1 < len(runes) && runes[i+1] == '/' {
				blockComment = false
				i++
			}
			continue
		}
		if quote != 0 {
			if escaped {
				escaped = false
				continue
			}
			if c == '\\' {
				escaped = true
				continue
			}
			if c == quote {
				quote = 0
			}
			continue
		}
		if c == '"' || c == '\'' {
			quote = c
			continue
		}
		if c == '/' && i+1 < len(runes) {
			switch runes[i+1] {
			case '/':
				lineComment = true
				i++
				continue
			case '*':
				blockComment = true
				i++
				continue
			}
		}
		switch c {
		case '{':
			braces++
		case '}':
			if braces == 0 {
				return nil, fmt.Errorf("unexpected } at %d", i)
			}
			braces--
		case '[':
			brackets++
		case ']':
			if brackets == 0 {
				return nil, fmt.Errorf("unexpected ] at %d", i)
			}
			brackets--
		case '(':
			parens++
		case ')':
			if parens == 0 {
				return nil, fmt.Errorf("unexpected ) at %d", i)
			}
			parens--
		case ';':
			if braces == 0 && brackets == 0 && parens == 0 {
				flush(i)
			}
		}
	}
	if quote != 0 {
		return nil, fmt.Errorf("unterminated string")
	}
	if blockComment {
		return nil, fmt.Errorf("unterminated block comment")
	}
	flush(len(runes))
	return parts, nil
}
