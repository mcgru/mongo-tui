package query

import (
	"strings"
	"testing"

	"go.mongodb.org/mongo-driver/v2/bson"
)

func TestParseFindPreservesFilterAndSortOrder(t *testing.T) {
	spec, err := Parse([]byte(`{
		"op": "find",
		"collection": "users",
		"filter": {"age": {"$gt": 30}, "name": "Ann"},
		"projection": {"name": 1, "_id": 0},
		"sort": {"name": 1, "age": -1},
		"limit": 10,
		"skip": 2
	}`))
	if err != nil {
		t.Fatalf("Parse() error = %v", err)
	}
	if spec.Op != Find || spec.Collection != "users" {
		t.Fatalf("spec = %+v, want find users", spec)
	}
	if got := keys(spec.Filter); got != "age,name" {
		t.Fatalf("filter keys = %q, want age,name", got)
	}
	if got := keys(spec.Sort); got != "name,age" {
		t.Fatalf("sort keys = %q, want name,age", got)
	}
	if spec.Limit != 10 || spec.Skip != 2 {
		t.Fatalf("limit/skip = %d/%d, want 10/2", spec.Limit, spec.Skip)
	}
}

func TestParseFindDefaultsFilterToEmptyDocument(t *testing.T) {
	spec, err := Parse([]byte(`{"op":"find","collection":"users"}`))
	if err != nil {
		t.Fatalf("Parse() error = %v", err)
	}
	if spec.Filter == nil || len(spec.Filter) != 0 {
		t.Fatalf("filter = %v, want an empty document", spec.Filter)
	}
}

func TestParseAggregatePreservesStageOrder(t *testing.T) {
	spec, err := Parse([]byte(`{
		"op": "aggregate",
		"collection": "orders",
		"pipeline": [
			{"$match": {"status": "paid"}},
			{"$sort": {"created_at": -1}},
			{"$limit": 5}
		]
	}`))
	if err != nil {
		t.Fatalf("Parse() error = %v", err)
	}
	if len(spec.Pipeline) != 3 {
		t.Fatalf("pipeline stages = %d, want 3", len(spec.Pipeline))
	}
	want := []string{"$match", "$sort", "$limit"}
	for i, stage := range spec.Pipeline {
		doc, ok := stage.(bson.D)
		if !ok || len(doc) == 0 {
			t.Fatalf("stage %d = %T %v, want a document", i, stage, stage)
		}
		if doc[0].Key != want[i] {
			t.Fatalf("stage %d starts with %q, want %q", i, doc[0].Key, want[i])
		}
	}
}

func TestParseWriteOperations(t *testing.T) {
	tests := []struct {
		name string
		body string
		op   Op
	}{
		{"insertOne", `{"op":"insertOne","collection":"users","document":{"name":"Ann"}}`, InsertOne},
		{"insertMany", `{"op":"insertMany","collection":"users","documents":[{"name":"Ann"},{"name":"Bob"}]}`, InsertMany},
		{"updateOne", `{"op":"updateOne","collection":"users","filter":{"_id":1},"update":{"$set":{"name":"Ann"}},"upsert":true}`, UpdateOne},
		{"replaceOne", `{"op":"replaceOne","collection":"users","filter":{"_id":1},"replacement":{"name":"Ann"}}`, ReplaceOne},
		{"deleteMany", `{"op":"deleteMany","collection":"users","filter":{}}`, DeleteMany},
		{"createIndex", `{"op":"createIndex","collection":"users","keys":{"email":1},"name":"email_1","unique":true}`, CreateIndex},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			spec, err := Parse([]byte(tc.body))
			if err != nil {
				t.Fatalf("Parse() error = %v", err)
			}
			if spec.Op != tc.op {
				t.Fatalf("op = %q, want %q", spec.Op, tc.op)
			}
			if !spec.IsWrite() {
				t.Fatalf("IsWrite() = false for %s", spec.Op)
			}
		})
	}
}

func TestParseTransactionOperations(t *testing.T) {
	for _, op := range []Op{BeginTransaction, CommitTransaction, AbortTransaction} {
		spec, err := Parse([]byte(`{"op":"` + string(op) + `"}`))
		if err != nil {
			t.Fatalf("Parse(%s) error = %v", op, err)
		}
		if spec.Op != op {
			t.Fatalf("op = %q, want %q", spec.Op, op)
		}
		if spec.IsWrite() {
			t.Fatalf("IsWrite() = true for %s", op)
		}
	}
	if _, err := Parse([]byte(`{"op":"beginTransaction","collection":"users"}`)); err == nil {
		t.Fatal("Parse() accepted a collection on beginTransaction")
	}
}

func TestParseRejectsBadEnvelopes(t *testing.T) {
	tests := []struct {
		name string
		body string
		want string
	}{
		{"invalid json", `{`, "invalid JSON"},
		{"not object", `[]`, "envelope must be a JSON object"},
		{"missing op", `{"collection":"users"}`, `missing "op"`},
		{"unknown op", `{"op":"remove","collection":"users"}`, "unknown op"},
		{"unknown field", `{"op":"find","collection":"users","where":{}}`, `unknown field "where"`},
		{"missing collection", `{"op":"find"}`, "needs a collection"},
		{"negative limit", `{"op":"find","collection":"users","limit":-1}`, "must not be negative"},
		{"fractional limit", `{"op":"find","collection":"users","limit":1.5}`, "must be an integer"},
		{"null collection", `{"op":"find","collection":null}`, "not null"},
		{"update without update", `{"op":"updateOne","collection":"users","filter":{}}`, `needs a non-empty "update"`},
		{"insert without document", `{"op":"insertOne","collection":"users"}`, `needs a non-empty "document"`},
		{"insertMany without documents", `{"op":"insertMany","collection":"users"}`, `needs a non-empty "documents"`},
		{"distinct without field", `{"op":"distinct","collection":"users"}`, `needs a non-empty "field"`},
		{"createIndex without keys", `{"op":"createIndex","collection":"users"}`, `needs a non-empty "keys"`},
		{"dropIndex without name", `{"op":"dropIndex","collection":"users"}`, `needs a non-empty "name"`},
		{"collMod without changes", `{"op":"collMod","collection":"users"}`, `needs "validator" or "options"`},
		{"rename without newName", `{"op":"renameCollection","collection":"users"}`, `needs a non-empty "newName"`},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			_, err := Parse([]byte(tc.body))
			if err == nil {
				t.Fatal("Parse() = nil, want an error")
			}
			if !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("error = %q, want it to contain %q", err, tc.want)
			}
		})
	}
}

func TestAggregateWriteStages(t *testing.T) {
	spec, err := Parse([]byte(`{
		"op":"aggregate",
		"collection":"orders",
		"pipeline":[{"$match":{}},{"$merge":{"into":"archive"}}]
	}`))
	if err != nil {
		t.Fatalf("Parse() error = %v", err)
	}
	if !spec.IsWrite() {
		t.Fatal("IsWrite() = false for $merge")
	}

	spec, err = Parse([]byte(`{"op":"aggregate","collection":"orders","pipeline":[{"$match":{}}]}`))
	if err != nil {
		t.Fatalf("Parse() error = %v", err)
	}
	if spec.IsWrite() {
		t.Fatal("IsWrite() = true for $match")
	}
}

func keys(doc bson.D) string {
	out := make([]string, 0, len(doc))
	for _, item := range doc {
		out = append(out, item.Key)
	}
	return strings.Join(out, ",")
}
