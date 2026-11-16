package tui

import (
	"fmt"
	"strings"
	"testing"

	"github.com/charmbracelet/lipgloss"

	"mongo-tui/internal/engine"
)

// A single overlong line used to wrap inside the pane, adding a row and
// scrolling the header off the screen when the tree list was long.
func TestPaneClipsOverlongLines(t *testing.T) {
	m := sized(New(testOptions()))

	lines := []string{
		strings.Repeat("x", 200),
		activeTitle.Render(strings.Repeat("y", 200)),
	}
	box := m.pane("TITLE", lines, 24, 10, true)

	if got := lipgloss.Height(box); got != 10 {
		t.Fatalf("pane height = %d, want 10\n%s", got, box)
	}
	for i, line := range strings.Split(box, "\n") {
		if got := lipgloss.Width(line); got > 24 {
			t.Fatalf("line %d width = %d, want <= 24: %q", i, got, line)
		}
	}
}

func TestLongTreeRowsDoNotGrowLayout(t *testing.T) {
	fake := &fakeMongoCatalog{
		fakeSession: &fakeSession{info: engine.Info{ServerVersion: "8.0.38", Database: "app"}},
	}
	m := mongoModel(t, fake)
	m.focus = focusTree

	// Many rows with details wider than the tree pane used to wrap, grow the
	// left pane and push the header out of the window.
	for i := 0; i < 40; i++ {
		m.tree.visible = append(m.tree.visible, &treeNode{
			kind:   nodeDatabase,
			title:  fmt.Sprintf("db%02d", i),
			depth:  1,
			detail: "1.25 MiB · a detail long enough to overflow the narrow tree pane",
		})
	}
	m.tree.cursor = len(m.tree.visible) - 1

	view := m.View()
	if got := lipgloss.Height(view); got != m.height {
		t.Fatalf("view height = %d, want %d (the header must stay on screen)", got, m.height)
	}
	if !strings.Contains(view, "mongo-tui") {
		t.Fatalf("header is missing from the view:\n%s", view)
	}
}
