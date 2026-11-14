#!/usr/bin/env python3
"""Stage 1 protocol probe for psql-tui.

Answers exactly one question: can a long-lived `psql -X -q --csv` child give
reliable request boundaries, and what does it really put on stdout/stderr?

The probe is a throwaway experiment; its findings are recorded in
docs/spike-psql-protocol.md. It is not part of the product.

Usage:
    python3 docs/spike/probe.py --conn postgresql://postgres:spikepass@127.0.0.1:55432/appdb
    python3 docs/spike/probe.py --tag psql18 --psql /tmp/psql-tui/psql18.sh \
        --conn "dbname=appdb user=postgres" --skip-signal
"""

from __future__ import annotations

import argparse
import csv
import io
import json
import os
import queue
import secrets
import signal
import subprocess
import sys
import threading
import time

MARKER_PREFIX = "__PGC_"
NULL_SENTINEL = "\u2400NULL\u2400"  # ␀NULL␀, used to test -P null=


# --- psql child -------------------------------------------------------------


class Eof:
    def __init__(self, detail: str) -> None:
        self.detail = detail


class SessionDead(RuntimeError):
    """The psql child is gone; the request cannot be delivered."""


class Session:
    """One long-lived psql child, driven exactly like the product will drive it."""

    def __init__(self, psql: str, conn: str, env: dict | None = None) -> None:
        self.psql = psql
        self.err: list[str] = []
        self._err_lock = threading.Lock()
        self.proc = subprocess.Popen(
            [psql, "-X", "-q", "--csv", "-d", conn],
            stdin=subprocess.PIPE,
            stdout=subprocess.PIPE,
            stderr=subprocess.PIPE,
            env=env,
            bufsize=0,
        )
        self._queue: queue.Queue = queue.Queue()
        threading.Thread(target=self._read, daemon=True).start()
        threading.Thread(target=self._drain_err, daemon=True).start()

    # -- plumbing ----------------------------------------------------------

    def _read(self) -> None:
        stream = io.TextIOWrapper(
            io.BufferedReader(self.proc.stdout), encoding="utf-8", errors="replace", newline=""
        )
        reader = csv.reader(stream)
        try:
            for record in reader:
                self._queue.put(record)
        except Exception as exc:  # noqa: BLE001 - probe, report anything
            self._queue.put(Eof(f"reader error: {exc!r}"))
            return
        self._queue.put(Eof("stdout closed"))

    def _drain_err(self) -> None:
        stream = io.BufferedReader(self.proc.stderr)
        for line in stream:
            with self._err_lock:
                self.err.append(line.decode("utf-8", "replace").rstrip("\n"))

    def send_raw(self, text: str) -> None:
        try:
            self.proc.stdin.write(text.encode())
            self.proc.stdin.flush()
        except (BrokenPipeError, ValueError) as exc:
            raise SessionDead(f"psql is gone: {exc}") from exc

    def interrupt(self) -> None:
        try:
            self.proc.send_signal(signal.SIGINT)
        except ProcessLookupError:
            pass

    def take_stderr(self) -> list[str]:
        with self._err_lock:
            lines, self.err = list(self.err), []
        return lines

    # -- requests ----------------------------------------------------------

    def execute(self, sql: str, timeout: float = 15.0, cancel_after: float | None = None):
        rid = secrets.token_hex(4)
        payload = sql.rstrip()
        if payload and not payload.endswith(";"):
            payload += ";"
        self.take_stderr()
        self.send_raw(
            payload
            + f"\n\\echo {MARKER_PREFIX}{rid}__|:ERROR|:SQLSTATE|:ROW_COUNT\n"
        )
        timer = None
        if cancel_after is not None:
            timer = threading.Timer(cancel_after, self.interrupt)
            timer.start()
        try:
            records, marker = self.collect(rid, timeout)
        finally:
            if timer:
                timer.cancel()
        return records, marker, self.take_stderr()

    def collect(self, rid: str, timeout: float):
        records: list[list[str]] = []
        deadline = time.monotonic() + timeout
        while True:
            left = deadline - time.monotonic()
            if left <= 0:
                return records, {"status": "timeout"}
            try:
                item = self._queue.get(timeout=left)
            except queue.Empty:
                return records, {"status": "timeout"}
            if isinstance(item, Eof):
                return records, {"status": "eof", "detail": item.detail}
            if len(item) == 1 and item[0].startswith(f"{MARKER_PREFIX}{rid}__|"):
                parts = item[0].split("|")
                return records, {
                    "status": "marker",
                    "error": parts[1] if len(parts) > 1 else "",
                    "sqlstate": parts[2] if len(parts) > 2 else "",
                    "row_count": parts[3] if len(parts) > 3 else "",
                }
            records.append(item)

    def close(self) -> int | None:
        try:
            self.proc.stdin.close()
        except Exception:  # noqa: BLE001
            pass
        try:
            return self.proc.wait(timeout=5)
        except subprocess.TimeoutExpired:
            self.proc.kill()
            return self.proc.wait()


# --- reporting --------------------------------------------------------------


def add(report: dict, case: str, records, marker, stderr, checks, note="") -> None:
    report["cases"].append(
        {
            "case": case,
            "records": records[:8],
            "record_count": len(records),
            "marker": marker,
            "stderr": stderr[:8],
            "checks": [{"what": what, "ok": bool(ok)} for what, ok in checks],
            "note": note,
        }
    )


def show(value) -> str:
    if value is None:
        return "<nil>"
    text = repr(value)
    return text if len(text) <= 70 else text[:67] + "..."


# --- cases ------------------------------------------------------------------

EDGE_CASES = {
    "null": None,
    "empty": "",
    "spaces": "   ",
    "comma": "a,b",
    "quote": 'say "hi"',
    "newline": "line1\nline2",
    "crlf": "line1\r\nline2",
    "tab": "a\tb",
    "unicode": "кириллица 🚀 ünïcode",
    "backslash": "C:\\temp\\x",
    "pipe": "a|b",
    "semicolon": "a;b",
    "marker-like": "__PGC_END_00000000__",
    "long": "x" * 5000,
}


def case_baseline(report, s) -> None:
    records, marker, stderr = s.execute(
        "SELECT 1 AS one, NULL AS n, '' AS empty, 'plain' AS t"
    )
    header_ok = bool(records) and records[0] == ["one", "n", "empty", "t"]
    row = records[1] if len(records) > 1 else []
    # A generic CSV parser cannot tell NULL from '' once quoting is stripped.
    distinguishable = len(row) == 4 and row[1] != row[2]
    add(
        report,
        "baseline / NULL vs empty string",
        records,
        marker,
        stderr,
        [
            ("header row is present and first", header_ok),
            ("NULL and empty string stay distinguishable after CSV parsing", distinguishable),
            ("marker is the last record, error=false", marker.get("error") == "false"),
        ],
        note="row[1] (NULL) and row[2] (empty) are both empty strings after csv.reader",
    )


def case_null_sentinel(report, psql, conn, env) -> None:
    s = Session(psql, conn, env)
    try:
        s.send_raw("\\pset null " + NULL_SENTINEL + "\n")
        records, marker, stderr = s.execute("SELECT NULL AS n, '' AS empty")
        row = records[1] if len(records) > 1 else []
        add(
            report,
            "NULL sentinel via -P null=",
            records,
            marker,
            stderr,
            [
                ("sentinel appears instead of NULL", len(row) > 0 and row[0] == NULL_SENTINEL),
                ("empty string is not the sentinel", len(row) > 1 and row[1] == ""),
            ],
            note="in-session \\pset printed a confirmation line into stdout before the result",
        )
    finally:
        s.close()


def case_torture(report, s) -> None:
    records, marker, stderr = s.execute(
        "SELECT label, v FROM app.edge_cases ORDER BY id"
    )
    seen = {}
    mismatches = []
    for row in records[1:]:
        if len(row) != 2:
            mismatches.append(("field count", row))
            continue
        label, value = row
        seen[label] = value
        want = EDGE_CASES.get(label)
        if want is None:
            continue
        if value != want:
            mismatches.append((label, value))
    add(
        report,
        "CSV torture: NULL, empty, comma, quote, LF, CRLF, tab, unicode, backslash, pipe, 5k text",
        records,
        marker,
        stderr,
        [
            (f"{label} round-trips", False) for label, _ in mismatches
        ]
        or [("every non-NULL value round-trips exactly", True)],
        note=f"values recovered: {len(seen)}/14; mismatches: {mismatches[:4]}",
    )


def case_types(report, s) -> None:
    records, marker, stderr = s.execute(
        "SELECT '\\xdeadbeef'::bytea AS b,"
        " 123456789012345678901234567890.123456789::numeric(40,9) AS n,"
        " '2026-11-13 12:00:00+00'::timestamptz AS ts"
    )
    row = records[1] if len(records) > 1 else []
    add(
        report,
        "types: bytea, numeric precision, timestamptz",
        records,
        marker,
        stderr,
        [
            ("bytea is hex text", bool(row) and row[0] == "\\xdeadbeef"),
            (
                "numeric keeps all digits",
                len(row) > 1 and row[1] == "123456789012345678901234567890.123456789",
            ),
        ],
    )


def case_ddl(report, s) -> None:
    records, marker, stderr = s.execute("CREATE TEMP TABLE spike_t (id int)")
    add(
        report,
        "DDL: no result rows, no command tag",
        records,
        marker,
        stderr,
        [
            ("no records at all (command tag suppressed by -q)", records == []),
            ("marker present with error=false", marker.get("error") == "false"),
            ("row_count reported", marker.get("row_count") == "0"),
        ],
    )


def case_insert(report, s) -> None:
    records, marker, stderr = s.execute("INSERT INTO spike_t VALUES (1), (2), (3)")
    add(
        report,
        "INSERT: row count comes from :ROW_COUNT",
        records,
        marker,
        stderr,
        [
            ("no records (tag suppressed)", records == []),
            ("row_count == 3", marker.get("row_count") == "3"),
        ],
    )


def case_select_after(report, s) -> None:
    records, marker, stderr = s.execute("SELECT id FROM spike_t ORDER BY id")
    add(
        report,
        "SELECT after DDL/INSERT: session survives",
        records,
        marker,
        stderr,
        [
            ("header + 3 rows", records[:1] == [["id"]] and len(records) == 4),
            ("row_count == 3 for SELECT", marker.get("row_count") == "3"),
        ],
    )


def case_sql_error(report, s) -> None:
    records, marker, stderr = s.execute("SELECT FROM;")
    add(
        report,
        "SQL syntax error: keeps session alive",
        records,
        marker,
        stderr,
        [
            ("no result records", records == []),
            ("error=true", marker.get("error") == "true"),
            ("sqlstate=42601", marker.get("sqlstate") == "42601"),
        ],
    )
    records, marker, stderr = s.execute("SELECT 42 AS answer")
    add(
        report,
        "query after an error",
        records,
        marker,
        stderr,
        [("still works", records[:1] == [["answer"]] and len(records) == 2)],
    )


def case_empty_result(report, s) -> None:
    records, marker, stderr = s.execute("SELECT id FROM spike_t WHERE false")
    add(
        report,
        "empty result set: header only",
        records,
        marker,
        stderr,
        [("one record (header), zero rows", records == [["id"]])],
    )


def case_notice(report, s) -> None:
    records, marker, stderr = s.execute(
        "DO $$ BEGIN RAISE NOTICE 'hello from notice'; RAISE WARNING 'hello from warning'; END $$"
    )
    joined = "\n".join(stderr)
    add(
        report,
        "NOTICE and WARNING go to stderr, not into the result stream",
        records,
        marker,
        stderr,
        [
            ("no records on stdout", records == []),
            ("NOTICE on stderr", "hello from notice" in joined),
            ("WARNING on stderr", "hello from warning" in joined),
        ],
    )


def case_multi_statement(report, s) -> None:
    records, marker, stderr = s.execute("SELECT 1 AS a; SELECT 2 AS b;")
    add(
        report,
        "two statements in one write: two result sets, one marker",
        records,
        marker,
        stderr,
        [
            ("both headers visible in one stream", len(records) == 4),
            ("single marker for both", marker.get("status") == "marker"),
        ],
        note="row_count reflects the last statement only",
    )


def case_trailing_semicolon(report, s) -> None:
    records, marker, stderr = s.execute("SELECT 3 AS three;")
    add(
        report,
        "statement already ends with ';' (we append another one)",
        records,
        marker,
        stderr,
        [("empty statement produces no extra record", records == [["three"], ["3"]])],
    )


def case_fetch_count(report, s) -> None:
    s.execute("SET FETCH_COUNT = 2")
    records, marker, stderr = s.execute(
        "SELECT n FROM app.numbers ORDER BY n LIMIT 5"
    )
    headers = [i for i, row in enumerate(records) if row == ["n"]]
    add(
        report,
        "FETCH_COUNT batching",
        records,
        marker,
        stderr,
        [
            ("5 rows delivered", marker.get("row_count") == "5"),
            ("header is NOT repeated per batch", headers == [0]),
        ],
        note=f"header at record indexes: {headers}",
    )


def case_incomplete(report, s) -> None:
    records, marker, stderr = s.execute("SELECT (1", timeout=3)
    add(
        report,
        "incomplete statement: marker arrives with no result",
        records,
        marker,
        stderr,
        [
            ("marker arrives immediately", marker.get("status") == "marker"),
            ("no records before the marker", records == []),
        ],
        note="proves the framing hole: the query buffer stays pending",
    )
    records, marker, stderr = s.execute("SELECT 100 AS hundred", timeout=3)
    add(
        report,
        "next request after an incomplete one",
        records,
        marker,
        stderr,
        [
            ("result leaked from the previous request", any("100" in cell for row in records for cell in row)),
        ],
        note="the pending buffer merges with the next statement",
    )
    s.send_raw("\\r\n")
    records, marker, stderr = s.execute("SELECT 7 AS seven")
    add(
        report,
        "\\r resets the query buffer",
        records,
        marker,
        stderr,
        [("clean single result after reset", records == [["seven"], ["7"]])],
    )


def case_gdesc(report, s) -> None:
    rid = secrets.token_hex(4)
    s.take_stderr()
    s.send_raw(
        "SELECT 1 AS a, now() AS b\n\\gdesc\n\\r\n"
        f"\\echo {MARKER_PREFIX}{rid}__|:ERROR|:SQLSTATE|:ROW_COUNT\n"
    )
    records, marker = s.collect(rid, 5)
    add(
        report,
        "\\gdesc for column types before execution",
        records,
        marker,
        s.take_stderr(),
        [
            ("\\gdesc returns a two-column result", bool(records) and records[0] == ["Column", "Type"]),
            ("buffer was reset afterwards", marker.get("status") == "marker"),
        ],
        note="\\gdesc describes the current query buffer without executing it",
    )


def case_copy_to(report, s) -> None:
    records, marker, stderr = s.execute(
        "COPY (SELECT 1 AS a, 'x,y' AS b) TO STDOUT WITH (FORMAT csv)"
    )
    add(
        report,
        "COPY TO STDOUT: raw data on stdout",
        records,
        marker,
        stderr,
        [
            ("COPY output is indistinguishable from result records", len(records) >= 2),
            ("marker still terminates the stream", marker.get("status") == "marker"),
        ],
        note="raw COPY data must be rejected in the editor or handled by a separate transport",
    )


def case_copy_from(report, psql, conn, env) -> None:
    s = Session(psql, conn, env)
    try:
        rid = secrets.token_hex(4)
        s.take_stderr()
        s.send_raw("COPY app.edge_cases FROM STDIN;\n")
        s.send_raw(f"\\echo {MARKER_PREFIX}{rid}__|:ERROR|:SQLSTATE|:ROW_COUNT\n")
        records, marker = s.collect(rid, 3)
        hung = marker.get("status") == "timeout"
        s.send_raw("\\.\n")
        after, marker_after = s.collect(rid, 5) if hung else (records, marker)
        add(
            report,
            "COPY FROM STDIN without data: hangs",
            records,
            marker,
            s.take_stderr(),
            [
                ("psql waits for COPY data and never reaches the marker", hung),
                ("session recovers after '\\.'", marker_after.get("status") == "marker"),
            ],
            note="a user typing COPY ... FROM STDIN would freeze the UI",
        )
    finally:
        s.close()


def case_cancel(report, psql, conn, env) -> None:
    s = Session(psql, conn, env)
    try:
        started = time.monotonic()
        records, marker, stderr = s.execute("SELECT pg_sleep(5)", timeout=10, cancel_after=1.0)
        elapsed = time.monotonic() - started
        add(
            report,
            "Ctrl+C (SIGINT) cancels the running query",
            records,
            marker,
            stderr,
            [
                ("marker arrived (session alive)", marker.get("status") == "marker"),
                ("error=true", marker.get("error") == "true"),
                ("sqlstate=57014 (query_canceled)", marker.get("sqlstate") == "57014"),
                ("cancelled well before 5s", elapsed < 4),
            ],
            note=f"elapsed {elapsed:.2f}s",
        )
        alive = s.proc.poll() is None
        add(
            report,
            "psql process after SIGINT",
            [],
            {"status": "alive" if alive else "dead", "exit": s.proc.poll()},
            s.take_stderr(),
            [("psql survived SIGINT", alive)],
        )
        if alive:
            records, marker, stderr = s.execute("SELECT 5 AS five")
            add(
                report,
                "query after cancel",
                records,
                marker,
                stderr,
                [("session still usable", records == [["five"], ["5"]])],
            )
    finally:
        s.close()


def case_terminate(report, psql, conn, env) -> None:
    s = Session(psql, conn, env)
    records, marker, stderr = s.execute("SELECT pg_terminate_backend(pg_backend_pid())")
    code = s.close()
    add(
        report,
        "server terminates the connection",
        records,
        marker,
        stderr,
        [
            ("no marker, stdout ends", marker.get("status") in {"eof", "timeout"}),
            ("psql exits", code is not None),
        ],
        note=f"psql exit code {code}",
    )


def as_user(conn: str, user: str) -> str:
    """Return the same connection string with a different user."""
    if conn.startswith(("postgres://", "postgresql://")):
        scheme, rest = conn.split("://", 1)
        if "@" in rest:
            _, host = rest.split("@", 1)
            return f"{scheme}://{user}@{host}"
        return f"{scheme}://{user}@{rest}"
    parts = conn.split()
    if any(p.startswith("user=") for p in parts):
        return " ".join(f"user={user}" if p.startswith("user=") else p for p in parts)
    return " ".join([f"user={user}", *parts])


def case_permission(report, psql, conn, env) -> None:
    lp_env = dict(env or os.environ)
    lp_env.pop("PGPASSFILE", None)
    lp_env["PGPASSWORD"] = "lowprivpass"
    s = Session(psql, as_user(conn, "lowpriv"), lp_env)
    try:
        records, marker, stderr = s.execute("SELECT * FROM private.secret")
        add(
            report,
            "permission denied is a normal result, not a crash",
            records,
            marker,
            stderr,
            [
                ("error=true", marker.get("error") == "true"),
                ("sqlstate=42501", marker.get("sqlstate") == "42501"),
            ],
        )
        records, marker, stderr = s.execute("SELECT count(*) FROM app.orders")
        add(
            report,
            "allowed object still readable for the same role",
            records,
            marker,
            stderr,
            [("count returns 3", records[:1] == [["count"]] and records[1:] == [["3"]])],
        )
    finally:
        s.close()


# --- password handling ------------------------------------------------------


def run_oneshot(psql, argv, env, timeout=15):
    return subprocess.run(
        ["setsid", psql, *argv],
        stdin=subprocess.DEVNULL,
        capture_output=True,
        text=True,
        env=env,
        timeout=timeout,
    )


def case_passwords(report, psql, base_conn) -> None:
    workdir = "/tmp/psql-tui"
    os.makedirs(workdir, exist_ok=True)

    clean = {k: v for k, v in os.environ.items() if not k.startswith("PG")}
    clean["PGCONNECT_TIMEOUT"] = "5"
    conn_argv = ["-X", "-q", "--csv", "-h", "127.0.0.1", "-p", "55432", "-d", "appdb", "-U", "postgres"]

    # No password source, no tty: must fail loudly instead of prompting.
    res = run_oneshot(psql, conn_argv + ["-c", "SELECT 1"], clean)
    add(
        report,
        "no passfile, no tty: fails instead of prompting",
        [],
        {"status": "oneshot", "exit": res.returncode},
        (res.stderr or "").splitlines(),
        [
            ("non-zero exit", res.returncode != 0),
            ("no password supplied", "no password supplied" in res.stderr),
        ],
        note="with a real /dev/tty psql would prompt over the TUI",
    )

    bad = os.path.join(workdir, "bad.pgpass")
    with open(bad, "w", encoding="utf-8") as fh:
        fh.write("127.0.0.1:55432:appdb:postgres:spikepass\n")
    os.chmod(bad, 0o644)
    env = dict(clean, PGPASSFILE=bad)
    res = run_oneshot(psql, conn_argv + ["-c", "SELECT 1"], env)
    add(
        report,
        "PGPASSFILE with mode 0644",
        [],
        {"status": "oneshot", "exit": res.returncode},
        (res.stderr or "").splitlines(),
        [
            ("file is silently ignored", res.returncode != 0),
            ("no warning about permissions", "permission" not in res.stderr.lower()),
        ],
        note="libpq ignores a world-readable passfile without complaining",
    )

    good = os.path.join(workdir, "good.pgpass")
    with open(good, "w", encoding="utf-8") as fh:
        fh.write("127.0.0.1:55432:appdb:postgres:spikepass\n")
    os.chmod(good, 0o600)
    env = dict(clean, PGPASSFILE=good)
    res = run_oneshot(psql, conn_argv + ["-c", "SELECT 1"], env)
    add(
        report,
        "PGPASSFILE with mode 0600",
        [],
        {"status": "oneshot", "exit": res.returncode},
        (res.stderr or "").splitlines(),
        [
            ("connects", res.returncode == 0),
            ("-q keeps the tag out of stdout", res.stdout.strip() in {"1", ""}),
        ],
        note=f"stdout={res.stdout.strip()!r}",
    )


# --- main -------------------------------------------------------------------


def main() -> int:
    ap = argparse.ArgumentParser()
    ap.add_argument("--psql", default=os.environ.get("PSQL", "psql"))
    ap.add_argument("--conn", default="postgresql://postgres:spikepass@127.0.0.1:55432/appdb")
    ap.add_argument("--tag", default="host")
    ap.add_argument("--skip-signal", action="store_true")
    ap.add_argument("--skip-password", action="store_true")
    ap.add_argument("--out", default="/tmp/psql-tui")
    args = ap.parse_args()

    os.makedirs(args.out, exist_ok=True)
    report: dict = {
        "tag": args.tag,
        "psql": args.psql,
        "conn": args.conn,
        "psql_version": subprocess.run(
            [args.psql, "--version"], capture_output=True, text=True
        ).stdout.strip(),
        "cases": [],
    }
    env = dict(os.environ)
    env.pop("PGPASSWORD", None)

    def guarded(name, fn, *fargs) -> bool:
        try:
            fn(*fargs)
            return True
        except SessionDead as exc:
            add(
                report,
                f"{name}: session died",
                [],
                {"status": "dead", "detail": str(exc)},
                [],
                [("psql survived the request", False)],
            )
            return False
        except Exception as exc:  # noqa: BLE001 - probe, report anything
            add(
                report,
                f"{name}: harness error",
                [],
                {"status": "exception", "detail": repr(exc)},
                [],
                [("case completed", False)],
            )
            return False

    s = Session(args.psql, args.conn, env)
    try:
        for fn in (
            case_baseline,
            case_torture,
            case_types,
            case_ddl,
            case_insert,
            case_select_after,
            case_sql_error,
            case_empty_result,
            case_notice,
            case_multi_statement,
            case_trailing_semicolon,
            case_fetch_count,
            case_copy_to,
            case_gdesc,
            case_incomplete,
        ):
            if not guarded(fn.__name__, fn, report, s):
                break
    finally:
        s.close()

    guarded("null sentinel", case_null_sentinel, report, args.psql, args.conn, env)
    guarded("copy from stdin", case_copy_from, report, args.psql, args.conn, env)
    guarded("permissions", case_permission, report, args.psql, args.conn, env)
    if not args.skip_password:
        guarded("passwords", case_passwords, report, args.psql, args.conn)
    if not args.skip_signal:
        guarded("cancel", case_cancel, report, args.psql, args.conn, env)
        guarded("terminate", case_terminate, report, args.psql, args.conn, env)

    path = os.path.join(args.out, f"probe-{args.tag}.json")
    with open(path, "w", encoding="utf-8") as fh:
        json.dump(report, fh, ensure_ascii=False, indent=2)

    print(f"{report['psql_version']}  conn={args.conn}")
    failed = 0
    for item in report["cases"]:
        bad = [c["what"] for c in item["checks"] if not c["ok"]]
        failed += len(bad)
        flag = "ok  " if not bad else "FAIL"
        print(f"[{flag}] {item['case']}: {item['record_count']} records, marker={item['marker']}")
        if item["note"]:
            print(f"        note: {item['note']}")
        for what in bad:
            print(f"        ! {what}")
    print(f"\nreport: {path}")
    print(f"failed checks: {failed}")
    return 0


if __name__ == "__main__":
    sys.exit(main())
