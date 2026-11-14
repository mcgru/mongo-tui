# 0005 — Cancel a query through a side connection, never with SIGINT

Status: superseded by 0006 on 2026-11-14; kept for history.

## Context

The specification says `Ctrl+C` should "forward the interrupt to psql". The spike
measured what that actually does: sending `SIGINT` to a non-interactive `psql` child
cancels the running query (`Cancel request sent`, SQLSTATE 57014) but then psql
**exits** with code 3. The session, its transaction and its temporary state are gone.
That is worse than no cancel at all, because the UI would look like it worked.

The cause is that psql installs its interactive cancel handler only when it reads
commands from a terminal; with a piped stdin the default signal disposition applies.

## Decision

Cancel server-side, not process-side:

1. Right after connecting, the adapter runs `SELECT pg_backend_pid()` and keeps the
   backend pid of the session.
2. Cancel is a one-shot `psql -X -q -tA -c 'SELECT pg_cancel_backend(<pid>)'` with the
   same conninfo and passfile. It is a command, not a session: no long-lived second
   connection, no state.
3. The adapter waits for the pending request's marker and reports the real outcome
   (`:SQLSTATE=57014` means cancelled, anything else means the cancel arrived too late
   or was rejected).
4. `SIGINT` is not used for cancel. It is reserved for shutdown, together with
   `SIGTERM`, and only after the session is already being torn down.

## Evidence

- `SIGINT` during `SELECT pg_sleep(5)`: query cancelled, psql exit code 3, stdout
  closed. Repeating the request fails with a broken pipe.
- `pg_cancel_backend(pid)` from a separate one-shot psql: SQLSTATE 57014, session
  alive, next `SELECT 5` works, 1.03 s end to end, on a superuser **and** on a plain
  login role (`lowpriv`).

## Consequences

- Cancelling needs no privileges beyond the user's own session: a role may cancel its
  own backends.
- The cancel path needs the pid, so `pg_backend_pid()` becomes part of session
  bootstrap (one extra round trip, ~1 ms).
- A second short-lived psql appears on cancel only. It contradicts "one process per
  session" for a moment, and that is acceptable: it carries no session state.
- If the cancel command itself fails (network, permissions), the UI says the request
  could not be cancelled and keeps waiting, instead of pretending.
- If the backend pid is stale after a reconnect, the cancel either fails or cancels
  nothing; the adapter re-reads the pid on every reconnect.

## Alternatives considered

- **SIGINT** — destroys the session (measured).
- **`statement_timeout`** — a blunt instrument that cannot be applied to an already
  running query and changes server-side semantics.
- **Kill the child and reconnect** — loses the transaction and everything else the
  "one live session" decision exists for.
- **Allocate a pty so psql thinks it is interactive** — would make SIGINT work but
  drags in psql's interactive behaviour (pager, prompts, line editing) and the screen
  competition the specification explicitly wants to avoid.
