package tui

import (
	"strings"
	"testing"

	"mongo-tui/internal/engine"
)

// The help screen must describe MongoDB, not the removed psql path.
func TestMongoHelpMatchesTheProduct(t *testing.T) {
	m := mongoModel(t, &fakeMongoCatalog{
		fakeSession: &fakeSession{info: engine.Info{ServerVersion: "8.0.38", Database: "app"}},
	})
	m.help = true
	view := m.View()

	for _, stale := range []string{"SET ROLE", "search_path", "default_tablespace", "ALTER TABLE", "CREATE OR REPLACE", "SELECT *"} {
		if strings.Contains(view, stale) {
			t.Fatalf("help still mentions %q:\n%s", stale, view)
		}
	}
	for _, want := range []string{"role editor", "collMod", "insertOne", "OTHER ROLES", "regexp search", "n / N"} {
		if !strings.Contains(view, want) {
			t.Fatalf("help is missing %q:\n%s", want, view)
		}
	}
}
