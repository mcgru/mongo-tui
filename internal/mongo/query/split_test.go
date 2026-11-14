package query

import (
	"strings"
	"testing"
)

func TestSplitRequestsMQLAndEnvelope(t *testing.T) {
	input := `db.users.find({name: "a;b"}); {"op":"find","collection":"users","filter":{"name":"x;y"}};`
	parts, err := SplitRequests(input)
	if err != nil {
		t.Fatalf("SplitRequests() error = %v", err)
	}
	if len(parts) != 2 {
		t.Fatalf("parts = %d, want 2: %#v", len(parts), parts)
	}
	if !strings.HasPrefix(parts[0], "db.users.find") || !strings.Contains(parts[0], `"a;b"`) {
		t.Fatalf("first part = %q", parts[0])
	}
	if !strings.HasPrefix(parts[1], `{"op":"find"`) {
		t.Fatalf("second part = %q", parts[1])
	}
}

func TestSplitRequestsKeepsNestedSemicolons(t *testing.T) {
	parts, err := SplitRequests(`db.items.insertOne({text: "a;b", nested: {x: 1}}); db.items.countDocuments({})`)
	if err != nil {
		t.Fatalf("SplitRequests() error = %v", err)
	}
	if len(parts) != 2 {
		t.Fatalf("parts = %d, want 2: %#v", len(parts), parts)
	}
}

func TestSplitRequestsCommentsAndEmptyParts(t *testing.T) {
	parts, err := SplitRequests("// one\n db.items.find({}); /* two; */ db.items.countDocuments({}); ;")
	if err != nil {
		t.Fatalf("SplitRequests() error = %v", err)
	}
	if len(parts) != 2 {
		t.Fatalf("parts = %d, want 2: %#v", len(parts), parts)
	}
}

func TestSplitRequestsRejectsUnbalancedInput(t *testing.T) {
	tests := []string{
		`db.items.find({text: "unterminated})`,
		`db.items.find({}) /* open`,
		`db.items.find({}) }`,
		`db.items.find({}) )`,
	}
	for _, input := range tests {
		if _, err := SplitRequests(input); err == nil {
			t.Fatalf("SplitRequests(%q) = nil, want an error", input)
		}
	}
}
