package mongo

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"go.mongodb.org/mongo-driver/v2/bson"

	"mongo-tui/internal/engine"
)

func TestFlattenKeepsIDFirstAndDistinguishesMissingNullAndEmpty(t *testing.T) {
	price, err := bson.ParseDecimal128("19.9900")
	if err != nil {
		t.Fatal(err)
	}
	oid := bson.NewObjectID()
	when := time.Date(2026, 11, 14, 12, 0, 0, 0, time.UTC)

	first := d(
		e("name", "widget"),
		e("_id", oid),
		e("qty", int32(3)),
		e("price", price),
		e("tags", bson.A{"a", "b"}),
		e("meta", d(
			e("color", "red"),
			e("dims", d(
				e("w", int32(10)),
				e("unit", d(e("name", "cm"))),
			)),
		)),
		e("created", bson.DateTime(when.UnixMilli())),
		e("note", nil),
	)
	second := d(
		e("_id", "string-id"),
		e("name", ""),
		e("qty", int64(7)),
		e("note", ""),
	)

	g := newGridBuilder()
	for _, doc := range []bson.D{first, second} {
		raw, err := bson.MarshalExtJSON(doc, false, false)
		if err != nil {
			t.Fatal(err)
		}
		g.add(doc, raw)
	}
	res := g.result()

	if len(res.Columns) == 0 || res.Columns[0] != "_id" {
		t.Fatalf("columns = %v, want _id first", res.Columns)
	}
	if len(res.Rows) != 2 {
		t.Fatalf("rows = %d, want 2", len(res.Rows))
	}
	row := rowMap(res, 0)
	if row["_id"] != `ObjectID("`+oid.Hex()+`")` {
		t.Fatalf("_id = %q", row["_id"])
	}
	if row["price"] != "19.9900" {
		t.Fatalf("price = %q, want the Decimal128 value", row["price"])
	}
	if row["created"] != when.Format(time.RFC3339Nano) {
		t.Fatalf("created = %q, want %q", row["created"], when.Format(time.RFC3339Nano))
	}
	if row["note"] != engine.NullSentinel {
		t.Fatalf("note = %q, want the null sentinel", row["note"])
	}
	if row["meta.dims.unit"] != `{"name":"cm"}` {
		t.Fatalf("meta.dims.unit = %q, want a depth-2 object", row["meta.dims.unit"])
	}

	secondRow := rowMap(res, 1)
	if secondRow["name"] != "" {
		t.Fatalf("name = %q, want an empty string", secondRow["name"])
	}
	if secondRow["price"] != engine.MissingSentinel {
		t.Fatalf("missing price = %q, want the missing sentinel", secondRow["price"])
	}
	if secondRow["note"] != "" {
		t.Fatalf("note = %q, want an empty string, not null", secondRow["note"])
	}
}

func TestFlattenColumnTypes(t *testing.T) {
	g := newGridBuilder()
	g.add(d(e("_id", 1), e("qty", int32(3)), e("note", nil)), []byte(`{}`))
	g.add(d(e("_id", 2), e("qty", 7.5), e("note", "x")), []byte(`{}`))
	res := g.result()

	types := map[string]string{}
	for i, name := range res.Columns {
		types[name] = res.ColumnTypes[i]
	}
	if types["qty"] != "mixed" {
		t.Fatalf("qty type = %q, want mixed", types["qty"])
	}
	if types["note"] != "mixed" {
		t.Fatalf("note type = %q, want mixed (null and string)", types["note"])
	}
	if types["_id"] != "int" {
		t.Fatalf("_id type = %q, want int", types["_id"])
	}
}

func TestFlattenStoresFullEJSONDocuments(t *testing.T) {
	doc := d(e("_id", bson.NewObjectID()), e("nested", d(e("a", int32(1)))))
	raw, err := bson.MarshalExtJSON(doc, false, false)
	if err != nil {
		t.Fatal(err)
	}
	g := newGridBuilder()
	g.add(doc, raw)
	res := g.result()

	if len(res.Documents) != 1 {
		t.Fatalf("documents = %d, want 1", len(res.Documents))
	}
	var decoded map[string]any
	if err := json.Unmarshal(res.Documents[0], &decoded); err != nil {
		t.Fatalf("document is not valid JSON: %v", err)
	}
	if _, ok := decoded["nested"]; !ok {
		t.Fatalf("full document lost the nested field: %s", res.Documents[0])
	}
}

func TestFlattenEmptyNestedObjectStaysVisible(t *testing.T) {
	g := newGridBuilder()
	g.add(d(e("_id", 1), e("meta", d())), []byte(`{}`))
	res := g.result()

	row := rowMap(res, 0)
	if !strings.Contains(row["meta"], "{") {
		t.Fatalf("meta = %q, want an empty object rendered as EJSON", row["meta"])
	}
}

func rowMap(res engine.Result, index int) map[string]string {
	out := map[string]string{}
	for i, name := range res.Columns {
		out[name] = res.Rows[index][i]
	}
	return out
}
