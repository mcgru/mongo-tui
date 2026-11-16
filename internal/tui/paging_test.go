package tui

import (
	"context"
	"regexp"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"mongo-tui/internal/engine"
)

// fakePager adds the optional paging capability to the standard fake session.
type fakePager struct {
	fakeSession
	runResult   engine.Result
	runMore     bool
	runErr      error
	pageResults []engine.Result
	pageMore    []bool
	fetchErr    error
	fetches     []bool
	closed      int
	closedScope string
}

func (f *fakePager) RunPaged(_ context.Context, request string, pageSize int) (engine.Result, bool, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.executed = append(f.executed, request)
	if f.runErr != nil {
		return engine.Result{}, false, f.runErr
	}
	return f.runResult, f.runMore, nil
}

func (f *fakePager) FetchPage(_ context.Context, all bool) (engine.Result, bool, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.fetches = append(f.fetches, all)
	if f.fetchErr != nil {
		return engine.Result{}, false, f.fetchErr
	}
	if len(f.pageResults) == 0 {
		return engine.Result{}, false, nil
	}
	res := f.pageResults[0]
	f.pageResults = f.pageResults[1:]
	more := false
	if len(f.pageMore) > 0 {
		more = f.pageMore[0]
		f.pageMore = f.pageMore[1:]
	}
	return res, more, nil
}

func (f *fakePager) ClosePageOf(database, collection string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.closed++
	f.closedScope = database + "." + collection
}

func pagedModel(t *testing.T, p *fakePager) (Model, *treeNode) {
	t.Helper()
	p.info = engine.Info{ServerVersion: "8.0.38", Database: "app"}
	m := mongoModel(t, p)
	node := &treeNode{kind: nodeCollection, schema: "app", name: "items", openable: true}
	return m, node
}

// A peek shows only the first page, keeps the cursor, and advertises the keys.
func TestPeekShowsFirstPageAndKeepsCursor(t *testing.T) {
	p := &fakePager{
		runResult: engine.Result{
			Columns:  []string{"_id", "name"},
			Rows:     [][]string{{"1", "ann"}, {"2", "bob"}},
			RowCount: 2,
		},
		runMore: true,
	}
	m, node := pagedModel(t, p)

	updated, cmd := m.runPeekSelect(node)
	m = updated.(Model)
	if cmd == nil || !m.running {
		t.Fatalf("peek did not start (running=%v)", m.running)
	}
	msg, ok := cmd().(execDoneMsg)
	if !ok {
		t.Fatalf("peek command returned %T, want execDoneMsg", cmd())
	}
	if !msg.paged || !msg.more {
		t.Fatalf("peek result paged=%v more=%v, want true/true", msg.paged, msg.more)
	}
	if strings.Contains(msg.statement, "limit(") {
		t.Fatalf("peek statement bakes a limit: %q", msg.statement)
	}

	updated, _ = m.Update(msg)
	m = updated.(Model)
	if m.grid == nil || len(m.grid.res.Rows) != 2 {
		t.Fatalf("grid rows = %v, want the first page", m.grid)
	}
	if !m.pageMore {
		t.Fatal("pageMore is false after a paged peek")
	}
	if !strings.Contains(m.status, "m more") || !strings.Contains(m.status, "A all") {
		t.Fatalf("status = %q, want the paging hints", m.status)
	}
}

// m appends the next page and keeps the column union in step; A drains the
// rest and ends the sequence.
func TestLoadMoreAndAllAppendPages(t *testing.T) {
	p := &fakePager{
		runResult: engine.Result{
			Columns:  []string{"_id", "name"},
			Rows:     [][]string{{"1", "ann"}},
			RowCount: 1,
		},
		runMore: true,
		pageResults: []engine.Result{
			{Columns: []string{"_id", "name", "age"}, Rows: [][]string{{"2", "bob", "30"}}, ColumnTypes: []string{"int", "string", "int"}, RowCount: 1},
			{Columns: []string{"_id", "name", "age"}, Rows: [][]string{{"3", "cy", "40"}}, ColumnTypes: []string{"int", "string", "int"}, RowCount: 1},
		},
		pageMore: []bool{true, false},
	}
	m, node := pagedModel(t, p)
	updated, cmd := m.runPeekSelect(node)
	m = updated.(Model)
	updated, _ = m.Update(cmd().(execDoneMsg))
	m = updated.(Model)
	m.focus = focusGrid
	m.tab = tabResult

	updated, cmd = m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'m'}})
	m = updated.(Model)
	if cmd == nil || !m.running {
		t.Fatalf("m did not start a page load (running=%v status=%q)", m.running, m.status)
	}
	updated, _ = m.Update(cmd().(pageDoneMsg))
	m = updated.(Model)
	if got := len(m.grid.res.Rows); got != 2 {
		t.Fatalf("rows after m = %d, want 2", got)
	}
	if len(m.grid.res.Columns) != 3 || m.grid.res.Columns[2] != "age" {
		t.Fatalf("columns after m = %v, want the new age column", m.grid.res.Columns)
	}
	if !m.pageMore {
		t.Fatal("pageMore is false after a page with more rows")
	}
	if !strings.Contains(m.status, "2 rows") {
		t.Fatalf("status after m = %q, want the total row count", m.status)
	}

	updated, cmd = m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'A'}})
	m = updated.(Model)
	if cmd == nil {
		t.Fatal("A did not start")
	}
	updated, _ = m.Update(cmd().(pageDoneMsg))
	m = updated.(Model)
	if got := len(m.grid.res.Rows); got != 3 {
		t.Fatalf("rows after A = %d, want 3", got)
	}
	if m.pageMore {
		t.Fatal("pageMore is true after the cursor was drained")
	}
	if len(p.fetches) != 2 || p.fetches[0] || !p.fetches[1] {
		t.Fatalf("FetchPage calls = %v, want [false true]", p.fetches)
	}
	if !strings.Contains(m.status, "end of result") {
		t.Fatalf("status after A = %q, want end of result", m.status)
	}

	updated, cmd = m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'m'}})
	m = updated.(Model)
	if cmd != nil || !strings.Contains(m.status, "end of result") {
		t.Fatalf("m after the end: cmd=%v status=%q", cmd, m.status)
	}
}

// The Cyrillic keys on the same physical positions must page like m and A.
func TestCyrillicLoadPageKeys(t *testing.T) {
	cases := []struct {
		name string
		r    rune
		all  bool
	}{
		{"more", 'ь', false},
		{"all", 'Ф', true},
	}
	for _, layout := range []string{layoutRU, layoutUS} {
		for _, tc := range cases {
			t.Run(layout+"/"+tc.name, func(t *testing.T) {
				p := &fakePager{fakeSession: fakeSession{info: engine.Info{Database: "app"}}}
				m := mongoModel(t, p)
				m.grid = newGrid(&engine.Result{Columns: []string{"_id"}, Rows: [][]string{{"1"}}})
				m.pageMore = true
				m.focus = focusGrid
				m.tab = tabResult
				m.keyboardLayout = layout

				updated, cmd := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{tc.r}})
				m = updated.(Model)
				if cmd == nil || !m.running {
					t.Fatalf("%q did not start a page load (running=%v status=%q)", string(tc.r), m.running, m.status)
				}
				updated, _ = m.Update(cmd().(pageDoneMsg))
				m = updated.(Model)
				if len(p.fetches) != 1 || p.fetches[0] != tc.all {
					t.Fatalf("FetchPage calls = %v, want [%v]", p.fetches, tc.all)
				}
			})
		}
	}
}

// Opening a card for another collection abandons the old grid and kills its
// server cursor; the same collection keeps both so paging can continue.
func TestCardSwitchReleasesGridAndCursor(t *testing.T) {
	p := &fakePager{}
	m, _ := pagedModel(t, p)
	m.grid = newGrid(&engine.Result{
		Columns:    []string{"_id"},
		Rows:       [][]string{{"1"}},
		Documents:  [][]byte{[]byte(`{"_id":1}`)},
		Database:   "app",
		Collection: "items",
	})
	m.pageMore = true

	updated, cmd := m.Update(mongoCardMsg{card: engine.CollectionCard{
		Database: "app", Collection: "other", Exists: true,
	}})
	m = updated.(Model)
	if m.grid != nil || m.pageMore {
		t.Fatalf("grid=%v pageMore=%v after switching collections, want both released", m.grid, m.pageMore)
	}
	if cmd == nil {
		t.Fatal("no command to close the abandoned cursor")
	}
	_ = cmd()
	if p.closed != 1 || p.closedScope != "app.items" {
		t.Fatalf("closed=%d scope=%q, want 1 app.items", p.closed, p.closedScope)
	}
}

func TestCardSameCollectionKeepsGrid(t *testing.T) {
	p := &fakePager{}
	m, _ := pagedModel(t, p)
	m.grid = newGrid(&engine.Result{
		Columns:    []string{"_id"},
		Rows:       [][]string{{"1"}},
		Documents:  [][]byte{[]byte(`{"_id":1}`)},
		Database:   "app",
		Collection: "items",
	})
	m.pageMore = true

	updated, cmd := m.Update(mongoCardMsg{card: engine.CollectionCard{
		Database: "app", Collection: "items", Exists: true,
	}})
	m = updated.(Model)
	if m.grid == nil || !m.pageMore {
		t.Fatalf("grid=%v pageMore=%v for the same collection, want both kept", m.grid, m.pageMore)
	}
	if cmd != nil {
		t.Fatal("same collection must not close the cursor")
	}
	if p.closed != 0 {
		t.Fatalf("ClosePageOf calls = %d, want 0", p.closed)
	}
}

// A large result is dropped before a new query starts, so the old heap does not
// double while the response is built.
func TestStartRunReleasesLargeGrid(t *testing.T) {
	oldThreshold := releaseFreeBytes
	releaseFreeBytes = 1
	defer func() { releaseFreeBytes = oldThreshold }()

	p := &fakePager{}
	m, _ := pagedModel(t, p)
	m.grid = newGrid(&engine.Result{
		Columns:    []string{"_id"},
		Rows:       [][]string{{"1"}},
		Documents:  [][]byte{[]byte(`{"_id":1}`)},
		Database:   "app",
		Collection: "items",
	})
	m.editor.SetValue("db.items.find({})")
	m.focus = focusEditor

	updated, _ := m.startRun()
	m = updated.(Model)
	if m.grid != nil {
		t.Fatalf("grid survived the start of a new query: %+v", m.grid)
	}
}

// Without the Pager capability peek falls back to a limited find.
func TestPeekFallsBackToLimitWithoutPager(t *testing.T) {
	fake := &fakeSession{info: engine.Info{Database: "app"}}
	m := mongoModel(t, fake)
	node := &treeNode{kind: nodeCollection, schema: "app", name: "items", openable: true}

	updated, cmd := m.runPeekSelect(node)
	m = updated.(Model)
	msg := cmd().(execDoneMsg)
	if !strings.Contains(msg.statement, ".limit(30)") {
		t.Fatalf("fallback statement = %q, want the 30-row limit", msg.statement)
	}
}

// A new page merges its fields into the grid: old rows mark new columns as
// missing, and a live search is re-evaluated over the grown set.
func TestGridAppendResultMergesColumns(t *testing.T) {
	g := newGrid(&engine.Result{
		Columns:   []string{"_id", "name"},
		Rows:      [][]string{{"1", "ann"}},
		Documents: [][]byte{[]byte(`{"_id":1,"name":"ann"}`)},
	})
	g.search = &gridSearch{pattern: "o", re: regexp.MustCompile("o"), col: 1, matches: []int{0}, cur: 0}

	g.appendResult(&engine.Result{
		Columns:     []string{"name", "age"},
		ColumnTypes: []string{"string", "int"},
		Rows:        [][]string{{"bob", "42"}},
		Documents:   [][]byte{[]byte(`{"name":"bob","age":42}`)},
	})

	if got := strings.Join(g.res.Columns, ","); got != "_id,name,age" {
		t.Fatalf("columns = %s, want _id,name,age", got)
	}
	if got := g.res.Rows[0][2]; got != engine.MissingSentinel {
		t.Fatalf("old row age = %q, want the missing sentinel", got)
	}
	if got := g.res.Rows[1][0]; got != engine.MissingSentinel {
		t.Fatalf("new row _id = %q, want the missing sentinel", got)
	}
	if got := g.res.Rows[1][2]; got != "42" {
		t.Fatalf("new row age = %q, want 42", got)
	}
	if len(g.widths) != 3 {
		t.Fatalf("widths = %v, want one per column", g.widths)
	}
	if len(g.search.matches) != 1 || g.search.matches[0] != 1 {
		t.Fatalf("matches = %v, want the appended bob row", g.search.matches)
	}
}
