# mongo-tui

A terminal UI for MongoDB: object browser on the left, MQL/JSON editor, result
grid and a document view on the right, saved connection profiles and
context-aware completion.

The product was called `psql-tui` before 2026-11-14. The PostgreSQL adapter was
removed after the M6 parity audit; only its architecture records remain in the
repository.

**Status: pre-alpha, M0–M6 done.** The MongoDB workbench, catalog, tree,
collection cards, MQL parser, completion, templates, the rename/config
migration and the removal of the PostgreSQL path are in place.

## Why

`mongosh` is a capable REPL, but it is still a console: no object tree, no
context completion, pretty-printed output that has to be read rather than
browsed. `mongo-tui` uses the official Go driver, so results are typed BSON
documents, cursors stream, transactions are driver sessions, and the UI can
offer a proper grid, an object card and a document view.

`mongo-tui` does not implement the MongoDB wire protocol and does not run
`mongosh` as a child process. One driver client covers every database, so
switching the current database is a local operation and keeps an open
transaction.

## Requirements

- MongoDB 8.0 or newer; a replica set is required for multi-document
  transactions
- Go 1.25+ to build from source
- Linux; macOS and Windows are a separate decision

## Build and run

```sh
make                 # list available targets
make build           # bin/mongo-tui
make run ARGS=--help
make test
```

## Quick start

```sh
./bin/mongo-tui 'mongodb://ann@127.0.0.1:27017/app?replicaSet=rs0'
./bin/mongo-tui --profile mongo-prod
./bin/mongo-tui                      # connection picker
```

The password is asked for in the TUI and is never written to the config, the
state file or the command line. A MongoDB URI cannot be combined with
`--profile`. Read-only mode is available with `--read-only`.

On the picker, `i` imports MongoDB URIs from a `~/.mongo*` file such as
`.mongorc.js` or a mongosh history. The file picker lists the matching regular
files; passwords are stripped from the saved URI, duplicate URIs are skipped,
and the source file path is remembered on the profile. When such a profile is
opened, the password is re-read from that file `.pgpass`-style instead of
being asked for again; if the file is gone or no longer matches, the TUI falls
back to the password prompt.

## Editor

The editor accepts one request per run: a JSON envelope or a restricted MQL
command. Both compile to the same internal query spec. Examples:

```json
{"op":"find","collection":"users","filter":{"age":{"$gt":30}},"sort":{"name":1},"limit":100}
```

```js
db.users.find({age: {$gt: 30}}).sort({name: 1}).limit(100)
db.orders.aggregate([{$match: {status: "paid"}}, {$group: {_id: "$customer", n: {$sum: 1}}}])
db.users.insertOne({name: "Ann", when: ISODate("2026-11-14T12:00:00Z")})
```

Supported operations: `find`, `aggregate`, `count`, `distinct`, `insertOne`,
`insertMany`, `updateOne`, `updateMany`, `replaceOne`, `deleteOne`,
`deleteMany`, `createCollection`, `dropCollection`, `createIndex`,
`dropIndex`, `collMod`, `renameCollection`, `beginTransaction`,
`commitTransaction`, `abortTransaction`.

MQL covers the CRUD/DDL subset above, the `find` chain (`sort`, `project`,
`skip`, `limit`), JSON5-like documents, `ObjectId`, `ISODate`,
`NumberDecimal`, `NumberLong`, comments, `db["name"]` and
`db.getCollection("name")`. Transactions and views still use the JSON
envelope. MQL is deliberately not JavaScript: no variables, loops, user
functions or arbitrary code.

`update`, `replace` and `delete` require an explicit `filter` (use `{}` to
target every document). The TUI validates the request before sending it, so an
incomplete envelope or MQL command never reaches the server.

## Object browser

The tree lists databases, collections, views, indexes, users and roles.
`Enter` on a database switches the session to it; `Enter` on a collection opens
a card with sampled fields, indexes, statistics, options and the JSON Schema
validator. `F5` reloads a branch. The current database is marked in the tree
and in the header.

The collection card distinguishes fields observed in a sample from index keys
and validator properties, and shows the collection stats. Fields are a sample,
not a schema.

`Ctrl+Space` completes databases, collections, methods, fields, query/update
operators, aggregation stages and JSON values. `/` inserts a find, `p` runs a
limited find, `+` inserts createIndex/createCollection, `-` inserts drop or
dropIndex, `=` inserts collMod; `.` inserts the selected collection name as a
JSON string.

`i` on the **Users** folder imports a MongoDB user from a `~/.mongo*` file: the
username, password, database and `authSource` are read from the chosen URI. A
checklist then picks which built-in database roles to grant (`read`,
`readWrite`, `dbOwner`, `dbAdmin`, `userAdmin`, Space toggles, Enter applies,
`readWrite` pre-checked); the roles are granted on the URI database, or on the
current one when the URI has none. If the file holds several URIs, a second list
asks which one to import. The user is created or updated with the chosen roles.
The password goes straight to `createUser`/`updateUser` and is never written to
the state file.

## Keys and layout

```
┌ mongo-tui | db:app | user: ann | MongoDB 8.0 | CONNECTED ┐
├──────────────────────┬───────────────────────────────────┤
│ MONGODB              │ [RESULT] [OBJECT] [MESSAGES]      │
│ ▾ Databases          ├───────────────────────────────────┤
│   ▸ app              │ ...                               │
│ ▾ Users              │                                   │
│ ▾ Roles              │                                   │
│                      ├───────────────────────────────────┤
│                      │ MQL / JSON EDITOR                 │
└──────────────────────┴───────────────────────────────────┘
```

- `Ctrl+J` run, `Ctrl+R` history, `Ctrl+Space` completion.
- `Ctrl+F1` help; F1 is also bound, but Konsole may keep it.
- `Tab`/`Shift+Tab` focus; `]`/`[` switch RESULT/OBJECT/MESSAGES.
- `Ctrl+←/→` resize the tree; `Ctrl+↑/↓` move the result/editor divider.
- `c` copies the cell, `C` copies the row as TSV through OSC 52.
- `/`, `p`, `+`, `-`, `=` insert MongoDB templates; `.` inserts a name.
- `Ctrl+Q` quits; `Ctrl+C` cancels a running request, then quits.

Every command is rebindable in `[keys]`; the help screen shows the effective
bindings.

## Transactions, cancel, read-only

- Transactions use driver sessions and need a replica set. A standalone server
  reports that transactions are unavailable.
- Cancel uses `killOp` with a per-request comment and maps code 11601 to
  “request cancelled”. It needs the `killop` privilege; without it the TUI says
  that cancellation is unavailable. Cancelling the Go context alone closes the
  connection and leaves the server operation running, so it is not used as the
  user-facing cancel.
- Read-only mode rejects write envelopes client-side. It is not a database
  security boundary; use a user with a read-only role.

## Configuration and migration

Config lives in `$XDG_CONFIG_HOME/mongo-tui/config.toml` (usually
`~/.config/mongo-tui/config.toml`). Profiles and connection history live in
`$XDG_STATE_HOME/mongo-tui/state.json`; no password is ever written there.

On the first start after the rename, the old `psql-tui` config and state are
copied into the new locations. New files are never overwritten, the old files
are left untouched, and a `.psql-tui.bak` copy is written next to each
migrated file. Old PostgreSQL profiles are kept as legacy data; they are not
connectable after M6 and are never silently dropped.

```toml
read_only = false
result_limit_rows = 10000
result_limit_bytes = 33554432

mouse = false

[mongodb]
sample_size = 50

[keys]
run = "ctrl+j"
history = "ctrl+r"
select_template = "/"
```

The first run writes the file with every setting commented out. An unknown
action, an empty key or two actions on one key is a startup error. Arrows,
Home/End, PgUp/PgDn, Enter/Space and Esc are fixed context navigation.

## Tests

```sh
make test          # unit tests; live tests skip without their DSN
make test-mongo    # MongoDB 8.0 replica set, adapter and TUI live tests
make test-mongo-auth # SCRAM/TLS/authSource live tests on a standalone
make mongo-down
make auth-down
```

The MongoDB live tests create and drop uniquely named collections.

## Repository layout

```
cmd/mongo-tui/      entry point
internal/cli/       command line
internal/config/    TOML config, XDG paths, profiles, legacy migration
internal/engine/    engine-neutral session/catalog types
internal/mongo/     MongoDB driver client, query/MQL parser, catalog, TUI adapter
internal/mongouri/  MongoDB connection-string parser (multi-host, password stripping)
internal/tui/       bubbletea models: layout, tree, editor, grid, completion
deploy/mongo/       docker-compose MongoDB 8.0 replica set
docs/adr/           architecture decisions
docs/mongo-parity.md parity audit and post-M6 hardening
```

## Documentation

- `PRD.md` — requirements and accepted decisions (Russian)
- `docs/adr/0006-mongodb-as-engine.md` — engine decision
- `docs/mongo-parity.md` — parity checklist and removal gate
- `docs/spike-mongo.md` — measured MongoDB behaviour
- `docs/spike-psql-protocol.md` — the old psql spike (historical)
- `docs/dialogue-log.md` — how the requirements were agreed (Russian)

## License

Not decided yet.
