// Package mongo is the MongoDB adapter: one driver client per session, JSON
// envelopes in, engine.Result out.
package mongo

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"

	"go.mongodb.org/mongo-driver/v2/bson"
	mg "go.mongodb.org/mongo-driver/v2/mongo"
	"go.mongodb.org/mongo-driver/v2/mongo/options"

	"mongo-tui/internal/engine"
	"mongo-tui/internal/mongo/query"
	"mongo-tui/internal/mongouri"
)

// Defaults for the result limits.
const (
	DefaultMaxDocs    = 10_000
	DefaultMaxBytes   = 32 << 20
	DefaultSampleSize = 50
)

// ConnectOptions describes what to open. URI never contains a password; the
// password is supplied separately and kept only in memory.
type ConnectOptions struct {
	URI        string
	Password   string
	ReadOnly   bool
	MaxDocs    int
	MaxBytes   int
	SampleSize int
	Timeout    time.Duration
}

// Session is one MongoDB client. A client covers every database, so switching
// the current database is a local operation and never reconnects.
type Session struct {
	client       *mg.Client
	version      string
	versionMajor int
	user         string
	dbName       string
	replicaSet   bool
	maxDocs      int
	maxBytes     int
	sampleSize   int
	readOnly     bool

	dbMu sync.RWMutex

	// reqMu serializes requests: one operation at a time.
	reqMu sync.Mutex
	// commentMu guards the comment of the request Cancel may kill.
	commentMu sync.Mutex
	comment   string

	txMu   sync.Mutex
	txSess *mg.Session
	tx     engine.TxState
}

// Connect opens the client, pings the deployment and reads its identity.
func Connect(ctx context.Context, opts ConnectOptions) (*Session, error) {
	if strings.TrimSpace(opts.URI) == "" {
		return nil, errors.New(`mongo: a "mongodb://" or "mongodb+srv://" URI is required`)
	}
	dbName, user, authSource, err := parseURI(opts.URI)
	if err != nil {
		return nil, err
	}
	if opts.Password != "" && user == "" {
		return nil, errors.New("mongo: a password was supplied but the URI has no username")
	}
	if opts.MaxDocs <= 0 {
		opts.MaxDocs = DefaultMaxDocs
	}
	if opts.MaxBytes <= 0 {
		opts.MaxBytes = DefaultMaxBytes
	}
	if opts.SampleSize <= 0 {
		opts.SampleSize = DefaultSampleSize
	}
	timeout := opts.Timeout
	if timeout <= 0 {
		timeout = 10 * time.Second
	}

	clientOpts := options.Client().
		ApplyURI(opts.URI).
		SetServerSelectionTimeout(timeout).
		SetConnectTimeout(timeout)
	if opts.Password != "" {
		clientOpts.SetAuth(options.Credential{
			Username:    user,
			Password:    opts.Password,
			PasswordSet: true,
			AuthSource:  authSource,
		})
	}

	client, err := mg.Connect(clientOpts)
	if err != nil {
		return nil, fmt.Errorf("mongo: cannot create a client: %w", err)
	}
	s := &Session{
		client:     client,
		user:       user,
		dbName:     dbName,
		maxDocs:    opts.MaxDocs,
		maxBytes:   opts.MaxBytes,
		sampleSize: opts.SampleSize,
		readOnly:   opts.ReadOnly,
		tx:         engine.TxIdle,
	}
	if err := s.bootstrap(ctx); err != nil {
		_ = client.Disconnect(context.Background())
		return nil, err
	}
	return s, nil
}

func (s *Session) bootstrap(ctx context.Context) error {
	admin := s.client.Database("admin")

	var build bson.M
	if err := admin.RunCommand(ctx, d(e("buildInfo", 1))).Decode(&build); err != nil {
		return fmt.Errorf("mongo: buildInfo failed: %w", err)
	}
	s.version, _ = build["version"].(string)
	if s.version == "" {
		s.version = "MongoDB"
	}
	if versions, ok := build["versionArray"].(bson.A); ok && len(versions) > 0 {
		s.versionMajor = int(numberOf(versions[0]))
	}
	if s.versionMajor == 0 {
		s.versionMajor = majorFromVersion(s.version)
	}
	if s.versionMajor > 0 && s.versionMajor < 8 {
		return fmt.Errorf("mongo: server %s is too old; MongoDB 8.0 or newer is required", s.version)
	}

	var hello bson.M
	if err := admin.RunCommand(ctx, d(e("hello", 1))).Decode(&hello); err != nil {
		return fmt.Errorf("mongo: hello failed: %w", err)
	}
	// setName is present only on a replica set. A mongos advertises itself with
	// msg=isdbgrid; both support multi-document transactions.
	s.replicaSet = hello["setName"] != nil || fmt.Sprint(hello["msg"]) == "isdbgrid"
	return nil
}

// Execute parses one JSON envelope or a restricted MQL command and runs it.
func (s *Session) Execute(ctx context.Context, request string) (engine.Result, error) {
	spec, _, err := query.ParseRequest(request)
	if err != nil {
		return engine.Result{}, err
	}
	return s.ExecuteSpec(ctx, spec)
}

// Summary returns the engine-neutral identity of the session.
func (s *Session) Summary() engine.Info {
	s.dbMu.RLock()
	defer s.dbMu.RUnlock()
	return engine.Info{ServerVersion: s.version, Database: s.dbName, User: s.user}
}

// ReadOnly reports whether write operations are rejected client-side.
func (s *Session) ReadOnly() bool { return s.readOnly }

// SwitchDatabase changes the default database for subsequent requests. Unlike
// a PostgreSQL session, the MongoDB client stays connected and keeps its transaction.
func (s *Session) SwitchDatabase(_ context.Context, database string) error {
	database = strings.TrimSpace(database)
	if database == "" {
		return errors.New("empty database name")
	}
	s.dbMu.Lock()
	s.dbName = database
	s.dbMu.Unlock()
	return nil
}

// TxState reports the transaction state tracked by the adapter.
func (s *Session) TxState(context.Context) engine.TxState {
	s.txMu.Lock()
	defer s.txMu.Unlock()
	return s.tx
}

// Close aborts an open transaction and disconnects.
func (s *Session) Close() error {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	s.txMu.Lock()
	if s.txSess != nil {
		_ = s.txSess.AbortTransaction(ctx)
		s.txSess.EndSession(ctx)
		s.txSess = nil
		s.tx = engine.TxIdle
	}
	s.txMu.Unlock()

	return s.client.Disconnect(ctx)
}

func (s *Session) currentDatabase() string {
	s.dbMu.RLock()
	defer s.dbMu.RUnlock()
	return s.dbName
}

func (s *Session) setComment(comment string) {
	s.commentMu.Lock()
	s.comment = comment
	s.commentMu.Unlock()
}

func (s *Session) getComment() string {
	s.commentMu.Lock()
	defer s.commentMu.Unlock()
	return s.comment
}

// parseURI returns the default database, the username and the auth source.
// A password inside the URI is rejected: it would leak through argv and logs.
// Multi-host authorities (host1:port1,host2:port2,...) are handled by
// mongouri, because net/url rejects them as invalid ports.
func parseURI(raw string) (dbName, user, authSource string, err error) {
	u, err := mongouri.Parse(raw)
	if err != nil {
		return "", "", "", fmt.Errorf("mongo: invalid URI: %w", err)
	}
	if u.HasPassword {
		return "", "", "", errors.New("mongo: passwords are not accepted in the URI; enter the password in the TUI")
	}
	user = u.User
	dbName = u.Database()
	if dbName == "" {
		// mongosh defaults to "test" when the URI has no database.
		dbName = "test"
	}
	query, _ := url.ParseQuery(u.Query)
	authSource = query.Get("authSource")
	if authSource == "" {
		if pathDB := u.Database(); pathDB != "" {
			authSource = pathDB
		} else {
			authSource = "admin"
		}
	}
	return dbName, user, authSource, nil
}

func majorFromVersion(version string) int {
	part := version
	if i := strings.IndexByte(part, '.'); i >= 0 {
		part = part[:i]
	}
	major, err := strconv.Atoi(part)
	if err != nil {
		return 0
	}
	return major
}

// Compile-time check: the MongoDB adapter satisfies the engine contract.
var _ engine.Session = (*Session)(nil)

// e and d keep BSON literals readable.
func e(k string, v any) bson.E { return bson.E{Key: k, Value: v} }
func d(items ...bson.E) bson.D { return bson.D(items) }
