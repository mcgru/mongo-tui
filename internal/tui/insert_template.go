package tui

import (
	"fmt"
	"sort"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"
)

// docField is one field offered by the insert template.
type docField struct {
	path string
	typ  string
}

// docNode is a document rendered from dotted field paths.
type docNode struct {
	name         string
	typ          string
	literal      string
	auto         bool
	cursorInside bool
	children     []*docNode
	index        map[string]*docNode
}

func (n *docNode) child(name string) *docNode {
	if n.index == nil {
		n.index = map[string]*docNode{}
	}
	if existing, ok := n.index[name]; ok {
		return existing
	}
	child := &docNode{name: name}
	n.index[name] = child
	n.children = append(n.children, child)
	return child
}

// insertResultDocument turns "+" in RESULT into an insertOne for the collection
// the grid came from: every known field gets a typed placeholder, createdAt is
// filled with the current time, and the cursor lands on the first empty value.
func (m Model) insertResultDocument() Model {
	if m.focus != focusGrid || m.tab != tabResult {
		m.status = "insert template is for the RESULT grid"
		return m
	}
	database, collection := m.resultSource()
	if collection == "" {
		m.status = "run a find on a collection first"
		return m
	}
	if database != m.currentDatabase() {
		m.status = "switch to " + database + " first"
		return m
	}

	fields, skippedID := m.insertFields(database, collection)
	statement, offset, count := buildInsertStatement(collection, fields)
	m = m.insertStatementAt(statement, offset)
	if count == 0 {
		m.status = "insert template · no fields"
	} else {
		m.status = fmt.Sprintf("insert template · %d fields", count)
	}
	if skippedID {
		m.status += " · _id server-assigned"
	}
	return m
}

// insertFields collects the fields of the collection from the grid (which has
// the observed types) and tops them up from the card or completion snapshot so
// a projected result still offers every known field.
func (m Model) insertFields(database, collection string) ([]docField, bool) {
	fields := make([]docField, 0, 8)
	seen := make(map[string]bool)
	skippedID := false

	add := func(name, typ string) {
		name = strings.TrimSpace(name)
		if name == "" || seen[name] {
			return
		}
		if strings.HasPrefix(name, "_id.") {
			return
		}
		if name == "_id" {
			// The server assigns the usual ObjectId. A custom _id (string,
			// number, ...) has to be typed by the user, so it gets a field.
			switch typ {
			case "", "objectId", "mixed":
				seen[name] = true
				skippedID = true
				return
			}
		}
		seen[name] = true
		fields = append(fields, docField{path: name, typ: typ})
	}

	if m.grid != nil && m.grid.res != nil {
		for i, name := range m.grid.res.Columns {
			typ := ""
			if i < len(m.grid.res.ColumnTypes) {
				typ = m.grid.res.ColumnTypes[i]
			}
			add(name, typ)
		}
	}
	if card := m.mongoCard; card != nil && card.Database == database && card.Collection == collection {
		for _, field := range card.Fields {
			add(field.Name, field.Types)
		}
	} else if m.mongoSnap != nil {
		for _, name := range m.mongoSnap.fields[database+"."+collection] {
			add(name, "")
		}
	}
	return fields, skippedID
}

// buildInsertStatement renders db.coll.insertOne({...}) and returns the rune
// offset of the first empty value.
func buildInsertStatement(collection string, fields []docField) (string, int, int) {
	now := time.Now().UTC()
	fields = orderInsertFields(fields)
	root := &docNode{}

	for _, field := range fields {
		parts := strings.Split(field.path, ".")
		node := root
		for _, part := range parts {
			node = node.child(part)
		}
		node.typ = field.typ
		if len(parts) == 1 && parts[0] == "createdAt" {
			node.auto = true
		}
	}

	leaves := make([]*docNode, 0, len(fields))
	var fill func(nodes []*docNode)
	fill = func(nodes []*docNode) {
		for _, node := range nodes {
			if len(node.children) > 0 {
				fill(node.children)
				continue
			}
			if node.auto {
				node.literal = createdAtLiteral(node.typ, now)
			} else {
				node.literal, node.cursorInside = placeholderForType(node.typ)
			}
			leaves = append(leaves, node)
		}
	}
	fill(root.children)

	// Prefer the first empty string: that is the field the user most likely
	// wants to type into. Otherwise land on the first placeholder.
	var target, fallback *docNode
	for _, node := range leaves {
		if node.auto {
			continue
		}
		if fallback == nil {
			fallback = node
		}
		if node.literal == `""` {
			target = node
			break
		}
	}
	if target == nil {
		target = fallback
	}

	var body strings.Builder
	pos := 0
	targetPos := -1
	var render func(nodes []*docNode)
	render = func(nodes []*docNode) {
		body.WriteByte('{')
		pos++
		for i, node := range nodes {
			if i > 0 {
				body.WriteString(", ")
				pos += 2
			}
			key := filterFieldKey(node.name)
			body.WriteString(key)
			pos += utf8.RuneCountInString(key)
			body.WriteString(": ")
			pos += 2
			if len(node.children) > 0 {
				render(node.children)
				continue
			}
			if node == target {
				if node.cursorInside {
					targetPos = pos + 1 // between the quotes of ""
				} else {
					targetPos = pos + utf8.RuneCountInString(node.literal)
				}
			}
			body.WriteString(node.literal)
			pos += utf8.RuneCountInString(node.literal)
		}
		body.WriteByte('}')
		pos++
	}
	render(root.children)

	if targetPos < 0 {
		// Nothing to fill: leave the cursor inside the document to add a field.
		if len(leaves) == 0 {
			targetPos = 1
		} else {
			targetPos = pos - 1
		}
	}

	ref := mongoCollectionRef(collection)
	prefix := ref + ".insertOne("
	return prefix + body.String() + ")", utf8.RuneCountInString(prefix) + targetPos, len(leaves)
}

// orderInsertFields puts the auto values first: a custom _id, then createdAt,
// then the fields the user has to fill, keeping the observed order otherwise.
func orderInsertFields(fields []docField) []docField {
	rank := func(path string) int {
		switch path {
		case "_id":
			return 0
		case "createdAt":
			return 1
		}
		return 2
	}
	sort.SliceStable(fields, func(i, j int) bool {
		return rank(fields[i].path) < rank(fields[j].path)
	})
	return fields
}

// placeholderForType is a valid skeleton value per column type; cursorInside is
// true when the cursor belongs between the opening and closing quote.
func placeholderForType(typ string) (literal string, cursorInside bool) {
	switch typ {
	case "", "string", "mixed", "index", "validator":
		return `""`, true
	case "int", "long", "double", "decimal":
		return "0", false
	case "bool":
		return "false", false
	case "array":
		return "[]", false
	case "object":
		return "{}", false
	}
	return "null", false
}

// createdAtLiteral auto-fills createdAt with the current UTC time in the shape
// the field already uses.
func createdAtLiteral(typ string, now time.Time) string {
	switch typ {
	case "string":
		return strconv.Quote(now.Format(time.RFC3339))
	case "int", "long", "double", "decimal":
		return strconv.FormatInt(now.Unix(), 10)
	}
	return "ISODate(" + strconv.Quote(now.Format(time.RFC3339)) + ")"
}
