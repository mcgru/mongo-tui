package mongo

import (
	"context"
	"fmt"
	"sort"
	"strings"

	"go.mongodb.org/mongo-driver/v2/bson"
	mg "go.mongodb.org/mongo-driver/v2/mongo"
	"go.mongodb.org/mongo-driver/v2/mongo/options"

	"mongo-tui/internal/engine"
)

type collectionInfo struct {
	Name    string `bson:"name"`
	Type    string `bson:"type"`
	Options bson.D `bson:"options"`
	Info    bson.D `bson:"info"`
}

// Databases lists the cluster's databases. The current one is marked so the
// tree can paint it green.
func (s *Session) Databases(ctx context.Context) ([]engine.Item, error) {
	var out struct {
		Databases []struct {
			Name       string `bson:"name"`
			SizeOnDisk int64  `bson:"sizeOnDisk"`
			Empty      bool   `bson:"empty"`
		} `bson:"databases"`
	}
	err := s.client.Database("admin").
		RunCommand(ctx, d(e("listDatabases", 1), e("nameOnly", false))).
		Decode(&out)
	if err != nil {
		return nil, err
	}
	current := s.currentDatabase()
	items := make([]engine.Item, 0, len(out.Databases))
	for _, db := range out.Databases {
		detail := humanBytes(db.SizeOnDisk)
		if db.Empty {
			detail = "empty"
		}
		items = append(items, engine.Item{
			Name:    db.Name,
			Detail:  detail,
			Current: db.Name == current,
		})
	}
	sortItems(items)
	return items, nil
}

// Collections lists ordinary and time-series collections, hiding MongoDB's
// internal system collections.
func (s *Session) Collections(ctx context.Context, database string) ([]engine.Item, error) {
	infos, err := s.collectionInfos(ctx, database, bson.M{
		"type": bson.M{"$in": bson.A{"collection", "timeseries"}},
	})
	if err != nil {
		return nil, err
	}
	items := make([]engine.Item, 0, len(infos))
	for _, info := range infos {
		if strings.HasPrefix(info.Name, "system.") {
			continue
		}
		items = append(items, engine.Item{Name: info.Name, Detail: collectionDetail(info)})
	}
	sortItems(items)
	return items, nil
}

// Views lists views of a database.
func (s *Session) Views(ctx context.Context, database string) ([]engine.Item, error) {
	infos, err := s.collectionInfos(ctx, database, bson.M{"type": "view"})
	if err != nil {
		return nil, err
	}
	items := make([]engine.Item, 0, len(infos))
	for _, info := range infos {
		if strings.HasPrefix(info.Name, "system.") {
			continue
		}
		items = append(items, engine.Item{Name: info.Name, Detail: collectionDetail(info)})
	}
	sortItems(items)
	return items, nil
}

// Indexes lists indexes of one collection.
func (s *Session) Indexes(ctx context.Context, database, collection string) ([]engine.Item, error) {
	docs, err := s.indexDocs(ctx, database, collection)
	if err != nil {
		return nil, err
	}
	items := make([]engine.Item, 0, len(docs))
	for _, doc := range docs {
		name := stringValue(doc["name"])
		detail := ejsonValue(doc["key"])
		if truthy(doc["unique"]) {
			detail += " unique"
		}
		items = append(items, engine.Item{Name: name, Detail: detail})
	}
	sortItems(items)
	return items, nil
}

// Users lists cluster users when the current user may inspect them.
func (s *Session) Users(ctx context.Context) ([]engine.Item, error) {
	var out struct {
		Users []struct {
			User  string `bson:"user"`
			DB    string `bson:"db"`
			Roles bson.A `bson:"roles"`
		} `bson:"users"`
	}
	err := s.client.Database("admin").
		RunCommand(ctx, d(e("usersInfo", d(e("forAllDBs", true))))).
		Decode(&out)
	if err != nil {
		return nil, err
	}
	items := make([]engine.Item, 0, len(out.Users))
	for _, user := range out.Users {
		detail := "db:" + user.DB
		if roles := roleSummary(user.Roles); roles != "" {
			detail += " · " + roles
		}
		items = append(items, engine.Item{Name: user.User, Detail: detail})
	}
	sortItems(items)
	return items, nil
}

// Roles lists roles from every database. Built-in roles live in admin and are
// included there; other databases contribute their custom roles.
func (s *Session) Roles(ctx context.Context) ([]engine.Item, error) {
	databases, err := s.Databases(ctx)
	if err != nil {
		databases = []engine.Item{{Name: "admin"}}
	}

	seen := map[string]bool{}
	items := make([]engine.Item, 0)
	var firstErr error
	for _, database := range databases {
		var out struct {
			Roles []struct {
				Role      string `bson:"role"`
				DB        string `bson:"db"`
				IsBuiltin bool   `bson:"isBuiltin"`
			} `bson:"roles"`
		}
		err := s.client.Database(database.Name).
			RunCommand(ctx, d(
				e("rolesInfo", 1),
				e("showBuiltinRoles", database.Name == "admin"),
			)).
			Decode(&out)
		if err != nil {
			if firstErr == nil {
				firstErr = err
			}
			continue
		}
		for _, role := range out.Roles {
			key := role.DB + "." + role.Role
			if seen[key] {
				continue
			}
			seen[key] = true
			detail := "db:" + role.DB
			if role.IsBuiltin {
				detail += " · builtin"
			}
			items = append(items, engine.Item{Name: role.Role, Detail: detail})
		}
	}
	if len(items) == 0 && firstErr != nil {
		return nil, firstErr
	}
	sortItems(items)
	return items, nil
}

// Fields returns the card's field list: a document sample, index keys and JSON
// Schema property names. Completion uses the same view as the card.
func (s *Session) Fields(ctx context.Context, database, collection string) ([]engine.FieldInfo, error) {
	card, err := s.CollectionCard(ctx, database, collection)
	if err != nil {
		return nil, err
	}
	return card.Fields, nil
}

// CollectionCard reads options, validator, indexes, a document sample and the
// collection stats. A failure in one part does not hide the others.
func (s *Session) CollectionCard(ctx context.Context, database, collection string) (engine.CollectionCard, error) {
	card := engine.CollectionCard{Database: database, Collection: collection}
	db := s.client.Database(database)

	infos, err := s.collectionInfos(ctx, database, bson.M{"name": collection})
	if err != nil {
		return card, err
	}
	if len(infos) == 0 {
		return card, nil
	}
	info := infos[0]
	card.Exists = true
	card.Type = collectionType(info)
	card.Options = "{}"
	if len(info.Options) > 0 {
		card.Options = ejsonDocument(info.Options)
	}
	var validatorNames []string
	if validator, ok := optionValue(info.Options, "validator"); ok && validator != nil {
		card.Validator = ejsonValue(validator)
		validatorNames = validatorFieldNames(validator)
	}

	var indexDocs []bson.M
	if docs, err := s.indexDocs(ctx, database, collection); err == nil {
		indexDocs = docs
		card.Indexes = make([]engine.IndexInfo, 0, len(docs))
		for _, doc := range docs {
			card.Indexes = append(card.Indexes, engine.IndexInfo{
				Name:   stringValue(doc["name"]),
				Keys:   ejsonValue(doc["key"]),
				Unique: truthy(doc["unique"]),
			})
		}
	}

	sampleSize := s.sampleSize
	if sampleSize <= 0 {
		sampleSize = DefaultSampleSize
	}
	fields, sampled, fieldErr := sampledFields(ctx, db.Collection(collection), sampleSize)
	card.Fields = fields
	card.SampleSize = sampled
	if fieldErr != nil {
		card.FieldsError = fieldErr.Error()
	}
	// Index keys are part of the card even when no sampled document happens to
	// contain them.
	seen := make(map[string]bool, len(card.Fields))
	for _, field := range card.Fields {
		seen[field.Name] = true
	}
	for _, doc := range indexDocs {
		for _, name := range indexKeyNames(doc["key"]) {
			if !seen[name] {
				card.Fields = append(card.Fields, engine.FieldInfo{
					Name: name, Types: "index", Present: 0, Total: sampled,
				})
				seen[name] = true
			}
		}
	}
	for _, name := range validatorNames {
		if !seen[name] {
			card.Fields = append(card.Fields, engine.FieldInfo{
				Name: name, Types: "validator", Present: 0, Total: sampled,
			})
			seen[name] = true
		}
	}

	var stats bson.M
	if err := db.RunCommand(ctx, d(e("collStats", collection))).Decode(&stats); err != nil {
		card.StatsError = err.Error()
	} else {
		card.Count = int64(numberOf(stats["count"]))
		card.SizeBytes = int64(numberOf(stats["size"]))
		if card.SizeBytes == 0 {
			card.SizeBytes = int64(numberOf(stats["storageSize"]))
		}
	}
	return card, nil
}

func (s *Session) collectionInfos(ctx context.Context, database string, filter bson.M) ([]collectionInfo, error) {
	cur, err := s.client.Database(database).
		ListCollections(ctx, filter, options.ListCollections().SetNameOnly(false))
	if err != nil {
		return nil, err
	}
	var infos []collectionInfo
	if err := cur.All(ctx, &infos); err != nil {
		return nil, err
	}
	return infos, nil
}

func (s *Session) indexDocs(ctx context.Context, database, collection string) ([]bson.M, error) {
	cur, err := s.client.Database(database).Collection(collection).Indexes().List(ctx)
	if err != nil {
		return nil, err
	}
	var docs []bson.M
	if err := cur.All(ctx, &docs); err != nil {
		return nil, err
	}
	return docs, nil
}

func collectionType(info collectionInfo) string {
	if info.Type == "view" {
		return "view"
	}
	if _, ok := optionValue(info.Options, "timeseries"); ok {
		return "time-series"
	}
	return "collection"
}

func collectionDetail(info collectionInfo) string {
	var parts []string
	if info.Type == "view" {
		parts = append(parts, "view")
		if viewOn, ok := optionValue(info.Options, "viewOn"); ok {
			parts = append(parts, "on "+stringValue(viewOn))
		}
	}
	if truthyOption(info.Options, "capped") {
		parts = append(parts, "capped")
	}
	if _, ok := optionValue(info.Options, "timeseries"); ok {
		parts = append(parts, "time-series")
	}
	if _, ok := optionValue(info.Options, "validator"); ok {
		parts = append(parts, "validator")
	}
	return strings.Join(parts, " · ")
}

// sampledFields reads up to limit documents and reports every field with the
// BSON types seen and how many sampled documents had it.
func sampledFields(ctx context.Context, coll *mg.Collection, limit int) ([]engine.FieldInfo, int, error) {
	pipeline := mg.Pipeline{{e("$sample", d(e("size", int32(limit))))}}
	cur, err := coll.Aggregate(ctx, pipeline)
	if err != nil {
		// Views and some restricted deployments reject $sample; a plain limit is
		// still a useful sample and keeps the card available.
		cur, err = coll.Find(ctx, bson.D{}, options.Find().SetLimit(int64(limit)))
		if err != nil {
			return nil, 0, err
		}
	}
	var docs []bson.D
	if err := cur.All(ctx, &docs); err != nil {
		return nil, 0, err
	}

	g := newGridBuilder()
	for _, doc := range docs {
		g.add(doc, nil)
	}
	total := len(docs)
	fields := make([]engine.FieldInfo, 0, len(g.columns))
	for _, name := range g.orderedColumns() {
		present := 0
		for _, row := range g.rows {
			if _, ok := row[name]; ok {
				present++
			}
		}
		fields = append(fields, engine.FieldInfo{
			Name:    name,
			Types:   columnType(g.types[name]),
			Present: present,
			Total:   total,
		})
	}
	return fields, total, nil
}

func optionValue(options bson.D, key string) (any, bool) {
	for _, item := range options {
		if item.Key == key {
			return item.Value, true
		}
	}
	return nil, false
}

func truthyOption(options bson.D, key string) bool {
	value, ok := optionValue(options, key)
	return ok && truthy(value)
}

func validatorFieldNames(value any) []string {
	validator, ok := stageDocument(value)
	if !ok {
		return nil
	}
	schema, ok := optionValue(validator, "$jsonSchema")
	if !ok {
		return nil
	}
	schemaDoc, ok := stageDocument(schema)
	if !ok {
		return nil
	}
	properties, ok := optionValue(schemaDoc, "properties")
	if !ok {
		return nil
	}
	propertiesDoc, ok := stageDocument(properties)
	if !ok {
		return nil
	}
	names := make([]string, 0, len(propertiesDoc))
	for _, item := range propertiesDoc {
		names = append(names, item.Key)
	}
	sort.Strings(names)
	return names
}

func indexKeyNames(key any) []string {
	switch v := key.(type) {
	case bson.D:
		names := make([]string, 0, len(v))
		for _, item := range v {
			names = append(names, item.Key)
		}
		return names
	case bson.M:
		names := make([]string, 0, len(v))
		for name := range v {
			names = append(names, name)
		}
		sort.Strings(names)
		return names
	}
	return nil
}

func truthy(value any) bool {
	switch v := value.(type) {
	case bool:
		return v
	case int32:
		return v != 0
	case int64:
		return v != 0
	case float64:
		return v != 0
	}
	return false
}

func stringValue(value any) string {
	switch v := value.(type) {
	case string:
		return v
	case nil:
		return ""
	default:
		return fmt.Sprint(v)
	}
}

func roleSummary(roles bson.A) string {
	parts := make([]string, 0, len(roles))
	for _, role := range roles {
		doc, ok := stageDocument(role)
		if !ok {
			continue
		}
		name := ""
		db := ""
		for _, item := range doc {
			switch item.Key {
			case "role":
				name = stringValue(item.Value)
			case "db":
				db = stringValue(item.Value)
			}
		}
		switch {
		case name != "" && db != "":
			parts = append(parts, db+":"+name)
		case name != "":
			parts = append(parts, name)
		}
	}
	return strings.Join(parts, ", ")
}

func sortItems(items []engine.Item) {
	sort.SliceStable(items, func(i, j int) bool { return items[i].Name < items[j].Name })
}

func humanBytes(n int64) string {
	const unit = 1024
	if n < unit {
		return fmt.Sprintf("%d B", n)
	}
	div, exp := int64(unit), 0
	for value := n / unit; value >= unit; value /= unit {
		div *= unit
		exp++
	}
	return fmt.Sprintf("%.1f %ciB", float64(n)/float64(div), "KMGTPE"[exp])
}

// Compile-time check: the MongoDB adapter implements the catalog contract.
var _ engine.Catalog = (*Session)(nil)
