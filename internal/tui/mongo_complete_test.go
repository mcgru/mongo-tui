package tui

import (
	"strings"
	"testing"

	"mongo-tui/internal/engine"
)

func TestMongoCompletionCollectionsAndMethods(t *testing.T) {
	m := mongoCompletionModel(t, []string{"name", "age"}, false)

	m.editor.SetValue("db.")
	m, _ = m.startMongoCompletion()
	if !hasCandidate(m.comp.items, "users") || !hasCandidate(m.comp.items, "orders") {
		t.Fatalf("collections = %+v, want users and orders", m.comp.items)
	}

	m.editor.SetValue("db.users.")
	m, _ = m.startMongoCompletion()
	if !hasCandidate(m.comp.items, "find") || !hasCandidate(m.comp.items, "aggregate") {
		t.Fatalf("methods = %+v, want find and aggregate", m.comp.items)
	}

	m.editor.SetValue("db.us")
	m, _ = m.startMongoCompletion()
	m = m.acceptCompletion()
	if got := m.editor.Value(); got != "db.users" {
		t.Fatalf("accepted value = %q, want db.users", got)
	}
}

func TestMongoCompletionFieldsAndOperators(t *testing.T) {
	m := mongoCompletionModel(t, []string{"name", "age", "meta.color"}, true)

	m.editor.SetValue("db.users.find({na")
	m, _ = m.startMongoCompletion()
	if !hasCandidate(m.comp.items, "name") {
		t.Fatalf("fields = %+v, want name", m.comp.items)
	}

	m.editor.SetValue("db.users.find({$g")
	m, _ = m.startMongoCompletion()
	if !hasCandidate(m.comp.items, "$gt") || !hasCandidate(m.comp.items, "$gte") {
		t.Fatalf("query operators = %+v, want $gt/$gte", m.comp.items)
	}

	m.editor.SetValue("db.users.updateOne({_id: 1}, {$s")
	m, _ = m.startMongoCompletion()
	if !hasCandidate(m.comp.items, "$set") {
		t.Fatalf("update operators = %+v, want $set", m.comp.items)
	}
}

func TestMongoCompletionStagesAndAggregateValues(t *testing.T) {
	m := mongoCompletionModel(t, []string{"status"}, true)

	m.editor.SetValue("db.orders.aggregate([{$m")
	m, _ = m.startMongoCompletion()
	if !hasCandidate(m.comp.items, "$match") || !hasCandidate(m.comp.items, "$merge") {
		t.Fatalf("stages = %+v, want $match/$merge", m.comp.items)
	}

	m.editor.SetValue("db.orders.aggregate([{$match: {$g")
	m, _ = m.startMongoCompletion()
	if !hasCandidate(m.comp.items, "$gt") {
		t.Fatalf("aggregate value operators = %+v, want $gt", m.comp.items)
	}
}

func TestMongoCompletionJSONValues(t *testing.T) {
	m := mongoCompletionModel(t, []string{"name"}, true)

	m.editor.SetValue(`{"op":"fi`)
	m, _ = m.startMongoCompletion()
	if !hasCandidate(m.comp.items, "find") {
		t.Fatalf("ops = %+v, want find", m.comp.items)
	}

	m.editor.SetValue(`{"collection":"us`)
	m, _ = m.startMongoCompletion()
	if !hasCandidate(m.comp.items, "users") {
		t.Fatalf("collections = %+v, want users", m.comp.items)
	}

	m.editor.SetValue(`{"database":"ap`)
	m, _ = m.startMongoCompletion()
	if !hasCandidate(m.comp.items, "app") {
		t.Fatalf("databases = %+v, want app", m.comp.items)
	}
}

func TestMongoCompletionUsesSelectedCollectionScope(t *testing.T) {
	m := mongoCompletionModel(t, []string{"name", "age"}, true)
	m.scopeCollection = "app.users"
	m.editor.SetValue("na")
	m, _ = m.startMongoCompletion()
	if !hasCandidate(m.comp.items, "name") {
		t.Fatalf("scope completion = %+v, want name", m.comp.items)
	}
}

func TestMongoCompletionLoadsFieldsOnDemand(t *testing.T) {
	m := mongoCompletionModel(t, []string{"name"}, false)
	delete(m.mongoSnap.fields, "app.users")

	m.editor.SetValue("db.users.find({na")
	updated, cmd := m.startMongoCompletion()
	m = updated
	if cmd == nil {
		t.Fatal("field completion returned no loading command")
	}
	msg := cmd()
	fieldsMsg, ok := msg.(mongoFieldsMsg)
	if !ok {
		t.Fatalf("command returned %T, want mongoFieldsMsg", msg)
	}
	if fieldsMsg.err != nil {
		t.Fatalf("fields error = %v", fieldsMsg.err)
	}
	afterFields, _ := m.Update(fieldsMsg)
	m = afterFields.(Model)
	if !m.comp.open || !hasCandidate(m.comp.items, "name") {
		t.Fatalf("completion after fields load = %+v", m.comp.items)
	}
}

func TestMongoCompletionNeedsMetadata(t *testing.T) {
	fake := &fakeSession{info: engine.Info{Database: "app", ServerVersion: "8.0.38"}}
	m := mongoModel(t, fake)
	m.mongoSnap = nil
	m.editor.SetValue("db.")
	m, _ = m.startMongoCompletion()
	if !strings.Contains(m.status, "still loading") {
		t.Fatalf("status = %q, want metadata loading", m.status)
	}
}

func mongoCompletionModel(t *testing.T, fields []string, cacheFields bool) Model {
	t.Helper()
	fieldInfos := make([]engine.FieldInfo, 0, len(fields))
	for _, name := range fields {
		fieldInfos = append(fieldInfos, engine.FieldInfo{Name: name, Types: "string"})
	}
	fake := &fakeMongoCatalog{
		fakeSession: &fakeSession{info: engine.Info{Database: "app", ServerVersion: "8.0.38"}},
		card:        engine.CollectionCard{Fields: fieldInfos},
	}
	m := mongoModel(t, fake)
	m.mongoSnap = &mongoSnapshot{
		databases: []string{"app", "admin"},
		collections: map[string][]string{
			"app": {"users", "orders"},
		},
		fields: map[string][]string{
			"app.users":  fields,
			"app.orders": fields,
		},
	}
	if !cacheFields {
		delete(m.mongoSnap.fields, "app.users")
		delete(m.mongoSnap.fields, "app.orders")
	}
	return m
}

func hasCandidate(items []suggestion, text string) bool {
	for _, item := range items {
		if item.Text == text {
			return true
		}
	}
	return false
}
