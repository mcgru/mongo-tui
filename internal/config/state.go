package config

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"time"
)

// StateVersion is the on-disk format version of state.json.
const StateVersion = 1

// Layout stores the pane sizes as percentages: the object tree width and the
// result pane height. It is machine-written state, not hand-edited config.
type Layout struct {
	LeftPct   int `json:"left_pct,omitempty"`
	ResultPct int `json:"result_pct,omitempty"`
}

// Profile is a saved connection without any secret in it.
type Profile struct {
	ID    string `json:"id"`
	Label string `json:"label"`
	// Engine is "postgres" or "mongo". Older profiles have it empty and mean
	// PostgreSQL, so existing state files keep working.
	Engine string `json:"engine,omitempty"`
	// URI is a MongoDB URI without a password. PostgreSQL profiles use the
	// individual fields below instead.
	URI string `json:"uri,omitempty"`
	// SourceFile is the path of the ~/.mongo* file this profile was imported
	// from. It is not a secret: the password is re-read from that file at
	// connect time, .pgpass-style, and never written to state.
	SourceFile string `json:"source_file,omitempty"`
	// Layout overrides the global pane sizes for this profile.
	Layout *Layout `json:"layout,omitempty"`
	// KeyboardLayout is "us" or "ru"; empty means the global value. The
	// terminal does not report the OS layout, so command keys are normalised
	// from this setting.
	KeyboardLayout  string     `json:"keyboard_layout,omitempty"`
	Host            string     `json:"host,omitempty"`
	Port            int        `json:"port,omitempty"`
	Database        string     `json:"database,omitempty"`
	Username        string     `json:"username,omitempty"`
	ServiceName     string     `json:"service_name,omitempty"`
	SSLMode         string     `json:"sslmode,omitempty"`
	ReadOnlyDefault bool       `json:"read_only_default,omitempty"`
	Favorite        bool       `json:"favorite,omitempty"`
	LastConnectedAt *time.Time `json:"last_connected_at,omitempty"`
	CreatedAt       time.Time  `json:"created_at"`
	UpdatedAt       time.Time  `json:"updated_at"`
}

// State is the local state.json.
type State struct {
	Version  int       `json:"version"`
	Profiles []Profile `json:"profiles"`
	// Layout is the default for sessions without a saved profile. A profile's
	// own Layout wins when it has one.
	Layout *Layout `json:"layout,omitempty"`
	// KeyboardLayout is the default command-key layout: "us", "ru", or empty
	// to detect from LC_ALL/LC_CTYPE/LANG.
	KeyboardLayout string `json:"keyboard_layout,omitempty"`
}

// NewState returns an empty state at the current version.
func NewState() State {
	return State{Version: StateVersion}
}

// LoadState reads path. A missing file yields an empty state; a state from a
// different format version is an error rather than a silent partial read.
func LoadState(path string) (State, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return NewState(), nil
		}
		return State{}, fmt.Errorf("cannot read %s: %w", path, err)
	}

	var st State
	if err := json.Unmarshal(data, &st); err != nil {
		return State{}, fmt.Errorf("invalid %s: %w", path, err)
	}
	if st.Version == 0 {
		st.Version = StateVersion
	}
	if st.Version != StateVersion {
		return State{}, fmt.Errorf("%s: unsupported state version %d, expected %d", path, st.Version, StateVersion)
	}
	return st, nil
}

// Save writes the state atomically: a temporary file in the same directory,
// then a rename. The file is private to the user.
func (s *State) Save(path string) error {
	s.Version = StateVersion

	dir := filepath.Dir(path)
	if err := EnsurePrivateDir(dir); err != nil {
		return err
	}

	tmp, err := os.CreateTemp(dir, "state-*.json")
	if err != nil {
		return fmt.Errorf("cannot create a temporary state file in %s: %w", dir, err)
	}
	tmpName := tmp.Name()
	defer os.Remove(tmpName)

	enc := json.NewEncoder(tmp)
	enc.SetIndent("", "  ")
	enc.SetEscapeHTML(false)
	if err := enc.Encode(s); err != nil {
		tmp.Close()
		return fmt.Errorf("cannot encode state: %w", err)
	}
	if err := tmp.Sync(); err != nil {
		tmp.Close()
		return fmt.Errorf("cannot flush %s: %w", tmpName, err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("cannot close %s: %w", tmpName, err)
	}
	if err := os.Chmod(tmpName, 0o600); err != nil {
		return fmt.Errorf("cannot set permissions on %s: %w", tmpName, err)
	}
	if err := os.Rename(tmpName, path); err != nil {
		return fmt.Errorf("cannot replace %s: %w", path, err)
	}
	return nil
}

// Upsert adds or replaces a profile by ID.
func (s *State) Upsert(p Profile) {
	now := time.Now().UTC()
	if p.CreatedAt.IsZero() {
		p.CreatedAt = now
	}
	p.UpdatedAt = now
	for i := range s.Profiles {
		if s.Profiles[i].ID == p.ID {
			p.CreatedAt = s.Profiles[i].CreatedAt
			s.Profiles[i] = p
			return
		}
	}
	s.Profiles = append(s.Profiles, p)
}

// Remove deletes a profile by ID and reports whether it existed.
func (s *State) Remove(id string) bool {
	for i := range s.Profiles {
		if s.Profiles[i].ID == id {
			s.Profiles = append(s.Profiles[:i], s.Profiles[i+1:]...)
			return true
		}
	}
	return false
}

// Find returns the profile with the given ID.
func (s *State) Find(id string) (Profile, bool) {
	for _, p := range s.Profiles {
		if p.ID == id {
			return p, true
		}
	}
	return Profile{}, false
}

// MarkConnected updates the last-used timestamp of a profile.
func (s *State) MarkConnected(id string, at time.Time) {
	for i := range s.Profiles {
		if s.Profiles[i].ID == id {
			ts := at.UTC()
			s.Profiles[i].LastConnectedAt = &ts
			return
		}
	}
}

// Sorted returns profiles for the picker: favorites first, then most recently
// used, then by label.
func (s *State) Sorted() []Profile {
	out := append([]Profile(nil), s.Profiles...)
	sort.SliceStable(out, func(i, j int) bool {
		a, b := out[i], out[j]
		if a.Favorite != b.Favorite {
			return a.Favorite
		}
		switch {
		case a.LastConnectedAt != nil && b.LastConnectedAt != nil && !a.LastConnectedAt.Equal(*b.LastConnectedAt):
			return a.LastConnectedAt.After(*b.LastConnectedAt)
		case a.LastConnectedAt != nil && b.LastConnectedAt == nil:
			return true
		case a.LastConnectedAt == nil && b.LastConnectedAt != nil:
			return false
		}
		return a.Label < b.Label
	})
	return out
}
