package tui

import (
	"errors"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"mongo-tui/internal/config"
	"mongo-tui/internal/engine"
)

func TestMongoNewWithURIConnects(t *testing.T) {
	opts := testOptions()
	opts.CLI.MongoURI = "mongodb://db.example:27017/app?replicaSet=rs0"

	m := New(opts)
	if m.scr != screenConnecting {
		t.Fatalf("screen = %v, want connecting", m.scr)
	}
	if m.mongoURI != opts.CLI.MongoURI {
		t.Fatalf("uri = %q, want %q", m.mongoURI, opts.CLI.MongoURI)
	}
}

func TestMongoConnectedBuildsPlaceholderTree(t *testing.T) {
	fake := &fakeSession{info: engine.Info{ServerVersion: "8.0.38", Database: "app"}}
	m := mongoModel(t, fake)

	if m.tree == nil || m.tree.root == nil {
		t.Fatal("tree = nil, want the MongoDB placeholder")
	}
	if m.tree.root.title != "MongoDB app" {
		t.Fatalf("tree root = %q, want MongoDB app", m.tree.root.title)
	}
	tree := strings.Join(m.tree.lines(40, 10, m.openObject), "\n")
	if !strings.Contains(tree, "Catalog") {
		t.Fatalf("tree = %v, want the Catalog placeholder", tree)
	}

	view := m.View()
	for _, want := range []string{"mongo-tui", "MongoDB 8.0.38", "db:app"} {
		if !strings.Contains(view, want) {
			t.Fatalf("view does not contain %q:\n%s", want, view)
		}
	}
}

func TestMongoRunSendsWholeEnvelope(t *testing.T) {
	fake := &fakeSession{
		info: engine.Info{ServerVersion: "8.0.38", Database: "app"},
		results: []engine.Result{{
			Columns:     []string{"_id", "name"},
			ColumnTypes: []string{"int", "string"},
			Rows:        [][]string{{"1", "Ann"}},
			Documents:   [][]byte{[]byte(`{"_id":1,"name":"Ann"}`)},
		}},
	}
	m := mongoModel(t, fake)
	envelope := `{"op":"find","collection":"items","limit":1}`
	m.editor.SetValue(envelope)
	m.focus = focusEditor

	m, cmd := press(t, m, tea.KeyCtrlJ)
	if cmd == nil {
		t.Fatal("Ctrl+J returned no command")
	}
	msg, ok := cmd().(execDoneMsg)
	if !ok {
		t.Fatalf("command returned %T, want execDoneMsg", msg)
	}
	if msg.err != nil {
		t.Fatalf("exec error = %v", msg.err)
	}
	executed := fake.statements()
	if len(executed) != 1 || executed[0] != envelope {
		t.Fatalf("executed = %q, want the whole envelope", executed)
	}

	updated, _ := m.Update(msg)
	m = updated.(Model)
	if m.grid == nil || len(m.grid.res.Rows) != 1 {
		t.Fatalf("grid rows = %+v, want one row", m.grid)
	}
	if len(m.grid.headers) != 2 || !strings.Contains(m.grid.headers[1], "string") {
		t.Fatalf("headers = %v, want the column type in the header", m.grid.headers)
	}

	m.tab = tabObject
	object := strings.Join(m.cardLines(80, 20), "\n")
	if !strings.Contains(object, `"name": "Ann"`) {
		t.Fatalf("object view does not show the full document:\n%s", object)
	}
}

func TestMongoGridSentinelsAndTypes(t *testing.T) {
	res := engine.Result{
		Columns:     []string{"_id", "name", "note"},
		ColumnTypes: []string{"int", "string", "null"},
		Rows: [][]string{
			{"1", "", engine.NullSentinel},
			{"2", engine.MissingSentinel, engine.MissingSentinel},
		},
	}
	g := newGrid(&res)
	if g.headers[1] != "name string" {
		t.Fatalf("header = %q, want the type suffix", g.headers[1])
	}
	lines := strings.Join(g.lines(80, 6), "\n")
	if !strings.Contains(lines, "NULL") || !strings.Contains(lines, "—") {
		t.Fatalf("grid does not distinguish null and missing:\n%s", lines)
	}
	if got := g.currentRowTSV(); got != "1\t\t" {
		t.Fatalf("TSV = %q, want null as an empty field", got)
	}
	g.move(1, 1, 6, 3)
	if got := g.currentCell(); got != "" {
		t.Fatalf("missing cell = %q, want empty", got)
	}
	g.move(1, -1, 6, 3)
	if got := g.currentRowTSV(); got != "2\t\t" {
		t.Fatalf("missing row TSV = %q, want empty fields for null/missing", got)
	}
}

func TestMongoCompletionMetadataAndTemplates(t *testing.T) {
	fake := &fakeSession{info: engine.Info{ServerVersion: "8.0.38", Database: "app"}}
	m := mongoModel(t, fake)
	updated, _ := m.startCompletion()
	m = updated.(Model)
	if !strings.Contains(m.status, "still loading") {
		t.Fatalf("status = %q, want metadata loading", m.status)
	}

	node := &treeNode{kind: nodeCollection, schema: "app", name: "items", openable: true}
	m = m.insertDdl(node, ddlCreate)
	if !strings.Contains(m.editor.Value(), "createIndex") {
		t.Fatalf("editor = %q, want a createIndex template", m.editor.Value())
	}
	peeked, cmd := m.runPeekSelect(node)
	m = peeked.(Model)
	if cmd == nil || !strings.Contains(m.status, "running") {
		t.Fatalf("peek status = %q, want a running query", m.status)
	}
}

func TestBackslashAliasPeeks(t *testing.T) {
	fake := &fakeSession{info: engine.Info{ServerVersion: "8.0.38", Database: "app"}}
	m := mongoModel(t, fake)
	m.focus = focusTree
	m.tree.visible = []*treeNode{{kind: nodeCollection, schema: "app", name: "items", openable: true}}
	m.tree.cursor = 0

	updated, cmd := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'\\'}})
	m = updated.(Model)
	if cmd == nil || !m.running {
		t.Fatalf("backslash did not start a peek (running=%v status=%q)", m.running, m.status)
	}
	if !strings.Contains(m.status, "running items") {
		t.Fatalf("status = %q, want running items", m.status)
	}
}

func TestMongoSwitchDatabaseKeepsTransaction(t *testing.T) {
	fake := &fakeSession{info: engine.Info{Database: "app"}, tx: engine.TxInTransaction}
	m := mongoModel(t, fake)
	m.tx = engine.TxInTransaction

	updated, cmd := m.switchDatabase("other")
	if cmd == nil {
		t.Fatal("switchDatabase() returned no command")
	}
	msg, ok := cmd().(switchedMsg)
	if !ok {
		t.Fatalf("command returned %T, want switchedMsg", msg)
	}
	updated, _ = updated.Update(msg)
	m = updated.(Model)

	if fake.switches != 1 {
		t.Fatalf("switches = %d, want 1", fake.switches)
	}
	if got := m.currentDatabase(); got != "other" {
		t.Fatalf("current database = %q, want other", got)
	}
	if m.tx != engine.TxInTransaction {
		t.Fatalf("tx = %q, want the transaction kept", m.tx)
	}
}

func TestProfileURI(t *testing.T) {
	if _, ok := profileURI(config.Profile{Host: "db.example", Database: "app"}); ok {
		t.Fatal("legacy PostgreSQL profile was accepted as a MongoDB profile")
	}
	uri, ok := profileURI(config.Profile{Engine: "mongo", URI: "mongodb://db.example/app"})
	if !ok || uri != "mongodb://db.example/app" {
		t.Fatalf("Mongo profile uri=%q ok=%v", uri, ok)
	}
}

func TestCancelFailureIsShown(t *testing.T) {
	m := mongoModel(t, &fakeSession{info: engine.Info{Database: "app"}})
	m.running = true
	updated, _ := m.Update(cancelDoneMsg{err: errors.New("not authorized on admin to execute command killOp")})
	m = updated.(Model)
	if !strings.Contains(strings.Join(m.messages, "\n"), "cancel failed") {
		t.Fatalf("messages = %v, want the cancel error", m.messages)
	}
}

func TestMongoRememberTreePlace(t *testing.T) {
	m := mongoModel(t, &fakeSession{info: engine.Info{Database: "app"}})
	collection := &treeNode{kind: nodeCollection, title: "items", schema: "app", name: "items", openable: true}
	collections := &treeNode{kind: nodeFolder, title: "Collections", expanded: true, loaded: true, children: []*treeNode{collection}}
	db := &treeNode{kind: nodeDatabase, title: "app", expanded: true, loaded: true, children: []*treeNode{collections}}
	databases := &treeNode{kind: nodeFolder, title: "Databases", expanded: true, loaded: true, children: []*treeNode{db}}
	root := &treeNode{kind: nodeRoot, title: "MongoDB app", expanded: true, children: []*treeNode{databases}}
	m.tree = newTree(root)
	if !m.treeSelect("items") {
		t.Fatal("items is not visible in the test tree")
	}
	m.openObject = "app.items"
	m.rememberPlace()

	place, ok := m.dbStates["app"]
	if !ok || strings.Join(place.path, "/") != "Databases/app/Collections/items" || place.openObject != "app.items" {
		t.Fatalf("stored place = %+v", place)
	}

	m.pendingPath = place.path
	m.pendingOpen = place.openObject
	restored, cmd := m.continuePath()
	m = restored.(Model)
	if m.tree.current() == nil || m.tree.current().title != "items" {
		t.Fatalf("restored cursor = %+v, want items", m.tree.current())
	}
	if cmd == nil {
		t.Fatal("restore did not reopen the collection card")
	}
}

func TestNeedsPasswordClassifiesErrors(t *testing.T) {
	if needsPassword(errors.New("connection refused")) {
		t.Fatal("network error was classified as an auth prompt")
	}
	for _, message := range []string{
		"Authentication failed",
		"requires authentication",
		"SCRAM conversation failed",
	} {
		if !needsPassword(errors.New(message)) {
			t.Fatalf("%q was not classified as an auth error", message)
		}
	}
}

func TestMongoURIHelpers(t *testing.T) {
	if got := mongoEndpoint("mongodb://ann@db.example:27017/app?replicaSet=rs0"); got != "ann@db.example:27017" {
		t.Fatalf("endpoint = %q", got)
	}
	if got := mongoEndpoint("mongodb://ann@mongod1:27021,mongod2:27022/stage"); got != "ann@mongod1:27021,mongod2:27022" {
		t.Fatalf("multi-host endpoint = %q", got)
	}
	if got := mongoDatabase("mongodb://db.example"); got != "test" {
		t.Fatalf("database = %q, want test", got)
	}
	if !looksLikeMongoURI("mongodb+srv://cluster.example/app") || looksLikeMongoURI("postgresql://db/app") {
		t.Fatal("looksLikeMongoURI() misclassified a URI")
	}
}

func mongoModel(t *testing.T, sess Executor) Model {
	t.Helper()
	opts := testOptions()
	opts.CLI.MongoURI = "mongodb://db.example:27017/app?replicaSet=rs0"

	m := New(opts)
	updated, _ := m.Update(tea.WindowSizeMsg{Width: 140, Height: 44})
	m = updated.(Model)
	updated, _ = m.Update(connectedMsg{
		sess:     sess,
		mongoURI: opts.CLI.MongoURI,
	})
	m = updated.(Model)
	if m.scr != screenMain {
		t.Fatalf("screen = %v, want main", m.scr)
	}
	return m
}
