package mongo

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"go.mongodb.org/mongo-driver/v2/bson"
	mg "go.mongodb.org/mongo-driver/v2/mongo"
	"go.mongodb.org/mongo-driver/v2/mongo/options"

	"mongo-tui/internal/engine"
	"mongo-tui/internal/mongo/query"
)

// ExecuteSpec runs one parsed operation. Server-side command errors come back
// as engine.Result with Error set; transport and programming errors are Go
// errors; transport errors are returned separately.
func (s *Session) ExecuteSpec(ctx context.Context, spec query.Spec) (engine.Result, error) {
	s.reqMu.Lock()
	defer s.reqMu.Unlock()

	// A new request replaces whatever the grid was paging through.
	s.closePageLocked()
	return s.executeLocked(ctx, spec)
}

// executeLocked is ExecuteSpec with reqMu already held.
func (s *Session) executeLocked(ctx context.Context, spec query.Spec) (engine.Result, error) {
	opCtx, comment, txState, err := s.prepare(ctx, spec)
	if err != nil {
		return engine.Result{}, err
	}
	defer s.setComment("")

	res, err := s.dispatch(opCtx, spec, comment)
	res, err = s.finish(res, err, txState, spec)
	if err == nil {
		res.Database, res.Collection = s.specSource(spec)
	}
	return res, err
}

// specSource resolves the database and collection a result came from. MQL has
// no database qualifier, so the UI needs the exact source to build follow-up
// queries.
func (s *Session) specSource(spec query.Spec) (string, string) {
	dbName := spec.Database
	if dbName == "" {
		dbName = s.currentDatabase()
	}
	return dbName, spec.Collection
}

// prepare applies the read-only, transaction and session guards shared by the
// normal and paged execution paths. The caller must clear the comment.
func (s *Session) prepare(ctx context.Context, spec query.Spec) (context.Context, string, engine.TxState, error) {
	if s.readOnly && spec.IsWrite() {
		return nil, "", "", fmt.Errorf("mongo: read-only session rejects %s", spec.Op)
	}

	txState := s.TxState(ctx)
	switch txState {
	case engine.TxAborted:
		if spec.Op != query.AbortTransaction {
			return nil, "", "", errors.New("mongo: transaction is aborted; run abortTransaction first")
		}
	case engine.TxIdle:
		if spec.Op == query.CommitTransaction || spec.Op == query.AbortTransaction {
			return nil, "", "", errors.New("mongo: no transaction in progress")
		}
	}

	opCtx := ctx
	if txState == engine.TxInTransaction && !isTransactionOp(spec.Op) {
		s.txMu.Lock()
		txSess := s.txSess
		s.txMu.Unlock()
		if txSess != nil {
			opCtx = mg.NewSessionContext(ctx, txSess)
		}
	}

	comment := newComment()
	s.setComment(comment)
	return opCtx, comment, txState, nil
}

// finish maps driver errors to results and keeps the transaction state honest.
func (s *Session) finish(res engine.Result, err error, txState engine.TxState, spec query.Spec) (engine.Result, error) {
	if err != nil {
		if isCancelled(err) {
			return engine.Result{
				Error:     true,
				Cancelled: true,
				Code:      "11601",
				Messages:  []string{"request cancelled"},
			}, nil
		}
		if mapped, ok := resultFromServerError(err); ok {
			if txState == engine.TxInTransaction && !isTransactionOp(spec.Op) {
				s.markTxAborted()
			}
			return mapped, nil
		}
		return engine.Result{}, err
	}
	if res.Error && txState == engine.TxInTransaction && !isTransactionOp(spec.Op) {
		s.markTxAborted()
	}
	return res, nil
}

func (s *Session) dispatch(ctx context.Context, spec query.Spec, comment string) (engine.Result, error) {
	if isTransactionOp(spec.Op) {
		switch spec.Op {
		case query.BeginTransaction:
			return s.beginTransaction(ctx)
		case query.CommitTransaction:
			return s.commitTransaction(ctx)
		case query.AbortTransaction:
			return s.abortTransaction(ctx)
		}
	}

	dbName := spec.Database
	if dbName == "" {
		dbName = s.currentDatabase()
	}
	db := s.client.Database(dbName)
	coll := db.Collection(spec.Collection)
	filter := spec.Filter
	if filter == nil {
		filter = bson.D{}
	}

	switch spec.Op {
	case query.Find, query.Aggregate:
		cur, err := s.openCursor(ctx, spec, comment)
		if err != nil {
			return engine.Result{}, err
		}
		return s.collectCursor(ctx, cur)

	case query.Count:
		opts := options.Count().SetComment(comment)
		if spec.Skip > 0 {
			opts.SetSkip(spec.Skip)
		}
		if spec.Limit > 0 {
			opts.SetLimit(spec.Limit)
		}
		n, err := coll.CountDocuments(ctx, filter, opts)
		if err != nil {
			return engine.Result{}, err
		}
		return engine.Result{
			Columns:     []string{"count"},
			ColumnTypes: []string{"long"},
			Rows:        [][]string{{strconv.FormatInt(n, 10)}},
			RowCount:    int(n),
			Messages:    []string{fmt.Sprintf("%d documents", n)},
		}, nil

	case query.Distinct:
		result := coll.Distinct(ctx, spec.Field, filter, options.Distinct().SetComment(comment))
		var values bson.A
		if err := result.Decode(&values); err != nil {
			return engine.Result{}, err
		}
		rows := make([][]string, 0, len(values))
		for _, value := range values {
			text, _ := renderValue(value)
			rows = append(rows, []string{text})
		}
		return engine.Result{
			Columns:     []string{spec.Field},
			ColumnTypes: []string{typeSummary(values)},
			Rows:        rows,
			RowCount:    len(rows),
			Messages:    []string{fmt.Sprintf("%d values", len(rows))},
		}, nil

	case query.InsertOne:
		res, err := coll.InsertOne(ctx, spec.Document, options.InsertOne().SetComment(comment))
		if err != nil {
			return engine.Result{}, err
		}
		return simpleResult(1, "inserted 1 document, _id="+ejsonValue(res.InsertedID)), nil

	case query.InsertMany:
		docs := make([]any, 0, len(spec.Documents))
		for _, doc := range spec.Documents {
			docs = append(docs, doc)
		}
		res, err := coll.InsertMany(ctx, docs, options.InsertMany().SetComment(comment))
		if err != nil {
			return engine.Result{}, err
		}
		return simpleResult(len(res.InsertedIDs), fmt.Sprintf("inserted %d documents", len(res.InsertedIDs))), nil

	case query.UpdateOne:
		res, err := coll.UpdateOne(ctx, filter, spec.Update,
			options.UpdateOne().SetComment(comment).SetUpsert(spec.Upsert))
		if err != nil {
			return engine.Result{}, err
		}
		return updateResult(res), nil

	case query.UpdateMany:
		res, err := coll.UpdateMany(ctx, filter, spec.Update,
			options.UpdateMany().SetComment(comment).SetUpsert(spec.Upsert))
		if err != nil {
			return engine.Result{}, err
		}
		return updateResult(res), nil

	case query.ReplaceOne:
		res, err := coll.ReplaceOne(ctx, filter, spec.Replacement,
			options.Replace().SetComment(comment).SetUpsert(spec.Upsert))
		if err != nil {
			return engine.Result{}, err
		}
		return updateResult(res), nil

	case query.DeleteOne:
		res, err := coll.DeleteOne(ctx, filter, options.DeleteOne().SetComment(comment))
		if err != nil {
			return engine.Result{}, err
		}
		return simpleResult(int(res.DeletedCount), fmt.Sprintf("deleted %d documents", res.DeletedCount)), nil

	case query.DeleteMany:
		res, err := coll.DeleteMany(ctx, filter, options.DeleteMany().SetComment(comment))
		if err != nil {
			return engine.Result{}, err
		}
		return simpleResult(int(res.DeletedCount), fmt.Sprintf("deleted %d documents", res.DeletedCount)), nil

	case query.CreateCollection:
		cmd := d(e("create", spec.Collection))
		cmd = appendOptions(cmd, spec.Options)
		if len(spec.Validator) > 0 {
			cmd = withField(cmd, "validator", spec.Validator)
		}
		return runCommand(ctx, db, cmd)

	case query.DropCollection:
		if err := coll.Drop(ctx); err != nil {
			return engine.Result{}, err
		}
		return simpleResult(0, "collection dropped"), nil

	case query.CreateIndex:
		name := spec.Name
		if name == "" {
			name = indexName(spec.Keys)
		}
		index := d(e("key", spec.Keys))
		index = appendOptions(index, spec.Options)
		index = withField(index, "name", name)
		if spec.Unique {
			index = withField(index, "unique", true)
		}
		cmd := d(e("createIndexes", spec.Collection), e("indexes", bson.A{index}))
		return runCommand(ctx, db, cmd)

	case query.DropIndex:
		if err := coll.Indexes().DropOne(ctx, spec.Name); err != nil {
			return engine.Result{}, err
		}
		return simpleResult(0, "index dropped: "+spec.Name), nil

	case query.CollMod:
		cmd := d(e("collMod", spec.Collection))
		cmd = appendOptions(cmd, spec.Options)
		if len(spec.Validator) > 0 {
			cmd = withField(cmd, "validator", spec.Validator)
		}
		return runCommand(ctx, db, cmd)

	case query.RenameCollection:
		from := dbName + "." + spec.Collection
		to := dbName + "." + spec.NewName
		cmd := d(e("renameCollection", from), e("to", to))
		if spec.DropTarget {
			cmd = append(cmd, e("dropTarget", true))
		}
		return runCommand(ctx, s.client.Database("admin"), cmd)
	}

	return engine.Result{}, fmt.Errorf("mongo: unsupported op %q", spec.Op)
}

// openCursor starts the server cursor behind find and aggregate, shared by the
// one-shot collect and the paged reader.
func (s *Session) openCursor(ctx context.Context, spec query.Spec, comment string) (*mg.Cursor, error) {
	dbName := spec.Database
	if dbName == "" {
		dbName = s.currentDatabase()
	}
	coll := s.client.Database(dbName).Collection(spec.Collection)

	switch spec.Op {
	case query.Find:
		filter := spec.Filter
		if filter == nil {
			filter = bson.D{}
		}
		opts := options.Find().
			SetComment(comment).
			SetLimit(spec.Limit).
			SetSkip(spec.Skip)
		if len(spec.Projection) > 0 {
			opts.SetProjection(spec.Projection)
		}
		if len(spec.Sort) > 0 {
			opts.SetSort(spec.Sort)
		}
		return coll.Find(ctx, filter, opts)
	case query.Aggregate:
		pipeline, err := toPipeline(spec.Pipeline)
		if err != nil {
			return nil, err
		}
		opts := options.Aggregate().SetComment(comment).SetAllowDiskUse(spec.AllowDiskUse)
		return coll.Aggregate(ctx, pipeline, opts)
	}
	return nil, fmt.Errorf("mongo: %s does not return a cursor", spec.Op)
}

func (s *Session) collectCursor(ctx context.Context, cur *mg.Cursor) (engine.Result, error) {
	defer cur.Close(context.Background())

	g := newGridBuilder()
	var bytes int
	truncated := false

	for cur.Next(ctx) {
		var doc bson.D
		if err := cur.Decode(&doc); err != nil {
			return engine.Result{}, err
		}
		raw, err := bson.MarshalExtJSON(doc, false, false)
		if err != nil {
			return engine.Result{}, err
		}
		if len(g.rows) >= s.maxDocs || (s.maxBytes > 0 && bytes+len(raw) > s.maxBytes) {
			truncated = true
			break
		}
		g.add(doc, raw)
		bytes += len(raw)
	}
	if err := cur.Err(); err != nil {
		return engine.Result{}, err
	}

	res := g.result()
	res.Truncated = res.Truncated || truncated
	return res, nil
}

func simpleResult(rows int, messages ...string) engine.Result {
	return engine.Result{RowCount: rows, Messages: messages}
}

func updateResult(res *mg.UpdateResult) engine.Result {
	msg := fmt.Sprintf("matched %d, modified %d", res.MatchedCount, res.ModifiedCount)
	if res.UpsertedID != nil {
		msg += ", upserted _id=" + ejsonValue(res.UpsertedID)
	}
	return engine.Result{RowCount: int(res.ModifiedCount), Messages: []string{msg}}
}

func runCommand(ctx context.Context, db *mg.Database, cmd bson.D) (engine.Result, error) {
	var out bson.D
	if err := db.RunCommand(ctx, cmd).Decode(&out); err != nil {
		return engine.Result{}, err
	}
	return engine.Result{Messages: []string{ejsonDocument(out)}}, nil
}

func toPipeline(stages bson.A) (mg.Pipeline, error) {
	pipeline := make(mg.Pipeline, 0, len(stages))
	for i, stage := range stages {
		doc, ok := stageDocument(stage)
		if !ok {
			return nil, fmt.Errorf("mongo: pipeline stage %d is %T, expected a document", i, stage)
		}
		pipeline = append(pipeline, doc)
	}
	return pipeline, nil
}

func appendOptions(dst bson.D, opts bson.D) bson.D {
	if len(opts) == 0 {
		return dst
	}
	return append(dst, opts...)
}

// withField replaces a field in place instead of appending a duplicate key.
func withField(dst bson.D, key string, value any) bson.D {
	out := make(bson.D, 0, len(dst)+1)
	for _, item := range dst {
		if item.Key != key {
			out = append(out, item)
		}
	}
	return append(out, e(key, value))
}

func indexName(keys bson.D) string {
	parts := make([]string, 0, len(keys))
	for _, item := range keys {
		parts = append(parts, fmt.Sprintf("%s_%v", item.Key, item.Value))
	}
	return strings.Join(parts, "_")
}

func isTransactionOp(op query.Op) bool {
	switch op {
	case query.BeginTransaction, query.CommitTransaction, query.AbortTransaction:
		return true
	}
	return false
}

func (s *Session) markTxAborted() {
	s.txMu.Lock()
	if s.tx == engine.TxInTransaction {
		s.tx = engine.TxAborted
	}
	s.txMu.Unlock()
}

func resultFromServerError(err error) (engine.Result, bool) {
	var se mg.ServerError
	if !errors.As(err, &se) {
		return engine.Result{}, false
	}
	code := ""
	if codes := se.ErrorCodes(); len(codes) > 0 {
		code = strconv.Itoa(codes[0])
	}
	return engine.Result{Error: true, Code: code, Messages: []string{err.Error()}}, true
}

func isCancelled(err error) bool {
	var se mg.ServerError
	if errors.As(err, &se) {
		for _, code := range se.ErrorCodes() {
			if code == 11601 {
				return true
			}
		}
	}
	return strings.Contains(strings.ToLower(err.Error()), "interrupted")
}

func newComment() string {
	buf := make([]byte, 8)
	if _, err := rand.Read(buf); err != nil {
		return fmt.Sprintf("mongo-tui-%d", time.Now().UnixNano())
	}
	return "mongo-tui-" + hex.EncodeToString(buf)
}

func stageDocument(stage any) (bson.D, bool) {
	switch value := stage.(type) {
	case bson.D:
		return value, true
	case bson.M:
		doc := make(bson.D, 0, len(value))
		for k, v := range value {
			doc = append(doc, e(k, v))
		}
		return doc, true
	}
	return nil, false
}
