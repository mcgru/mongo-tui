// Command mongo-tui is a terminal UI for MongoDB.
package main

import (
	"fmt"
	"os"

	"mongo-tui/internal/cli"
	"mongo-tui/internal/config"
	"mongo-tui/internal/tui"
)

// version is set at build time: make build VERSION=1.2.3
var version = "0.1.0-dev"

func main() {
	os.Exit(cli.Run(os.Args[1:], os.Stdout, os.Stderr, version, runApp))
}

// runApp prepares the environment and hands control to the TUI.
func runApp(opts cli.Options) int {
	paths, err := config.DefaultPaths()
	if err != nil {
		return fail(err)
	}
	if migrateErr := config.MigrateLegacy(paths); migrateErr != nil {
		// The old files stay untouched; report the problem but let the user
		// continue with a fresh config if the new files are usable.
		fmt.Fprintf(os.Stderr, "mongo-tui: %v\n", migrateErr)
	}
	cfg, err := config.Load(paths.ConfigFile)
	if err != nil {
		return fail(err)
	}
	// The config is optional: a read-only home directory must not stop the
	// client from connecting, so a failure to write the sample is not fatal.
	_ = config.EnsureFile(paths.ConfigFile)
	state, err := config.LoadState(paths.StateFile)
	if err != nil {
		return fail(err)
	}

	return tui.Run(tui.Options{
		CLI:     opts,
		Config:  cfg,
		State:   state,
		Paths:   paths,
		Version: version,
	})
}

func fail(err error) int {
	fmt.Fprintf(os.Stderr, "mongo-tui: %v\n", err)
	return 1
}
