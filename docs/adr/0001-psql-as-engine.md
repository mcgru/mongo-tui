# 0001 — Keep psql as the connection engine, validate SQL on the client

Status: superseded by 0006 on 2026-11-14; kept for history.

## Context

The original request was explicit: the TUI should not implement the PostgreSQL
protocol, it should hold a connection through `psql`, generate commands for it and
present the results. That keeps transactions, temporary tables, `SET`, prepared
statements and server-side state working with almost no protocol code.

`psql` is a terminal client, not a structured API. Its default output is a
human-aligned table, and its stdin is a stateful query buffer, not a request/response
channel.

## Decision

1. One long-lived child per session: `psql -X -q --csv`, no pty. The adapter owns
   stdin, stdout and stderr.
2. `-q` is mandatory, not cosmetic: without it psql writes command tags (`SET`,
   `BEGIN`, `CREATE TABLE`) to stdout and the CSV stream breaks. Row counts come from
   the `:ROW_COUNT` psql variable in the end marker.
3. Each execution is framed with a unique random marker printed by `\echo`, carrying
   `:ERROR`, `:SQLSTATE` and `:ROW_COUNT`; full error text and NOTICE/WARNING are read
   from stderr in parallel.
4. Before anything is written to psql, the statement is checked for syntactic
   completeness and split with a real PostgreSQL grammar (`libpg_query`). Exactly one
   complete statement is sent per request.

## Why completeness check is not optional

The marker approach has a hole that heuristics cannot close. If the user's SQL is
incomplete (unbalanced quote or parenthesis), psql keeps a non-empty query buffer;
the `\echo` marker executes immediately, so it is printed *before* the result, and the
result arrives during the next request. Boundaries shift, and the pending tail later
merges with the following statement, corrupting the session. Validating on the client
makes framing deterministic and costs less than post-hoc repair.

## Consequences

- Transactions and session state behave exactly like `psql`.
- No wire protocol, no driver dependency in the MVP.
- psql 14+ is a hard runtime requirement; behaviour is version-sensitive and must be
  covered by the version matrix.
- `COPY ... TO STDOUT` / `COPY ... FROM STDIN` conflict with the line parser and are
  rejected in the editor with an explanation.
- Results arrive in batches, not row by row; the UI caps rows/bytes and reports
  truncation instead of promising streaming.
- Password prompts must never happen: with a piped stdin psql opens `/dev/tty` and
  reads over the TUI. Authentication goes through `PGPASSFILE` or the user's existing
  `~/.pgpass` / service file.

## Alternatives considered

- **libpq/pgx driver inside the app.** Better types, real cancel, COPY support. More
  work up front and contradicts the "hold the connection through psql" requirement.
  Kept as the fallback behind the same `SessionAdapter` interface if the stage 1
  spike fails.
- **One `psql -c` per statement.** Simpler prototype, but loses transactions, temp
  tables and session `SET`. Diagnostic fallback at most.
- **Shelling out with a command string.** Rejected: injection risk and no argv
  control.

## Amendment after the stage 1 spike (2026-11-13)

Measured against PostgreSQL 18.7 with psql 14.24 and 18.7; full results in
`docs/spike-psql-protocol.md`. Four corrections to the design above:

- `SIGINT` cancels the query but kills the non-interactive psql (exit code 3). Cancel
  goes through `pg_cancel_backend()` from a one-shot psql, see ADR 0005.
- The end marker cannot be trusted while psql holds an incomplete statement. An
  unclosed `(` makes psql accumulate input forever (following statements are never
  executed) and the marker prints `:ERROR` unsubstituted; an unclosed quote swallows
  the marker line entirely. The completeness check is therefore the only thing that
  keeps framing sane. A literal `:ERROR` inside the marker is the alarm signal and
  `\r` is the recovery.
- After CSV parsing, NULL and `''` are indistinguishable. `-P null=␀NULL␀` is now part
  of the argv; a field equal to the sentinel is NULL.
- stderr is asynchronous relative to the marker: wait for stderr quiescence (bounded)
  before reporting an error message, otherwise `:ERROR=true` can arrive with no text.
- A real grammar (`libpg_query`, via a WASM binding) was evaluated as the completeness
  gate and rejected for that job: it reports `SELECT FROM;` as "syntax error at end of
  input", so it would block a statement psql handles correctly and diagnoses better.
  The gate is lexical instead — quotes, dollar quotes, comments and parenthesis depth —
  which covers exactly the constructs that make psql buffer input. The grammar stays
  available for the stage 5 completion engine.

`COPY ... TO STDOUT` was confirmed silently shaped like a valid result set (no header,
`:ROW_COUNT=0`), and `COPY ... FROM STDIN` swallows the marker and hangs — both are
rejected in the editor.
