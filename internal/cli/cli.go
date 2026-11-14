// Package cli parses the mongo-tui command line.
package cli

import (
	"errors"
	"fmt"
	"io"
	"strings"
)

// Mode says what the command line asks for.
type Mode int

const (
	ModeRun Mode = iota
	ModeHelp
	ModeVersion
)

// Options is the parsed command line.
type Options struct {
	Mode      Mode
	MongoURI  string
	Profile   string
	ReadOnly  bool
	NoHistory bool
}

type usageError struct {
	msg string
	try string
}

func (e *usageError) Error() string { return e.msg }

func usage(msg, try string) error { return &usageError{msg: msg, try: try} }

// Parse reads the command line. MongoDB connections are given as a URI or a
// saved profile.
func Parse(args []string) (Options, error) {
	var (
		opts     Options
		position []string
	)
	for i := 0; i < len(args); i++ {
		arg := args[i]
		switch {
		case arg == "--":
			position = append(position, args[i+1:]...)
			i = len(args)
		case arg == "-?" || arg == "--help" || arg == "-h":
			opts.Mode = ModeHelp
		case arg == "-V" || arg == "--version":
			opts.Mode = ModeVersion
		case arg == "--read-only":
			opts.ReadOnly = true
		case arg == "--no-history":
			opts.NoHistory = true
		case arg == "--profile":
			if i+1 >= len(args) {
				return Options{}, usage("--profile needs a value", command("--profile", "NAME"))
			}
			i++
			opts.Profile = args[i]
		case strings.HasPrefix(arg, "--profile="):
			opts.Profile = strings.TrimPrefix(arg, "--profile=")
		case strings.HasPrefix(arg, "-"):
			return Options{}, usage("unknown option "+arg, command("--help"))
		default:
			position = append(position, arg)
		}
	}

	if len(position) > 1 {
		return Options{}, usage("only one MongoDB URI may be given", command(position[0]))
	}
	if len(position) == 1 {
		if opts.Profile != "" {
			return Options{}, usage("--profile and a MongoDB URI cannot be combined", command("--profile", opts.Profile))
		}
		if !isMongoURI(position[0]) {
			return Options{}, usage("expected a mongodb:// or mongodb+srv:// URI", command(position[0]))
		}
		opts.MongoURI = position[0]
	}
	return opts, nil
}

func isMongoURI(s string) bool {
	lower := strings.ToLower(strings.TrimSpace(s))
	return strings.HasPrefix(lower, "mongodb://") || strings.HasPrefix(lower, "mongodb+srv://")
}

func command(args ...string) string {
	quoted := make([]string, 0, len(args)+1)
	quoted = append(quoted, "mongo-tui")
	for _, arg := range args {
		if arg == "" || strings.ContainsAny(arg, " \t\n\"'\\$`") {
			quoted = append(quoted, "'"+strings.ReplaceAll(arg, "'", `'\''`)+"'")
			continue
		}
		quoted = append(quoted, arg)
	}
	return strings.Join(quoted, " ")
}

// Run parses args, handles help/version and hands the rest to launch. It
// returns the process exit code.
func Run(args []string, stdout, stderr io.Writer, version string, launch func(Options) int) int {
	opts, err := Parse(args)
	if err != nil {
		fmt.Fprintf(stderr, "mongo-tui: %s\n", err)
		var ue *usageError
		if errors.As(err, &ue) && ue.try != "" {
			fmt.Fprintln(stderr, "try: "+ue.try)
		}
		return 2
	}
	switch opts.Mode {
	case ModeHelp:
		fmt.Fprint(stdout, usageText)
		return 0
	case ModeVersion:
		fmt.Fprintf(stdout, "mongo-tui %s\n", version)
		return 0
	}
	return launch(opts)
}

const usageText = `mongo-tui — terminal client for MongoDB

Usage:
  mongo-tui mongodb://host:27017/db  open the MongoDB workbench
  mongo-tui mongodb+srv://user@cluster.example/db
  mongo-tui --profile NAME           open a saved MongoDB profile
  mongo-tui                          open the connection picker
  mongo-tui --help                   show this help
  mongo-tui --version                show the version

Options:
  --profile NAME     saved MongoDB profile
  --read-only        reject write requests client-side
  --no-history       do not record this session in the connection history

The password is asked for in the TUI and is never stored. A URI containing a
password is rejected. MongoDB URIs cannot be combined with --profile.

Exit status: 0 on success, 1 on runtime error, 2 on a command-line error.
`
