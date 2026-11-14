// Package mongouri parses MongoDB connection strings.
//
// MongoDB allows several hosts in one authority:
//
//	mongodb://user:pass@host1:27017,host2:27018,host3:27019/db?replicaSet=rs0
//
// net/url rejects that authority ("invalid port"), while the driver accepts it.
// This parser is deliberately string-based: it separates scheme, userinfo,
// host list, path and query without validating ports.
package mongouri

import (
	"fmt"
	"net/url"
	"strings"
)

// URI is one parsed MongoDB connection string.
type URI struct {
	Scheme      string
	User        string
	Host        string
	Path        string // without the leading slash
	Query       string // without the leading ?
	HasPassword bool
}

// Parse reads a mongodb:// or mongodb+srv:// URI.
func Parse(raw string) (URI, error) {
	raw = strings.TrimSpace(raw)
	sep := strings.Index(raw, "://")
	if sep <= 0 {
		return URI{}, fmt.Errorf("missing mongodb:// scheme")
	}
	scheme := strings.ToLower(raw[:sep])
	switch scheme {
	case "mongodb", "mongodb+srv":
	default:
		return URI{}, fmt.Errorf("unsupported scheme %q", raw[:sep])
	}

	rest := raw[sep+3:]
	authority := rest
	tail := ""
	if i := strings.IndexAny(rest, "/?#"); i >= 0 {
		authority, tail = rest[:i], rest[i:]
	}
	if i := strings.IndexByte(tail, '#'); i >= 0 {
		tail = tail[:i]
	}

	u := URI{Scheme: scheme, Host: authority}
	if at := strings.LastIndex(authority, "@"); at >= 0 {
		userinfo := authority[:at]
		u.Host = authority[at+1:]
		if colon := strings.IndexByte(userinfo, ':'); colon >= 0 {
			u.User = userinfo[:colon]
			u.HasPassword = true
		} else {
			u.User = userinfo
		}
		if decoded, err := url.PathUnescape(u.User); err == nil {
			u.User = decoded
		}
	}
	if u.Host == "" {
		return URI{}, fmt.Errorf("missing host")
	}

	switch {
	case strings.HasPrefix(tail, "/"):
		path := tail[1:]
		if i := strings.IndexByte(path, '?'); i >= 0 {
			u.Path, u.Query = path[:i], path[i+1:]
		} else {
			u.Path = path
		}
	case strings.HasPrefix(tail, "?"):
		u.Query = tail[1:]
	}
	return u, nil
}

// WithoutPassword rebuilds the URI with the password removed. The username and
// every other component stay intact.
func (u URI) WithoutPassword() string {
	var b strings.Builder
	b.WriteString(u.Scheme)
	b.WriteString("://")
	if u.User != "" {
		b.WriteString(url.User(u.User).String())
		b.WriteByte('@')
	}
	b.WriteString(u.Host)
	if u.Path != "" {
		b.WriteByte('/')
		b.WriteString(u.Path)
	}
	if u.Query != "" {
		b.WriteByte('?')
		b.WriteString(u.Query)
	}
	return b.String()
}

// Label is user@host/db for the picker; it never contains a password.
func (u URI) Label() string {
	label := u.Host
	if u.User != "" {
		label = u.User + "@" + label
	}
	if db := u.Database(); db != "" {
		label += "/" + db
	}
	return label
}

// Endpoint is user@host without the database.
func (u URI) Endpoint() string {
	endpoint := u.Host
	if u.User != "" {
		endpoint = u.User + "@" + endpoint
	}
	return endpoint
}

// Database is the first path segment; MongoDB uses it as the default database.
func (u URI) Database() string {
	path := strings.TrimPrefix(u.Path, "/")
	if i := strings.IndexByte(path, '/'); i >= 0 {
		path = path[:i]
	}
	return path
}
