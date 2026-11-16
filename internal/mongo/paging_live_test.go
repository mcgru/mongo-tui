package mongo

import (
	"context"
	"testing"

	"go.mongodb.org/mongo-driver/v2/bson"
)

func pagingDocs(t *testing.T, s *Session, db string, n int) {
	t.Helper()
	docs := make([]any, 0, n)
	for i := 1; i <= n; i++ {
		docs = append(docs, bson.D{{Key: "_id", Value: i}, {Key: "n", Value: i}})
	}
	ctx := context.Background()
	if _, err := s.client.Database(db).Collection("items").InsertMany(ctx, docs); err != nil {
		t.Fatalf("InsertMany: %v", err)
	}
}

// The first page is small, more streams the cursor, and an exhausted cursor
// answers with a clear error instead of silently repeating rows.
func TestLivePagedFind(t *testing.T) {
	ctx := context.Background()
	s := liveSession(t, ConnectOptions{})
	db := newTestDB(t, s)
	pagingDocs(t, s, db, 65)
	request := env(db, `"op":"find","collection":"items","sort":{"_id":1}`)

	res, more, err := s.RunPaged(ctx, request, 30)
	if err != nil || res.Error {
		t.Fatalf("RunPaged: err=%v res=%v", err, res.Messages)
	}
	if len(res.Rows) != 30 || !more {
		t.Fatalf("first page rows=%d more=%v, want 30/true", len(res.Rows), more)
	}

	res, more, err = s.FetchPage(ctx, false)
	if err != nil {
		t.Fatalf("FetchPage: %v", err)
	}
	if len(res.Rows) != 30 || !more {
		t.Fatalf("second page rows=%d more=%v, want 30/true", len(res.Rows), more)
	}

	res, more, err = s.FetchPage(ctx, false)
	if err != nil {
		t.Fatalf("FetchPage: %v", err)
	}
	if len(res.Rows) != 5 || more {
		t.Fatalf("last page rows=%d more=%v, want 5/false", len(res.Rows), more)
	}

	if _, _, err := s.FetchPage(ctx, false); err == nil {
		t.Fatal("FetchPage after the cursor closed returned no error")
	}
}

// Load-all stops at the session row limit even when the collection is larger,
// and reports the truncation instead of growing without bound.
func TestLivePagedLoadAllHitsCaps(t *testing.T) {
	ctx := context.Background()
	s := liveSession(t, ConnectOptions{MaxDocs: 40, MaxBytes: 1 << 20})
	db := newTestDB(t, s)
	pagingDocs(t, s, db, 65)
	request := env(db, `"op":"find","collection":"items","sort":{"_id":1}`)

	if _, more, err := s.RunPaged(ctx, request, 30); err != nil || !more {
		t.Fatalf("RunPaged: err=%v more=%v", err, more)
	}
	res, more, err := s.FetchPage(ctx, true)
	if err != nil {
		t.Fatalf("FetchPage(all): %v", err)
	}
	if len(res.Rows) != 10 {
		t.Fatalf("load-all rows = %d, want the remaining 10 under MaxDocs=40", len(res.Rows))
	}
	if more || !res.Truncated {
		t.Fatalf("load-all more=%v truncated=%v, want false/true", more, res.Truncated)
	}
}

// A find with its own limit pages to that limit and then ends, without reading
// rows the query never asked for.
func TestLivePagedHonoursQueryLimit(t *testing.T) {
	ctx := context.Background()
	s := liveSession(t, ConnectOptions{})
	db := newTestDB(t, s)
	pagingDocs(t, s, db, 50)
	request := env(db, `"op":"find","collection":"items","sort":{"_id":1},"limit":35`)

	res, more, err := s.RunPaged(ctx, request, 30)
	if err != nil || len(res.Rows) != 30 || !more {
		t.Fatalf("first page rows=%d more=%v err=%v, want 30/true", len(res.Rows), more, err)
	}
	if _, more, err = s.FetchPage(ctx, false); err != nil || more {
		t.Fatalf("second page more=%v err=%v, want false", more, err)
	}
	if _, _, err := s.FetchPage(ctx, false); err == nil {
		t.Fatal("paging continued past the query limit")
	}
}
