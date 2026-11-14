package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestParseMongoURIs(t *testing.T) {
	data := []byte(`
connect("mongodb://ann:secret@db.example:27017/app?replicaSet=rs0")
mongosh "mongodb+srv://bob@cluster.example/analytics"
const again = "mongodb://ann:secret@db.example:27017/app?replicaSet=rs0";
`)
	got := ParseMongoURIs(data)
	if len(got) != 2 {
		t.Fatalf("URIs = %#v, want 2 unique", got)
	}
	if got[0] != "mongodb://ann:secret@db.example:27017/app?replicaSet=rs0" {
		t.Fatalf("first URI = %q", got[0])
	}
	if got[1] != "mongodb+srv://bob@cluster.example/analytics" {
		t.Fatalf("second URI = %q", got[1])
	}
}

func TestNormalizeMongoURIHandlesMultiHostAndPlaceholders(t *testing.T) {
	const multi = `mongodb://user:pass@mongod1:27021,mongod2:27022,mongod3:27023/stage?authSource=admin&replicaset=dbrs&appName=mongosh+2.9.2`
	got, ok := NormalizeMongoURI(multi)
	if !ok {
		t.Fatal("NormalizeMongoURI() rejected a multi-host URI")
	}
	if strings.Contains(got, "pass") || !strings.Contains(got, "mongod1:27021,mongod2:27022,mongod3:27023") {
		t.Fatalf("normalized multi-host URI = %q", got)
	}

	// A history line may contain a placeholder instead of real userinfo.
	const placeholder = `mongodb://<credentials>@mongod1:27021,mongod2:27022,mongod3:27023/stage?authSource=admin&replicaset=dbrs&appName=mongosh+2.9.2`
	got, ok = NormalizeMongoURI(placeholder)
	if !ok {
		t.Fatal("NormalizeMongoURI() rejected a placeholder userinfo")
	}
	if strings.Contains(got, "<") || !strings.Contains(got, "mongod1:27021,mongod2:27022,mongod3:27023") {
		t.Fatalf("normalized placeholder URI = %q", got)
	}
}

func TestParseMongoURIsSkipsComments(t *testing.T) {
	data := []byte("# mongodb://old.example/old\n  # mongodb://commented.example/app\nconnect(\"mongodb://real.example/app\") # trailing comment\n")
	got := ParseMongoURIs(data)
	if len(got) != 1 || got[0] != "mongodb://real.example/app" {
		t.Fatalf("URIs = %#v, want only the real one", got)
	}
}

func TestNormalizeMongoURIStripsPassword(t *testing.T) {
	got, ok := NormalizeMongoURI("mongodb://ann:secret@db.example:27017/app?authSource=admin")
	if !ok {
		t.Fatal("NormalizeMongoURI() rejected a valid URI")
	}
	if strings.Contains(got, "secret") || !strings.Contains(got, "ann@db.example") {
		t.Fatalf("normalized URI = %q, password was not removed", got)
	}
	if _, ok := NormalizeMongoURI("postgresql://db/app"); ok {
		t.Fatal("NormalizeMongoURI() accepted a PostgreSQL URI")
	}
}

func TestMongoLabel(t *testing.T) {
	if got := MongoLabel("mongodb://ann@db.example:27017/app"); got != "ann@db.example:27017/app" {
		t.Fatalf("label = %q", got)
	}
	if got := MongoLabel("mongodb+srv://cluster.example"); got != "cluster.example" {
		t.Fatalf("label = %q", got)
	}
	if got := MongoLabel("mongodb://uuu@mongod1:27021,mongod2:27022,host3:27023/stage"); got != "uuu@mongod1:27021,mongod2:27022,host3:27023/stage" {
		t.Fatalf("multi-host label = %q", got)
	}
}

func TestMongoFilesListsOnlyRegularFiles(t *testing.T) {
	home := t.TempDir()
	if err := os.WriteFile(filepath.Join(home, ".mongorc.js"), []byte("one"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(home, ".mongo_history"), []byte("two"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(filepath.Join(home, ".mongodb"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(home, "other"), []byte("three"), 0o600); err != nil {
		t.Fatal(err)
	}
	files, err := MongoFiles(home)
	if err != nil {
		t.Fatalf("MongoFiles() error = %v", err)
	}
	if len(files) != 2 {
		t.Fatalf("files = %v, want only .mongo* regular files", files)
	}
	for _, file := range files {
		if strings.Contains(file, "other") || strings.Contains(file, ".mongodb") {
			t.Fatalf("unexpected file %q", file)
		}
	}
}

func TestHasMongoURI(t *testing.T) {
	st := NewState()
	st.Profiles = []Profile{{Engine: "mongo", URI: "mongodb://db/app"}}
	if !st.HasMongoURI("mongodb://db/app") {
		t.Fatal("HasMongoURI() = false, want true")
	}
	if st.HasMongoURI("mongodb://db/other") {
		t.Fatal("HasMongoURI() = true for a different URI")
	}
}
