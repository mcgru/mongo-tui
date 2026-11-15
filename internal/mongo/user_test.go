package mongo

import (
	"context"
	"strings"
	"testing"
	"time"

	"mongo-tui/internal/engine"
)

func TestLiveUpsertUser(t *testing.T) {
	ctx := context.Background()
	s := liveSession(t, ConnectOptions{})
	targetDB := newTestDB(t, s)

	username := "tui_import_" + time.Now().Format("150405")
	const authSource = "admin"

	if err := s.UpsertUser(ctx, username, "p4ssword", targetDB, authSource, []string{"readWrite"}); err != nil {
		t.Fatalf("UpsertUser(create) error = %v", err)
	}

	detail := userDetail(t, s, username)
	if !strings.Contains(detail, "db:admin") {
		t.Fatalf("user detail = %q, want db:admin", detail)
	}
	if !strings.Contains(detail, targetDB+":readWrite") {
		t.Fatalf("user detail = %q, want %s:readWrite", detail, targetDB)
	}

	// The same name updates the password and replaces the roles.
	if err := s.UpsertUser(ctx, username, "newp4ss", targetDB, authSource, []string{"read", "dbOwner"}); err != nil {
		t.Fatalf("UpsertUser(update) error = %v", err)
	}
	detail = userDetail(t, s, username)
	if !strings.Contains(detail, targetDB+":read") || !strings.Contains(detail, targetDB+":dbOwner") {
		t.Fatalf("user detail = %q, want read and dbOwner on %s", detail, targetDB)
	}
	if strings.Contains(detail, "readWrite") {
		t.Fatalf("user detail = %q, readWrite should have been replaced", detail)
	}

	// An empty roles list defaults to readWrite.
	if err := s.UpsertUser(ctx, username, "newp4ss", targetDB, authSource, nil); err != nil {
		t.Fatalf("UpsertUser(default) error = %v", err)
	}
	if detail := userDetail(t, s, username); !strings.Contains(detail, targetDB+":readWrite") {
		t.Fatalf("user detail = %q, want default readWrite", detail)
	}

	// Clean up the user; the target database is dropped by newTestDB.
	_ = s.client.Database(authSource).RunCommand(ctx, d(e("dropUser", username))).Err()
}

func userDetail(t *testing.T, s *Session, username string) string {
	t.Helper()
	users, err := s.Users(context.Background())
	if err != nil {
		t.Fatalf("Users() error = %v", err)
	}
	item, ok := findItem(users, username)
	if !ok {
		t.Fatalf("Users() = %v, want %q", users, username)
	}
	return item.Detail
}

// TestLiveDatabaseMaterializesOnWrite checks the assumption behind staging a
// database: it is absent from listDatabases until a document is written into it.
func TestLiveDatabaseMaterializesOnWrite(t *testing.T) {
	ctx := context.Background()
	s := liveSession(t, ConnectOptions{})
	db := newTestDB(t, s)

	if err := s.SwitchDatabase(ctx, db); err != nil {
		t.Fatalf("SwitchDatabase() error = %v", err)
	}

	dbs := mustDatabases(t, s)
	if _, ok := findItem(dbs, db); ok {
		t.Fatalf("Databases() lists %q before any data was written", db)
	}

	if _, err := s.client.Database(db).Collection("seed").InsertOne(ctx, d(e("a", 1))); err != nil {
		t.Fatalf("InsertOne() error = %v", err)
	}

	dbs = mustDatabases(t, s)
	item, ok := findItem(dbs, db)
	if !ok {
		t.Fatalf("Databases() = %v, want %q after a document is written", dbs, db)
	}
	if !item.Current {
		t.Fatalf("materialized %q should be marked current", db)
	}
}

func mustDatabases(t *testing.T, s *Session) []engine.Item {
	t.Helper()
	dbs, err := s.Databases(context.Background())
	if err != nil {
		t.Fatalf("Databases() error = %v", err)
	}
	return dbs
}
