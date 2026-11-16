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
	// Database and Collection name where the rows came from, when the engine
	// can tell. The UI uses them to build a follow-up query against the same
	// collection instead of guessing from the current tree selection.
	Database   string
	Collection string
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
	// Database is the owning database, set where it matters (users, roles) so
	// the tree can open that object without re-deriving it.
	Database string
}

// RoleRef is one {role, db} grant, exactly as MongoDB stores it on a user.
type RoleRef struct {
	Role string
	DB   string
}

// UserCard is the object pane view of one MongoDB user: where they live, how
// they authenticate, and their role grants. Roles are read from usersInfo.
type UserCard struct {
	Username   string
	Database   string // the database the user is defined in (auth source)
	Mechanisms []string
	Roles      []RoleRef
	Exists     bool
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

// Pager is an optional session capability: a read query can keep its server
// cursor open, so the UI fetches further rows without re-running the query.
// Totals are capped by the session's result limits, not per page.
type Pager interface {
	// RunPaged executes the request and returns at most pageSize rows.
	// hasMore reports whether the cursor may still hold rows.
	RunPaged(ctx context.Context, request string, pageSize int) (res Result, hasMore bool, err error)
	// FetchPage reads the next page; all reads until the cursor or the result
	// limits are exhausted. The returned Result carries only the new rows.
	FetchPage(ctx context.Context, all bool) (res Result, hasMore bool, err error)
	// ClosePageOf releases the last paged cursor if it belongs to db.collection;
	// an empty collection means any cursor. Safe with nothing open.
	ClosePageOf(database, collection string)
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
	// UserCard reads one user's definition (auth database, mechanisms, roles).
	UserCard(ctx context.Context, database, username string) (UserCard, error)
	// UpdateUserRoles replaces a user's role grants without touching the
	// password. database is the user's auth database.
	UpdateUserRoles(ctx context.Context, database, username string, roles []RoleRef) error
	// Fields returns field names for completion: the document sample plus index
	// keys and JSON Schema properties observed in the collection card.
	Fields(ctx context.Context, database, collection string) ([]FieldInfo, error)
	CollectionCard(ctx context.Context, database, collection string) (CollectionCard, error)
	// UpsertUser creates a MongoDB user in the authSource database with every
	// role in roles granted on database, or updates the password and roles when
	// the user already exists. An empty roles list defaults to readWrite.
	UpsertUser(ctx context.Context, username, password, database, authSource string, roles []string) error
}
