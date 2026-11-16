package tui

import (
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"mongo-tui/internal/engine"
)

func driveKey(t *testing.T, m Model, key tea.KeyType) Model {
	t.Helper()
	updated, cmd := m.Update(tea.KeyMsg{Type: key})
	m = updated.(Model)
	for cmd != nil {
		msg := cmd()
		if msg == nil {
			break
		}
		updated, cmd = m.Update(msg)
		m = updated.(Model)
	}
	return m
}

// Expanding the root has no loader to call: it must expand without asking for
// a fetch. This used to return the root from toggle(), and loadNodeCmd then
// called a nil function.
func TestToggleRootWithoutLoaderDoesNotPanic(t *testing.T) {
	root := &treeNode{
		kind:     nodeRoot,
		title:    "MongoDB app",
		children: []*treeNode{{kind: nodeFolder, title: "Databases"}, {kind: nodeFolder, title: "Users"}},
	}
	tr := newTree(root)

	if node := tr.toggle(); node != nil {
		t.Fatalf("expanding the root returned %+v for loading", node)
	}
	if !root.expanded {
		t.Fatal("the root should be expanded")
	}
	if node := tr.toggle(); node != nil {
		t.Fatalf("collapsing the root returned %+v for loading", node)
	}
	if node := tr.toggle(); node != nil {
		t.Fatalf("re-expanding the root returned %+v for loading", node)
	}
}

// Home on the first row folds every level; expanding the top again used to
// crash with a nil loader.
func TestFoldAllThenExpandTopDoesNotPanic(t *testing.T) {
	fake := &fakeMongoCatalog{
		fakeSession: &fakeSession{info: engine.Info{ServerVersion: "8.0.38", Database: "app"}},
		databases: []engine.Item{
			{Name: "app", Current: true},
			{Name: "admin"},
		},
		users: []engine.Item{{Name: "ann", Database: "admin"}},
		roles: []engine.Item{{Name: "read", Database: "admin"}},
	}
	m := mongoModel(t, fake)

	for i, node := range m.tree.visible {
		if node.title == "Databases" {
			m.tree.cursor = i
			break
		}
	}
	m = driveKey(t, m, tea.KeyRight)

	m = driveKey(t, m, tea.KeyHome)  // cursor to the root
	m = driveKey(t, m, tea.KeyHome)  // fold every expanded level
	m = driveKey(t, m, tea.KeyRight) // collapse the root
	m = driveKey(t, m, tea.KeyRight) // expand it again
	m = driveKey(t, m, tea.KeyEnter)
	m = driveKey(t, m, tea.KeyRight)

	if m.tree.current() == nil {
		t.Fatal("the tree lost its cursor")
	}
	if got := lipgloss.Height(m.View()); got != m.height {
		t.Fatalf("view height = %d, want %d", got, m.height)
	}
}

func TestLoadNodeCmdWithoutLoaderIsNil(t *testing.T) {
	m := mongoModel(t, &fakeMongoCatalog{
		fakeSession: &fakeSession{info: engine.Info{ServerVersion: "8.0.38", Database: "app"}},
	})
	if cmd := m.loadNodeCmd(&treeNode{title: "root"}); cmd != nil {
		t.Fatal("loadNodeCmd should return nil when the node has no loader")
	}
	if cmd := m.loadNodeCmd(nil); cmd != nil {
		t.Fatal("loadNodeCmd should return nil for a nil node")
	}
}
