package mongo

import (
	"context"
	"os"
	"strings"
	"testing"
	"time"
)

func authSession(t *testing.T, uriEnv, passwordEnv string) *Session {
	t.Helper()
	uri := os.Getenv(uriEnv)
	password := os.Getenv(passwordEnv)
	if uri == "" || password == "" {
		t.Skip(uriEnv + " or " + passwordEnv + " is not set")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	s, err := Connect(ctx, ConnectOptions{URI: uri, Password: password, Timeout: 10 * time.Second})
	if err != nil {
		t.Fatalf("Connect() error = %v", err)
	}
	t.Cleanup(func() { s.Close() })
	return s
}

func TestLiveAuthTLS(t *testing.T) {
	s := authSession(t, "MONGODB_AUTH_URI", "MONGODB_AUTH_PASSWORD")
	if got := s.Summary().User; got != "root" {
		t.Fatalf("user = %q, want root", got)
	}

	ctx := context.Background()
	res, err := s.Execute(ctx, `{"database":"app","op":"find","collection":"items","filter":{"_id":1}}`)
	if err != nil || res.Error {
		t.Fatalf("authenticated find: err=%v res=%+v", err, res)
	}
	if len(res.Rows) != 1 || cell(res, 0, "name") != "Ann" {
		t.Fatalf("rows = %v, want Ann", res.Rows)
	}

	// A wrong password must be reported as an auth error, not a network error.
	badCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_, err = Connect(badCtx, ConnectOptions{
		URI:      os.Getenv("MONGODB_AUTH_URI"),
		Password: "definitely-wrong",
		Timeout:  3 * time.Second,
	})
	if err == nil {
		t.Fatal("Connect() with a wrong password succeeded")
	}
	if !strings.Contains(strings.ToLower(err.Error()), "auth") {
		t.Fatalf("wrong-password error = %v, want an auth error", err)
	}
}

func TestLiveReaderCannotWrite(t *testing.T) {
	s := authSession(t, "MONGODB_AUTH_READER_URI", "MONGODB_AUTH_READER_PASSWORD")
	ctx := context.Background()

	res, err := s.Execute(ctx, `{"database":"app","op":"insertOne","collection":"items","document":{"_id":99}}`)
	if err != nil {
		t.Fatalf("reader insert transport error = %v", err)
	}
	if !res.Error {
		t.Fatal("reader insert was accepted")
	}
	if res.Code != "13" {
		t.Fatalf("reader insert code = %q, want 13 (Unauthorized)", res.Code)
	}
}
