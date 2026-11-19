// SPDX-License-Identifier: AGPL-3.0-or-later

package main

import (
	"errors"
	"flag"
	"fmt"

	"github.com/some-du6e/pairtime/internal/server/config"
	"github.com/some-du6e/pairtime/internal/server/store"
)

// newFlagSet returns a flag set for the named command that reports errors
// on e.stderr instead of exiting.
func newFlagSet(name, summary string, e *env) *flag.FlagSet {
	fs := flag.NewFlagSet(name, flag.ContinueOnError)
	fs.SetOutput(e.stderr)
	fs.Usage = func() {
		fmt.Fprintf(e.stderr, "Usage: pairtimed %s [flags]\n\n%s.\n\nFlags:\n", name, summary)
		fs.PrintDefaults()
	}
	return fs
}

// parse parses args. When it returns done, the command returns code: 0
// after -h, 2 after a usage error.
func parse(fs *flag.FlagSet, args []string) (code int, done bool) {
	if err := fs.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return 0, true
		}
		return 2, true
	}
	if fs.NArg() > 0 {
		fmt.Fprintf(fs.Output(), "pairtimed %s: unexpected arguments %q\n", fs.Name(), fs.Args())
		return 2, true
	}
	return 0, false
}

// configFlags adds -config and -secrets to fs and returns a function that
// loads the configuration they name.
func configFlags(fs *flag.FlagSet, e *env) func() (*config.Config, error) {
	defaultPath := e.getenv(config.EnvConfig)
	if defaultPath == "" {
		defaultPath = config.DefaultPath
	}
	path := fs.String("config", defaultPath, "the pairtime.toml to load ($"+config.EnvConfig+")")
	secrets := fs.String("secrets", e.getenv(config.EnvSecretsFile),
		"a secrets.env to read, mode 0600; compose passes secrets as environment variables instead ($"+config.EnvSecretsFile+")")
	return func() (*config.Config, error) {
		return config.Load(*path, *secrets, e.environ)
	}
}

// storeOptions says how to open the database cfg names.
func storeOptions(cfg *config.Config, e *env) store.Options {
	o := store.Options{Path: cfg.Database.Path}
	switch cfg.Database.Engine {
	case config.EnginePostgres:
		o.Engine = store.Postgres
		o.URL = cfg.Secrets.Get(config.DatabaseURL)
	default:
		o.Engine = store.SQLite
	}
	if e.storeOptions != nil {
		e.storeOptions(&o)
	}
	return o
}
