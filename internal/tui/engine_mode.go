package tui

import (
	"strings"

	"mongo-tui/internal/mongouri"
)

func (m Model) isMongo() bool { return true }

func (m Model) connectionDisplay() string { return mongoEndpoint(m.mongoURI) }

func (m Model) appName() string { return "mongo-tui" }

func (m Model) connectionEndpoint() string { return mongoEndpoint(m.mongoURI) }

func (m Model) currentDatabase() string {
	if m.sess != nil {
		if db := m.sess.Summary().Database; db != "" {
			return db
		}
	}
	return mongoDatabase(m.mongoURI)
}

func (m Model) engineLabel() string { return "MongoDB " + m.serverVersion() }

// mongoEndpoint is the host part shown in the header; the URI stays the source
// of truth for reconnects.
func mongoEndpoint(uri string) string {
	u, err := mongouri.Parse(uri)
	if err != nil {
		return uri
	}
	return u.Endpoint()
}

// mongoDatabase is the URI path. mongosh uses "test" when the URI has no path.
func mongoDatabase(uri string) string {
	u, err := mongouri.Parse(uri)
	if err != nil {
		return "test"
	}
	if db := u.Database(); db != "" {
		return db
	}
	return "test"
}

func mongoHasUser(uri string) bool {
	u, err := mongouri.Parse(uri)
	return err == nil && u.User != ""
}

func looksLikeMongoURI(text string) bool {
	lower := strings.ToLower(strings.TrimSpace(text))
	return strings.HasPrefix(lower, "mongodb://") || strings.HasPrefix(lower, "mongodb+srv://")
}
