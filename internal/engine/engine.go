// Package engine defines the types and the session contract the UI uses to
// talk to MongoDB. The PostgreSQL implementation was removed in M6; these
// types stay engine-neutral so the adapter can evolve without touching the UI.
package engine

import "context"

// Sentinels keep values the grid must not confuse: a NULL from an empty string,
// and a field that is absent from a document from a field explicitly set to
// null. NullSentinel is the canonical text the grid renders for BSON null.
const (
	NullSentinel    = "\u2400NULL\u2400"
	MissingSentinel = "\u2400MISSING\u2400"
)

// Result is one executed request.
type Result struct {
	Columns []string
	// ColumnTypes is aligned with Columns. A single engine type name per column,
	// or "mixed" when documents disagree; empty when the engine has no types.
	ColumnTypes []string
	Rows        [][]string
	// Documents holds the full record for each row when the engine can provide
	// it (MongoDB EJSON), so the UI can show it without re-reading the source.
	Documents [][]byte
	Error     bool
	Cancelled bool
	// Code is the engine's error code (the numeric MongoDB code as text).
	Code      string
	RowCount  int
	Messages  []string
	Truncated bool
}

// Info is the identity of the live session.
type Info struct {
	ServerVersion string
	Database      string
	User          string
}

// TxState is what the session's transaction looks like right now.
type TxState string

const (
	TxIdle          TxState = "idle"
	TxInTransaction TxState = "in transaction"
	TxAborted       TxState = "aborted"
)

// Item is one entry in an object tree. It is intentionally small: an engine
// fills the fields it can, and the UI renders what is present.
type Item struct {
	Name string
	// Detail carries extra facts (current database, index keys, roles). The
	// object kind itself is omitted: the parent group already says it.
	Detail string
	// Current marks the database the session is connected to.
	Current bool
}

// Session is one live connection to MongoDB, backed by one driver client.
type Session interface {
	Execute(ctx context.Context, request string) (Result, error)
	Cancel(ctx context.Context) error
	TxState(ctx context.Context) TxState
	SwitchDatabase(ctx context.Context, database string) error
	Summary() Info
	ReadOnly() bool
	Close() error
}

// Queryer is the part of a session catalog and completion loaders need.
type Queryer interface {
	Execute(ctx context.Context, request string) (Result, error)
}

// IndexInfo is one index of a MongoDB collection.
type IndexInfo struct {
	Name   string
	Keys   string
	Unique bool
}

// FieldInfo is one field observed in a sample of documents.
type FieldInfo struct {
	Name    string
	Types   string
	Present int
	Total   int
}

// CollectionCard is what the object pane shows for a MongoDB collection or
// view. Options and Validator are relaxed EJSON; fields are an observation of
// the sampled documents, never a schema.
type CollectionCard struct {
	Database   string
	Collection string
	Type       string
	Exists     bool
	Options    string
	Validator  string
	Indexes    []IndexInfo
	Fields     []FieldInfo
	Count      int64
	SizeBytes  int64
	SampleSize int
	StatsError string
	// FieldsError is set when the field sample could not be read; the rest of
	// the card is still useful.
	FieldsError string
}

// Catalog is the MongoDB object browser contract. The TUI asks for the parts
// it renders; the MongoDB adapter implements it.
type Catalog interface {
	Databases(ctx context.Context) ([]Item, error)
	Collections(ctx context.Context, database string) ([]Item, error)
	Views(ctx context.Context, database string) ([]Item, error)
	Indexes(ctx context.Context, database, collection string) ([]Item, error)
	Users(ctx context.Context) ([]Item, error)
	Roles(ctx context.Context) ([]Item, error)
	// Fields returns field names for completion: the document sample plus index
	// keys and JSON Schema properties observed in the collection card.
	Fields(ctx context.Context, database, collection string) ([]FieldInfo, error)
	CollectionCard(ctx context.Context, database, collection string) (CollectionCard, error)
}
