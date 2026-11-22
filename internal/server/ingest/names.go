// SPDX-License-Identifier: AGPL-3.0-or-later

package ingest

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net/http"
	"time"
	"unicode/utf8"

	"github.com/some-du6e/pairtime/internal/server/device"
	"github.com/some-du6e/pairtime/internal/server/projectnames"
	"github.com/some-du6e/pairtime/internal/server/writer"
	"github.com/some-du6e/pairtime/pkg/projectname"
)

// NamesPath is where a device sends the names its owner turned on or off,
// one project at a time on the computer, for their own dashboard
// (docs/spec/project-names-v1.md; D-owner-names).
const NamesPath = "/api/v1/projects/names"

// namesContext starts a names request's signed message, so a names
// signature never passes for an upload, nor an upload's for a names
// request (project-names-v1 1.2).
const namesContext = "pairtime-project-names-v1\n"

// NamesMessage returns M_names, the bytes a names request's signature
// covers (project-names-v1 1.2): Message's fields after namesContext,
//
//	"pairtime-project-names-v1\n" || device_id || "\n" || req || "\n" || time || "\n" || hex(SHA-256(raw body))
func NamesMessage(deviceID, req, time, bodySHA256 string) []byte {
	m := make([]byte, 0, len(namesContext)+len(deviceID)+len(req)+len(time)+len(bodySHA256)+3)
	m = append(m, namesContext...)
	m = append(m, deviceID...)
	m = append(m, '\n')
	m = append(m, req...)
	m = append(m, '\n')
	m = append(m, time...)
	m = append(m, '\n')
	return append(m, bodySHA256...)
}

// MaxNameItems is how many items a names request's set and delete hold
// together (project-names-v1 1.4).
const MaxNameItems = 200

// namesBody is a names request's body: the names to store and the keys
// whose names to delete.
type namesBody struct {
	set []projectnames.Name
	del []string
}

// namesAnswer is a names request's 200 response: every name the server
// holds for the device afterwards (project-names-v1 1.6).
type namesAnswer struct {
	Names []projectnames.Name `json:"names"`
}

// parseNames reads a names request's inflated body (project-names-v1 1.3
// and 1.5 steps 1 and 2), refusing the first thing that fails, in order.
func parseNames(data []byte) (namesBody, *refusal) {
	var nb namesBody
	if !utf8.Valid(data) || !json.Valid(data) {
		return nb, refuse(CodeJSONInvalid, "The body must be one JSON object in valid UTF-8.")
	}
	shape := refuse(CodeBodyShape, "The body is an object with set, an array of {project, label}, and delete, an array of keys, each at most once.")
	tooMany := refuse(CodeTooManyNames, "A request holds at most 200 names to set and delete together; split it.")
	if jsonKind(data) != '{' {
		return nb, shape
	}
	dec := json.NewDecoder(bytes.NewReader(data))
	if t, err := dec.Token(); err != nil || t != json.Delim('{') {
		return nb, shape
	}
	var set, del []json.RawMessage
	seen := map[string]bool{}
	for dec.More() {
		t, err := dec.Token()
		name, ok := t.(string)
		if err != nil || !ok || seen[name] {
			return namesBody{}, shape
		}
		seen[name] = true
		switch name {
		case "set":
			set, err = topArray(dec, MaxNameItems)
		case "delete":
			del, err = topArray(dec, MaxNameItems)
		default:
			err = errShape
		}
		switch {
		case errors.Is(err, errTooMany):
			return namesBody{}, tooMany
		case err != nil:
			return namesBody{}, shape
		}
	}
	if len(set)+len(del) > MaxNameItems {
		return namesBody{}, tooMany
	}
	keys := map[string]bool{}
	project := func(key string) *refusal {
		switch {
		case !isKey(key):
			return refuse(CodeProjectInvalid, "Each project is a key: 1 to 128 letters, digits, _ or -.")
		case keys[key]:
			return refuse(CodeProjectRepeated, "A project key is at most once in a request, in set or in delete.")
		}
		keys[key] = true
		return nil
	}
	for _, raw := range set {
		var n projectnames.Name
		var gotProject, gotLabel, bad bool
		ok := eachMember(raw, func(member string, v json.RawMessage) bool {
			var s string
			switch {
			case jsonKind(v) != '"' || json.Unmarshal(v, &s) != nil:
				bad = true
			case member == "project" && !gotProject:
				n.Project, gotProject = s, true
			case member == "label" && !gotLabel:
				n.Label, gotLabel = s, true
			default:
				bad = true
			}
			return !bad
		})
		if !ok || bad || !gotProject || !gotLabel {
			return namesBody{}, shape
		}
		if r := project(n.Project); r != nil {
			return namesBody{}, r
		}
		if !projectname.ValidLabel(n.Label) {
			return namesBody{}, refuse(CodeLabelInvalid, "A label is 1 to 64 characters of valid UTF-8, already cleaned as pkg/projectname's CleanLabel cleans it.")
		}
		nb.set = append(nb.set, n)
	}
	for _, raw := range del {
		var key string
		if jsonKind(raw) != '"' || json.Unmarshal(raw, &key) != nil {
			return namesBody{}, shape
		}
		if r := project(key); r != nil {
			return namesBody{}, r
		}
		nb.del = append(nb.del, key)
	}
	return nb, nil
}

// names is POST /api/v1/projects/names: ingest-v1 2.5's steps 1 to 12 with
// M_names, then project-names-v1 1.5's checks and 1.6's processing.
func (s *Service) names(w http.ResponseWriter, r *http.Request) {
	if !s.On() {
		s.refuse(w, refusalOff)
		return
	}
	dev, ref := s.authenticate(r)
	if ref == nil && len(dev.PublicKey) != ed25519.PublicKeySize {
		ref = keyMissing()
	}
	var env envelope
	if ref == nil {
		env, ref = parseEnvelope(r.Header)
	}
	if ref == nil {
		ref = checkContent(r.Header)
	}
	if ref == nil {
		ref = s.checkClock(env.time)
	}
	var raw []byte
	if ref == nil {
		_ = http.NewResponseController(w).SetReadDeadline(time.Now().Add(BodyTimeout))
		raw, ref = readRaw(r)
	}
	if ref == nil {
		sum := sha256.Sum256(raw)
		switch {
		case hex.EncodeToString(sum[:]) != env.bodySHA:
			ref = refuse(CodeBodyHashMismatch, "The body's SHA-256 isn't X-Pairtime-Body-SHA256.")
		case !ed25519.Verify(dev.PublicKey, NamesMessage(dev.ID, env.reqText, env.timeText, env.bodySHA), env.signature):
			ref = badSignature(dev)
		}
	}
	if ref == nil {
		var done func()
		if done, ref = s.rate.take(dev.ID, s.o.Now()); ref == nil {
			defer done()
		}
	}
	var inflated []byte
	if ref == nil {
		inflated, ref = inflate(raw)
	}
	var nb namesBody
	if ref == nil {
		nb, ref = parseNames(inflated)
	}
	if ref != nil {
		s.refuse(w, ref)
		return
	}
	var out outcome
	err := s.o.Writer.Do(r.Context(), len(nb.set)+len(nb.del)+1, func(ctx context.Context, tx writer.Tx) error {
		var err error
		out, err = s.processNames(ctx, tx, dev, env, nb)
		return err
	})
	var refused refusedError
	switch {
	case errors.As(err, &refused):
		s.refuse(w, refused.r)
	case err != nil:
		if r.Context().Err() == nil {
			s.o.Logger.Error("ingest: storing project names failed", "err", err)
		}
		s.refuse(w, unavailable())
	case out.refusal != nil:
		s.refuse(w, out.refusal)
	default:
		s.writeJSON(w, http.StatusOK, out.response)
	}
}

// processNames runs project-names-v1 1.5 step 3 and 1.6 in the writer's
// transaction: the account check and the counter as ingest-v1 2.5 step 13
// has them, without the stored reply, then the names. A refusal after the
// first write rolls back everything the request wrote.
func (s *Service) processNames(ctx context.Context, tx writer.Tx, dev device.Device, env envelope, nb namesBody) (outcome, error) {
	foreign, err := foreignRows(ctx, tx, dev.ID, dev.Account)
	if err != nil {
		return outcome{}, err
	}
	if !foreign {
		if foreign, err = projectnames.Foreign(ctx, tx, dev.ID, dev.Account); err != nil {
			return outcome{}, err
		}
	}
	if foreign {
		return outcome{refusal: anotherAccount()}, nil
	}
	st, err := s.o.Devices.LoadState(ctx, tx, dev)
	switch {
	case errors.Is(err, device.ErrRevoked):
		return outcome{refusal: revoked()}, nil
	case err != nil:
		return outcome{}, err
	}
	if out, done := checkCounter(st, env); done {
		return out, nil
	}
	n, err := projectnames.Apply(ctx, tx, dev.Account, dev.ID, nb.set, nb.del, s.nowMS())
	if err != nil {
		return outcome{}, err
	}
	if n > projectnames.MaxPerDevice {
		return outcome{}, refusedError{refuse(CodeTooManyNames, "The server holds at most 1000 names for a device; turn some off first.")}
	}
	names, err := projectnames.OfDevice(ctx, tx, dev.ID)
	if err != nil {
		return outcome{}, err
	}
	data, err := json.Marshal(namesAnswer{Names: names})
	if err != nil {
		return outcome{}, err
	}
	st.Req = env.req
	switch err := s.o.Devices.SaveState(ctx, tx, dev, st); {
	case errors.Is(err, device.ErrRevoked):
		return outcome{}, refusedError{revoked()}
	case err != nil:
		return outcome{}, err
	}
	return outcome{response: data}, nil
}
