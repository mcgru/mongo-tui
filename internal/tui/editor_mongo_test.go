package tui

import (
	"strings"
	"testing"
)

func TestHighlightMongoBuffer(t *testing.T) {
	text := `db.users.find({age: 30, name: "Ann", $gt: null, ok: true}) // comment`
	styles := highlightMongoBuffer([]rune(text))

	check := func(fragment string, want synKind) {
		t.Helper()
		idx := strings.Index(text, fragment)
		if idx < 0 || idx >= len(styles) {
			t.Fatalf("fragment %q not found", fragment)
		}
		if styles[idx] != want {
			t.Fatalf("%q style = %v, want %v", fragment, styles[idx], want)
		}
	}
	check("db", synKeywordKind)
	check("find", synKeywordKind)
	check("users", synPlain)
	check("30", synNumberKind)
	check(`"Ann"`, synStringKind)
	check("$gt", synKeywordKind)
	check("null", synKeywordKind)
	check("true", synKeywordKind)
	check("// comment", synCommentKind)
	check("{", synPunctKind)
}
