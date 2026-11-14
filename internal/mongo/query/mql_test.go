package query

import (
	"strings"
	"testing"
	"time"

	"go.mongodb.org/mongo-driver/v2/bson"
)

func TestParseMQLFindChain(t *testing.T) {
	spec, err := ParseMQL(`db.users.find({age: {$gt: 30}, name: "Ann"}, {name: 1, _id: 0}).sort({name: 1}).skip(2).limit(10)`)
	if err != nil {
		t.Fatalf("ParseMQL() error = %v", err)
	}
	if spec.Op != Find || spec.Collection != "users" {
		t.Fatalf("spec = %+v, want find users", spec)
	}
	if got := keys(spec.Filter); got != "age,name" {
		t.Fatalf("filter keys = %q, want age,name", got)
	}
	if got := keys(spec.Projection); got != "name,_id" {
		t.Fatalf("projection keys = %q", got)
	}
	if got := keys(spec.Sort); got != "name" {
		t.Fatalf("sort keys = %q", got)
	}
	if spec.Skip != 2 || spec.Limit != 10 {
		t.Fatalf("skip/limit = %d/%d, want 2/10", spec.Skip, spec.Limit)
	}
}

func TestParseMQLAggregate(t *testing.T) {
	spec, err := ParseMQL(`db.orders.aggregate([{$match: {status: "paid"}}, {$sort: {created_at: -1}}], {allowDiskUse: true})`)
	if err != nil {
		t.Fatalf("ParseMQL() error = %v", err)
	}
	if spec.Op != Aggregate || len(spec.Pipeline) != 2 {
		t.Fatalf("spec = %+v, want two stages", spec)
	}
	if !spec.AllowDiskUse {
		t.Fatal("AllowDiskUse = false, want true")
	}
}

func TestParseMQLTypedValues(t *testing.T) {
	spec, err := ParseMQL(`db.users.insertOne({_id: ObjectId("507f1f77bcf86cd799439011"), name: 'Ann', when: ISODate("2026-11-14T12:00:00Z"), price: NumberDecimal("19.99"), big: NumberLong("9007199254740993"), nil: null, ok: true})`)
	if err != nil {
		t.Fatalf("ParseMQL() error = %v", err)
	}
	if spec.Op != InsertOne || len(spec.Document) != 7 {
		t.Fatalf("spec = %+v", spec)
	}
	if _, ok := spec.Document[0].Value.(bson.ObjectID); !ok {
		t.Fatalf("_id = %T, want bson.ObjectID", spec.Document[0].Value)
	}
	if got, ok := spec.Document[2].Value.(time.Time); !ok || !got.Equal(time.Date(2026, 11, 14, 12, 0, 0, 0, time.UTC)) {
		t.Fatalf("when = %#v", spec.Document[2].Value)
	}
	if _, ok := spec.Document[3].Value.(bson.Decimal128); !ok {
		t.Fatalf("price = %T, want bson.Decimal128", spec.Document[3].Value)
	}
	if got, ok := spec.Document[4].Value.(int64); !ok || got != 9007199254740993 {
		t.Fatalf("big = %#v", spec.Document[4].Value)
	}
}

func TestParseMQLWrites(t *testing.T) {
	tests := []struct {
		input string
		op    Op
	}{
		{`db.users.insertMany([{name: "Ann"}, {name: "Bob"}])`, InsertMany},
		{`db.users.updateOne({_id: 1}, {$set: {name: "Ann"}}, {upsert: true})`, UpdateOne},
		{`db.users.updateMany({}, {$set: {active: true}})`, UpdateMany},
		{`db.users.replaceOne({_id: 1}, {name: "Ann"})`, ReplaceOne},
		{`db.users.deleteOne({_id: 1})`, DeleteOne},
		{`db.users.deleteMany({})`, DeleteMany},
	}
	for _, tc := range tests {
		t.Run(string(tc.op), func(t *testing.T) {
			spec, err := ParseMQL(tc.input)
			if err != nil {
				t.Fatalf("ParseMQL() error = %v", err)
			}
			if spec.Op != tc.op {
				t.Fatalf("op = %q, want %q", spec.Op, tc.op)
			}
		})
	}
}

func TestParseMQLDDL(t *testing.T) {
	spec, err := ParseMQL(`db.createCollection("users", {validator: {$jsonSchema: {required: ["name"]}}})`)
	if err != nil {
		t.Fatalf("createCollection error = %v", err)
	}
	if spec.Op != CreateCollection || spec.Collection != "users" || len(spec.Options) == 0 {
		t.Fatalf("createCollection spec = %+v", spec)
	}

	spec, err = ParseMQL(`db.users.createIndex({email: 1}, {name: "email_1", unique: true})`)
	if err != nil {
		t.Fatalf("createIndex error = %v", err)
	}
	if spec.Op != CreateIndex || spec.Name != "email_1" || !spec.Unique {
		t.Fatalf("createIndex spec = %+v", spec)
	}

	spec, err = ParseMQL(`db.users.dropIndex("email_1")`)
	if err != nil {
		t.Fatalf("dropIndex error = %v", err)
	}
	if spec.Op != DropIndex || spec.Name != "email_1" {
		t.Fatalf("dropIndex spec = %+v", spec)
	}

	spec, err = ParseMQL(`db.users.drop()`)
	if err != nil {
		t.Fatalf("drop error = %v", err)
	}
	if spec.Op != DropCollection {
		t.Fatalf("drop spec = %+v", spec)
	}

	spec, err = ParseMQL(`db.users.collMod({validator: {$jsonSchema: {}}})`)
	if err != nil {
		t.Fatalf("collMod error = %v", err)
	}
	if spec.Op != CollMod || len(spec.Options) == 0 {
		t.Fatalf("collMod spec = %+v", spec)
	}

	spec, err = ParseMQL(`db.users.rename("people", {dropTarget: true})`)
	if err != nil {
		t.Fatalf("rename error = %v", err)
	}
	if spec.Op != RenameCollection || spec.NewName != "people" || !spec.DropTarget {
		t.Fatalf("rename spec = %+v", spec)
	}
}

func TestParseMQLCollectionNotation(t *testing.T) {
	for _, input := range []string{
		`db["weird.name"].find({})`,
		`db.getCollection("weird.name").find({})`,
	} {
		spec, err := ParseMQL(input)
		if err != nil {
			t.Fatalf("ParseMQL(%s) error = %v", input, err)
		}
		if spec.Collection != "weird.name" || spec.Op != Find {
			t.Fatalf("spec = %+v for %s", spec, input)
		}
	}
}

func TestParseMQLRejectsDangerousOrBadInput(t *testing.T) {
	tests := []struct {
		input string
		want  string
	}{
		{`db.users.deleteMany()`, "explicit filter"},
		{`db.users.unknown({})`, "unsupported method"},
		{`db.users.find({age: variable})`, "unquoted identifier"},
		{`db.users.find({age: 1}`, `expected ","`},
		{`db.users.find({}) extra`, "unexpected"},
		{`db.users.updateOne({}, {})`, `needs a non-empty "update"`},
		{`db.users.find({})..limit(1)`, "expected an identifier"},
	}
	for _, tc := range tests {
		t.Run(tc.input, func(t *testing.T) {
			_, err := ParseMQL(tc.input)
			if err == nil {
				t.Fatal("ParseMQL() = nil, want an error")
			}
			if !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("error = %q, want it to contain %q", err, tc.want)
			}
		})
	}
}

func TestParseRequestDetectsEnvelopeAndMQL(t *testing.T) {
	spec, isMQL, err := ParseRequest(`{"op":"find","collection":"users"}`)
	if err != nil || isMQL || spec.Op != Find {
		t.Fatalf("JSON envelope: spec=%+v isMQL=%v err=%v", spec, isMQL, err)
	}

	spec, isMQL, err = ParseRequest(`db.users.find({})`)
	if err != nil || !isMQL || spec.Op != Find {
		t.Fatalf("MQL: spec=%+v isMQL=%v err=%v", spec, isMQL, err)
	}

	if _, _, err := ParseRequest(`SELECT 1`); err == nil {
		t.Fatal("ParseRequest() accepted SQL")
	}
}
