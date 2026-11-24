// SPDX-License-Identifier: AGPL-3.0-or-later

// Package privacy is pairtimed's side of Hack Club's privacy hub (WP-SV13;
// DESIGN 8.9; TP B3): POST /api/privacy/delete erases a person's account,
// and POST /api/privacy/export reports what pairtimed holds of them. The
// hub calls each with a short-lived ES256 JWT it signs. It calls delete
// first with dry_run true, which runs the same erasure in a transaction
// that rolls back, and once a person has approved the counts, again for
// real. Both answer counts and reasons and never anything about the
// person, and log only the token's request_id and the outcome.
//
// A token's jti is recorded before the request acts, in a transaction of
// its own (Take), so a token works once even across a restart, and a dry
// run's jti stays when its erasure rolls back (D-sv13-jti). The eraser
// deletes every server row of DESIGN 4.4 that is the account's, through
// the packages that own build days, devlogs, project names and
// submissions where they do (erase), and keeps the account's leaf hashes in day trees that other
// accounts' batches share, which it reports.
package privacy

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/some-du6e/pairtime/internal/server/accounts"
	"github.com/some-du6e/pairtime/internal/server/store"
	"github.com/some-du6e/pairtime/internal/server/writer"
)

// The endpoints' paths (DESIGN 8.9).
const (
	DeletePath = "/api/privacy/delete"
	ExportPath = "/api/privacy/export"
)

// Options builds a Service.
type Options struct {
	// Verifier checks the hub's tokens for this server's audience.
	Verifier *Verifier
	DB       *store.DB
	Writer   *writer.Writer
	// Emails gives the keyed index of an email address, which finds an
	// account by the subject's emails.
	Emails *accounts.EmailCipher
	Logger *slog.Logger
	// Now is the clock; nil is time.Now.
	Now func() time.Time
}

// Service serves the two endpoints.
type Service struct {
	o Options
}

// New returns the service of o.
func New(o Options) (*Service, error) {
	if o.Verifier == nil || o.DB == nil || o.Writer == nil || o.Emails == nil || o.Logger == nil {
		return nil, errors.New("privacy: New needs a verifier, a database, a writer, an email cipher and a logger")
	}
	if o.Now == nil {
		o.Now = time.Now
	}
	return &Service{o: o}, nil
}

// Register adds the endpoints to mux.
func (s *Service) Register(mux *http.ServeMux) {
	mux.HandleFunc("POST "+DeletePath, func(w http.ResponseWriter, r *http.Request) { s.serve(w, r, ActionDelete) })
	mux.HandleFunc("POST "+ExportPath, func(w http.ResponseWriter, r *http.Request) { s.serve(w, r, ActionExport) })
}

// serve answers one request for action. It reads only the token: the
// request's body and query never name anyone (the hub's spec, "verifying
// it").
func (s *Service) serve(w http.ResponseWriter, r *http.Request, action string) {
	w.Header().Set("Cache-Control", "no-store")
	ctx := r.Context()
	now := s.o.Now()
	token, ok := bearer(r.Header.Get("Authorization"))
	if !ok {
		s.refuse(w, action, "", badToken("missing_token"))
		return
	}
	c, err := s.o.Verifier.Verify(ctx, token, action, now)
	if err != nil {
		s.refuse(w, action, "", err)
		return
	}
	if err := Take(ctx, s.o.Writer, c, now); err != nil {
		s.refuse(w, action, c.RequestID, err)
		return
	}
	var rep Report
	switch action {
	case ActionDelete:
		rep, err = s.deleteSubject(ctx, c)
	default:
		rep, err = s.exportSubject(ctx, c)
	}
	if err != nil {
		s.refuse(w, action, c.RequestID, err)
		return
	}
	outcome := "not_found"
	switch {
	case !rep.Found:
	case action == ActionExport:
		outcome = "exported"
	case c.DryRun:
		outcome = "dry_run"
	default:
		outcome = "deleted"
	}
	s.o.Logger.Info("privacy request", "request_id", c.RequestID, "action", action, "outcome", outcome)
	writeJSON(w, http.StatusOK, rep)
}

// deleteSubject runs the erasure in one writer op, which a dry run rolls back,
// alone in its batch's savepoint. A read-only count first sizes the
// op's batch.
func (s *Service) deleteSubject(ctx context.Context, c Claims) (Report, error) {
	sized, err := s.exportSubject(ctx, c)
	if err != nil {
		return Report{}, err
	}
	var rep Report
	err = s.o.Writer.Do(ctx, rowsOf(sized), func(ctx context.Context, tx writer.Tx) error {
		var err error
		if rep, err = erase(ctx, tx, s.o.Emails, c); err != nil {
			return err
		}
		if c.DryRun {
			return errDryRun
		}
		return nil
	})
	if err != nil && !errors.Is(err, errDryRun) {
		return Report{}, err
	}
	return rep, nil
}

// exportSubject counts what is held in one read transaction, so the counts
// agree with each other: SQLite's WAL gives a transaction one snapshot,
// and on Postgres, whose READ COMMITTED takes one a statement, the
// transaction asks for REPEATABLE READ.
func (s *Service) exportSubject(ctx context.Context, c Claims) (Report, error) {
	var opts *sql.TxOptions
	if s.o.DB.Engine == store.Postgres {
		opts = &sql.TxOptions{Isolation: sql.LevelRepeatableRead, ReadOnly: true}
	}
	tx, err := s.o.DB.Read.BeginTx(ctx, opts)
	if err != nil {
		return Report{}, err
	}
	defer tx.Rollback()
	return export(ctx, tx, s.o.Emails, c)
}

// refuse answers err: an *Error as itself, and anything else as a 500,
// which the hub retries with a fresh token. It logs the request_id, when
// a token got far enough to give one, and the code.
func (s *Service) refuse(w http.ResponseWriter, action, requestID string, err error) {
	var e *Error
	if !errors.As(err, &e) {
		s.o.Logger.Error("privacy request failed", "request_id", requestID, "action", action, "err", err)
		e = &Error{Status: http.StatusInternalServerError, Code: "internal_error"}
	}
	args := []any{"request_id", requestID, "action", action, "outcome", e.Code, "status", e.Status}
	if e.Status >= 500 {
		// The hub retries these, and a person may need to see why.
		if e.Err != nil {
			args = append(args, "err", e.Err)
		}
		s.o.Logger.Warn("privacy request refused", args...)
	} else {
		s.o.Logger.Info("privacy request refused", args...)
	}
	body := map[string]string{"error": e.Code}
	if e.Reason != "" {
		body["reason"] = e.Reason
	}
	writeJSON(w, e.Status, body)
}

// bearer returns the token of an Authorization header of the Bearer
// scheme, whose name is case-insensitive (RFC 6750 2.1).
func bearer(h string) (string, bool) {
	scheme, token, ok := strings.Cut(h, " ")
	if !ok || !strings.EqualFold(scheme, "Bearer") || token == "" || strings.ContainsAny(token, " \t") {
		return "", false
	}
	return token, true
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(v)
}
