// Package query defines the JSON envelope mongo-tui executes.
//
// One envelope is one operation:
//
//	{"op":"find","collection":"users","filter":{"age":{"$gt":30}},"limit":100}
//	{"op":"aggregate","collection":"orders","pipeline":[{"$match":{...}}]}
//	{"op":"insertOne","collection":"users","document":{"name":"Ann"}}
//
// A restricted MQL front-end will compile to the same Spec later; this package
// only parses and validates the canonical form. Unknown fields and unknown ops
// are errors, not silently ignored: a typo in a query must not turn into a
// different operation.
package query

import (
	"bytes"
	"encoding/json"
	"fmt"
	"sort"
	"strings"

	"go.mongodb.org/mongo-driver/v2/bson"
)

// Op is one supported operation.
type Op string

const (
	Find              Op = "find"
	Aggregate         Op = "aggregate"
	Count             Op = "count"
	Distinct          Op = "distinct"
	InsertOne         Op = "insertOne"
	InsertMany        Op = "insertMany"
	UpdateOne         Op = "updateOne"
	UpdateMany        Op = "updateMany"
	ReplaceOne        Op = "replaceOne"
	DeleteOne         Op = "deleteOne"
	DeleteMany        Op = "deleteMany"
	CreateCollection  Op = "createCollection"
	DropCollection    Op = "dropCollection"
	CreateIndex       Op = "createIndex"
	DropIndex         Op = "dropIndex"
	CollMod           Op = "collMod"
	RenameCollection  Op = "renameCollection"
	BeginTransaction  Op = "beginTransaction"
	CommitTransaction Op = "commitTransaction"
	AbortTransaction  Op = "abortTransaction"
)

// Spec is one parsed operation. Fields that do not apply to the chosen Op stay
// zero; validation rejects fields that are present but meaningless.
type Spec struct {
	Op         Op
	Database   string
	Collection string

	Filter     bson.D
	Projection bson.D
	Sort       bson.D
	Limit      int64
	Skip       int64

	Pipeline     bson.A
	AllowDiskUse bool

	Document  bson.D
	Documents []bson.D
	Update    bson.D

	Replacement bson.D
	Upsert      bool

	Field string

	Keys   bson.D
	Name   string
	Unique bool

	NewName    string
	DropTarget bool

	Validator bson.D
	Options   bson.D
}

// Parse reads one JSON envelope. Trailing data, a non-object, an unknown op and
// an unknown field are errors.
func Parse(data []byte) (Spec, error) {
	trimmed := bytes.TrimSpace(data)
	if len(trimmed) == 0 || trimmed[0] != '{' {
		return Spec{}, fmt.Errorf("envelope must be a JSON object")
	}
	var raw map[string]json.RawMessage
	if err := json.Unmarshal(data, &raw); err != nil {
		return Spec{}, fmt.Errorf("invalid JSON: %w", err)
	}
	if raw == nil {
		return Spec{}, fmt.Errorf("envelope must be a JSON object")
	}
	opRaw, ok := raw["op"]
	if !ok {
		return Spec{}, fmt.Errorf(`missing "op"`)
	}
	var opText string
	if err := json.Unmarshal(opRaw, &opText); err != nil || opText == "" {
		return Spec{}, fmt.Errorf(`"op" must be a non-empty string`)
	}
	op := Op(opText)
	if !op.known() {
		return Spec{}, fmt.Errorf("unknown op %q; known: %s", opText, strings.Join(knownOps(), ", "))
	}

	allowed := allowedKeys(op)
	for key := range raw {
		if !allowed[key] {
			return Spec{}, fmt.Errorf("unknown field %q for op %q", key, op)
		}
	}

	s := Spec{Op: op}
	var err error
	if s.Database, err = optionalString(raw, "database"); err != nil {
		return Spec{}, err
	}
	if s.Collection, err = optionalString(raw, "collection"); err != nil {
		return Spec{}, err
	}

	docFields := []struct {
		name string
		dst  *bson.D
	}{
		{"filter", &s.Filter},
		{"projection", &s.Projection},
		{"sort", &s.Sort},
		{"document", &s.Document},
		{"update", &s.Update},
		{"replacement", &s.Replacement},
		{"keys", &s.Keys},
		{"validator", &s.Validator},
		{"options", &s.Options},
	}
	for _, f := range docFields {
		if value, ok := raw[f.name]; ok {
			if *f.dst, err = decodeDoc(value); err != nil {
				return Spec{}, fmt.Errorf("%q: %w", f.name, err)
			}
		}
	}

	if value, ok := raw["pipeline"]; ok {
		if s.Pipeline, err = decodeArray(value); err != nil {
			return Spec{}, fmt.Errorf("%q: %w", "pipeline", err)
		}
	}
	if value, ok := raw["documents"]; ok {
		if s.Documents, err = decodeDocuments(value); err != nil {
			return Spec{}, fmt.Errorf("%q: %w", "documents", err)
		}
	}
	if value, ok := raw["limit"]; ok {
		if s.Limit, err = decodeInt64(value); err != nil {
			return Spec{}, fmt.Errorf("%q: %w", "limit", err)
		}
	}
	if value, ok := raw["skip"]; ok {
		if s.Skip, err = decodeInt64(value); err != nil {
			return Spec{}, fmt.Errorf("%q: %w", "skip", err)
		}
	}
	if value, ok := raw["field"]; ok {
		if s.Field, err = decodeString(value); err != nil {
			return Spec{}, fmt.Errorf("%q: %w", "field", err)
		}
	}
	if value, ok := raw["name"]; ok {
		if s.Name, err = decodeString(value); err != nil {
			return Spec{}, fmt.Errorf("%q: %w", "name", err)
		}
	}
	if value, ok := raw["newName"]; ok {
		if s.NewName, err = decodeString(value); err != nil {
			return Spec{}, fmt.Errorf("%q: %w", "newName", err)
		}
	}
	if value, ok := raw["upsert"]; ok {
		if s.Upsert, err = decodeBool(value); err != nil {
			return Spec{}, fmt.Errorf("%q: %w", "upsert", err)
		}
	}
	if value, ok := raw["unique"]; ok {
		if s.Unique, err = decodeBool(value); err != nil {
			return Spec{}, fmt.Errorf("%q: %w", "unique", err)
		}
	}
	if value, ok := raw["dropTarget"]; ok {
		if s.DropTarget, err = decodeBool(value); err != nil {
			return Spec{}, fmt.Errorf("%q: %w", "dropTarget", err)
		}
	}
	if value, ok := raw["allowDiskUse"]; ok {
		if s.AllowDiskUse, err = decodeBool(value); err != nil {
			return Spec{}, fmt.Errorf("%q: %w", "allowDiskUse", err)
		}
	}

	if err := s.validate(); err != nil {
		return Spec{}, err
	}
	return s, nil
}

// KnownOps returns the envelope operation names, sorted, for completion and
// diagnostics.
func KnownOps() []string { return knownOps() }

// IsWrite reports whether the operation changes data or metadata. The read-only
// guard uses it before anything reaches the server; it is deliberately
// conservative for aggregation pipelines.
func (s Spec) IsWrite() bool {
	switch s.Op {
	case InsertOne, InsertMany, UpdateOne, UpdateMany, ReplaceOne,
		DeleteOne, DeleteMany, CreateCollection, DropCollection,
		CreateIndex, DropIndex, CollMod, RenameCollection:
		return true
	case Aggregate:
		for _, stage := range s.Pipeline {
			doc, ok := stageDocument(stage)
			if !ok {
				continue
			}
			for _, item := range doc {
				if item.Key == "$out" || item.Key == "$merge" {
					return true
				}
			}
		}
	}
	return false
}

// String names the operation for errors and status lines.
func (s Spec) String() string {
	switch {
	case s.Database != "" && s.Collection != "":
		return fmt.Sprintf("%s %s.%s", s.Op, s.Database, s.Collection)
	case s.Collection != "":
		return fmt.Sprintf("%s %s", s.Op, s.Collection)
	default:
		return string(s.Op)
	}
}

func (s *Spec) validate() error {
	if !s.Op.known() {
		return fmt.Errorf("unknown op %q", s.Op)
	}
	if s.Limit < 0 {
		return fmt.Errorf(`"limit" must not be negative`)
	}
	if s.Skip < 0 {
		return fmt.Errorf(`"skip" must not be negative`)
	}

	switch s.Op {
	case BeginTransaction, CommitTransaction, AbortTransaction:
		if s.Collection != "" {
			return fmt.Errorf("op %q does not take a collection", s.Op)
		}
		return nil
	}

	if s.Collection == "" {
		return fmt.Errorf("op %q needs a collection", s.Op)
	}

	switch s.Op {
	case Find, Count:
		if s.Filter == nil {
			s.Filter = bson.D{}
		}
	case UpdateOne, UpdateMany, ReplaceOne, DeleteOne, DeleteMany:
		if s.Filter == nil {
			return fmt.Errorf(`%s needs an explicit "filter" (use {} to target every document)`, s.Op)
		}
	case Aggregate:
		if s.Pipeline == nil {
			s.Pipeline = bson.A{}
		}
	}

	switch s.Op {
	case Distinct:
		if s.Field == "" {
			return fmt.Errorf(`distinct needs a non-empty "field"`)
		}
	case InsertOne:
		if len(s.Document) == 0 {
			return fmt.Errorf(`insertOne needs a non-empty "document"`)
		}
	case InsertMany:
		if len(s.Documents) == 0 {
			return fmt.Errorf(`insertMany needs a non-empty "documents" array`)
		}
	case UpdateOne, UpdateMany:
		if len(s.Update) == 0 {
			return fmt.Errorf(`%s needs a non-empty "update"`, s.Op)
		}
	case ReplaceOne:
		if len(s.Replacement) == 0 {
			return fmt.Errorf(`replaceOne needs a non-empty "replacement"`)
		}
	case CreateIndex:
		if len(s.Keys) == 0 {
			return fmt.Errorf(`createIndex needs a non-empty "keys" document`)
		}
	case DropIndex:
		if s.Name == "" {
			return fmt.Errorf(`dropIndex needs a non-empty "name"`)
		}
	case CollMod:
		if len(s.Validator) == 0 && len(s.Options) == 0 {
			return fmt.Errorf(`collMod needs "validator" or "options"`)
		}
	case RenameCollection:
		if s.NewName == "" {
			return fmt.Errorf(`renameCollection needs a non-empty "newName"`)
		}
	}
	return nil
}

func (o Op) known() bool {
	switch o {
	case Find, Aggregate, Count, Distinct, InsertOne, InsertMany, UpdateOne,
		UpdateMany, ReplaceOne, DeleteOne, DeleteMany, CreateCollection,
		DropCollection, CreateIndex, DropIndex, CollMod, RenameCollection,
		BeginTransaction, CommitTransaction, AbortTransaction:
		return true
	}
	return false
}

func knownOps() []string {
	ops := []string{
		string(Find), string(Aggregate), string(Count), string(Distinct),
		string(InsertOne), string(InsertMany), string(UpdateOne), string(UpdateMany),
		string(ReplaceOne), string(DeleteOne), string(DeleteMany),
		string(CreateCollection), string(DropCollection), string(CreateIndex),
		string(DropIndex), string(CollMod), string(RenameCollection),
		string(BeginTransaction), string(CommitTransaction), string(AbortTransaction),
	}
	sort.Strings(ops)
	return ops
}

func allowedKeys(op Op) map[string]bool {
	keys := map[string]bool{"op": true, "database": true}
	add := func(names ...string) {
		for _, name := range names {
			keys[name] = true
		}
	}
	switch op {
	case Find:
		add("collection", "filter", "projection", "sort", "limit", "skip")
	case Aggregate:
		add("collection", "pipeline", "allowDiskUse")
	case Count:
		add("collection", "filter", "limit", "skip")
	case Distinct:
		add("collection", "field", "filter")
	case InsertOne:
		add("collection", "document")
	case InsertMany:
		add("collection", "documents")
	case UpdateOne, UpdateMany:
		add("collection", "filter", "update", "upsert")
	case ReplaceOne:
		add("collection", "filter", "replacement", "upsert")
	case DeleteOne, DeleteMany:
		add("collection", "filter")
	case CreateCollection:
		add("collection", "validator", "options")
	case DropCollection:
		add("collection")
	case CreateIndex:
		add("collection", "keys", "name", "unique", "options")
	case DropIndex:
		add("collection", "name")
	case CollMod:
		add("collection", "validator", "options")
	case RenameCollection:
		add("collection", "newName", "dropTarget")
	case BeginTransaction, CommitTransaction, AbortTransaction:
		// no collection
	}
	return keys
}

func optionalString(raw map[string]json.RawMessage, key string) (string, error) {
	value, ok := raw[key]
	if !ok {
		return "", nil
	}
	return decodeString(value)
}

func decodeString(raw json.RawMessage) (string, error) {
	if isNull(raw) {
		return "", fmt.Errorf("must be a string, not null")
	}
	var s string
	if err := json.Unmarshal(raw, &s); err != nil {
		return "", fmt.Errorf("must be a string")
	}
	return s, nil
}

func decodeInt64(raw json.RawMessage) (int64, error) {
	if isNull(raw) {
		return 0, fmt.Errorf("must be an integer, not null")
	}
	var n int64
	if err := json.Unmarshal(raw, &n); err != nil {
		return 0, fmt.Errorf("must be an integer")
	}
	return n, nil
}

func decodeBool(raw json.RawMessage) (bool, error) {
	if isNull(raw) {
		return false, fmt.Errorf("must be a boolean, not null")
	}
	var b bool
	if err := json.Unmarshal(raw, &b); err != nil {
		return false, fmt.Errorf("must be a boolean")
	}
	return b, nil
}

func decodeDoc(raw json.RawMessage) (bson.D, error) {
	if isNull(raw) {
		return nil, fmt.Errorf("must be a JSON object, not null")
	}
	var doc bson.D
	if err := bson.UnmarshalExtJSON(raw, false, &doc); err != nil {
		return nil, fmt.Errorf("must be a JSON object: %w", err)
	}
	return doc, nil
}

func decodeArray(raw json.RawMessage) (bson.A, error) {
	if isNull(raw) {
		return nil, fmt.Errorf("must be a JSON array, not null")
	}
	var arr bson.A
	if err := bson.UnmarshalExtJSON(raw, false, &arr); err != nil {
		return nil, fmt.Errorf("must be a JSON array: %w", err)
	}
	return arr, nil
}

func decodeDocuments(raw json.RawMessage) ([]bson.D, error) {
	var items []json.RawMessage
	if err := json.Unmarshal(raw, &items); err != nil {
		return nil, fmt.Errorf("must be a JSON array of objects")
	}
	docs := make([]bson.D, 0, len(items))
	for i, item := range items {
		doc, err := decodeDoc(item)
		if err != nil {
			return nil, fmt.Errorf("document %d: %w", i, err)
		}
		docs = append(docs, doc)
	}
	return docs, nil
}

func isNull(raw json.RawMessage) bool {
	return bytes.Equal(bytes.TrimSpace(raw), []byte("null"))
}

func stageDocument(stage any) (bson.D, bool) {
	switch value := stage.(type) {
	case bson.D:
		return value, true
	case bson.M:
		doc := make(bson.D, 0, len(value))
		for k, v := range value {
			doc = append(doc, bson.E{Key: k, Value: v})
		}
		return doc, true
	}
	return nil, false
}
