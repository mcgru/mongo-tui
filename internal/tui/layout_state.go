package tui

import (
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"mongo-tui/internal/config"
)

// layoutSaveDelay debounces the state write while Ctrl+arrow is held.
var layoutSaveDelay = 400 * time.Millisecond

// layoutSaveMsg fires after the delay; seq drops ticks from earlier resizes.
type layoutSaveMsg struct{ seq int }

// applyLayout restores pane sizes, ignoring values outside the live ranges so a
// hand-edited state file cannot collapse the layout.
func (m *Model) applyLayout(layout *config.Layout) {
	if layout == nil {
		return
	}
	if layout.LeftPct >= minLeftPct && layout.LeftPct <= maxLeftPct {
		m.leftPct = layout.LeftPct
	}
	if layout.ResultPct >= minResultPct && layout.ResultPct <= maxResultPct {
		m.resultPct = layout.ResultPct
	}
}

// layoutForProfile prefers the profile's own sizes and falls back to the
// global default stored in state.json.
func (m Model) layoutForProfile(profileID string) *config.Layout {
	if profileID != "" {
		if p, ok := m.opts.State.Find(profileID); ok && p.Layout != nil {
			return p.Layout
		}
	}
	return m.opts.State.Layout
}

// rememberLayout stores the current sizes in the active profile, or in the
// global state when the session has no saved profile, and schedules the write.
func (m *Model) rememberLayout() tea.Cmd {
	if m.opts.CLI.NoHistory {
		return nil
	}
	layout := config.Layout{LeftPct: m.leftPct, ResultPct: m.resultPct}
	stored := false
	if m.profileID != "" {
		if p, ok := m.opts.State.Find(m.profileID); ok {
			p.Layout = &layout
			m.opts.State.Upsert(p)
			stored = true
		}
	}
	if !stored {
		m.opts.State.Layout = &layout
	}

	m.layoutSeq++
	seq := m.layoutSeq
	return tea.Tick(layoutSaveDelay, func(time.Time) tea.Msg {
		return layoutSaveMsg{seq: seq}
	})
}

// saveLayoutNow writes the pending sizes at quit time, when the debounce tick
// would not get a chance to run.
func (m Model) saveLayoutNow() {
	if m.opts.CLI.NoHistory || m.layoutSeq == 0 {
		return
	}
	_ = m.opts.State.Save(m.opts.Paths.StateFile)
}
