package query

import (
	"fmt"
	"strconv"
	"strings"
	"time"
	"unicode"

	"go.mongodb.org/mongo-driver/v2/bson"
)

// ParseRequest accepts either the canonical JSON envelope or a restricted MQL
// command. The bool reports whether the input was MQL; the TUI uses it for the
// status line and diagnostics.
func ParseRequest(text string) (Spec, bool, error) {
	trimmed := strings.TrimSpace(text)
	if strings.HasPrefix(trimmed, "{") {
		spec, err := Parse([]byte(trimmed))
		return spec, false, err
	}
	if strings.HasPrefix(trimmed, "db.") || strings.HasPrefix(trimmed, "db[") {
		spec, err := ParseMQL(trimmed)
		return spec, true, err
	}
	return Spec{}, false, fmt.Errorf(`request must be a JSON envelope or an MQL command starting with "db."`)
}

// ParseMQL parses a deliberately small MongoDB shell subset. It is a parser,
// not a JavaScript engine: no variables, loops, functions or arbitrary code.
func ParseMQL(text string) (Spec, error) {
	p := &mqlParser{src: []rune(text)}
	spec, err := p.parseCommand()
	if err != nil {
		return Spec{}, fmt.Errorf("mql: %w", err)
	}
	p.skipSpace()
	if !p.eof() {
		return Spec{}, fmt.Errorf("mql: unexpected %q at %d", string(p.peek()), p.pos)
	}
	if err := spec.validate(); err != nil {
		return Spec{}, fmt.Errorf("mql: %w", err)
	}
	return spec, nil
}

type mqlParser struct {
	src []rune
	pos int
}

func (p *mqlParser) eof() bool { return p.pos >= len(p.src) }

func (p *mqlParser) peek() rune {
	if p.eof() {
		return 0
	}
	return p.src[p.pos]
}

func (p *mqlParser) next() rune {
	if p.eof() {
		return 0
	}
	c := p.src[p.pos]
	p.pos++
	return c
}

func (p *mqlParser) skipSpace() {
	for !p.eof() {
		switch {
		case unicode.IsSpace(p.peek()):
			p.pos++
		case p.peek() == '/' && p.pos+1 < len(p.src) && p.src[p.pos+1] == '/':
			for !p.eof() && p.peek() != '\n' {
				p.pos++
			}
		case p.peek() == '/' && p.pos+1 < len(p.src) && p.src[p.pos+1] == '*':
			p.pos += 2
			for p.pos+1 < len(p.src) && !(p.src[p.pos] == '*' && p.src[p.pos+1] == '/') {
				p.pos++
			}
			if p.pos+1 < len(p.src) {
				p.pos += 2
			} else {
				p.pos = len(p.src)
			}
		default:
			return
		}
	}
}

func (p *mqlParser) accept(c rune) bool {
	p.skipSpace()
	if p.peek() == c {
		p.pos++
		return true
	}
	return false
}

func (p *mqlParser) expect(c rune) error {
	p.skipSpace()
	if p.peek() != c {
		return fmt.Errorf("expected %q at %d, got %q", string(c), p.pos, string(p.peek()))
	}
	p.pos++
	return nil
}

func (p *mqlParser) identifier() (string, error) {
	p.skipSpace()
	start := p.pos
	if p.eof() || !isIdentStart(p.peek()) {
		return "", fmt.Errorf("expected an identifier at %d, got %q", p.pos, string(p.peek()))
	}
	p.pos++
	for !p.eof() && isIdentPart(p.peek()) {
		p.pos++
	}
	return string(p.src[start:p.pos]), nil
}

func isIdentStart(c rune) bool {
	return c == '_' || c == '$' || unicode.IsLetter(c)
}

func isIdentPart(c rune) bool {
	return isIdentStart(c) || unicode.IsDigit(c)
}

func (p *mqlParser) parseCommand() (Spec, error) {
	name, err := p.identifier()
	if err != nil {
		return Spec{}, err
	}
	if name != "db" {
		return Spec{}, fmt.Errorf("expected db at %d", 0)
	}
	if p.accept('[') {
		collection, err := p.parseString()
		if err != nil {
			return Spec{}, err
		}
		if err := p.expect(']'); err != nil {
			return Spec{}, err
		}
		return p.parseCollectionChain(collection)
	}
	if err := p.expect('.'); err != nil {
		return Spec{}, err
	}
	member, err := p.identifier()
	if err != nil {
		return Spec{}, err
	}
	if member == "getCollection" {
		args, err := p.parseArguments()
		if err != nil {
			return Spec{}, err
		}
		if len(args) != 1 {
			return Spec{}, fmt.Errorf("getCollection needs one collection name")
		}
		collection, ok := args[0].(string)
		if !ok {
			return Spec{}, fmt.Errorf("getCollection name must be a string")
		}
		return p.parseCollectionChain(collection)
	}
	if member == "createCollection" {
		args, err := p.parseArguments()
		if err != nil {
			return Spec{}, err
		}
		if len(args) < 1 || len(args) > 2 {
			return Spec{}, fmt.Errorf("createCollection takes a name and optional options")
		}
		collection, ok := args[0].(string)
		if !ok || collection == "" {
			return Spec{}, fmt.Errorf("createCollection name must be a non-empty string")
		}
		spec := Spec{Op: CreateCollection, Collection: collection}
		if len(args) == 2 {
			options, ok := args[1].(bson.D)
			if !ok {
				return Spec{}, fmt.Errorf("createCollection options must be an object")
			}
			spec.Options = options
		}
		return spec, nil
	}
	return p.parseCollectionChain(member)
}

func (p *mqlParser) parseCollectionChain(collection string) (Spec, error) {
	if err := p.expect('.'); err != nil {
		return Spec{}, err
	}
	method, err := p.identifier()
	if err != nil {
		return Spec{}, err
	}
	args, err := p.parseArguments()
	if err != nil {
		return Spec{}, err
	}

	spec := Spec{Collection: collection}
	switch method {
	case "find", "findOne":
		spec.Op = Find
		if len(args) > 0 {
			spec.Filter, err = docArg(args[0], "filter")
			if err != nil {
				return Spec{}, err
			}
		}
		if len(args) > 1 {
			spec.Projection, err = docArg(args[1], "projection")
			if err != nil {
				return Spec{}, err
			}
		}
		if len(args) > 2 {
			return Spec{}, fmt.Errorf("%s takes at most filter and projection", method)
		}
		if method == "findOne" {
			spec.Limit = 1
		}
		if err := p.parseFindChain(&spec); err != nil {
			return Spec{}, err
		}

	case "aggregate":
		spec.Op = Aggregate
		if len(args) > 0 {
			pipeline, ok := args[0].(bson.A)
			if !ok {
				return Spec{}, fmt.Errorf("aggregate pipeline must be an array")
			}
			spec.Pipeline = pipeline
		}
		if len(args) > 1 {
			opts, err := docArg(args[1], "aggregate options")
			if err != nil {
				return Spec{}, err
			}
			if value, ok := optionValue(opts, "allowDiskUse"); ok {
				spec.AllowDiskUse = truthy(value)
			}
		}
		if len(args) > 2 {
			return Spec{}, fmt.Errorf("aggregate takes a pipeline and optional options")
		}

	case "count", "countDocuments":
		spec.Op = Count
		if len(args) > 0 {
			spec.Filter, err = docArg(args[0], "filter")
			if err != nil {
				return Spec{}, err
			}
		}
		if len(args) > 1 {
			opts, err := docArg(args[1], "count options")
			if err != nil {
				return Spec{}, err
			}
			if value, ok := optionValue(opts, "skip"); ok {
				spec.Skip = int64Of(value)
			}
			if value, ok := optionValue(opts, "limit"); ok {
				spec.Limit = int64Of(value)
			}
		}

	case "distinct":
		spec.Op = Distinct
		if len(args) < 1 || len(args) > 2 {
			return Spec{}, fmt.Errorf("distinct takes a field and optional filter")
		}
		field, ok := args[0].(string)
		if !ok || field == "" {
			return Spec{}, fmt.Errorf("distinct field must be a non-empty string")
		}
		spec.Field = field
		if len(args) == 2 {
			spec.Filter, err = docArg(args[1], "filter")
			if err != nil {
				return Spec{}, err
			}
		}

	case "insertOne":
		spec.Op = InsertOne
		if len(args) != 1 {
			return Spec{}, fmt.Errorf("insertOne takes one document")
		}
		spec.Document, err = docArg(args[0], "document")
		if err != nil {
			return Spec{}, err
		}

	case "insertMany":
		spec.Op = InsertMany
		if len(args) != 1 {
			return Spec{}, fmt.Errorf("insertMany takes one array of documents")
		}
		docs, ok := args[0].(bson.A)
		if !ok {
			return Spec{}, fmt.Errorf("insertMany must be given an array")
		}
		spec.Documents = make([]bson.D, 0, len(docs))
		for i, doc := range docs {
			d, ok := doc.(bson.D)
			if !ok {
				return Spec{}, fmt.Errorf("insertMany document %d must be an object", i)
			}
			spec.Documents = append(spec.Documents, d)
		}

	case "updateOne", "updateMany":
		spec.Op = UpdateOne
		if method == "updateMany" {
			spec.Op = UpdateMany
		}
		if len(args) < 2 || len(args) > 3 {
			return Spec{}, fmt.Errorf("%s takes filter, update and optional options", method)
		}
		spec.Filter, err = docArg(args[0], "filter")
		if err != nil {
			return Spec{}, err
		}
		spec.Update, err = docArg(args[1], "update")
		if err != nil {
			return Spec{}, err
		}
		if len(args) == 3 {
			if spec.Upsert, err = boolOption(args[2], "upsert"); err != nil {
				return Spec{}, err
			}
		}

	case "replaceOne":
		spec.Op = ReplaceOne
		if len(args) < 2 || len(args) > 3 {
			return Spec{}, fmt.Errorf("replaceOne takes filter, replacement and optional options")
		}
		spec.Filter, err = docArg(args[0], "filter")
		if err != nil {
			return Spec{}, err
		}
		spec.Replacement, err = docArg(args[1], "replacement")
		if err != nil {
			return Spec{}, err
		}
		if len(args) == 3 {
			if spec.Upsert, err = boolOption(args[2], "upsert"); err != nil {
				return Spec{}, err
			}
		}

	case "deleteOne", "deleteMany":
		spec.Op = DeleteOne
		if method == "deleteMany" {
			spec.Op = DeleteMany
		}
		if len(args) != 1 {
			return Spec{}, fmt.Errorf("%s takes an explicit filter (use {} for every document)", method)
		}
		spec.Filter, err = docArg(args[0], "filter")
		if err != nil {
			return Spec{}, err
		}

	case "createIndex":
		spec.Op = CreateIndex
		if len(args) < 1 || len(args) > 2 {
			return Spec{}, fmt.Errorf("createIndex takes keys and optional options")
		}
		spec.Keys, err = docArg(args[0], "keys")
		if err != nil {
			return Spec{}, err
		}
		if len(args) == 2 {
			opts, err := docArg(args[1], "index options")
			if err != nil {
				return Spec{}, err
			}
			spec.Options = opts
			if value, ok := optionValue(opts, "name"); ok {
				spec.Name, _ = value.(string)
			}
			if value, ok := optionValue(opts, "unique"); ok {
				spec.Unique = truthy(value)
			}
		}

	case "dropIndex":
		spec.Op = DropIndex
		if len(args) != 1 {
			return Spec{}, fmt.Errorf("dropIndex takes an index name")
		}
		spec.Name, _ = args[0].(string)
		if spec.Name == "" {
			return Spec{}, fmt.Errorf("dropIndex name must be a non-empty string")
		}

	case "drop":
		spec.Op = DropCollection
		if len(args) != 0 {
			return Spec{}, fmt.Errorf("drop takes no arguments")
		}

	case "collMod":
		spec.Op = CollMod
		if len(args) != 1 {
			return Spec{}, fmt.Errorf("collMod takes an options object")
		}
		spec.Options, err = docArg(args[0], "collMod options")
		if err != nil {
			return Spec{}, err
		}

	case "rename", "renameCollection":
		spec.Op = RenameCollection
		if len(args) < 1 || len(args) > 2 {
			return Spec{}, fmt.Errorf("rename takes a new name and optional options")
		}
		spec.NewName, _ = args[0].(string)
		if spec.NewName == "" {
			return Spec{}, fmt.Errorf("rename name must be a non-empty string")
		}
		if len(args) == 2 {
			opts, err := docArg(args[1], "rename options")
			if err != nil {
				return Spec{}, err
			}
			if value, ok := optionValue(opts, "dropTarget"); ok {
				spec.DropTarget = truthy(value)
			}
		}

	default:
		return Spec{}, fmt.Errorf("unsupported method %q", method)
	}
	return spec, nil
}

func (p *mqlParser) parseFindChain(spec *Spec) error {
	for {
		p.skipSpace()
		if p.peek() != '.' {
			return nil
		}
		p.pos++
		method, err := p.identifier()
		if err != nil {
			return err
		}
		args, err := p.parseArguments()
		if err != nil {
			return err
		}
		switch method {
		case "sort":
			if len(args) != 1 {
				return fmt.Errorf("sort takes one object")
			}
			if spec.Sort, err = docArg(args[0], "sort"); err != nil {
				return err
			}
		case "project", "projection":
			if len(args) != 1 {
				return fmt.Errorf("%s takes one object", method)
			}
			if spec.Projection, err = docArg(args[0], "projection"); err != nil {
				return err
			}
		case "skip":
			if len(args) != 1 {
				return fmt.Errorf("skip takes one number")
			}
			spec.Skip = int64Of(args[0])
		case "limit":
			if len(args) != 1 {
				return fmt.Errorf("limit takes one number")
			}
			spec.Limit = int64Of(args[0])
		default:
			return fmt.Errorf("unsupported find chain method %q", method)
		}
	}
}

func (p *mqlParser) parseArguments() ([]any, error) {
	if err := p.expect('('); err != nil {
		return nil, err
	}
	args := make([]any, 0, 2)
	if p.accept(')') {
		return args, nil
	}
	for {
		value, err := p.parseValue()
		if err != nil {
			return nil, err
		}
		args = append(args, value)
		if p.accept(')') {
			return args, nil
		}
		if err := p.expect(','); err != nil {
			return nil, err
		}
	}
}

func (p *mqlParser) parseValue() (any, error) {
	p.skipSpace()
	switch c := p.peek(); {
	case c == '{':
		return p.parseDocument()
	case c == '[':
		return p.parseArray()
	case c == '"' || c == '\'':
		return p.parseString()
	case c == '-' || unicode.IsDigit(c):
		return p.parseNumber()
	case isIdentStart(c):
		name, err := p.identifier()
		if err != nil {
			return nil, err
		}
		switch name {
		case "true":
			return true, nil
		case "false":
			return false, nil
		case "null", "undefined":
			return nil, nil
		case "ObjectId", "ISODate", "NumberDecimal", "NumberLong", "Date":
			return p.parseTypedValue(name)
		default:
			return nil, fmt.Errorf("unquoted identifier %q; quote strings", name)
		}
	}
	return nil, fmt.Errorf("unexpected %q at %d", string(p.peek()), p.pos)
}

func (p *mqlParser) parseTypedValue(name string) (any, error) {
	if err := p.expect('('); err != nil {
		return nil, err
	}
	raw, err := p.parseString()
	if err != nil {
		return nil, err
	}
	if err := p.expect(')'); err != nil {
		return nil, err
	}
	switch name {
	case "ObjectId":
		oid, err := bson.ObjectIDFromHex(raw)
		if err != nil {
			return nil, fmt.Errorf("invalid ObjectId %q: %w", raw, err)
		}
		return oid, nil
	case "ISODate", "Date":
		t, err := time.Parse(time.RFC3339, raw)
		if err != nil {
			return nil, fmt.Errorf("invalid %s %q, want RFC3339: %w", name, raw, err)
		}
		return t, nil
	case "NumberDecimal":
		dec, err := bson.ParseDecimal128(raw)
		if err != nil {
			return nil, fmt.Errorf("invalid NumberDecimal %q: %w", raw, err)
		}
		return dec, nil
	case "NumberLong":
		n, err := strconv.ParseInt(raw, 10, 64)
		if err != nil {
			return nil, fmt.Errorf("invalid NumberLong %q: %w", raw, err)
		}
		return n, nil
	}
	return nil, fmt.Errorf("unsupported constructor %q", name)
}

func (p *mqlParser) parseDocument() (bson.D, error) {
	if err := p.expect('{'); err != nil {
		return nil, err
	}
	doc := bson.D{}
	if p.accept('}') {
		return doc, nil
	}
	for {
		key, err := p.parseKey()
		if err != nil {
			return nil, err
		}
		if err := p.expect(':'); err != nil {
			return nil, err
		}
		value, err := p.parseValue()
		if err != nil {
			return nil, err
		}
		doc = append(doc, bson.E{Key: key, Value: value})
		if p.accept('}') {
			return doc, nil
		}
		if err := p.expect(','); err != nil {
			return nil, err
		}
	}
}

func (p *mqlParser) parseArray() (bson.A, error) {
	if err := p.expect('['); err != nil {
		return nil, err
	}
	arr := bson.A{}
	if p.accept(']') {
		return arr, nil
	}
	for {
		value, err := p.parseValue()
		if err != nil {
			return nil, err
		}
		arr = append(arr, value)
		if p.accept(']') {
			return arr, nil
		}
		if err := p.expect(','); err != nil {
			return nil, err
		}
	}
}

func (p *mqlParser) parseKey() (string, error) {
	p.skipSpace()
	if p.peek() == '"' || p.peek() == '\'' {
		return p.parseString()
	}
	return p.identifier()
}

func (p *mqlParser) parseString() (string, error) {
	p.skipSpace()
	quote := p.next()
	if quote != '"' && quote != '\'' {
		return "", fmt.Errorf("expected a quoted string at %d", p.pos-1)
	}
	var b strings.Builder
	for !p.eof() {
		c := p.next()
		switch c {
		case quote:
			return b.String(), nil
		case '\\':
			if p.eof() {
				return "", fmt.Errorf("unterminated escape")
			}
			esc := p.next()
			switch esc {
			case 'n':
				b.WriteRune('\n')
			case 'r':
				b.WriteRune('\r')
			case 't':
				b.WriteRune('\t')
			case 'b':
				b.WriteRune('\b')
			case 'f':
				b.WriteRune('\f')
			case '\\', '/', '"', '\'':
				b.WriteRune(esc)
			case 'u':
				if p.pos+4 > len(p.src) {
					return "", fmt.Errorf("short \\u escape")
				}
				hex := string(p.src[p.pos : p.pos+4])
				code, err := strconv.ParseUint(hex, 16, 16)
				if err != nil {
					return "", fmt.Errorf("invalid \\u escape %q", hex)
				}
				p.pos += 4
				b.WriteRune(rune(code))
			default:
				return "", fmt.Errorf("unsupported escape \\%c", esc)
			}
		default:
			b.WriteRune(c)
		}
	}
	return "", fmt.Errorf("unterminated string")
}

func (p *mqlParser) parseNumber() (any, error) {
	start := p.pos
	if p.peek() == '-' {
		p.pos++
	}
	for !p.eof() && unicode.IsDigit(p.peek()) {
		p.pos++
	}
	isFloat := false
	if p.peek() == '.' {
		isFloat = true
		p.pos++
		for !p.eof() && unicode.IsDigit(p.peek()) {
			p.pos++
		}
	}
	if p.peek() == 'e' || p.peek() == 'E' {
		isFloat = true
		p.pos++
		if p.peek() == '+' || p.peek() == '-' {
			p.pos++
		}
		for !p.eof() && unicode.IsDigit(p.peek()) {
			p.pos++
		}
	}
	raw := string(p.src[start:p.pos])
	if isFloat {
		value, err := strconv.ParseFloat(raw, 64)
		if err != nil {
			return nil, fmt.Errorf("invalid number %q", raw)
		}
		return value, nil
	}
	value, err := strconv.ParseInt(raw, 10, 64)
	if err != nil {
		return nil, fmt.Errorf("invalid integer %q", raw)
	}
	return value, nil
}

func docArg(value any, what string) (bson.D, error) {
	doc, ok := value.(bson.D)
	if !ok {
		return nil, fmt.Errorf("%s must be an object", what)
	}
	return doc, nil
}

func boolOption(value any, key string) (bool, error) {
	doc, err := docArg(value, "options")
	if err != nil {
		return false, err
	}
	if option, ok := optionValue(doc, key); ok {
		return truthy(option), nil
	}
	return false, nil
}

func optionValue(doc bson.D, key string) (any, bool) {
	for _, item := range doc {
		if item.Key == key {
			return item.Value, true
		}
	}
	return nil, false
}

func truthy(value any) bool {
	switch v := value.(type) {
	case bool:
		return v
	case int64:
		return v != 0
	case float64:
		return v != 0
	}
	return false
}

func int64Of(value any) int64 {
	switch v := value.(type) {
	case int64:
		return v
	case float64:
		return int64(v)
	}
	return 0
}
