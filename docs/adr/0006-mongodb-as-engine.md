# 0006 — MongoDB as the only engine, through the official Go driver

Status: accepted, 2026-11-14

Supersedes: 0001 (psql as the engine), 0002 (one session per database),
0005 (cancel through a side connection). Keeps 0003 (local state) and 0004
(Go and bubbletea).

## Context

psql-tui holds one long-lived `psql` child and relies on its CSV output, an
`\echo` end marker, `:ROW_COUNT`, stderr diagnostics, a libpq passfile and
`pg_cancel_backend()`. Those mechanisms are the product of psql being a
line-oriented terminal client with a stateful query buffer.

The owner decided to replace PostgreSQL with MongoDB entirely. The target
product is `mongo-tui`; the psql implementation stays in the tree as a
reference until `docs/mongo-parity.md` is green, then it is removed.

MongoDB has no equivalent of that framing contract:

- `mongosh` is a Node.js REPL. It pretty-prints JavaScript/EJSON, reads
  line-oriented input and has no reliable end marker; one-shot `--eval`
  invocations lose transactions and cursors.
- The official Go driver returns typed BSON documents, streams cursors and
  exposes sessions, transactions and context cancellation. It is the same
  fallback ADR 0001 kept behind the `SessionAdapter` seam.

## Decision

1. One engine: MongoDB 8.0 or newer. The driver is
   `go.mongodb.org/mongo-driver/v2`; no `mongosh` child process.
2. Connection: `mongodb://` and `mongodb+srv://` URIs plus saved profiles.
   The password is prompted in the TUI and never stored; a URI carrying a
   password is rejected, because it would leak through argv and process lists.
3. Query language: hybrid. The canonical editable form is a JSON envelope
   (`{"op":"find","collection":"users","filter":{...}}`). A restricted MQL
   text form (`db.users.find({...}).limit(100)`) is sugar that compiles to the
   same `query.Spec`. Neither form evaluates arbitrary JavaScript; loops,
   variables and user functions stay in `mongosh`.
4. One client covers every database. The current database is UI state;
   switching it does not reconnect and does not reset the session. ADR 0002's
   restart is gone.
5. Transactions use driver sessions and require a replica set. On a standalone
   server the UI reports that transactions are unavailable instead of failing
   silently.
6. Cancel is server-side: a per-request comment identifies the operation in
   `$currentOp`, and `killOp` stops it. Context cancellation alone is not
   cancel; it closes the client connection, returns a transport error and
   leaves the server operation running (measured). The server reports a killed
   operation as code 11601 (`Interrupted`).
7. Read-only is a client-side guard plus a least-privilege database user.
   MongoDB has no session-level `default_transaction_read_only`; the guard is
   not a security boundary and the UI must say so.
8. Results are BSON documents flattened into a grid: the union of top-level
   keys per batch, `_id` first, nested objects as dotted paths to a configured
   depth, arrays and deeper objects as relaxed EJSON, and a full EJSON document
   in the OBJECT tab. Missing field, BSON null and empty string are three
   distinct cell states.
9. Catalog comes from `listDatabases`, `listCollections`, `listIndexes`,
   `usersInfo`, `rolesInfo`, `buildInfo` and `hello`. Collection fields are an
   observation: sample documents plus the JSON Schema validator plus index
   keys, never a schema. There are no schemas, tablespaces, sequences,
   user-defined types or collection owners.
10. The product is renamed to `mongo-tui`: module, binary and XDG config/state
    paths. Old `psql-tui` files are read as a fallback and migrated with a
    backup; old PostgreSQL profiles are converted or flagged for review, never
    silently dropped. PostgreSQL code is removed only after parity.

## Evidence

Spike on 2026-11-14, MongoDB 8.0.38 in a single-node replica set (`rs0`),
driver v2.10.0, `mongo:8.0` image. Full report in `docs/spike-mongo.md`.

- Transactions: `before=4`, aborted transaction `after=4`, committed
  transaction `after=5`.
- Context cancellation: transport error after 300 ms and the server operation
  was still running. `killOp` stopped it with code 11601 in about 2 ms after
  the kill request; follow-up queries worked after both paths.
- A mixed fixture flattened into 13 columns: ObjectID and string `_id`,
  int32/int64/double `qty`, Decimal128 `price`, date, array, object, null and
  missing fields.
- Sampling 50 documents out of 100 000 took 16–30 ms; sampling a 4-document
  collection took 0.4–0.5 ms.
- A 5 MiB string produced 5 242 906 bytes of relaxed EJSON; the server's BSON
  limit is 16 777 216 bytes.
- Validator, indexes and collection options are readable through
  `listCollections` and `listIndexes`.

## Consequences

- SQL, SQL highlighting and SQL completion disappear. The editor edits JSON
  and a restricted MQL subset; joins exist only as `$lookup`.
- `SET ROLE`, `search_path`, `default_tablespace`, schemas, tablespaces,
  sequences, user-defined types and owner colouring have no MongoDB analog and
  are removed from the UI.
- `killOp` needs the `killop` privilege. Without it the UI must say that
  cancellation is unavailable; closing the connection is not equivalent,
  because the server operation may continue.
- Transactions require a replica set; a standalone server is a supported but
  limited mode.
- Field cards are samples, not schemas, and must show the sample size. Results
  are cached per collection with a TTL.
- Large documents need cell truncation and a document view; the 16 MiB BSON
  limit is part of the result-limit design.
- Authentication mechanisms, TLS and `mongodb+srv://` were not measured in
  this spike and stay on the next stage's checklist.

## Alternatives considered

- **Keep a `mongosh` child.** Rejected: no framing, no CSV, line-oriented JS
  REPL; one-shot evaluation loses transactions and cursors.
- **Atlas SQL / BI Connector.** An external service, not part of a self-hosted
  MongoDB deployment; out of scope.
- **Multi-engine with PostgreSQL and MongoDB side by side.** Rejected by the
  owner. PostgreSQL remains temporarily as a reference implementation only,
  not as a shipped second engine.
- **Translate SQL to MQL locally.** Rejected: an incomplete SQL dialect would
  be worse than a clear MongoDB-native editor.

## Postscript (2026-11-14, M5–M6)

The product is renamed to `mongo-tui`: the module, `cmd/mongo-tui`, the binary
and the XDG paths now use the new name. On first start the old `psql-tui`
config and state are copied to the new locations; new files are never
overwritten, old files stay in place, and a `.psql-tui.bak` copy is written
next to each migrated file. Old PostgreSQL profiles are kept as legacy data
but are no longer connectable: the PostgreSQL adapter and its packages were
removed in M6 after the parity audit. The remaining auth/TLS/SRV hardening is
tracked in `docs/mongo-parity.md` as post-M6 work.
