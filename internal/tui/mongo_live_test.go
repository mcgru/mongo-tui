package tui

import (
	"context"
	"fmt"
	"os"
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"mongo-tui/internal/config"
	"mongo-tui/internal/engine"
)

func TestMongoLiveProfile(t *testing.T) {
	uri := os.Getenv("MONGODB_URI")
	if uri == "" {
		t.Skip("MONGODB_URI is not set")
	}
	opts := testOptions()
	opts.CLI.Profile = "prod"
	opts.State.Profiles = []config.Profile{{
		ID: "prod", Label: "prod", Engine: "mongo", URI: uri,
	}}
	m := New(opts)
	if m.scr != screenConnecting || m.mongoURI != uri {
		t.Fatalf("screen/uri = %v/%q, want connecting/%q", m.scr, m.mongoURI, uri)
	}
	msg := m.connectCmd("")()
	connected, ok := msg.(connectedMsg)
	if !ok {
		if failure, isErr := msg.(connectErrMsg); isErr {
			t.Fatalf("profile connect error: %v", failure.err)
		}
		t.Fatalf("profile connect returned %T", msg)
	}
	updated, _ := m.Update(connected)
	m = updated.(Model)
	if m.scr != screenMain {
		t.Fatalf("screen = %v, want main", m.scr)
	}
}

// TestMongoLiveWorkbench is the end-to-end smoke test: the TUI parses a URI,
// connects with the real driver and runs one envelope. It is read-only and
// skipped unless make test-mongo starts the cluster.
func TestMongoLiveWorkbench(t *testing.T) {
	uri := os.Getenv("MONGODB_URI")
	if uri == "" {
		t.Skip("MONGODB_URI is not set")
	}

	opts := testOptions()
	opts.CLI.MongoURI = uri
	m := New(opts)

	msg := m.connectCmd("")()
	connected, ok := msg.(connectedMsg)
	if !ok {
		if failure, isErr := msg.(connectErrMsg); isErr {
			t.Fatalf("connect error: %v", failure.err)
		}
		t.Fatalf("connect returned %T, want connectedMsg", msg)
	}
	updated, _ := m.Update(connected)
	m = updated.(Model)
	if m.scr != screenMain {
		t.Fatalf("screen = %v, want main", m.scr)
	}
	if m.tree == nil || m.tree.root == nil {
		t.Fatal("MongoDB placeholder tree is missing")
	}
	if m.currentDatabase() == "" {
		t.Fatal("current database is empty")
	}

	// A count on a missing collection returns one row with 0 and creates nothing.
	m.editor.SetValue(`{"op":"count","collection":"___mongo_tui_smoke___","filter":{}}`)
	m, cmd := press(t, m, tea.KeyCtrlJ)
	if cmd == nil {
		t.Fatal("Ctrl+J returned no command")
	}
	execMsg, ok := cmd().(execDoneMsg)
	if !ok {
		t.Fatalf("command returned %T, want execDoneMsg", execMsg)
	}
	if execMsg.err != nil {
		t.Fatalf("execute error = %v", execMsg.err)
	}
	updated, _ = m.Update(execMsg)
	m = updated.(Model)

	if m.grid == nil || len(m.grid.res.Rows) != 1 || m.grid.res.Rows[0][0] != "0" {
		t.Fatalf("grid = %+v, want a single count row with 0", m.grid)
	}
	if len(m.grid.headers) == 0 || m.grid.headers[0] != "count long" {
		t.Fatalf("headers = %v, want count long", m.grid.headers)
	}
}

// TestMongoLiveTree walks the real catalog through the tree loaders and opens
// the collection card. It creates and removes one uniquely named collection.
func TestMongoLiveTree(t *testing.T) {
	uri := os.Getenv("MONGODB_URI")
	if uri == "" {
		t.Skip("MONGODB_URI is not set")
	}
	opts := testOptions()
	opts.CLI.MongoURI = uri
	m := New(opts)
	connected, ok := m.connectCmd("")().(connectedMsg)
	if !ok {
		t.Fatal("connect did not return connectedMsg")
	}
	updated, _ := m.Update(connected)
	m = updated.(Model)

	ctx := context.Background()
	collection := fmt.Sprintf("___mongo_tui_tree_%d", time.Now().UnixNano())
	database := m.currentDatabase()
	envelope := fmt.Sprintf(`{"database":%q,"op":"insertOne","collection":%q,"document":{"_id":"tree"}}`, database, collection)
	if res, err := m.sess.Execute(ctx, envelope); err != nil || res.Error {
		t.Fatalf("insert fixture: err=%v res=%+v", err, res)
	}
	t.Cleanup(func() {
		_, _ = m.sess.Execute(context.Background(), fmt.Sprintf(`{"database":%q,"op":"dropCollection","collection":%q}`, database, collection))
	})

	m.buildTree()
	databases := mustChild(t, m.tree.root, "Databases")
	dbNodes, err := databases.load(ctx)
	if err != nil {
		t.Fatalf("Databases load error = %v", err)
	}
	dbNode := mustNode(t, dbNodes, database)
	groups, err := dbNode.load(ctx)
	if err != nil {
		t.Fatalf("database load error = %v", err)
	}
	collections, err := mustNode(t, groups, "Collections").load(ctx)
	if err != nil {
		t.Fatalf("Collections load error = %v", err)
	}
	collNode := mustNode(t, collections, collection)
	if collNode.kind != nodeCollection || !collNode.openable {
		t.Fatalf("collection node = %+v, want an openable collection", collNode)
	}

	msg := m.mongoCardCmd(collNode)()
	cardMsg, ok := msg.(mongoCardMsg)
	if !ok {
		t.Fatalf("card command returned %T", msg)
	}
	if cardMsg.err != nil {
		t.Fatalf("card error = %v", cardMsg.err)
	}
	updated, _ = m.Update(cardMsg)
	m = updated.(Model)
	if m.mongoCard == nil || !m.mongoCard.Exists {
		t.Fatalf("card = %+v, want an existing collection", m.mongoCard)
	}
	if m.mongoCard.SampleSize != 1 {
		t.Fatalf("sample size = %d, want 1", m.mongoCard.SampleSize)
	}
	if !strings.Contains(strings.Join(m.cardLines(100, 40), "\n"), collection) {
		t.Fatalf("card lines do not mention %q", collection)
	}

	// The catalog must be visible through the same interface the tree uses.
	if _, ok := m.sess.(engine.Catalog); !ok {
		t.Fatal("session does not implement engine.Catalog")
	}
}

// TestMongoLiveCompletion checks the snapshot loader and lazy field sampling
// against the real catalog.
func TestMongoLiveCompletion(t *testing.T) {
	uri := os.Getenv("MONGODB_URI")
	if uri == "" {
		t.Skip("MONGODB_URI is not set")
	}
	opts := testOptions()
	opts.CLI.MongoURI = uri
	m := New(opts)
	connected, ok := m.connectCmd("")().(connectedMsg)
	if !ok {
		t.Fatal("connect did not return connectedMsg")
	}
	updated, _ := m.Update(connected)
	m = updated.(Model)

	ctx := context.Background()
	database := m.currentDatabase()
	collection := fmt.Sprintf("___mongo_tui_complete_%d", time.Now().UnixNano())
	envelope := fmt.Sprintf(`{"database":%q,"op":"insertOne","collection":%q,"document":{"_id":1,"name":"Ann"}}`, database, collection)
	if res, err := m.sess.Execute(ctx, envelope); err != nil || res.Error {
		t.Fatalf("insert fixture: err=%v res=%+v", err, res)
	}
	t.Cleanup(func() {
		_, _ = m.sess.Execute(context.Background(), fmt.Sprintf(`{"database":%q,"op":"dropCollection","collection":%q}`, database, collection))
	})

	msg := m.loadMongoSnapshotCmd()()
	snapshot, ok := msg.(mongoSnapshotMsg)
	if !ok || snapshot.err != nil {
		t.Fatalf("snapshot = %#v", msg)
	}
	m.mongoSnap = snapshot.snap

	m.editor.SetValue("db.")
	m, _ = m.startMongoCompletion()
	if !hasCandidate(m.comp.items, collection) {
		t.Fatalf("collections = %+v, want %s", m.comp.items, collection)
	}

	m.editor.SetValue("db." + collection + ".find({na")
	completionModel, cmd := m.startMongoCompletion()
	m = completionModel
	if cmd == nil {
		t.Fatal("field completion returned no loading command")
	}
	fields, ok := cmd().(mongoFieldsMsg)
	if !ok || fields.err != nil {
		t.Fatalf("fields = %#v", fields)
	}
	afterFields, _ := m.Update(fields)
	m = afterFields.(Model)
	if !hasCandidate(m.comp.items, "name") {
		t.Fatalf("fields = %+v, want name", m.comp.items)
	}
}

// TestLiveServerFilterFromCell builds the Ctrl+F filter for a real grid cell
// and runs it: the generated find must return that same document.
func TestLiveServerFilterFromCell(t *testing.T) {
	uri := os.Getenv("MONGODB_URI")
	if uri == "" {
		t.Skip("MONGODB_URI is not set")
	}
	opts := testOptions()
	opts.CLI.MongoURI = uri
	m := New(opts)
	connected, ok := m.connectCmd("")().(connectedMsg)
	if !ok {
		t.Fatal("connect did not return connectedMsg")
	}
	updated, _ := m.Update(connected)
	m = updated.(Model)

	ctx := context.Background()
	database := m.currentDatabase()
	collection := fmt.Sprintf("___mongo_tui_filter_%d", time.Now().UnixNano())
	envelope := fmt.Sprintf(
		`{"database":%q,"op":"insertMany","collection":%q,"documents":[{"_id":1,"name":"Ann","qty":3},{"_id":2,"name":"Bob","qty":7}]}`,
		database, collection)
	if res, err := m.sess.Execute(ctx, envelope); err != nil || res.Error {
		t.Fatalf("insert fixture: err=%v res=%+v", err, res)
	}
	t.Cleanup(func() {
		_, _ = m.sess.Execute(context.Background(),
			fmt.Sprintf(`{"database":%q,"op":"dropCollection","collection":%q}`, database, collection))
	})

	m.editor.SetValue(fmt.Sprintf("db.%s.find({})", collection))
	m, cmd := press(t, m, tea.KeyCtrlJ)
	if cmd == nil {
		t.Fatal("find returned no command")
	}
	execMsg := cmd().(execDoneMsg)
	if execMsg.err != nil || execMsg.result.Error {
		t.Fatalf("find failed: err=%v res=%+v", execMsg.err, execMsg.result)
	}
	updated, _ = m.Update(execMsg)
	m = updated.(Model)
	if m.grid == nil || len(m.grid.res.Rows) == 0 {
		t.Fatalf("grid = %+v, want rows", m.grid)
	}

	nameCol := -1
	for i, column := range m.grid.res.Columns {
		if column == "name" {
			nameCol = i
			break
		}
	}
	if nameCol < 0 {
		t.Fatalf("columns = %v, want name", m.grid.res.Columns)
	}
	m.grid.curCol = nameCol
	want := m.grid.currentCell()
	m.focus = focusGrid
	m.tab = tabResult

	updated, _ = m.openServerFilter(false)
	m = updated.(Model)
	if !strings.Contains(m.editor.Value(), `find({ name: "`) {
		t.Fatalf("editor = %q, want the generated name filter", m.editor.Value())
	}

	m, cmd = press(t, m, tea.KeyCtrlJ)
	if cmd == nil {
		t.Fatal("filter returned no command")
	}
	filterMsg := cmd().(execDoneMsg)
	if filterMsg.err != nil || filterMsg.result.Error {
		t.Fatalf("filter failed: err=%v res=%+v", filterMsg.err, filterMsg.result)
	}
	updated, _ = m.Update(filterMsg)
	m = updated.(Model)
	if m.grid == nil || len(m.grid.res.Rows) != 1 {
		t.Fatalf("filtered rows = %+v, want exactly one", m.grid)
	}
	if got := m.grid.res.Rows[0][nameCol]; got != want {
		t.Fatalf("filtered name = %q, want %q", got, want)
	}
}
