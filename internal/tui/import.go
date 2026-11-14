package tui

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	tea "github.com/charmbracelet/bubbletea"

	"mongo-tui/internal/config"
)

// maxImportBytes keeps a runaway history file from being read into memory.
const maxImportBytes = 4 << 20

func (m Model) openImportFiles() (Model, tea.Cmd) {
	home, err := os.UserHomeDir()
	if err != nil {
		m.status = "cannot find the home directory: " + err.Error()
		return m, nil
	}
	files, err := config.MongoFiles(home)
	if err != nil {
		m.status = err.Error()
		return m, nil
	}
	if len(files) == 0 {
		m.status = "no ~/.mongo* files found"
		return m, nil
	}
	m.importFiles = files
	m.importCursor = 0
	m.status = ""
	m.scr = screenImport
	return m, nil
}

func (m Model) handleImportKey(key string) (tea.Model, tea.Cmd) {
	switch key {
	case "up", "k":
		if m.importCursor > 0 {
			m.importCursor--
		}
	case "down", "j":
		if m.importCursor < len(m.importFiles)-1 {
			m.importCursor++
		}
	case "enter":
		if len(m.importFiles) == 0 {
			return m, nil
		}
		return m.importProfilesFile(m.importFiles[m.importCursor]), nil
	case "esc", "q":
		m.importFiles = nil
		m.scr = screenPicker
		m.status = ""
	}
	return m, nil
}

func (m Model) viewImport() string {
	var b strings.Builder
	b.WriteString(titleStyle.Render("Import profiles"))
	b.WriteString("\n")
	b.WriteString(dimStyle.Render("MongoDB URIs from ~/.mongo*; passwords are stripped and never saved"))
	b.WriteString("\n\n")

	visible := maxInt(3, m.height-10)
	start := 0
	if m.importCursor >= visible {
		start = m.importCursor - visible + 1
	}
	for i, path := range m.importFiles {
		if i < start || i >= start+visible {
			continue
		}
		cursor := "  "
		name := filepath.Base(path)
		if i == m.importCursor {
			cursor = "> "
			name = titleStyle.Render(name)
		}
		b.WriteString(cursor + name)
		b.WriteString(dimStyle.Render("  " + filepath.Dir(path)))
		b.WriteString("\n")
	}
	if m.status != "" {
		b.WriteString("\n")
		b.WriteString(dimStyle.Render(m.status))
	}
	b.WriteString("\n")
	b.WriteString(dimStyle.Render("↑/↓ choose    Enter import    Esc back    Ctrl+Q quit"))
	return m.frame(b.String())
}

func (m Model) importProfilesFile(path string) Model {
	info, err := os.Stat(path)
	if err != nil {
		m.status = "cannot read " + filepath.Base(path) + ": " + err.Error()
		return m
	}
	if info.Size() > maxImportBytes {
		m.status = fmt.Sprintf("%s is too large (%d bytes, limit %d)", filepath.Base(path), info.Size(), maxImportBytes)
		return m
	}
	data, err := os.ReadFile(path)
	if err != nil {
		m.status = "cannot read " + filepath.Base(path) + ": " + err.Error()
		return m
	}

	uris := config.ParseMongoURIs(data)
	if len(uris) == 0 {
		m.status = "no MongoDB URIs found in " + filepath.Base(path)
		return m
	}

	added, skipped, invalid := 0, 0, 0
	for _, raw := range uris {
		uri, ok := config.NormalizeMongoURI(raw)
		if !ok {
			invalid++
			continue
		}
		if m.opts.State.HasMongoURI(uri) {
			skipped++
			continue
		}
		m.opts.State.Upsert(config.Profile{
			ID:         newProfileID(),
			Label:      config.MongoLabel(uri),
			Engine:     "mongo",
			URI:        uri,
			SourceFile: path,
		})
		added++
	}
	if added == 0 {
		m.status = fmt.Sprintf("nothing imported: %d duplicate(s), %d invalid", skipped, invalid)
		return m
	}

	saveErr := m.opts.State.Save(m.opts.Paths.StateFile)
	m.rebuildProfiles()
	m.importFiles = nil
	m.scr = screenPicker
	switch {
	case saveErr != nil:
		m.status = fmt.Sprintf("imported %d, but state was not saved: %v", added, saveErr)
	case skipped > 0 || invalid > 0:
		m.status = fmt.Sprintf("imported %d, skipped %d duplicate(s), %d invalid", added, skipped, invalid)
	default:
		m.status = fmt.Sprintf("imported %d profile(s)", added)
	}
	return m
}
