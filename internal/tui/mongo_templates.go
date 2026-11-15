package tui

import (
	"fmt"
	"strconv"
	"strings"
	"time"
	"unicode"
)

// ddlOperation is the template family inserted from the tree.
type ddlOperation int

const (
	ddlCreate ddlOperation = iota
	ddlDrop
	ddlAlter
)

func ddlOperationName(op ddlOperation) string {
	switch op {
	case ddlCreate:
		return "create"
	case ddlDrop:
		return "drop"
	case ddlAlter:
		return "alter"
	}
	return "unknown"
}

// mongoCollectionRef returns db.<name> or db["name"] for names that are not
// plain JavaScript identifiers.
func mongoCollectionRef(name string) string {
	if isPlainMongoName(name) {
		return "db." + name
	}
	return "db[" + strconv.Quote(name) + "]"
}

func isPlainMongoName(name string) bool {
	if name == "" {
		return false
	}
	for i, r := range name {
		if i == 0 {
			if r != '_' && !unicode.IsLetter(r) {
				return false
			}
			continue
		}
		if r != '_' && !unicode.IsLetter(r) && !unicode.IsDigit(r) {
			return false
		}
	}
	return true
}

// mongoNodeInCurrentDB reports whether an MQL template would target the right
// database. MQL has no database qualifier; the user switches with Enter.
func (m Model) mongoNodeInCurrentDB(node *treeNode) bool {
	if node.schema == "" {
		return true
	}
	return strings.EqualFold(node.schema, m.currentDatabase())
}

// insertMongoTemplate turns +, - and = on a MongoDB node into MQL.
func (m Model) insertMongoTemplate(node *treeNode, op ddlOperation) Model {
	if node == nil {
		m.status = "no node selected"
		return m
	}
	switch op {
	case ddlCreate:
		switch node.kind {
		case nodeFolder:
			// "+" on a database's Collections folder adds a collection there,
			// which is the same new-document template as on the database node.
			if node.title != "Collections" {
				break
			}
			if !strings.EqualFold(node.schema, m.currentDatabase()) {
				m.status = "switch to " + node.schema + " first"
				return m
			}
			return m.insertStatementAfter(fmt.Sprintf(
				`db.new_collection.insertOne({createdAt: ISODate(%q)})`,
				time.Now().UTC().Format(time.RFC3339)), "new_collection")
		case nodeDatabase:
			if !strings.EqualFold(node.title, m.currentDatabase()) {
				m.status = "switch to " + node.title + " first"
				return m
			}
			// A document creates its collection on the first insert, so this is
			// how a new collection is added. createdAt is the current UTC time;
			// _id is left to MongoDB. The cursor lands after the collection name.
			return m.insertStatementAfter(fmt.Sprintf(
				`db.new_collection.insertOne({createdAt: ISODate(%q)})`,
				time.Now().UTC().Format(time.RFC3339)), "new_collection")
		case nodeCollection:
			if !m.mongoNodeInCurrentDB(node) {
				m.status = "switch to " + node.schema + " first"
				return m
			}
			return m.insertStatementAfter(fmt.Sprintf(
				`%s.createIndex({field: 1}, {name: "field_1"})`, mongoCollectionRef(node.name)), "field")
		}
	case ddlDrop:
		switch node.kind {
		case nodeCollection:
			if !m.mongoNodeInCurrentDB(node) {
				m.status = "switch to " + node.schema + " first"
				return m
			}
			return m.insertStatement(fmt.Sprintf(`%s.drop()`, mongoCollectionRef(node.name)))
		case nodeIndex:
			if !m.mongoNodeInCurrentDB(node) {
				m.status = "switch to " + node.schema + " first"
				return m
			}
			return m.insertStatement(fmt.Sprintf(
				`%s.dropIndex(%q)`, mongoCollectionRef(node.name), node.title))
		}
	case ddlAlter:
		if node.kind == nodeCollection {
			if !m.mongoNodeInCurrentDB(node) {
				m.status = "switch to " + node.schema + " first"
				return m
			}
			return m.insertStatement(fmt.Sprintf(
				`%s.collMod({validator: {$jsonSchema: {}}})`, mongoCollectionRef(node.name)))
		}
	}
	m.status = "no " + ddlOperationName(op) + " template for this node"
	return m
}
