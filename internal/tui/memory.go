package tui

import (
	"runtime/debug"

	tea "github.com/charmbracelet/bubbletea"

	"mongo-tui/internal/engine"
)

// releaseFreeBytes is the result size above which dropping a grid also returns
// the heap to the OS. FreeOSMemory stops the world, so it is only worth it for
// results that are actually large.
var releaseFreeBytes int64 = 8 << 20

// resultBytes estimates the heap held by a result: raw EJSON documents plus the
// rendered cells.
func resultBytes(res *engine.Result) int64 {
	if res == nil {
		return 0
	}
	var n int64
	for _, doc := range res.Documents {
		n += int64(len(doc))
	}
	for _, row := range res.Rows {
		for _, cell := range row {
			n += int64(len(cell))
		}
	}
	return n
}

func (m Model) gridBytes() int64 {
	if m.grid == nil {
		return 0
	}
	return resultBytes(m.grid.res)
}

// resultSource is the database and collection the grid came from.
func (m Model) resultSource() (database, collection string) {
	if m.grid == nil || m.grid.res == nil || m.grid.res.Collection == "" {
		return "", ""
	}
	database = m.grid.res.Database
	if database == "" {
		database = m.currentDatabase()
	}
	return database, m.grid.res.Collection
}

// releaseResult drops the current grid and its payload. Large results are
// returned to the OS right away instead of waiting for the next GC cycle.
func (m *Model) releaseResult() {
	size := m.gridBytes()
	if m.grid != nil && m.grid.res != nil {
		// Drop the payload before the grid itself so nothing keeps it alive.
		m.grid.res.Rows = nil
		m.grid.res.Documents = nil
	}
	m.grid = nil
	m.pageMore = false
	if size >= releaseFreeBytes {
		debug.FreeOSMemory()
	}
}

// replaceGrid installs a new result, returning the old heap to the OS when a
// large grid shrinks to a small one.
func (m *Model) replaceGrid(res *engine.Result) {
	old := m.gridBytes()
	m.grid = newGrid(res)
	if old >= releaseFreeBytes && resultBytes(res) < releaseFreeBytes {
		debug.FreeOSMemory()
	}
}

// closePageCmd kills the paged cursor of the grid that was just abandoned. It
// runs off the UI thread: a page fetch may still hold the session lock, and
// ClosePageOf also ignores a cursor that already belongs to another collection.
func (m Model) closePageCmd(database, collection string) tea.Cmd {
	pager, ok := m.sess.(engine.Pager)
	if !ok {
		return nil
	}
	return func() tea.Msg {
		pager.ClosePageOf(database, collection)
		return nil
	}
}
