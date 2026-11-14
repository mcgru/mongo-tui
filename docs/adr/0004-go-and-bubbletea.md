# 0004 — Go with bubbletea

Status: accepted, 2026-11-13

## Context

The TUI needs: raw terminal input, resize handling, split panes, Unicode-aware text
layout, a scrollable table, a multi-line editor with highlighting, and a subprocess
adapter with piped I/O. Distribution matters — ideally one static binary with no
runtime.

Candidates were Rust + ratatui, Go + bubbletea, Python + Textual.

## Decision

Go 1.25 with `charmbracelet/bubbletea`, `bubbles` (viewport, textinput, table,
list, spinner) and `lipgloss` for styling.

SQL completeness and splitting will use `libpg_query` bindings. Preference is a
binding that does not need cgo at build time (WASM-based `go-pgquery`); if it proves
too slow or incomplete for statements, `pg_query_go` with a static libpg_query is
acceptable, since the cgo cost is build-time only.

## Consequences

- Single binary, fast startup, straightforward subprocess and signal handling.
- The project uses the owner's preferred language, so maintenance cost is lower.
- Table/grid widgets in `bubbles` are simpler than ratatui's; expect to write custom
  rendering for the result grid and the object tree.
- The team must handle bubbletea's model/update discipline carefully, especially
  around long-running commands (bubbletea `Cmd` returning messages, never blocking
  `Update`).
- Python + Textual would have given the richest widgets but adds an interpreter and
  packaging step; Rust + ratatui remains the fallback if the Go TUI hits a wall.
