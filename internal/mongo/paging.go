package mongo

import (
	"context"
	"errors"
	"time"

	"go.mongodb.org/mongo-driver/v2/bson"
	mg "go.mongodb.org/mongo-driver/v2/mongo"

	"mongo-tui/internal/engine"
	"mongo-tui/internal/mongo/query"
)

// DefaultPageSize is the number of rows a peek shows before the user asks for
// more. It is independent of the terminal height: a small first page keeps the
// TUI responsive even on a collection with millions of documents.
const DefaultPageSize = 30

// pageState is the live server cursor behind a paged grid. The result limits
// apply to the whole page sequence, so "load all" cannot grow past maxDocs or
// maxBytes no matter how often more is pressed.
type pageState struct {
	cur        *mg.Cursor
	pageSize   int
	all        bool
	limit      int64 // the query's own limit, 0 means unbounded
	fetched    int
	bytes      int64
	maxDocs    int
	maxBytes   int
	database   string
	collection string
	more       bool
	truncated  bool
}

// RunPaged executes a read query and returns only its first page, keeping the
// server cursor open for FetchPage. Queries that do not return a cursor run
// normally and report hasMore=false.
func (s *Session) RunPaged(ctx context.Context, request string, pageSize int) (engine.Result, bool, error) {
	spec, _, err := query.ParseRequest(request)
	if err != nil {
		return engine.Result{}, false, err
	}
	if pageSize <= 0 {
		pageSize = DefaultPageSize
	}

	s.reqMu.Lock()
	defer s.reqMu.Unlock()
	s.closePageLocked()

	if spec.Op != query.Find && spec.Op != query.Aggregate {
		res, err := s.executeLocked(ctx, spec)
		return res, false, err
	}

	opCtx, comment, txState, err := s.prepare(ctx, spec)
	if err != nil {
		return engine.Result{}, false, err
	}
	defer s.setComment("")

	cur, err := s.openCursor(opCtx, spec, comment)
	if err != nil {
		res, ferr := s.finish(engine.Result{}, err, txState, spec)
		return res, false, ferr
	}
	dbName, collection := s.specSource(spec)
	page := &pageState{
		cur:        cur,
		pageSize:   pageSize,
		limit:      spec.Limit,
		maxDocs:    s.maxDocs,
		maxBytes:   s.maxBytes,
		database:   dbName,
		collection: collection,
	}
	s.page = page
	res, err := s.readPageLocked(opCtx, page)
	if err != nil {
		s.closePageLocked()
		return engine.Result{}, false, err
	}
	if !page.more {
		s.closePageLocked()
	}
	return res, page.more, nil
}

// FetchPage reads the next page of the open cursor. With all it reads until the
// cursor or the result limits are exhausted; either way the caller receives
// only the new rows.
func (s *Session) FetchPage(ctx context.Context, all bool) (engine.Result, bool, error) {
	s.reqMu.Lock()
	defer s.reqMu.Unlock()

	if s.page == nil {
		return engine.Result{}, false, errors.New("mongo: no paged result; run the query again")
	}
	if all {
		s.page.all = true
	}
	res, err := s.readPageLocked(ctx, s.page)
	if err != nil {
		// A failed read (timeout, expired cursor) leaves the cursor unusable.
		s.closePageLocked()
		return engine.Result{}, false, err
	}
	more := s.page.more
	if !more {
		s.closePageLocked()
	}
	return res, more, nil
}

// ClosePageOf releases the cursor of the last paged read when it belongs to
// db.collection. An empty collection means any cursor. A new query or a
// database switch does this on its own; the UI calls it when the grid is
// abandoned without running anything.
func (s *Session) ClosePageOf(database, collection string) {
	s.reqMu.Lock()
	defer s.reqMu.Unlock()

	if s.page == nil {
		return
	}
	if collection != "" && (s.page.database != database || s.page.collection != collection) {
		return
	}
	s.closePageLocked()
}

// readPageLocked drains up to one page from the cursor. It must run with reqMu
// held. A page that hits the row or byte limit stops there and marks the result
// truncated; the cursor is closed by the caller.
func (s *Session) readPageLocked(ctx context.Context, page *pageState) (engine.Result, error) {
	g := newGridBuilder()
	want := page.wantSize()
	if want <= 0 {
		page.more = false
		return g.result(), nil
	}

	count := 0
	hitCap := false
	for count < want {
		if !page.cur.Next(ctx) {
			break
		}
		var doc bson.D
		if err := page.cur.Decode(&doc); err != nil {
			return engine.Result{}, err
		}
		raw, err := bson.MarshalExtJSON(doc, false, false)
		if err != nil {
			return engine.Result{}, err
		}
		if page.fetched >= page.maxDocs ||
			(page.maxBytes > 0 && page.bytes+int64(len(raw)) > int64(page.maxBytes)) {
			hitCap = true
			break
		}
		g.add(doc, raw)
		page.fetched++
		page.bytes += int64(len(raw))
		count++
		if page.limit > 0 && int64(page.fetched) >= page.limit {
			break
		}
	}
	if err := page.cur.Err(); err != nil {
		return engine.Result{}, err
	}
	limitHit := page.limit > 0 && int64(page.fetched) >= page.limit
	page.more = false
	switch {
	case hitCap:
		page.truncated = true
	case !limitHit && count == want && page.atCap():
		// The page ended exactly on the row or byte limit. Look one row ahead
		// so "truncated" is reported only when something was left behind.
		if page.cur.Next(ctx) {
			page.truncated = true
		}
		if err := page.cur.Err(); err != nil {
			return engine.Result{}, err
		}
	case !limitHit && count == want:
		page.more = true
	}

	res := g.result()
	res.Database = page.database
	res.Collection = page.collection
	res.Truncated = page.truncated
	return res, nil
}

// atCap reports whether the session result limits are used up.
func (p *pageState) atCap() bool {
	if p.maxDocs > 0 && p.fetched >= p.maxDocs {
		return true
	}
	return p.maxBytes > 0 && p.bytes >= int64(p.maxBytes)
}

// wantSize is how many rows the next read may take: one page, or everything
// left under the query and session limits when all is set.
func (p *pageState) wantSize() int {
	want := p.pageSize
	if p.all {
		want = p.maxDocs - p.fetched
	}
	if p.limit > 0 {
		if left := p.limit - int64(p.fetched); left < int64(want) {
			want = int(left)
		}
	}
	return want
}

// closePageLocked releases the server cursor. The caller must hold reqMu.
func (s *Session) closePageLocked() {
	if s.page == nil {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	_ = s.page.cur.Close(ctx)
	cancel()
	s.page = nil
}

// Compile-time check: the page reader is an optional engine capability.
var _ engine.Pager = (*Session)(nil)
