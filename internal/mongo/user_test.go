package mongo

import (
	"context"
	"strings"
	"testing"
	"time"
)

func TestLiveUpsertUser(t *testing.T) {
	ctx := context.Background()
	s := liveSession(t, ConnectOptions{})
	targetDB := newTestDB(t, s)

	username := "tui_import_" + time.Now().Format("150405")
	const authSource = "admin"

	if err := s.UpsertUser(ctx, username, "p4ssword", targetDB, authSource); err != nil {
		t.Fatalf("UpsertUser(create) error = %v", err)
	}

	users, err := s.Users(ctx)
	if err != nil {
		t.Fatalf("Users() error = %v", err)
	}
	item, ok := findItem(users, username)
	if !ok {
		t.Fatalf("Users() = %v, want %q", users, username)
	}
	if !strings.Contains(item.Detail, "db:admin") {
		t.Fatalf("user detail = %q, want db:admin", item.Detail)
	}

	// The same name updates the password and roles instead of failing.
	if err := s.UpsertUser(ctx, username, "newp4ss", targetDB, authSource); err != nil {
		t.Fatalf("UpsertUser(update) error = %v", err)
	}

	// Clean up the user; the target database is dropped by newTestDB.
	_ = s.client.Database(authSource).RunCommand(ctx, d(e("dropUser", username))).Err()
}
