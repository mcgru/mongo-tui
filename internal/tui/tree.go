package tui

import (
	"context"
	"strings"

	"mongo-tui/internal/engine"
)

// nodeKind is what a tree row represents.
type nodeKind int

const (
	nodeRoot nodeKind = iota
	nodeFolder
	nodeDatabase
	nodeCollection
	nodeIndex
	nodeUser
	nodeMongoRole
)

// treeNode is one row of the object tree. Children are loaded lazily: a heavy
// catalog query at startup would block the whole UI.
type treeNode struct {
	kind   nodeKind
	title  string
	detail string
	depth  int

	children []*treeNode
	expanded bool
	loaded   bool
	loading  bool
	err      string

	// current marks the database the session is connected to.
	current bool

	// pending marks a database staged client-side (use before any document is
	// written): it is not on the server yet, so the tree paints it faded pink
	// until MongoDB starts listing it.
	pending bool

	// Identity for the object card and for MQL templates.
	schema string
	name   string
	// openable marks rows that Enter opens in the object pane.
	openable bool

	load func(context.Context) ([]*treeNode, error)
}

// tree flattens the node hierarchy for rendering and keeps the cursor.
type tree struct {
	root    *treeNode
	visible []*treeNode
	cursor  int
	off     int
}

func newTree(root *treeNode) *tree {
	t := &tree{root: root}
	t.rebuild()
	return t
}

func (t *tree) rebuild() {
	// Remember the selected node: after expanding, the flat index points at a
	// different row, and the cursor must stay where the user left it.
	var selected *treeNode
	if t.cursor >= 0 && t.cursor < len(t.visible) {
		selected = t.visible[t.cursor]
	}

	t.visible = t.visible[:0]
	var walk func(n *treeNode, depth int)
	walk = func(n *treeNode, depth int) {
		n.depth = depth
		t.visible = append(t.visible, n)
		if !n.expanded {
			return
		}
		for _, child := range n.children {
			walk(child, depth+1)
		}
	}
	walk(t.root, 0)

	if selected != nil {
		for i, node := range t.visible {
			if node == selected {
				t.cursor = i
				return
			}
		}
	}
	t.cursor = clampInt(t.cursor, 0, maxInt(0, len(t.visible)-1))
}

func (t *tree) current() *treeNode {
	if t.cursor < 0 || t.cursor >= len(t.visible) {
		return nil
	}
	return t.visible[t.cursor]
}

func (t *tree) move(delta int) {
	if len(t.visible) == 0 {
		return
	}
	t.cursor = clampInt(t.cursor+delta, 0, len(t.visible)-1)
}

// toggle expands or collapses the current node and returns the node that needs
// its children loaded, if any.
func (t *tree) toggle() *treeNode {
	node := t.current()
	if node == nil {
		return nil
	}
	if len(node.children) == 0 && node.load == nil {
		return nil
	}
	if node.expanded {
		node.expanded = false
		t.rebuild()
		return nil
	}

	node.expanded = true
	t.rebuild()

	// Only nodes with a loader need a fetch. The root has prebuilt children
	// and no loader, so returning it here would call a nil load function.
	if node.load == nil || node.loaded || node.loading {
		return nil
	}
	node.loading = true
	return node
}

// home jumps to the first row. When the cursor is already there it folds every
// expanded node below the root, so a second Home goes up one level.
func (t *tree) home() bool {
	if t.cursor > 0 {
		t.cursor = 0
		return false
	}
	folded := false
	var walk func(n *treeNode)
	walk = func(n *treeNode) {
		for _, child := range n.children {
			if child.expanded {
				child.expanded = false
				folded = true
			}
			walk(child)
		}
	}
	if t.root != nil {
		walk(t.root)
	}
	t.rebuild()
	t.cursor = 0
	return folded
}

// end jumps to the last visible row.
func (t *tree) end() {
	if len(t.visible) == 0 {
		return
	}
	t.cursor = len(t.visible) - 1
}

// pathTo returns the titles from the top-level folder down to the node, which is
// what a later restore walks through.
func (t *tree) pathTo(target *treeNode) []string {
	if t == nil || t.root == nil || target == nil {
		return nil
	}
	var walk func(n *treeNode, acc []string) []string
	walk = func(n *treeNode, acc []string) []string {
		if n == target {
			if n == t.root {
				return nil
			}
			return acc
		}
		for _, child := range n.children {
			next := append(append([]string(nil), acc...), child.title)
			if found := walk(child, next); found != nil {
				return found
			}
		}
		return nil
	}
	return walk(t.root, nil)
}

// collapse folds the current node, or jumps to the parent when it is already
// folded.
func (t *tree) collapse() {
	node := t.current()
	if node == nil {
		return
	}
	if node.expanded {
		node.expanded = false
		t.rebuild()
		return
	}
	for i := t.cursor - 1; i >= 0; i-- {
		if t.visible[i].depth < node.depth {
			t.cursor = i
			return
		}
	}
}

// lines renders the visible part of the tree. openObject is "schema.name" of the
// relation whose card is on screen, if any.
func (t *tree) lines(width, height int, openObject string) []string {
	if t == nil || width <= 0 || height <= 0 {
		return nil
	}
	if t.cursor < t.off {
		t.off = t.cursor
	}
	// Keep one row of margin below the cursor so the next level is visible.
	if t.cursor >= t.off+height-1 {
		t.off = t.cursor - height + 2
	}
	if maxOff := maxInt(0, len(t.visible)-height); t.off > maxOff {
		t.off = maxOff
	}

	out := make([]string, 0, height)
	for i := t.off; i < len(t.visible) && len(out) < height; i++ {
		node := t.visible[i]
		open := openObject != "" && nodeKey(node) == openObject
		out = append(out, renderNodeLine(node, i == t.cursor, open, node.current, width))
	}
	return out
}

// renderNodeLine draws one row. The head is truncated before any styling is
// applied: cutting styled text would slice an escape sequence and push the line
// past the pane width, which is what used to wrap long details into the border.
func nodeKey(node *treeNode) string {
	if node == nil || node.name == "" {
		return ""
	}
	if node.kind == nodeIndex {
		// An index is not the collection card; keep it from lighting up as open.
		return node.schema + "." + node.name + "#" + node.title
	}
	return node.schema + "." + node.name
}

func renderNodeLine(node *treeNode, selected, open, chosen bool, width int) string {
	if width <= 0 {
		return ""
	}

	prefix := "  "
	if selected {
		prefix = "› "
	}
	indent := strings.Repeat("  ", node.depth)

	marker := "·"
	switch {
	case open:
		marker = "◆"
	case node.expanded:
		marker = "▾"
	case node.load != nil || len(node.children) > 0:
		marker = "▸"
	}

	head := prefix + indent + marker + " " + node.title

	remaining := width - displayWidth(head)
	if remaining <= 0 {
		head = truncate(head, width)
		remaining = 0
	}

	extras := ""
	switch {
	case node.err != "":
		extras = " [" + node.err + "]"
	case node.loading:
		extras = " loading…"
	case node.detail != "":
		extras = " " + node.detail
	}

	if remaining > 0 {
		extras = truncate(extras, remaining)
	} else {
		extras = ""
	}

	extrasView := ""
	if extras != "" {
		extrasView = dimStyle.Render(extras)
	}

	plainHead := prefix + indent + marker + " " + node.title

	switch {
	case selected:
		return activeTitle.Render(head) + extrasView
	case node.pending:
		return pendingDBStyle.Render(plainHead) + extrasView
	case open:
		return openObjectStyle.Render(plainHead) + extrasView
	case chosen:
		return currentDBStyle.Render(plainHead) + extrasView
	}
	return head + extrasView
}

// buildTree wires the MongoDB catalog loaders.
func (m *Model) buildTree() { m.buildMongoTree() }

// buildMongoTree wires the MongoDB catalog loaders. The workbench is usable
// even when the catalog needs privileges the user does not have: the error is
// shown in the node instead of hiding the editor.
func (m *Model) buildMongoTree() {
	if m.sess == nil {
		m.tree = nil
		return
	}
	db := m.sess.Summary().Database
	root := &treeNode{
		kind:     nodeRoot,
		title:    "MongoDB " + db,
		expanded: true,
	}

	cat, ok := m.sess.(engine.Catalog)
	if !ok {
		root.children = []*treeNode{{
			kind:   nodeFolder,
			title:  "Catalog",
			detail: "not available for this session",
		}}
		m.tree = newTree(root)
		m.cursor = 0
		return
	}

	root.children = []*treeNode{
		{kind: nodeFolder, title: "Databases", load: mongoDatabasesLoader(cat)},
		{kind: nodeFolder, title: "Users", load: mongoUsersLoader(cat)},
		{kind: nodeFolder, title: "Roles", load: mongoRolesLoader(cat)},
	}
	m.tree = newTree(root)
	m.cursor = 0
}

// itemNodes turns catalog rows into leaves. Databases are leaves on purpose:
// their schemas are unreachable without switching the session to them.
func itemNodes(items []engine.Item, kind nodeKind) []*treeNode {
	nodes := make([]*treeNode, 0, len(items))
	for _, item := range items {
		nodes = append(nodes, &treeNode{
			kind:    kind,
			title:   item.Name,
			detail:  item.Detail,
			current: item.Current,
		})
	}
	return nodes
}
