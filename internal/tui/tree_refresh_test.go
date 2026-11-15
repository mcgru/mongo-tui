package tui

import (
	"context"
	"testing"

	"mongo-tui/internal/engine"
)

// A successful write that creates a collection must carry the target database
// and the new collection name so the tree can be reloaded without a re-login.
func TestExecCmdFlagsCollectionWrites(t *testing.T) {
	fake := &fakeMongoCatalog{
		fakeSession: &fakeSession{info: engine.Info{ServerVersion: "8.0.38", Database: "app"}},
	}
	m := mongoModel(t, fake)

	cases := []struct {
		statement  string
		refreshDB  string
		refreshNam string
	}{
		{`db.items.insertOne({a: 1})`, "app", "items"},
		{`db.createCollection("logs")`, "app", "logs"},
		{`db.items.updateOne({a: 1}, {$set: {b: 2}}, {upsert: true})`, "app", "items"},
		// A read does not touch the collection list.
		{`db.items.find({})`, "", ""},
		// An update without upsert does not create a collection.
		{`db.items.updateOne({a: 1}, {$set: {b: 2}})`, "", ""},
	}
	for _, tc := range cases {
		msg := m.runCmd([]string{tc.statement})()
		done, ok := msg.(execDoneMsg)
		if !ok {
			t.Fatalf("runCmd(%q) = %T, want execDoneMsg", tc.statement, msg)
		}
		if done.refreshDB != tc.refreshDB || done.refreshName != tc.refreshNam {
			t.Fatalf("runCmd(%q) refresh = %q/%q, want %q/%q",
				tc.statement, done.refreshDB, done.refreshName, tc.refreshDB, tc.refreshNam)
		}
	}
}

// A database that was staged (provisional) resolves once a write materializes
// it: the tree branch reloads so it shows green and the new collection appears.
func TestRevealAfterWriteResetsStaleCollections(t *testing.T) {
	m := mongoModel(t, &fakeMongoCatalog{
		fakeSession: &fakeSession{info: engine.Info{ServerVersion: "8.0.38", Database: "app"}},
	})

	collections := &treeNode{
		kind:     nodeFolder,
		title:    "Collections",
		schema:   "app",
		loaded:   true,
		expanded: true,
		children: []*treeNode{{kind: nodeCollection, title: "old", schema: "app"}},
		load: func(context.Context) ([]*treeNode, error) {
			return []*treeNode{{kind: nodeCollection, title: "newcoll", schema: "app"}}, nil
		},
	}
	db := &treeNode{
		kind:     nodeDatabase,
		title:    "app",
		loaded:   true,
		expanded: true,
		children: []*treeNode{collections},
	}
	databases := &treeNode{
		kind:     nodeFolder,
		title:    "Databases",
		loaded:   true,
		expanded: true,
		children: []*treeNode{db},
	}
	root := &treeNode{kind: nodeRoot, title: "MongoDB app", expanded: true,
		children: []*treeNode{databases}}
	m.tree = newTree(root)

	updated, cmd := m.revealAfterWrite("app", "newcoll")
	m = updated

	if collections.loaded {
		t.Fatal("Collections folder should be reset for a reload")
	}
	if len(collections.children) != 0 {
		t.Fatalf("Collections children should be cleared, got %d", len(collections.children))
	}
	if cmd == nil {
		t.Fatal("revealAfterWrite should schedule a branch reload")
	}
	if m.pendingWaiting != "Collections" {
		t.Fatalf("pendingWaiting = %q, want Collections", m.pendingWaiting)
	}

	// Drive the scheduled reload; continuePath must navigate onto the new node.
	after, _ := m.Update(cmd())
	m = after.(Model)
	if collections.loaded != true {
		t.Fatal("Collections folder should be reloaded")
	}
	found := false
	for _, child := range collections.children {
		if child.title == "newcoll" {
			found = true
		}
	}
	if !found {
		t.Fatalf("reloaded Collections children = %+v, want newcoll", collections.children)
	}
}

// A provisional database that the write materialized must lose its pink marker:
// revealAfterWrite clears it when it reloads the Databases branch.
func TestRevealAfterWriteClearsProvisional(t *testing.T) {
	m := mongoModel(t, &fakeMongoCatalog{
		fakeSession: &fakeSession{info: engine.Info{ServerVersion: "8.0.38", Database: "app"}},
		databases:   []engine.Item{{Name: "app", Current: true}},
	})

	updated, cmd := m.revealAfterWrite("app", "newcoll")
	m = updated
	if cmd == nil {
		t.Fatal("expected a reload command")
	}
	_ = cmd()
}
