package tui

import (
	"context"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"mongo-tui/internal/engine"
)

type fakeMongoCatalog struct {
	*fakeSession
	databases   []engine.Item
	collections map[string][]engine.Item
	views       map[string][]engine.Item
	indexes     map[string][]engine.Item
	users       []engine.Item
	roles       []engine.Item
	card        engine.CollectionCard
	upsertUser  struct {
		username, password, database, authSource string
		err                                      error
	}
}

func (f *fakeMongoCatalog) Databases(context.Context) ([]engine.Item, error) {
	return f.databases, nil
}

func (f *fakeMongoCatalog) Collections(_ context.Context, database string) ([]engine.Item, error) {
	return f.collections[database], nil
}

func (f *fakeMongoCatalog) Views(_ context.Context, database string) ([]engine.Item, error) {
	return f.views[database], nil
}

func (f *fakeMongoCatalog) Indexes(_ context.Context, database, collection string) ([]engine.Item, error) {
	return f.indexes[database+"."+collection], nil
}

func (f *fakeMongoCatalog) Users(context.Context) ([]engine.Item, error) { return f.users, nil }
func (f *fakeMongoCatalog) Roles(context.Context) ([]engine.Item, error) { return f.roles, nil }

func (f *fakeMongoCatalog) Fields(context.Context, string, string) ([]engine.FieldInfo, error) {
	return f.card.Fields, nil
}

func (f *fakeMongoCatalog) CollectionCard(context.Context, string, string) (engine.CollectionCard, error) {
	return f.card, nil
}

func (f *fakeMongoCatalog) UpsertUser(_ context.Context, username, password, database, authSource string) error {
	f.upsertUser.username = username
	f.upsertUser.password = password
	f.upsertUser.database = database
	f.upsertUser.authSource = authSource
	return f.upsertUser.err
}

func TestMongoTreeLoadsCatalog(t *testing.T) {
	fake := &fakeMongoCatalog{
		fakeSession: &fakeSession{info: engine.Info{ServerVersion: "8.0.38", Database: "app"}},
		databases:   []engine.Item{{Name: "app", Detail: "1.0 MiB", Current: true}},
		collections: map[string][]engine.Item{"app": {{Name: "items", Detail: "validator"}}},
		views:       map[string][]engine.Item{"app": {{Name: "items_view", Detail: "view · on items"}}},
		indexes:     map[string][]engine.Item{"app.items": {{Name: "_id_", Detail: `{"_id":1}`}}},
		users:       []engine.Item{{Name: "ann", Detail: "db:admin"}},
		roles:       []engine.Item{{Name: "readAny", Detail: "db:admin · builtin"}},
	}
	m := mongoModel(t, fake)
	ctx := context.Background()

	databases := mustChild(t, m.tree.root, "Databases")
	dbNodes, err := databases.load(ctx)
	if err != nil {
		t.Fatalf("Databases load error = %v", err)
	}
	if len(dbNodes) != 1 || dbNodes[0].title != "app" || !dbNodes[0].current {
		t.Fatalf("database nodes = %+v, want app marked current", dbNodes)
	}

	groups, err := dbNodes[0].load(ctx)
	if err != nil {
		t.Fatalf("database load error = %v", err)
	}
	collectionFolder := mustNode(t, groups, "Collections")
	viewFolder := mustNode(t, groups, "Views")

	collections, err := collectionFolder.load(ctx)
	if err != nil {
		t.Fatalf("Collections load error = %v", err)
	}
	if len(collections) != 1 || collections[0].kind != nodeCollection || !collections[0].openable {
		t.Fatalf("collection nodes = %+v, want an openable collection", collections)
	}
	if collections[0].schema != "app" || collections[0].name != "items" {
		t.Fatalf("collection node = %+v, want app.items", collections[0])
	}

	indexes, err := collections[0].load(ctx)
	if err != nil {
		t.Fatalf("Indexes load error = %v", err)
	}
	if len(indexes) != 1 || indexes[0].kind != nodeIndex || indexes[0].title != "_id_" {
		t.Fatalf("index nodes = %+v, want _id_", indexes)
	}

	views, err := viewFolder.load(ctx)
	if err != nil {
		t.Fatalf("Views load error = %v", err)
	}
	if len(views) != 1 || views[0].kind != nodeCollection || views[0].name != "items_view" {
		t.Fatalf("view nodes = %+v, want items_view", views)
	}

	users, err := mustChild(t, m.tree.root, "Users").load(ctx)
	if err != nil {
		t.Fatalf("Users load error = %v", err)
	}
	if len(users) != 1 || users[0].kind != nodeUser {
		t.Fatalf("user nodes = %+v, want a user", users)
	}

	roles, err := mustChild(t, m.tree.root, "Roles").load(ctx)
	if err != nil {
		t.Fatalf("Roles load error = %v", err)
	}
	if len(roles) != 1 || roles[0].kind != nodeMongoRole {
		t.Fatalf("role nodes = %+v, want a role", roles)
	}
}

func TestMongoCollectionCard(t *testing.T) {
	fake := &fakeMongoCatalog{
		fakeSession: &fakeSession{info: engine.Info{ServerVersion: "8.0.38", Database: "app"}},
		card: engine.CollectionCard{
			Database:   "app",
			Collection: "items",
			Type:       "collection",
			Exists:     true,
			Options:    `{"validator":{"$jsonSchema":{"required":["name"]}}}`,
			Validator:  `{"$jsonSchema":{"required":["name"]}}`,
			Indexes:    []engine.IndexInfo{{Name: "_id_", Keys: `{"_id":1}`}},
			Fields:     []engine.FieldInfo{{Name: "name", Types: "string", Present: 2, Total: 2}},
			Count:      2,
			SizeBytes:  2048,
			SampleSize: 2,
		},
	}
	m := mongoModel(t, fake)
	node := &treeNode{kind: nodeCollection, schema: "app", name: "items", openable: true}

	msg := m.mongoCardCmd(node)()
	cardMsg, ok := msg.(mongoCardMsg)
	if !ok {
		t.Fatalf("command returned %T, want mongoCardMsg", msg)
	}
	if cardMsg.err != nil {
		t.Fatalf("card error = %v", cardMsg.err)
	}
	updated, _ := m.Update(cardMsg)
	m = updated.(Model)
	if m.mongoCard == nil || m.tab != tabObject || m.openObject != "app.items" {
		t.Fatalf("model = card %+v tab %v open %q", m.mongoCard, m.tab, m.openObject)
	}

	lines := strings.Join(m.cardLines(100, 40), "\n")
	for _, want := range []string{"app.items", "FIELDS", "sample 2", "name", "INDEXES", "_id_", "STATS", "2 documents", "VALIDATOR"} {
		if !strings.Contains(lines, want) {
			t.Fatalf("card does not contain %q:\n%s", want, lines)
		}
	}
}

func TestMongoEnterOnCollectionOpensCard(t *testing.T) {
	fake := &fakeMongoCatalog{
		fakeSession: &fakeSession{info: engine.Info{ServerVersion: "8.0.38", Database: "app"}},
		card: engine.CollectionCard{
			Database:   "app",
			Collection: "items",
			Type:       "collection",
			Exists:     true,
			Options:    "{}",
		},
	}
	m := mongoModel(t, fake)
	root := &treeNode{kind: nodeRoot, title: "MongoDB app", expanded: true}
	root.children = []*treeNode{{
		kind:     nodeCollection,
		title:    "items",
		schema:   "app",
		name:     "items",
		openable: true,
	}}
	m.tree = newTree(root)
	m.tree.cursor = 1
	m.focus = focusTree

	m, cmd := press(t, m, tea.KeyEnter)
	if cmd == nil {
		t.Fatal("Enter on a collection returned no command")
	}
	msg, ok := cmd().(mongoCardMsg)
	if !ok {
		t.Fatalf("command returned %T, want mongoCardMsg", msg)
	}
	updated, _ := m.Update(msg)
	m = updated.(Model)
	if m.mongoCard == nil || m.mongoCard.Collection != "items" {
		t.Fatalf("card = %+v, want items", m.mongoCard)
	}
}

func mustChild(t *testing.T, node *treeNode, title string) *treeNode {
	t.Helper()
	for _, child := range node.children {
		if child.title == title {
			return child
		}
	}
	t.Fatalf("node %q has no child %q", node.title, title)
	return nil
}

func mustNode(t *testing.T, nodes []*treeNode, title string) *treeNode {
	t.Helper()
	for _, node := range nodes {
		if node.title == title {
			return node
		}
	}
	t.Fatalf("no node %q in %+v", title, nodes)
	return nil
}
