package tui

import (
	"context"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	tea "github.com/charmbracelet/bubbletea"

	"mongo-tui/internal/engine"
	"mongo-tui/internal/mongo/query"
)

func strconvUnquote(raw string) (string, error) { return strconv.Unquote(raw) }

// mongoSnapshot is the metadata completion needs. Collections are loaded at
// connect/switch; fields are loaded on demand and cached per collection.
type mongoSnapshot struct {
	databases   []string
	collections map[string][]string
	fields      map[string][]string
}

type mongoSnapshotMsg struct {
	snap *mongoSnapshot
	err  error
}

type mongoFieldsMsg struct {
	database   string
	collection string
	fields     []string
	err        error
}

func (m Model) loadMongoSnapshotCmd() tea.Cmd {
	cat, ok := m.sess.(engine.Catalog)
	if !ok {
		return nil
	}
	database := m.currentDatabase()
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 25*time.Second)
		defer cancel()

		snap := &mongoSnapshot{
			collections: map[string][]string{},
			fields:      map[string][]string{},
		}
		databases, err := cat.Databases(ctx)
		if err != nil {
			return mongoSnapshotMsg{err: err}
		}
		for _, item := range databases {
			snap.databases = append(snap.databases, item.Name)
		}
		collections, err := cat.Collections(ctx, database)
		if err != nil {
			return mongoSnapshotMsg{err: err}
		}
		views, err := cat.Views(ctx, database)
		if err != nil {
			return mongoSnapshotMsg{err: err}
		}
		names := make([]string, 0, len(collections)+len(views))
		for _, item := range collections {
			names = append(names, item.Name)
		}
		for _, item := range views {
			names = append(names, item.Name)
		}
		sort.Strings(names)
		snap.collections[database] = names
		return mongoSnapshotMsg{snap: snap}
	}
}

type mongoCompletionKind int

const (
	mongoCompleteNone mongoCompletionKind = iota
	mongoCompleteCollections
	mongoCompleteMethods
	mongoCompleteFields
	mongoCompleteStages
	mongoCompleteDatabases
	mongoCompleteOps
)

type mongoCompletionContext struct {
	kind       mongoCompletionKind
	prefix     string
	database   string
	collection string
	method     string
	needFields bool
}

// startMongoCompletion analyzes the editor and either opens the popup or asks
// the catalog for the collection's sampled fields.
func (m Model) startMongoCompletion() (Model, tea.Cmd) {
	m.comp = compState{}
	if m.mongoSnap == nil {
		m.status = "completion metadata is still loading"
		return m, nil
	}

	ctx := analyzeMongoCompletion(m.editor.Value(), m.editor.CursorOffset())
	if ctx.kind == mongoCompleteNone && m.scopeCollection != "" {
		parts := strings.SplitN(m.scopeCollection, ".", 2)
		if len(parts) == 2 {
			ctx = mongoCompletionContext{
				kind: mongoCompleteFields, database: parts[0], collection: parts[1],
				needFields: true,
			}
		}
	}
	if ctx.kind == mongoCompleteNone {
		m.status = "no completion here"
		return m, nil
	}
	database := ctx.database
	if database == "" {
		database = m.currentDatabase()
	}
	if ctx.needFields && ctx.collection != "" {
		key := database + "." + ctx.collection
		if _, loaded := m.mongoSnap.fields[key]; !loaded {
			cat, ok := m.sess.(engine.Catalog)
			if ok {
				m.status = "loading fields for " + ctx.collection + "…"
				collection := ctx.collection
				return m, func() tea.Msg {
					loadCtx, cancel := context.WithTimeout(context.Background(), 25*time.Second)
					defer cancel()
					fields, err := cat.Fields(loadCtx, database, collection)
					names := make([]string, 0, len(fields))
					for _, field := range fields {
						names = append(names, field.Name)
					}
					return mongoFieldsMsg{database: database, collection: collection, fields: names, err: err}
				}
			}
		}
	}

	items := mongoCandidates(m.mongoSnap, ctx, database)
	if len(items) == 0 {
		m.status = "no completions"
		return m, nil
	}
	m.comp = compState{open: true, items: items, prefix: utf8.RuneCountInString(ctx.prefix)}
	return m, nil
}

func analyzeMongoCompletion(text string, cursor int) mongoCompletionContext {
	if cursor > len(text) {
		cursor = len(text)
	}
	before := text[:cursor]
	prefix := trailingMongoWord(before)
	head := before[:len(before)-len(prefix)]

	if strings.HasPrefix(strings.TrimSpace(before), "{") {
		switch jsonValueKey(head) {
		case "op":
			return mongoCompletionContext{kind: mongoCompleteOps, prefix: prefix}
		case "database":
			return mongoCompletionContext{kind: mongoCompleteDatabases, prefix: prefix}
		case "collection":
			return mongoCompletionContext{kind: mongoCompleteCollections, prefix: prefix}
		}
	}

	if strings.HasSuffix(head, "db.") {
		return mongoCompletionContext{kind: mongoCompleteCollections, prefix: prefix}
	}
	if database, collection, ok := mongoDotCollection(head); ok {
		return mongoCompletionContext{
			kind: mongoCompleteMethods, prefix: prefix,
			database: database, collection: collection,
		}
	}
	if database, collection, method, inside, afterOpen := lastMongoCall(before); collection != "" && inside {
		if method == "aggregate" && stageKeyPosition(afterOpen) {
			return mongoCompletionContext{
				kind: mongoCompleteStages, prefix: prefix,
				database: database, collection: collection, method: method,
			}
		}
		return mongoCompletionContext{
			kind: mongoCompleteFields, prefix: prefix,
			database: database, collection: collection, method: method, needFields: true,
		}
	}
	return mongoCompletionContext{}
}

func trailingMongoWord(text string) string {
	i := len(text)
	for i > 0 {
		r, size := utf8.DecodeLastRuneInString(text[:i])
		if r == '$' || r == '_' || unicode.IsLetter(r) || unicode.IsDigit(r) {
			i -= size
			continue
		}
		break
	}
	return text[i:]
}

func jsonValueKey(head string) string {
	for _, key := range []string{"op", "database", "collection"} {
		re := regexp.MustCompile(`"` + key + `"\s*:\s*"[^"]*$`)
		if re.MatchString(head) {
			return key
		}
	}
	return ""
}

func mongoDotCollection(head string) (string, string, bool) {
	s := strings.TrimSuffix(head, ".")
	switch {
	case strings.HasPrefix(s, "db["):
		if strings.HasSuffix(s, "]") {
			raw := strings.TrimSpace(s[3 : len(s)-1])
			if name, err := strconvUnquote(raw); err == nil {
				return "", name, true
			}
		}
	case strings.HasPrefix(s, "db."):
		name := strings.TrimPrefix(s, "db.")
		if name != "" && !strings.Contains(name, ".") && !strings.Contains(name, "[") && !strings.Contains(name, "(") {
			return "", name, true
		}
	}
	if match := getCollectionRE.FindStringSubmatch(s); match != nil {
		if name, err := strconvUnquote(match[1]); err == nil {
			return "", name, true
		}
	}
	return "", "", false
}

func lastMongoCall(before string) (database, collection, method string, inside bool, afterOpen string) {
	matches := mongoCallRE.FindAllStringSubmatchIndex(before, -1)
	if len(matches) == 0 {
		return "", "", "", false, ""
	}
	match := matches[len(matches)-1]
	collection = before[match[2]:match[3]]
	if collection == "" {
		collection, _ = strconvUnquote(before[match[4]:match[5]])
	}
	method = before[match[6]:match[7]]
	afterOpen = before[match[1]:]
	balance := 1
	for _, r := range afterOpen {
		switch r {
		case '(':
			balance++
		case ')':
			balance--
		}
	}
	inside = balance > 0
	return "", collection, method, inside, afterOpen
}

func stageKeyPosition(afterOpen string) bool {
	// In aggregate([ the cursor is at a stage key position until a stage value
	// object opens after its colon. Inside that value, fields are more useful.
	lastColon := strings.LastIndex(afterOpen, ":")
	lastBrace := strings.LastIndex(afterOpen, "{")
	return lastColon < 0 || lastBrace < lastColon
}

func mongoCandidates(snap *mongoSnapshot, ctx mongoCompletionContext, database string) []suggestion {
	var items []suggestion
	switch ctx.kind {
	case mongoCompleteCollections:
		items = mongoStringCandidates(snap.collections[database], suggestRelation, "collection")
	case mongoCompleteMethods:
		items = mongoStringCandidates(mongoMethods, suggestKeyword, "method")
	case mongoCompleteFields:
		key := database + "." + ctx.collection
		items = mongoStringCandidates(snap.fields[key], suggestColumn, "field")
		items = append(items, mongoOperatorCandidates(ctx.method)...)
	case mongoCompleteStages:
		items = mongoStringCandidates(mongoStages, suggestKeyword, "stage")
	case mongoCompleteDatabases:
		items = mongoStringCandidates(snap.databases, suggestSchema, "database")
	case mongoCompleteOps:
		items = mongoStringCandidates(query.KnownOps(), suggestKeyword, "op")
	}
	return filterCandidates(items, ctx.prefix)
}

func mongoOperatorCandidates(method string) []suggestion {
	switch method {
	case "updateOne", "updateMany", "replaceOne":
		items := mongoStringCandidates(mongoQueryOps, suggestKeyword, "query op")
		return append(items, mongoStringCandidates(mongoUpdateOps, suggestKeyword, "update op")...)
	case "find", "findOne", "count", "countDocuments", "deleteOne", "deleteMany", "distinct", "aggregate":
		return mongoStringCandidates(mongoQueryOps, suggestKeyword, "query op")
	}
	return nil
}

func mongoStringCandidates(names []string, kind suggestionKind, detail string) []suggestion {
	items := make([]suggestion, 0, len(names))
	for _, name := range names {
		items = append(items, suggestion{Text: name, Kind: kind, Detail: detail})
	}
	return items
}

func filterCandidates(items []suggestion, prefix string) []suggestion {
	seen := map[string]bool{}
	out := make([]suggestion, 0, len(items))
	lower := strings.ToLower(prefix)
	for _, item := range items {
		if prefix != "" && !strings.HasPrefix(strings.ToLower(item.Text), lower) {
			continue
		}
		if seen[item.Text] {
			continue
		}
		seen[item.Text] = true
		out = append(out, item)
	}
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].Kind != out[j].Kind {
			if out[i].Kind == suggestColumn {
				return true
			}
			if out[j].Kind == suggestColumn {
				return false
			}
		}
		return out[i].Text < out[j].Text
	})
	return out
}

var (
	mongoCallRE     = regexp.MustCompile(`db(?:\.([A-Za-z_][A-Za-z0-9_]*)|\[("(?:[^"\\]|\\.)*")\])\.([A-Za-z_][A-Za-z0-9_]*)\s*\(`)
	getCollectionRE = regexp.MustCompile(`^db\.getCollection\(\s*("(?:[^"\\]|\\.)*")\s*\)$`)
	mongoMethods    = []string{"find", "findOne", "aggregate", "countDocuments", "distinct", "insertOne", "insertMany", "updateOne", "updateMany", "replaceOne", "deleteOne", "deleteMany", "createIndex", "dropIndex", "drop", "collMod", "rename"}
	mongoQueryOps   = []string{"$and", "$elemMatch", "$eq", "$exists", "$expr", "$gt", "$gte", "$in", "$lt", "$lte", "$ne", "$nin", "$nor", "$not", "$or", "$regex", "$size", "$text", "$type", "$where"}
	mongoUpdateOps  = []string{"$addToSet", "$currentDate", "$inc", "$max", "$min", "$mul", "$pop", "$pull", "$push", "$rename", "$set", "$setOnInsert", "$unset"}
	mongoStages     = []string{"$addFields", "$bucket", "$collStats", "$count", "$facet", "$graphLookup", "$group", "$indexStats", "$limit", "$lookup", "$match", "$merge", "$out", "$project", "$replaceRoot", "$replaceWith", "$sample", "$set", "$skip", "$sort", "$sortByCount", "$unionWith", "$unwind", "$unset"}
)
