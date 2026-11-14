package mongo

import (
	"encoding/json"
	"fmt"
	"sort"
	"strconv"
	"time"

	"go.mongodb.org/mongo-driver/v2/bson"

	"mongo-tui/internal/engine"
)

// maxFlattenDepth is how many nested object levels become dotted columns.
// Deeper objects stay one EJSON cell, so a wide document cannot explode the grid.
const maxFlattenDepth = 2

type gridBuilder struct {
	columns []string
	seen    map[string]bool
	types   map[string]map[string]int
	rows    []map[string]string
	docs    [][]byte
}

func newGridBuilder() *gridBuilder {
	return &gridBuilder{
		seen:  map[string]bool{},
		types: map[string]map[string]int{},
	}
}

func (g *gridBuilder) add(doc bson.D, raw []byte) {
	row := map[string]string{}
	flattenInto(g, doc, "", 0, row)
	g.rows = append(g.rows, row)
	g.docs = append(g.docs, raw)
}

func (g *gridBuilder) addColumn(name, typ string) {
	if !g.seen[name] {
		g.seen[name] = true
		g.columns = append(g.columns, name)
	}
	if g.types[name] == nil {
		g.types[name] = map[string]int{}
	}
	g.types[name][typ]++
}

func (g *gridBuilder) result() engine.Result {
	columns := g.orderedColumns()
	rows := make([][]string, 0, len(g.rows))
	for _, row := range g.rows {
		cells := make([]string, 0, len(columns))
		for _, name := range columns {
			value, ok := row[name]
			if !ok {
				value = engine.MissingSentinel
			}
			cells = append(cells, value)
		}
		rows = append(rows, cells)
	}
	types := make([]string, 0, len(columns))
	for _, name := range columns {
		types = append(types, columnType(g.types[name]))
	}
	return engine.Result{
		Columns:     columns,
		ColumnTypes: types,
		Rows:        rows,
		Documents:   g.docs,
		RowCount:    len(rows),
	}
}

func (g *gridBuilder) orderedColumns() []string {
	if !g.seen["_id"] {
		return g.columns
	}
	out := make([]string, 0, len(g.columns))
	out = append(out, "_id")
	for _, name := range g.columns {
		if name != "_id" {
			out = append(out, name)
		}
	}
	return out
}

func flattenInto(g *gridBuilder, doc bson.D, prefix string, depth int, row map[string]string) {
	for _, item := range doc {
		name := item.Key
		if prefix != "" {
			name = prefix + "." + item.Key
		}
		switch value := item.Value.(type) {
		case bson.D:
			if depth < maxFlattenDepth && len(value) > 0 {
				flattenInto(g, value, name, depth+1, row)
				continue
			}
		case bson.M:
			if depth < maxFlattenDepth && len(value) > 0 {
				flattenInto(g, docFromMap(value), name, depth+1, row)
				continue
			}
		}
		text, typ := renderValue(item.Value)
		g.addColumn(name, typ)
		row[name] = text
	}
}

func docFromMap(value bson.M) bson.D {
	keys := make([]string, 0, len(value))
	for k := range value {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	doc := make(bson.D, 0, len(keys))
	for _, k := range keys {
		doc = append(doc, e(k, value[k]))
	}
	return doc
}

func renderValue(value any) (string, string) {
	switch v := value.(type) {
	case nil:
		return engine.NullSentinel, "null"
	case string:
		return v, "string"
	case bool:
		return strconv.FormatBool(v), "bool"
	case int:
		return strconv.Itoa(v), "int"
	case int32:
		return strconv.FormatInt(int64(v), 10), "int"
	case int64:
		return strconv.FormatInt(v, 10), "long"
	case float64:
		return strconv.FormatFloat(v, 'g', -1, 64), "double"
	case bson.ObjectID:
		return fmt.Sprintf("ObjectID(%q)", v.Hex()), "objectId"
	case bson.Decimal128:
		return v.String(), "decimal"
	case bson.DateTime:
		return v.Time().UTC().Format(time.RFC3339Nano), "date"
	case bson.A:
		return ejsonValue(v), "array"
	case bson.D:
		if len(v) == 0 {
			return "{}", "object"
		}
		return ejsonValue(v), "object"
	case bson.M:
		if len(v) == 0 {
			return "{}", "object"
		}
		return ejsonValue(v), "object"
	case bson.Binary:
		return ejsonValue(v), "binary"
	case bson.Timestamp:
		return ejsonValue(v), "timestamp"
	case bson.Regex:
		return ejsonValue(v), "regex"
	case bson.MinKey:
		return "MinKey", "minKey"
	case bson.MaxKey:
		return "MaxKey", "maxKey"
	default:
		return fmt.Sprint(v), fmt.Sprintf("%T", v)
	}
}

func columnType(counts map[string]int) string {
	types := make([]string, 0, len(counts))
	for typ := range counts {
		if typ != "" {
			types = append(types, typ)
		}
	}
	switch len(types) {
	case 0:
		return ""
	case 1:
		return types[0]
	default:
		sort.Strings(types)
		return "mixed"
	}
}

func typeSummary(values bson.A) string {
	seen := map[string]bool{}
	for _, value := range values {
		_, typ := renderValue(value)
		seen[typ] = true
	}
	if len(seen) == 0 {
		return ""
	}
	if len(seen) == 1 {
		for typ := range seen {
			return typ
		}
	}
	return "mixed"
}

// ejsonValue renders one BSON value as relaxed EJSON. Wrapping it in a document
// lets the encoder return a value instead of a top-level fragment.
func ejsonValue(value any) string {
	raw, err := bson.MarshalExtJSON(d(e("v", value)), false, false)
	if err != nil {
		return fmt.Sprint(value)
	}
	var wrap struct {
		V json.RawMessage `json:"v"`
	}
	if err := json.Unmarshal(raw, &wrap); err != nil {
		return string(raw)
	}
	return string(wrap.V)
}

func ejsonDocument(doc bson.D) string {
	raw, err := bson.MarshalExtJSON(doc, false, false)
	if err != nil {
		return fmt.Sprint(doc)
	}
	return string(raw)
}
