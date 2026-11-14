# 0002 — One session at a time; expanding another database switches it

Status: superseded by 0006 on 2026-11-14; kept for history.

## Context

A PostgreSQL session is bound to one database. `pg_database`, `pg_roles` and
`pg_tablespace` are readable from any database, but schemas and tables of another
database are not reachable from the current session at all.

The specification sketches a tree that nests schemas under every database, which
would require either a second connection or a metadata-only side process.

## Decision

- Cluster-level nodes (databases, roles, tablespaces, server info) are always visible
  and come from the active session.
- Expanding a non-active database is an explicit switch: confirm, close the current
  psql session, open a new one on that database.
- If an open transaction is detected, the switch warns about losing uncommitted work.
- All SQL tabs share the single session, therefore the single transaction state. The
  status line shows `RW`/`RO` and the transaction state for the whole window.

## Consequences

- No parallel sessions, no second psql, no `postgres_fdw`/`dblink` requirement.
- Cross-database exploration is a deliberate action, visible in the status line.
- `ROLLBACK` in one tab affects every other tab — this must be visible in the UI, not
  just in the docs.
- Several clusters at once, and read-only browsing of foreign databases, are later
  features.

## Alternatives considered

- **Short-lived `psql -d other` for metadata only.** Fills the tree without switching,
  but adds a second error path and a hidden second psql.
- **Parallel sessions per database.** Best UX, most state and resource management;
  explicitly out of MVP scope.
