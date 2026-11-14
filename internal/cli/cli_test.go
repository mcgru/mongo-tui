package cli

import (
	"bytes"
	"strings"
	"testing"
)

func TestParseMongoURI(t *testing.T) {
	tests := []struct {
		name string
		args []string
		want string
	}{
		{"positional", []string{"mongodb://ann@db.example:27017/app?replicaSet=rs0"},
			"mongodb://ann@db.example:27017/app?replicaSet=rs0"},
		{"srv", []string{"mongodb+srv://cluster.example/app"}, "mongodb+srv://cluster.example/app"},
		{"after separator", []string{"--", "mongodb://db.example/app"}, "mongodb://db.example/app"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := Parse(tt.args)
			if err != nil {
				t.Fatalf("Parse() error = %v", err)
			}
			if got.MongoURI != tt.want {
				t.Fatalf("MongoURI = %q, want %q", got.MongoURI, tt.want)
			}
		})
	}
}

func TestParseReadOnlyWithURI(t *testing.T) {
	got, err := Parse([]string{"--read-only", "mongodb://db.example/app"})
	if err != nil {
		t.Fatalf("Parse() error = %v", err)
	}
	if !got.ReadOnly || got.MongoURI != "mongodb://db.example/app" {
		t.Fatalf("options = %+v", got)
	}
}

func TestParseProfileAndFlags(t *testing.T) {
	got, err := Parse([]string{"--profile", "mongo-prod", "--read-only", "--no-history"})
	if err != nil {
		t.Fatalf("Parse() error = %v", err)
	}
	if got.Profile != "mongo-prod" || !got.ReadOnly || !got.NoHistory {
		t.Fatalf("options = %+v", got)
	}
	got, err = Parse([]string{"--profile=mongo-prod"})
	if err != nil || got.Profile != "mongo-prod" {
		t.Fatalf("--profile= options = %+v err=%v", got, err)
	}
}

func TestParseModes(t *testing.T) {
	for _, args := range [][]string{{"--help"}, {"-?"}, {"-h"}} {
		got, err := Parse(args)
		if err != nil || got.Mode != ModeHelp {
			t.Fatalf("Parse(%v) = %+v err=%v, want help", args, got, err)
		}
	}
	for _, args := range [][]string{{"--version"}, {"-V"}} {
		got, err := Parse(args)
		if err != nil || got.Mode != ModeVersion {
			t.Fatalf("Parse(%v) = %+v err=%v, want version", args, got, err)
		}
	}
}

func TestParseRejectsBadInput(t *testing.T) {
	tests := []struct {
		args []string
		want string
	}{
		{[]string{"--profile", "p", "mongodb://db/app"}, "cannot be combined"},
		{[]string{"--nope"}, "unknown option"},
		{[]string{"-d", "db"}, "unknown option"},
		{[]string{"postgresql://db/app"}, "expected a mongodb:// or mongodb+srv:// URI"},
		{[]string{"mongodb://a/app", "mongodb://b/app"}, "only one MongoDB URI"},
		{[]string{"--profile"}, "--profile needs a value"},
	}
	for _, tt := range tests {
		_, err := Parse(tt.args)
		if err == nil || !strings.Contains(err.Error(), tt.want) {
			t.Fatalf("Parse(%v) error = %v, want %q", tt.args, err, tt.want)
		}
	}
}

func TestRunHandlesHelpAndVersion(t *testing.T) {
	var out, errOut bytes.Buffer
	called := false
	launch := func(Options) int { called = true; return 0 }

	code := Run([]string{"--help"}, &out, &errOut, "9.9.9", launch)
	if code != 0 || called || !strings.Contains(out.String(), "mongo-tui — terminal client for MongoDB") {
		t.Fatalf("help code=%d called=%v out=%q", code, called, out.String())
	}
	out.Reset()
	code = Run([]string{"--version"}, &out, &errOut, "9.9.9", launch)
	if code != 0 || out.String() != "mongo-tui 9.9.9\n" {
		t.Fatalf("version code=%d out=%q", code, out.String())
	}
}

func TestRunPassesOptionsAndReportsUsage(t *testing.T) {
	var out, errOut bytes.Buffer
	var got Options
	code := Run([]string{"mongodb://db/app", "--read-only"}, &out, &errOut, "1", func(o Options) int {
		got = o
		return 7
	})
	if code != 7 || got.MongoURI != "mongodb://db/app" || !got.ReadOnly {
		t.Fatalf("code=%d options=%+v", code, got)
	}

	code = Run([]string{"--nope"}, &out, &errOut, "1", func(Options) int { return 0 })
	if code != 2 || !strings.Contains(errOut.String(), "try: mongo-tui --help") {
		t.Fatalf("code=%d stderr=%q", code, errOut.String())
	}
}
