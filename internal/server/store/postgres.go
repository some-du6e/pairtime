// SPDX-License-Identifier: AGPL-3.0-or-later

package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"net/url"

	// lib/pq registers the "postgres" driver. The standard library has no
	// Postgres client, and lib/pq is pure Go, MIT-licensed and has no
	// dependencies of its own.
	_ "github.com/lib/pq"
)

// postgresReadConns caps the read pool, well under Postgres's default
// max_connections of 100.
const postgresReadConns = 16

func openPostgres(ctx context.Context, o Options) (*DB, error) {
	if o.URL == "" {
		return nil, errors.New("store: no Postgres URL")
	}
	readURL, err := readOnlyURL(o.URL)
	if err != nil {
		return nil, err
	}

	w, err := sql.Open("postgres", o.URL)
	if err != nil {
		return nil, fmt.Errorf("store: opening Postgres: %w", err)
	}
	w.SetMaxOpenConns(1)
	w.SetMaxIdleConns(1)
	var version string
	if err := w.QueryRowContext(ctx, "SHOW server_version").Scan(&version); err != nil {
		w.Close()
		return nil, fmt.Errorf("store: connecting to Postgres: %w", err)
	}

	r, err := sql.Open("postgres", readURL)
	if err != nil {
		w.Close()
		return nil, fmt.Errorf("store: opening Postgres: %w", err)
	}
	r.SetMaxOpenConns(postgresReadConns)
	r.SetMaxIdleConns(postgresReadConns / 4)
	if err := r.PingContext(ctx); err != nil {
		w.Close()
		r.Close()
		return nil, fmt.Errorf("store: connecting to Postgres: %w", err)
	}
	return &DB{Engine: Postgres, Version: version, Write: w, Read: r}, nil
}

// readOnlyURL adds default_transaction_read_only=on to a connection URL.
// lib/pq sends parameters it doesn't know itself to the server as run-time
// settings, so every read-pool connection starts read-only. Errors never
// quote the URL, because it holds a password.
func readOnlyURL(raw string) (string, error) {
	u, err := url.Parse(raw)
	if err != nil || (u.Scheme != "postgres" && u.Scheme != "postgresql") {
		return "", errors.New("store: the Postgres URL must be of the form postgres://user:password@host:port/database")
	}
	q := u.Query()
	q.Set("default_transaction_read_only", "on")
	u.RawQuery = q.Encode()
	return u.String(), nil
}
