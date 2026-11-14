package mongo

import (
	"context"
	"strings"
	"testing"
	"time"

	"go.mongodb.org/mongo-driver/v2/bson"

	"mongo-tui/internal/engine"
)

func TestLiveCatalog(t *testing.T) {
	ctx := context.Background()
	s := liveSession(t, ConnectOptions{})
	db := newTestDB(t, s)
	if err := s.SwitchDatabase(ctx, db); err != nil {
		t.Fatalf("SwitchDatabase() error = %v", err)
	}

	run(t, s, ctx, env(db, `"op":"createCollection","collection":"items","validator":{"$jsonSchema":{"bsonType":"object","required":["name"],"properties":{"name":{"bsonType":"string"},"validator_only":{"bsonType":"string"}}}}`))
	run(t, s, ctx, env(db, `"op":"insertMany","collection":"items","documents":[
		{"_id":1,"name":"Ann","age":30,"meta":{"color":"red"},"tags":["a"]},
		{"_id":2,"name":"Bob","age":null}
	]`))
	run(t, s, ctx, env(db, `"op":"createIndex","collection":"items","keys":{"name":1},"name":"name_1"`))
	run(t, s, ctx, env(db, `"op":"createIndex","collection":"items","keys":{"indexed_only":1},"name":"indexed_only_1"`))

	// A view is created through the driver because the envelope does not have a
	// createView op yet.
	if err := s.client.Database(db).RunCommand(ctx, d(
		e("create", "items_view"),
		e("viewOn", "items"),
		e("pipeline", bson.A{d(e("$match", d(e("name", d(e("$exists", true))))))}),
	)).Err(); err != nil {
		t.Fatalf("create view: %v", err)
	}
	if err := s.client.Database(db).RunCommand(ctx, d(
		e("create", "metrics"),
		e("timeseries", d(e("timeField", "ts"))),
	)).Err(); err != nil {
		t.Fatalf("create time-series: %v", err)
	}
	if _, err := s.client.Database(db).Collection("metrics").InsertOne(ctx, d(
		e("ts", time.Now()),
		e("value", 1),
	)); err != nil {
		t.Fatalf("insert time-series document: %v", err)
	}

	dbs, err := s.Databases(ctx)
	if err != nil {
		t.Fatalf("Databases() error = %v", err)
	}
	item, ok := findItem(dbs, db)
	if !ok {
		t.Fatalf("Databases() = %v, want %q", dbs, db)
	}
	if !item.Current {
		t.Fatalf("database %q is not marked current", db)
	}

	collections, err := s.Collections(ctx, db)
	if err != nil {
		t.Fatalf("Collections() error = %v", err)
	}
	item, ok = findItem(collections, "items")
	if !ok {
		t.Fatalf("Collections() = %v, want items", collections)
	}
	if !strings.Contains(item.Detail, "validator") {
		t.Fatalf("collection detail = %q, want validator", item.Detail)
	}
	item, ok = findItem(collections, "metrics")
	if !ok || !strings.Contains(item.Detail, "time-series") {
		t.Fatalf("collections = %v, want metrics marked time-series", collections)
	}

	views, err := s.Views(ctx, db)
	if err != nil {
		t.Fatalf("Views() error = %v", err)
	}
	item, ok = findItem(views, "items_view")
	if !ok {
		t.Fatalf("Views() = %v, want items_view", views)
	}
	if !strings.Contains(item.Detail, "view") {
		t.Fatalf("view detail = %q, want view", item.Detail)
	}

	indexes, err := s.Indexes(ctx, db, "items")
	if err != nil {
		t.Fatalf("Indexes() error = %v", err)
	}
	for _, name := range []string{"_id_", "name_1", "indexed_only_1"} {
		if _, ok := findItem(indexes, name); !ok {
			t.Fatalf("Indexes() = %v, want %s", indexes, name)
		}
	}

	card, err := s.CollectionCard(ctx, db, "items")
	if err != nil {
		t.Fatalf("CollectionCard() error = %v", err)
	}
	if !card.Exists || card.Type != "collection" {
		t.Fatalf("card = %+v, want an existing collection", card)
	}
	if !strings.Contains(card.Validator, "required") {
		t.Fatalf("validator = %q, want the JSON schema", card.Validator)
	}
	if len(card.Indexes) < 2 {
		t.Fatalf("indexes = %v, want _id_ and name_1", card.Indexes)
	}
	if card.Count != 2 || card.SampleSize != 2 {
		t.Fatalf("count/sample = %d/%d, want 2/2", card.Count, card.SampleSize)
	}
	if card.SizeBytes <= 0 {
		t.Fatalf("size = %d, want a positive storage size", card.SizeBytes)
	}
	if card.FieldsError != "" {
		t.Fatalf("field sample error = %q", card.FieldsError)
	}
	if _, ok := findField(card.Fields, "name"); !ok {
		t.Fatalf("fields = %v, want name", card.Fields)
	}
	if _, ok := findField(card.Fields, "meta.color"); !ok {
		t.Fatalf("fields = %v, want the nested meta.color field", card.Fields)
	}
	indexedOnly, ok := findField(card.Fields, "indexed_only")
	if !ok || indexedOnly.Types != "index" {
		t.Fatalf("fields = %v, want indexed_only marked as an index key", card.Fields)
	}
	validatorOnly, ok := findField(card.Fields, "validator_only")
	if !ok || validatorOnly.Types != "validator" {
		t.Fatalf("fields = %v, want validator_only marked as a validator property", card.Fields)
	}

	viewCard, err := s.CollectionCard(ctx, db, "items_view")
	if err != nil {
		t.Fatalf("view card error = %v", err)
	}
	if !viewCard.Exists || viewCard.Type != "view" {
		t.Fatalf("view card = %+v, want an existing view", viewCard)
	}

	timeSeriesCard, err := s.CollectionCard(ctx, db, "metrics")
	if err != nil {
		t.Fatalf("time-series card error = %v", err)
	}
	if !timeSeriesCard.Exists || timeSeriesCard.Type != "time-series" {
		t.Fatalf("time-series card = %+v, want type time-series", timeSeriesCard)
	}

	missing, err := s.CollectionCard(ctx, db, "no_such_collection")
	if err != nil {
		t.Fatalf("missing card error = %v", err)
	}
	if missing.Exists {
		t.Fatalf("missing card = %+v, want Exists false", missing)
	}

	if _, err := s.Users(ctx); err != nil {
		t.Fatalf("Users() error = %v", err)
	}
	if _, err := s.Roles(ctx); err != nil {
		t.Fatalf("Roles() error = %v", err)
	}
}

func findItem(items []engine.Item, name string) (engine.Item, bool) {
	for _, item := range items {
		if item.Name == name {
			return item, true
		}
	}
	return engine.Item{}, false
}

func findField(fields []engine.FieldInfo, name string) (engine.FieldInfo, bool) {
	for _, field := range fields {
		if field.Name == name {
			return field, true
		}
	}
	return engine.FieldInfo{}, false
}
