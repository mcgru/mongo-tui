package config

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"mongo-tui/internal/mongouri"
)

// mongoURIRe finds MongoDB URIs in shell history, .mongorc.js and similar
// files. Quotes, whitespace, backticks and # (a shell comment) end a URI;
// angle brackets are allowed so placeholder userinfo can be stripped later.
var mongoURIRe = regexp.MustCompile(`mongodb(?:\+srv)?://[^\s"'` + "`" + `#]+`)

// maxSourceFileBytes keeps a runaway history file from being read into memory
// when re-reading a password for a connection.
const maxSourceFileBytes = 4 << 20

// MongoFiles lists regular files matching ~/.mongo* (mongosh history,
// .mongorc.js, old mongo shell history). Directories such as ~/.mongodb are
// skipped. Newest files come first, then by name.
func MongoFiles(home string) ([]string, error) {
	matches, err := filepath.Glob(filepath.Join(home, ".mongo*"))
	if err != nil {
		return nil, fmt.Errorf("cannot list %s: %w", filepath.Join(home, ".mongo*"), err)
	}
	files := make([]string, 0, len(matches))
	for _, path := range matches {
		info, err := os.Stat(path)
		if err != nil || !info.Mode().IsRegular() {
			continue
		}
		files = append(files, path)
	}
	sort.SliceStable(files, func(i, j int) bool {
		a, errA := os.Stat(files[i])
		b, errB := os.Stat(files[j])
		if errA == nil && errB == nil && !a.ModTime().Equal(b.ModTime()) {
			return a.ModTime().After(b.ModTime())
		}
		return filepath.Base(files[i]) < filepath.Base(files[j])
	})
	return files, nil
}

// ParseMongoURIs extracts unique MongoDB URIs from a file's contents. Full-line
// # comments are ignored; commented-out example connections do not become
// profiles.
func ParseMongoURIs(data []byte) []string {
	out := make([]string, 0)
	seen := map[string]bool{}
	for _, line := range strings.Split(string(data), "\n") {
		if strings.HasPrefix(strings.TrimSpace(line), "#") {
			continue
		}
		for _, raw := range mongoURIRe.FindAllString(line, -1) {
			raw = strings.TrimRight(raw, ".,;)]}>")
			if raw == "" || seen[raw] {
				continue
			}
			seen[raw] = true
			out = append(out, raw)
		}
	}
	return out
}

// NormalizeMongoURI validates a MongoDB URI and removes the password from the
// userinfo. A password must never reach config, state or logs.
func NormalizeMongoURI(raw string) (string, bool) {
	u, err := mongouri.Parse(raw)
	if err != nil {
		return "", false
	}
	if strings.ContainsAny(u.User, `<>"'`) {
		u.User = ""
	}
	return u.WithoutPassword(), true
}

// MongoLabel is the short profile name derived from a URI.
func MongoLabel(uri string) string {
	u, err := mongouri.Parse(uri)
	if err != nil {
		return "mongo"
	}
	return u.Label()
}

// HasMongoURI reports whether a MongoDB profile with this exact URI exists.
func (s *State) HasMongoURI(uri string) bool {
	for _, profile := range s.Profiles {
		if profile.Engine == "mongo" && profile.URI == uri {
			return true
		}
	}
	return false
}

// PasswordFromFile re-reads a ~/.mongo* file and returns the password of the
// URI that normalizes to profileURI. It is the .pgpass-like companion of the
// import: the profile stores only the file path, and the password is read from
// the file when the connection is opened. A missing file, an unreadable or
// changed URI and an empty password all report "not found", which lets the
// caller fall back to the password prompt.
func PasswordFromFile(path, profileURI string) (string, bool) {
	info, err := os.Stat(path)
	if err != nil {
		return "", false
	}
	if info.Size() > maxSourceFileBytes {
		return "", false
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return "", false
	}
	for _, raw := range ParseMongoURIs(data) {
		u, err := mongouri.Parse(raw)
		if err != nil || !u.HasPassword || u.Password == "" {
			continue
		}
		if u.WithoutPassword() == profileURI {
			return u.Password, true
		}
	}
	return "", false
}
