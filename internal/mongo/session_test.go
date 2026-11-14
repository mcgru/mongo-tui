package mongo

import (
	"context"
	"fmt"
	"os"
	"strings"
	"testing"
	"time"

	"go.mongodb.org/mongo-driver/v2/bson"

	"mongo-tui/internal/engine"
	"mongo-tui/internal/mongo/query"
)

func TestLiveCRUD(t *testing.T) {
	ctx := context.Background()
	s := liveSession(t, ConnectOptions{})
	db := newTestDB(t, s)

	run(t, s, ctx, env(db, `"op":"insertOne","collection":"items","document":{"_id":1,"name":"Ann","qty":3,"tags":["a"],"meta":{"color":"red"},"note":null}`))
	run(t, s, ctx, env(db, `"op":"insertMany","collection":"items","documents":[{"_id":2,"name":"Bob","qty":7},{"_id":3,"name":"","qty":0.5}]`))

	res := run(t, s, ctx, env(db, `"op":"find","collection":"items","sort":{"_id":1}`))
	if len(res.Rows) != 3 {
		t.Fatalf("rows = %d, want 3", len(res.Rows))
	}
	if res.Columns[0] != "_id" {
		t.Fatalf("first column = %q, want _id", res.Columns[0])
	}
	if got := cell(res, 0, "note"); got != engine.NullSentinel {
		t.Fatalf("note = %q, want the null sentinel", got)
	}
	if got := cell(res, 2, "note"); got != engine.MissingSentinel {
		t.Fatalf("missing note = %q, want the missing sentinel", got)
	}
	if got := cell(res, 2, "name"); got != "" {
		t.Fatalf("empty name = %q, want an empty string", got)
	}
	if got := cell(res, 0, "meta.color"); got != "red" {
		t.Fatalf("meta.color = %q, want red", got)
	}
	if len(res.Documents) != len(res.Rows) {
		t.Fatalf("documents = %d, rows = %d", len(res.Documents), len(res.Rows))
	}
	if got := typeOf(res, "qty"); got != "mixed" {
		t.Fatalf("qty type = %q, want mixed", got)
	}

	res = run(t, s, ctx, env(db, `"op":"find","collection":"items","filter":{"qty":{"$gte":3}},"sort":{"_id":1}`))
	if len(res.Rows) != 2 {
		t.Fatalf("filtered rows = %d, want 2", len(res.Rows))
	}

	res = run(t, s, ctx, env(db, `"op":"find","collection":"items","sort":{"_id":1},"projection":{"name":1,"_id":0},"skip":1,"limit":2`))
	if len(res.Columns) != 1 || res.Columns[0] != "name" {
		t.Fatalf("projected columns = %v, want only name", res.Columns)
	}
	if len(res.Rows) != 2 {
		t.Fatalf("projected rows = %d, want 2", len(res.Rows))
	}

	res = run(t, s, ctx, env(db, `"op":"aggregate","collection":"items","pipeline":[{"$match":{"qty":{"$gte":3}}},{"$group":{"_id":null,"count":{"$sum":1}}}]`))
	if len(res.Rows) != 1 || cell(res, 0, "count") != "2" {
		t.Fatalf("aggregate result = %v, want one row with count=2", res.Rows)
	}

	res = run(t, s, ctx, env(db, `"op":"count","collection":"items","filter":{}`))
	if got := res.Rows[0][0]; got != "3" {
		t.Fatalf("count = %q, want 3", got)
	}

	res = run(t, s, ctx, env(db, `"op":"distinct","collection":"items","field":"name"`))
	if len(res.Rows) != 3 {
		t.Fatalf("distinct values = %d, want 3", len(res.Rows))
	}

	res = run(t, s, ctx, env(db, `"op":"updateOne","collection":"items","filter":{"_id":2},"update":{"$set":{"name":"Bobby"}}`))
	if res.RowCount != 1 {
		t.Fatalf("updated = %d, want 1", res.RowCount)
	}

	res = run(t, s, ctx, env(db, `"op":"replaceOne","collection":"items","filter":{"_id":2},"replacement":{"_id":2,"name":"Robert","qty":9}`))
	if res.RowCount != 1 {
		t.Fatalf("replaced = %d, want 1", res.RowCount)
	}

	res = run(t, s, ctx, env(db, `"op":"deleteMany","collection":"items","filter":{"qty":{"$lt":1}}`))
	if res.RowCount != 1 {
		t.Fatalf("deleted = %d, want 1", res.RowCount)
	}

	res = run(t, s, ctx, env(db, `"op":"count","collection":"items","filter":{}`))
	if got := res.Rows[0][0]; got != "2" {
		t.Fatalf("count after delete = %q, want 2", got)
	}
}

func TestLiveLimits(t *testing.T) {
	ctx := context.Background()
	s := liveSession(t, ConnectOptions{MaxDocs: 2})
	db := newTestDB(t, s)

	run(t, s, ctx, env(db, `"op":"insertMany","collection":"items","documents":[
		{"_id":1},{"_id":2},{"_id":3},{"_id":4},{"_id":5}
	]`))

	res := run(t, s, ctx, env(db, `"op":"find","collection":"items","sort":{"_id":1}`))
	if len(res.Rows) != 2 {
		t.Fatalf("rows = %d, want the MaxDocs limit of 2", len(res.Rows))
	}
	if !res.Truncated {
		t.Fatal("Truncated = false, want true")
	}
}

func TestLiveReadOnlyRejectsWrites(t *testing.T) {
	ctx := context.Background()
	s := liveSession(t, ConnectOptions{ReadOnly: true})
	db := newTestDB(t, s)

	_, err := s.Execute(ctx, env(db, `"op":"insertOne","collection":"items","document":{"_id":1}`))
	if err == nil || !strings.Contains(err.Error(), "read-only") {
		t.Fatalf("insert error = %v, want a read-only rejection", err)
	}

	// Reads still work.
	run(t, s, ctx, env(db, `"op":"find","collection":"items"`))
}

func TestLiveTransactions(t *testing.T) {
	ctx := context.Background()
	s := liveSession(t, ConnectOptions{})
	db := newTestDB(t, s)

	run(t, s, ctx, env(db, `"op":"insertOne","collection":"items","document":{"_id":1}`))

	run(t, s, ctx, `{"op":"beginTransaction"}`)
	if got := s.TxState(ctx); got != engine.TxInTransaction {
		t.Fatalf("tx state = %q, want in transaction", got)
	}
	run(t, s, ctx, env(db, `"op":"insertOne","collection":"items","document":{"_id":2}`))
	run(t, s, ctx, `{"op":"abortTransaction"}`)
	if got := s.TxState(ctx); got != engine.TxIdle {
		t.Fatalf("tx state after abort = %q, want idle", got)
	}
	if got := countDocuments(t, s, ctx, db); got != "1" {
		t.Fatalf("count after abort = %q, want 1", got)
	}

	run(t, s, ctx, `{"op":"beginTransaction"}`)
	run(t, s, ctx, env(db, `"op":"insertOne","collection":"items","document":{"_id":3}`))
	run(t, s, ctx, `{"op":"commitTransaction"}`)
	if got := s.TxState(ctx); got != engine.TxIdle {
		t.Fatalf("tx state after commit = %q, want idle", got)
	}
	if got := countDocuments(t, s, ctx, db); got != "2" {
		t.Fatalf("count after commit = %q, want 2", got)
	}

	// A duplicate key inside a transaction marks it aborted; abort clears it.
	run(t, s, ctx, `{"op":"beginTransaction"}`)
	res, err := s.Execute(ctx, env(db, `"op":"insertOne","collection":"items","document":{"_id":1}`))
	if err != nil {
		t.Fatalf("duplicate insert returned a transport error: %v", err)
	}
	if !res.Error {
		t.Fatal("duplicate insert did not produce an error result")
	}
	if got := s.TxState(ctx); got != engine.TxAborted {
		t.Fatalf("tx state = %q, want aborted", got)
	}
	run(t, s, ctx, `{"op":"abortTransaction"}`)
	if got := countDocuments(t, s, ctx, db); got != "2" {
		t.Fatalf("count after aborted tx = %q, want 2", got)
	}
}

func TestLiveCancel(t *testing.T) {
	ctx := context.Background()
	s := liveSession(t, ConnectOptions{})
	db := newTestDB(t, s)
	run(t, s, ctx, env(db, `"op":"insertOne","collection":"items","document":{"_id":1}`))

	body := "function(){var t=Date.now()+3000; while(Date.now()<t){} return 1;}"
	spec := query.Spec{
		Op:         query.Aggregate,
		Database:   db,
		Collection: "items",
		Pipeline: bson.A{
			d(e("$limit", int32(1))),
			d(e("$project", d(e("x", d(e("$function", d(
				e("body", body),
				e("args", bson.A{}),
				e("lang", "js"),
			))))))),
		},
	}

	type outcome struct {
		res engine.Result
		err error
	}
	opCtx, cancelOp := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancelOp()
	ch := make(chan outcome, 1)
	go func() {
		r, err := s.ExecuteSpec(opCtx, spec)
		ch <- outcome{res: r, err: err}
	}()

	time.Sleep(300 * time.Millisecond)
	select {
	case out := <-ch:
		message := strings.Join(out.res.Messages, " ") + " " + errText(out.err)
		if strings.Contains(strings.ToLower(message), "javascript") ||
			strings.Contains(message, "$function") ||
			strings.Contains(message, "Unrecognized pipeline stage") {
			t.Skip("server-side JavaScript is disabled in this deployment")
		}
		t.Fatalf("operation finished before it could be cancelled: %+v", out)
	default:
	}

	deadline := time.Now().Add(2 * time.Second)
	for {
		if err := s.Cancel(ctx); err == nil {
			break
		} else if time.Now().After(deadline) {
			t.Fatalf("Cancel() error = %v", err)
		}
		time.Sleep(50 * time.Millisecond)
	}

	out := <-ch
	if out.err != nil {
		t.Fatalf("ExecuteSpec() error = %v", out.err)
	}
	if !out.res.Cancelled || !out.res.Error {
		t.Fatalf("result = %+v, want a cancelled error result", out.res)
	}
	if out.res.Code != "11601" {
		t.Fatalf("code = %q, want 11601", out.res.Code)
	}

	// The client survives the kill.
	if got := countDocuments(t, s, ctx, db); got != "1" {
		t.Fatalf("count after cancel = %q, want 1", got)
	}
}

func TestLiveDDL(t *testing.T) {
	ctx := context.Background()
	s := liveSession(t, ConnectOptions{})
	db := newTestDB(t, s)

	run(t, s, ctx, env(db, `"op":"createCollection","collection":"items","validator":{"$jsonSchema":{"bsonType":"object","required":["name"],"properties":{"name":{"bsonType":"string"}}}}`))

	// The validator must reject a document without the required field.
	res, err := s.Execute(ctx, env(db, `"op":"insertOne","collection":"items","document":{"_id":1}`))
	if err != nil {
		t.Fatalf("invalid insert returned a transport error: %v", err)
	}
	if !res.Error {
		t.Fatal("invalid insert was accepted, the validator did not apply")
	}
	run(t, s, ctx, env(db, `"op":"insertOne","collection":"items","document":{"_id":1,"name":"Ann"}`))

	run(t, s, ctx, env(db, `"op":"createIndex","collection":"items","keys":{"name":1},"name":"name_1"`))
	indexes := indexNames(t, s, ctx, db, "items")
	if !contains(indexes, "name_1") {
		t.Fatalf("indexes = %v, want name_1", indexes)
	}
	run(t, s, ctx, env(db, `"op":"dropIndex","collection":"items","name":"name_1"`))

	run(t, s, ctx, env(db, `"op":"collMod","collection":"items","validator":{"$jsonSchema":{"bsonType":"object","required":["name","age"],"properties":{"name":{"bsonType":"string"},"age":{"bsonType":"int"}}}}`))
	res, err = s.Execute(ctx, env(db, `"op":"insertOne","collection":"items","document":{"_id":2,"name":"Bob"}`))
	if err != nil {
		t.Fatalf("collMod insert returned a transport error: %v", err)
	}
	if !res.Error {
		t.Fatal("insert without age was accepted after collMod")
	}

	run(t, s, ctx, env(db, `"op":"renameCollection","collection":"items","newName":"items2"`))
	collections := collectionNames(t, s, ctx, db)
	if !contains(collections, "items2") || contains(collections, "items") {
		t.Fatalf("collections = %v, want items2 instead of items", collections)
	}

	run(t, s, ctx, env(db, `"op":"dropCollection","collection":"items2"`))
	if collections := collectionNames(t, s, ctx, db); contains(collections, "items2") {
		t.Fatalf("collections = %v, items2 survived dropCollection", collections)
	}
}

func TestLiveExecuteAfterClose(t *testing.T) {
	s := liveSession(t, ConnectOptions{})
	if err := s.Close(); err != nil {
		t.Fatalf("Close() error = %v", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	if _, err := s.Execute(ctx, `{"op":"find","collection":"items"}`); err == nil {
		t.Fatal("Execute() after Close() = nil, want an error")
	}
}

func TestLiveMQL(t *testing.T) {
	ctx := context.Background()
	s := liveSession(t, ConnectOptions{})
	db := newTestDB(t, s)
	if err := s.SwitchDatabase(ctx, db); err != nil {
		t.Fatalf("SwitchDatabase() error = %v", err)
	}

	res, err := s.Execute(ctx, `db.items.insertOne({_id: 1, name: "Ann", qty: 3})`)
	if err != nil || res.Error {
		t.Fatalf("MQL insert: err=%v res=%+v", err, res)
	}

	res, err = s.Execute(ctx, `db.items.find({qty: {$gte: 3}}).sort({name: 1}).limit(5)`)
	if err != nil || res.Error {
		t.Fatalf("MQL find: err=%v res=%+v", err, res)
	}
	if len(res.Rows) != 1 || cell(res, 0, "name") != "Ann" {
		t.Fatalf("MQL find rows = %v", res.Rows)
	}

	res, err = s.Execute(ctx, `db.items.updateOne({_id: 1}, {$set: {qty: 4}}, {upsert: true})`)
	if err != nil || res.Error || res.RowCount != 1 {
		t.Fatalf("MQL update: err=%v res=%+v", err, res)
	}

	res, err = s.Execute(ctx, `db.items.countDocuments({qty: 4})`)
	if err != nil || res.Error || len(res.Rows) != 1 || res.Rows[0][0] != "1" {
		t.Fatalf("MQL count: err=%v res=%+v", err, res)
	}

	res, err = s.Execute(ctx, `db.items.deleteMany({})`)
	if err != nil || res.Error || res.RowCount != 1 {
		t.Fatalf("MQL delete: err=%v res=%+v", err, res)
	}
}

func TestLiveSwitchDatabase(t *testing.T) {
	ctx := context.Background()
	s := liveSession(t, ConnectOptions{})
	first := newTestDB(t, s)
	second := newTestDB(t, s)

	run(t, s, ctx, env(first, `"op":"insertOne","collection":"items","document":{"_id":1,"where":"first"}`))
	run(t, s, ctx, env(second, `"op":"insertOne","collection":"items","document":{"_id":2,"where":"second"}`))

	if err := s.SwitchDatabase(ctx, second); err != nil {
		t.Fatalf("SwitchDatabase() error = %v", err)
	}
	if got := s.Summary().Database; got != second {
		t.Fatalf("summary database = %q, want %q", got, second)
	}

	// No database in the envelope: the session's current database is used.
	res := run(t, s, ctx, `{"op":"find","collection":"items"}`)
	if len(res.Rows) != 1 || cell(res, 0, "where") != "second" {
		t.Fatalf("rows = %v, want only the document from the second database", res.Rows)
	}
	if s.Summary().ServerVersion == "" {
		t.Fatal("server version is empty")
	}
}

func TestCancelWithoutRequest(t *testing.T) {
	s := &Session{}
	if err := s.Cancel(context.Background()); err == nil || !strings.Contains(err.Error(), "no request") {
		t.Fatalf("Cancel() error = %v, want a no-request error", err)
	}
}

func TestConnectNetworkError(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	_, err := Connect(ctx, ConnectOptions{
		URI:     "mongodb://127.0.0.1:1/?directConnection=true",
		Timeout: 200 * time.Millisecond,
	})
	if err == nil {
		t.Fatal("Connect() = nil, want a network error")
	}
	lower := strings.ToLower(err.Error())
	if strings.Contains(lower, "authentication") {
		t.Fatalf("network error was classified as auth: %v", err)
	}
	if !strings.Contains(lower, "server selection") && !strings.Contains(lower, "connect") && !strings.Contains(lower, "refused") {
		t.Fatalf("error does not look like a network error: %v", err)
	}
}

func TestMajorFromVersion(t *testing.T) {
	tests := map[string]int{
		"8.0.38": 8,
		"7.0.14": 7,
		"8":      8,
		"":       0,
		"dev":    0,
	}
	for version, want := range tests {
		if got := majorFromVersion(version); got != want {
			t.Fatalf("majorFromVersion(%q) = %d, want %d", version, got, want)
		}
	}
}

func TestStandaloneTransactionsUnavailable(t *testing.T) {
	s := &Session{tx: engine.TxIdle}
	_, err := s.beginTransaction(context.Background())
	if err == nil || !strings.Contains(err.Error(), "replica set") {
		t.Fatalf("beginTransaction() error = %v, want a replica-set explanation", err)
	}
}

func TestParseURI(t *testing.T) {
	tests := []struct {
		name       string
		uri        string
		db         string
		user       string
		authSource string
		wantErr    string
	}{
		{
			name: "user and database",
			uri:  "mongodb://ann@db.example:27017/app?authSource=admin",
			db:   "app", user: "ann", authSource: "admin",
		},
		{
			name: "defaults to test and admin",
			uri:  "mongodb://db.example:27017",
			db:   "test", authSource: "admin",
		},
		{
			name: "auth source defaults to the database",
			uri:  "mongodb+srv://ann@cluster.example/app",
			db:   "app", user: "ann", authSource: "app",
		},
		{
			name: "multi host",
			uri:  "mongodb://uuu@mongod1:27021,mongod2:27022,mongod3:27023/stage?authSource=admin&replicaset=dbrs",
			db:   "stage", user: "uuu", authSource: "admin",
		},
		{
			name:    "password in URI is rejected",
			uri:     "mongodb://ann:secret@db.example/app",
			wantErr: "passwords are not accepted",
		},
		{
			name:    "wrong scheme",
			uri:     "postgresql://ann@db.example/app",
			wantErr: "unsupported scheme",
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			db, user, authSource, err := parseURI(tc.uri)
			if tc.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tc.wantErr) {
					t.Fatalf("error = %v, want it to contain %q", err, tc.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatalf("parseURI() error = %v", err)
			}
			if db != tc.db || user != tc.user || authSource != tc.authSource {
				t.Fatalf("got db=%q user=%q authSource=%q, want %q/%q/%q",
					db, user, authSource, tc.db, tc.user, tc.authSource)
			}
		})
	}
}

func TestConnectRejectsPasswordInURI(t *testing.T) {
	_, err := Connect(context.Background(), ConnectOptions{
		URI:     "mongodb://ann:secret@127.0.0.1:1/app",
		Timeout: 50 * time.Millisecond,
	})
	if err == nil || !strings.Contains(err.Error(), "passwords are not accepted") {
		t.Fatalf("Connect() error = %v, want a password rejection", err)
	}
}

func indexNames(t *testing.T, s *Session, ctx context.Context, db, collection string) []string {
	t.Helper()
	cur, err := s.client.Database(db).Collection(collection).Indexes().List(ctx)
	if err != nil {
		t.Fatalf("list indexes: %v", err)
	}
	var docs []bson.M
	if err := cur.All(ctx, &docs); err != nil {
		t.Fatalf("decode indexes: %v", err)
	}
	names := make([]string, 0, len(docs))
	for _, doc := range docs {
		if name, ok := doc["name"].(string); ok {
			names = append(names, name)
		}
	}
	return names
}

func collectionNames(t *testing.T, s *Session, ctx context.Context, db string) []string {
	t.Helper()
	cur, err := s.client.Database(db).ListCollections(ctx, bson.M{})
	if err != nil {
		t.Fatalf("list collections: %v", err)
	}
	var docs []bson.M
	if err := cur.All(ctx, &docs); err != nil {
		t.Fatalf("decode collections: %v", err)
	}
	names := make([]string, 0, len(docs))
	for _, doc := range docs {
		if name, ok := doc["name"].(string); ok {
			names = append(names, name)
		}
	}
	return names
}

func contains(values []string, want string) bool {
	for _, value := range values {
		if value == want {
			return true
		}
	}
	return false
}

func liveSession(t *testing.T, opts ConnectOptions) *Session {
	t.Helper()
	uri := os.Getenv("MONGODB_URI")
	if uri == "" {
		t.Skip("MONGODB_URI is not set")
	}
	opts.URI = uri
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	s, err := Connect(ctx, opts)
	if err != nil {
		t.Fatalf("Connect() error = %v", err)
	}
	t.Cleanup(func() { s.Close() })
	return s
}

func newTestDB(t *testing.T, s *Session) string {
	t.Helper()
	db := fmt.Sprintf("mongo_tui_test_%d", time.Now().UnixNano())
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = s.client.Database(db).Drop(ctx)
	})
	return db
}

func env(db, body string) string {
	return fmt.Sprintf(`{"database":%q,%s}`, db, body)
}

func run(t *testing.T, s *Session, ctx context.Context, envelope string) engine.Result {
	t.Helper()
	res, err := s.Execute(ctx, envelope)
	if err != nil {
		t.Fatalf("Execute(%s) error = %v", envelope, err)
	}
	if res.Error {
		t.Fatalf("Execute(%s) server error = %v (code %s)", envelope, res.Messages, res.Code)
	}
	return res
}

func countDocuments(t *testing.T, s *Session, ctx context.Context, db string) string {
	t.Helper()
	res := run(t, s, ctx, env(db, `"op":"count","collection":"items","filter":{}`))
	return res.Rows[0][0]
}

func cell(res engine.Result, row int, column string) string {
	index := rowIndex(res, column)
	if index < 0 || row >= len(res.Rows) || index >= len(res.Rows[row]) {
		return ""
	}
	return res.Rows[row][index]
}

func typeOf(res engine.Result, column string) string {
	index := rowIndex(res, column)
	if index < 0 || index >= len(res.ColumnTypes) {
		return ""
	}
	return res.ColumnTypes[index]
}

func rowIndex(res engine.Result, column string) int {
	for i, name := range res.Columns {
		if name == column {
			return i
		}
	}
	return -1
}

func errText(err error) string {
	if err == nil {
		return ""
	}
	return err.Error()
}
