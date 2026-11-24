// SPDX-License-Identifier: AGPL-3.0-or-later

package devlog

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"slices"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/some-du6e/pairtime/internal/server/device"
	"github.com/some-du6e/pairtime/internal/server/writer"
)

// The teen layer's paths (DESIGN 3.10; docs/spec/teen-layer-v1.md). A
// delete names its item by id after the path: DELETE /api/v1/devlogs/{id}.
const (
	DevlogsPath = "/api/v1/devlogs"
	BadgesPath  = "/api/v1/badges"
)

// maxBody bounds a request's body. A devlog's Markdown is at most
// MaxMarkdown bytes, and JSON's escapes can make it up to six times that.
const maxBody = 6*MaxMarkdown + 4<<10

// Devices finds the device of a token: *device.Store.
type Devices interface {
	Authenticate(ctx context.Context, token string) (device.Device, error)
}

// Doer runs a write through the writer goroutine. *writer.Writer is one.
type Doer interface {
	Do(ctx context.Context, rows int, op writer.Op) error
}

// Options wire a Service.
type Options struct {
	// Devices authenticates the device token each request carries.
	Devices Devices
	// Writer takes every write, and Read is the read pool.
	Writer Doer
	Read   Querier
	// Now is the clock. A devlog's time is Now inside the writer op that
	// stores it.
	Now func() time.Time
	// Logger hears of reads and writes that fail.
	Logger *slog.Logger
}

// Service is the devlog and badge API, for the device token of the
// account's own computers (orchestrator, 2026-11-23: no request signature,
// since DESIGN 4.3's publishing row asks for none).
type Service struct{ o Options }

// New returns the API.
func New(o Options) *Service { return &Service{o: o} }

// Register adds the API's routes to mux.
func (s *Service) Register(mux *http.ServeMux) {
	mux.HandleFunc("POST "+DevlogsPath, s.publish)
	mux.HandleFunc("GET "+DevlogsPath, s.devlogs)
	mux.HandleFunc("DELETE "+DevlogsPath+"/{id}", s.delete)
	mux.HandleFunc("POST "+BadgesPath, s.show)
	mux.HandleFunc("GET "+BadgesPath, s.badges)
	mux.HandleFunc("DELETE "+BadgesPath+"/{id}", s.remove)
}

// errorBody is the JSON of a refusal, as ingest's (docs/spec/ingest-v1.md).
type errorBody struct {
	Error  string `json:"error"`
	Detail string `json:"detail"`
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	h := w.Header()
	h.Set("Content-Type", "application/json; charset=utf-8")
	h.Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(v)
}

func refuse(w http.ResponseWriter, status int, code, detail string) {
	writeJSON(w, status, errorBody{Error: code, Detail: detail})
}

func (s *Service) unavailable(w http.ResponseWriter, what string, err error) {
	s.o.Logger.Error(what, "err", err)
	refuse(w, http.StatusServiceUnavailable, "unavailable", "The server couldn't do that just now; send it again later.")
}

// authenticate finds the device of r's bearer token, or answers 401 or 503
// itself and reports false.
func (s *Service) authenticate(w http.ResponseWriter, r *http.Request) (device.Device, bool) {
	scheme, token, found := strings.Cut(r.Header.Get("Authorization"), " ")
	token = strings.TrimSpace(token)
	if !found || !strings.EqualFold(scheme, "Bearer") || token == "" {
		w.Header().Set("WWW-Authenticate", `Bearer realm="pairtime"`)
		refuse(w, http.StatusUnauthorized, "token_missing", "Send the device token as Authorization: Bearer ptd_...")
		return device.Device{}, false
	}
	dev, err := s.o.Devices.Authenticate(r.Context(), token)
	switch {
	case err == nil && !dev.Revoked:
		return dev, true
	case err == nil, errors.Is(err, device.ErrBadToken), errors.Is(err, device.ErrUnknownToken), errors.Is(err, device.ErrRevoked):
		w.Header().Set("WWW-Authenticate", `Bearer realm="pairtime", error="invalid_token"`)
		refuse(w, http.StatusUnauthorized, "token_invalid", "This device token isn't valid, or its device was removed. Run pairtime login again.")
	default:
		s.unavailable(w, "devlog: authenticating a device", err)
	}
	return device.Device{}, false
}

// readObject reads r's body, at most maxBody bytes of valid UTF-8, as one
// JSON object into v, refusing a member v lacks, or answers 400 or 413
// itself and reports false.
func readObject(w http.ResponseWriter, r *http.Request, v any, shape string) bool {
	data, err := io.ReadAll(http.MaxBytesReader(w, r.Body, maxBody))
	var tooLarge *http.MaxBytesError
	switch {
	case errors.As(err, &tooLarge):
		refuse(w, http.StatusRequestEntityTooLarge, "body_too_large", "The body is too large: a devlog's Markdown is at most 64 KiB.")
		return false
	case err != nil:
		refuse(w, http.StatusBadRequest, "body_unread", "The body didn't arrive whole.")
		return false
	case !utf8.Valid(data):
		refuse(w, http.StatusBadRequest, "json_invalid", "The body must be one JSON object in valid UTF-8.")
		return false
	}
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.DisallowUnknownFields()
	if err := dec.Decode(v); err != nil || dec.More() || !bytes.HasPrefix(bytes.TrimSpace(data), []byte("{")) {
		refuse(w, http.StatusBadRequest, "body_shape", shape)
		return false
	}
	if _, err := dec.Token(); err != io.EOF {
		refuse(w, http.StatusBadRequest, "body_shape", shape)
		return false
	}
	return true
}

// devlogRequest is the body of POST /api/v1/devlogs.
type devlogRequest struct {
	ID         *string `json:"id"`
	Project    *string `json:"project"`
	Markdown   *string `json:"markdown"`
	AIAssisted *bool   `json:"ai_assisted"`
}

const devlogShape = "The body is an object with id and project, each a key, markdown, a string, and ai_assisted, true or false."

// publish is POST /api/v1/devlogs: 201 with the devlog stored now, 200
// with the one stored before for an id the account holds, and 410 for an
// id the teen deleted.
func (s *Service) publish(w http.ResponseWriter, r *http.Request) {
	dev, ok := s.authenticate(w, r)
	if !ok {
		return
	}
	var req devlogRequest
	if !readObject(w, r, &req, devlogShape) {
		return
	}
	switch {
	case req.ID == nil || req.Project == nil || req.Markdown == nil || req.AIAssisted == nil:
		refuse(w, http.StatusBadRequest, "body_shape", devlogShape)
		return
	case !isKey(*req.ID):
		refuse(w, http.StatusBadRequest, "id_invalid", "The id is 1 to 128 letters, digits, _ or -, made at random by the client.")
		return
	case !isKey(*req.Project):
		refuse(w, http.StatusBadRequest, "project_invalid", "The project is the device's key of the project: 1 to 128 letters, digits, _ or -.")
		return
	case *req.Markdown == "":
		refuse(w, http.StatusBadRequest, "markdown_empty", "A devlog needs some Markdown.")
		return
	case len(*req.Markdown) > MaxMarkdown:
		refuse(w, http.StatusRequestEntityTooLarge, "markdown_too_large", "A devlog's Markdown is at most 64 KiB.")
		return
	}
	d := Devlog{ID: *req.ID, ProjectKey: *req.Project, Markdown: *req.Markdown, AIAssisted: *req.AIAssisted}
	var out Devlog
	var created bool
	err := s.o.Writer.Do(r.Context(), 2, func(ctx context.Context, tx writer.Tx) error {
		var err error
		out, created, err = Publish(ctx, tx, dev.Account, dev.ID, d, s.o.Now().UnixMilli())
		return err
	})
	switch {
	case errors.Is(err, ErrRemoved):
		refuse(w, http.StatusGone, "removed", "This devlog was deleted, so the server won't take it again.")
	case err != nil:
		s.unavailable(w, "devlog: publishing a devlog", err)
	case created:
		writeJSON(w, http.StatusCreated, out)
	default:
		writeJSON(w, http.StatusOK, out)
	}
}

// devlogs is GET /api/v1/devlogs: the account's devlogs, oldest first.
func (s *Service) devlogs(w http.ResponseWriter, r *http.Request) {
	dev, ok := s.authenticate(w, r)
	if !ok {
		return
	}
	all, err := List(r.Context(), s.o.Read, dev.Account)
	if err != nil {
		s.unavailable(w, "devlog: reading devlogs", err)
		return
	}
	writeJSON(w, http.StatusOK, struct {
		Devlogs []Devlog `json:"devlogs"`
	}{Devlogs: orEmpty(all)})
}

// delete is DELETE /api/v1/devlogs/{id}: the devlog goes at once, and only
// its id and the time stay, whether or not the server held it.
func (s *Service) delete(w http.ResponseWriter, r *http.Request) {
	s.take(w, r, Delete, "devlog: deleting a devlog")
}

// badgeRequest is the body of POST /api/v1/badges.
type badgeRequest struct {
	ID   *string `json:"id"`
	Code *string `json:"code"`
}

const badgeShape = "The body is an object with id, a key, and code, one of the badge codes."

// show is POST /api/v1/badges, as publish is for a devlog.
func (s *Service) show(w http.ResponseWriter, r *http.Request) {
	dev, ok := s.authenticate(w, r)
	if !ok {
		return
	}
	var req badgeRequest
	if !readObject(w, r, &req, badgeShape) {
		return
	}
	switch {
	case req.ID == nil || req.Code == nil:
		refuse(w, http.StatusBadRequest, "body_shape", badgeShape)
		return
	case !isKey(*req.ID):
		refuse(w, http.StatusBadRequest, "id_invalid", "The id is 1 to 128 letters, digits, _ or -, made at random by the client.")
		return
	case !slices.Contains(Codes, *req.Code):
		refuse(w, http.StatusBadRequest, "code_unknown", "The code is one of "+strings.Join(Codes, ", ")+".")
		return
	}
	var out Badge
	var created bool
	err := s.o.Writer.Do(r.Context(), 1, func(ctx context.Context, tx writer.Tx) error {
		var err error
		out, created, err = Show(ctx, tx, dev.Account, Badge{ID: *req.ID, Code: *req.Code})
		return err
	})
	switch {
	case errors.Is(err, ErrRemoved):
		refuse(w, http.StatusGone, "removed", "This badge was removed, so the server won't take it again.")
	case err != nil:
		s.unavailable(w, "devlog: showing a badge", err)
	case created:
		writeJSON(w, http.StatusCreated, out)
	default:
		writeJSON(w, http.StatusOK, out)
	}
}

// badges is GET /api/v1/badges: the badges the account shows.
func (s *Service) badges(w http.ResponseWriter, r *http.Request) {
	dev, ok := s.authenticate(w, r)
	if !ok {
		return
	}
	all, err := Badges(r.Context(), s.o.Read, dev.Account)
	if err != nil {
		s.unavailable(w, "devlog: reading badges", err)
		return
	}
	writeJSON(w, http.StatusOK, struct {
		Badges []Badge `json:"badges"`
	}{Badges: orEmpty(all)})
}

// remove is DELETE /api/v1/badges/{id}, as delete is for a devlog.
func (s *Service) remove(w http.ResponseWriter, r *http.Request) {
	s.take(w, r, Remove, "devlog: removing a badge")
}

// take runs a delete or removal of the request's id: 204 once its row is
// gone and its removal kept.
func (s *Service) take(w http.ResponseWriter, r *http.Request, op func(context.Context, writer.Tx, string, string, int64) error, what string) {
	dev, ok := s.authenticate(w, r)
	if !ok {
		return
	}
	id := r.PathValue("id")
	if !isKey(id) {
		refuse(w, http.StatusBadRequest, "id_invalid", "The id is 1 to 128 letters, digits, _ or -.")
		return
	}
	if err := s.o.Writer.Do(r.Context(), 2, func(ctx context.Context, tx writer.Tx) error {
		return op(ctx, tx, dev.Account, id, s.o.Now().UnixMilli())
	}); err != nil {
		s.unavailable(w, what, err)
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(http.StatusNoContent)
}

// orEmpty makes a nil list an empty one, so the JSON holds [] and not null.
func orEmpty[T any](s []T) []T {
	if s == nil {
		return []T{}
	}
	return s
}
