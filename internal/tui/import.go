package tui

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	tea "github.com/charmbracelet/bubbletea"

	"mongo-tui/internal/config"
	"mongo-tui/internal/mongouri"
)

// maxImportBytes keeps a runaway history file from being read into memory.
const maxImportBytes = 4 << 20

// builtinDBRoles are the MongoDB built-in database roles offered when importing
// a user; each is granted on the target database (the URI path, or the current
// one when the URI has none).
var builtinDBRoles = []string{"read", "readWrite", "dbOwner", "dbAdmin", "userAdmin"}

// openImportFiles lists the regular ~/.mongo* files and opens the picker. In
// profile mode (user=false) Enter imports connection profiles; in user mode
// (user=true) it creates or updates a MongoDB user from the chosen URI.
func (m Model) openImportFiles(user bool) (Model, tea.Cmd) {
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
	m.importURIs = nil
	m.importCursor = 0
	m.importUser = user
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
		path := m.importFiles[m.importCursor]
		if m.importUser {
			return m.importUserFile(path)
		}
		return m.importProfilesFile(path), nil
	case "esc", "q":
		user := m.importUser
		m.importFiles = nil
		m.importURIs = nil
		m.importURI = ""
		m.importRoleSel = nil
		m.importUser = false
		m.status = ""
		if user {
			m.scr = screenMain
		} else {
			m.scr = screenPicker
		}
	}
	return m, nil
}

// handleImportURIsKey is the second level of the user import: several URIs were
// found in one file, so the user picks which one to import.
func (m Model) handleImportURIsKey(key string) (tea.Model, tea.Cmd) {
	switch key {
	case "up", "k":
		if m.importCursor > 0 {
			m.importCursor--
		}
	case "down", "j":
		if m.importCursor < len(m.importURIs)-1 {
			m.importCursor++
		}
	case "enter":
		if len(m.importURIs) == 0 {
			return m, nil
		}
		return m.openRolePicker(m.importURIs[m.importCursor])
	case "esc", "q":
		m.importURIs = nil
		m.importURI = ""
		m.importRoleSel = nil
		m.importCursor = 0
		m.status = ""
		m.scr = screenImport
	}
	return m, nil
}

func (m Model) viewImport() string {
	var b strings.Builder
	if m.importUser {
		b.WriteString(titleStyle.Render("Import user"))
		b.WriteString("\n")
		b.WriteString(dimStyle.Render("choose a ~/.mongo* file; a user is created or updated from its URI"))
	} else {
		b.WriteString(titleStyle.Render("Import profiles"))
		b.WriteString("\n")
		b.WriteString(dimStyle.Render("MongoDB URIs from ~/.mongo*; passwords are stripped and never saved"))
	}
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

func (m Model) viewImportURIs() string {
	var b strings.Builder
	b.WriteString(titleStyle.Render("Choose a URI"))
	b.WriteString("\n")
	b.WriteString(dimStyle.Render("several URIs were found; pick the user to import"))
	b.WriteString("\n\n")

	visible := maxInt(3, m.height-10)
	start := 0
	if m.importCursor >= visible {
		start = m.importCursor - visible + 1
	}
	for i, raw := range m.importURIs {
		if i < start || i >= start+visible {
			continue
		}
		cursor := "  "
		name := config.MongoLabel(raw)
		if i == m.importCursor {
			cursor = "> "
			name = titleStyle.Render(name)
		}
		b.WriteString(cursor + name)
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

func (m Model) viewImportRoles() string {
	var b strings.Builder
	b.WriteString(titleStyle.Render("Choose roles"))
	b.WriteString("\n")
	b.WriteString(dimStyle.Render("grant the checked roles on the target database"))
	b.WriteString("\n\n")

	for i, role := range builtinDBRoles {
		check := "[ ]"
		if i < len(m.importRoleSel) && m.importRoleSel[i] {
			check = "[x]"
		}
		cursor := "  "
		line := check + " " + role
		if i == m.importRoleCursor {
			cursor = "> "
			line = titleStyle.Render(line)
		}
		b.WriteString(cursor + line)
		b.WriteString("\n")
	}
	if m.status != "" {
		b.WriteString("\n")
		b.WriteString(dimStyle.Render(m.status))
	}
	b.WriteString("\n")
	b.WriteString(dimStyle.Render("↑/↓ move    Space toggle    Enter apply    Esc back    Ctrl+Q quit"))
	return m.frame(b.String())
}

// importUserFile reads a file and either imports its single credential URI or
// asks which URI to use when there is more than one.
func (m Model) importUserFile(path string) (Model, tea.Cmd) {
	info, err := os.Stat(path)
	if err != nil {
		m.status = "cannot read " + filepath.Base(path) + ": " + err.Error()
		return m, nil
	}
	if info.Size() > maxImportBytes {
		m.status = fmt.Sprintf("%s is too large (%d bytes, limit %d)", filepath.Base(path), info.Size(), maxImportBytes)
		return m, nil
	}
	data, err := os.ReadFile(path)
	if err != nil {
		m.status = "cannot read " + filepath.Base(path) + ": " + err.Error()
		return m, nil
	}

	var uris []string
	for _, raw := range config.ParseMongoURIs(data) {
		u, err := mongouri.Parse(raw)
		if err != nil || u.User == "" || u.Password == "" {
			continue
		}
		uris = append(uris, raw)
	}

	switch len(uris) {
	case 0:
		m.status = "no URI with a username and password in " + filepath.Base(path)
		return m, nil
	case 1:
		return m.openRolePicker(uris[0])
	default:
		m.importURIs = uris
		m.importCursor = 0
		m.status = ""
		m.scr = screenImportURIs
		return m, nil
	}
}

// openRolePicker holds the chosen URI and shows the role checklist. readWrite
// is pre-checked, so pressing Enter keeps the previous single-role behaviour.
func (m Model) openRolePicker(raw string) (Model, tea.Cmd) {
	m.importURI = raw
	m.importRoleSel = defaultRoleSelection()
	m.importRoleCursor = 0
	m.status = ""
	m.scr = screenImportRoles
	return m, nil
}

// defaultRoleSelection checks the readWrite row and leaves the rest unchecked.
func defaultRoleSelection() []bool {
	sel := make([]bool, len(builtinDBRoles))
	for i, role := range builtinDBRoles {
		sel[i] = role == "readWrite"
	}
	return sel
}

// selectedRoles returns the checked role names in list order.
func (m Model) selectedRoles() []string {
	out := make([]string, 0, len(builtinDBRoles))
	for i, role := range builtinDBRoles {
		if i < len(m.importRoleSel) && m.importRoleSel[i] {
			out = append(out, role)
		}
	}
	return out
}

// handleImportRolesKey drives the role checklist: Space toggles the row under
// the cursor, Enter imports with the checked roles, Esc backs out to the URI
// chooser (or the file list when there was only one URI).
func (m Model) handleImportRolesKey(key string) (tea.Model, tea.Cmd) {
	switch key {
	case "up", "k":
		if m.importRoleCursor > 0 {
			m.importRoleCursor--
		}
	case "down", "j":
		if m.importRoleCursor < len(builtinDBRoles)-1 {
			m.importRoleCursor++
		}
	case " ":
		if m.importRoleCursor < len(m.importRoleSel) {
			m.importRoleSel[m.importRoleCursor] = !m.importRoleSel[m.importRoleCursor]
		}
	case "enter":
		roles := m.selectedRoles()
		if len(roles) == 0 {
			m.status = "select at least one role"
			return m, nil
		}
		return m.createUserFromURI(m.importURI, roles)
	case "esc", "q":
		m.importURI = ""
		m.importRoleSel = nil
		m.importRoleCursor = 0
		m.status = ""
		if len(m.importURIs) > 0 {
			m.scr = screenImportURIs
		} else {
			m.scr = screenImport
		}
	}
	return m, nil
}

// createUserFromURI extracts user, password, database and authSource from one
// URI and runs UpsertUser with the chosen roles. The roles target the URI
// database, or the current one when the URI has none; the user lives in
// authSource (admin when absent).
func (m Model) createUserFromURI(raw string, roles []string) (Model, tea.Cmd) {
	u, err := mongouri.Parse(raw)
	if err != nil || u.User == "" || u.Password == "" {
		m.status = "the selected URI has no username and password"
		return m, nil
	}
	database := u.Database()
	if database == "" {
		database = m.currentDatabase()
	}
	authSource := u.AuthSource()
	if authSource == "" {
		authSource = "admin"
	}

	m.importFiles = nil
	m.importURIs = nil
	m.importURI = ""
	m.importRoleSel = nil
	m.importUser = false
	m.status = "creating user " + u.User + "…"
	m.scr = screenMain
	return m, m.upsertUserCmd(u.User, u.Password, database, authSource, roles)
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
