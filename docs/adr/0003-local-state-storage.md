# 0003 — Local state: TOML config plus a versioned JSON file

Status: accepted, 2026-11-13

## Context

The tool needs settings (keybindings, panel sizes, psql path, result limits),
connection profiles without secrets, connection history, and later a SQL history and
a metadata cache.

The specification proposes SQLite for this. SQLite is the right destination, but it
adds a database dependency to a stage where the schema is still moving.

## Decision

- Config: TOML at `$XDG_CONFIG_HOME/psql-tui/config.toml`.
- State: JSON at `$XDG_STATE_HOME/psql-tui/state.json`, with an explicit
  `"version": 1` field and profile records shaped as in the specification.
- No password is ever stored. Secrets live in libpq's own mechanisms
  (`~/.pgpass`, `PGPASSFILE`, service file).
- Credentials for a session come from an ephemeral `PGPASSFILE` (mode 0600, private
  temp directory, removed on exit and on signal).

## Consequences

- Stage 2 needs no storage dependency; the state file is inspectable and easy to
  reset.
- When history search or volume demands it, the same records move into SQLite behind
  the existing `Store` interface; the JSON file is migrated once and kept as a
  backup.
- Atomic writes (write to a temp file in the same directory, then rename) are required
  from the start so an interrupted run does not corrupt state.
