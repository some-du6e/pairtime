// SPDX-License-Identifier: AGPL-3.0-or-later

package uploader

import (
	"bytes"
	"compress/gzip"
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"io"
	"maps"
	"net/http"
	"regexp"
	"slices"
	"strconv"

	"github.com/some-du6e/pairtime/pkg/projectname"
)

// projectKeyForm is an opaque key (ingest-v1 1).
var projectKeyForm = regexp.MustCompile(`^[A-Za-z0-9_-]{1,128}$`)

// projectNames is POST /api/v1/projects/names as
// docs/spec/project-names-v1.md says, written from the spec alone and
// sharing none of the uploader's wire types: ingest-v1 2.5's checks with
// M_names in place of M, the body's shape (1.3, 1.5), the device's one
// request counter, which uploads move too, and the answer, every name the
// device has, in key order, the keys of those hidden on the dashboard, and
// the code of each of its keys the test gave the server (1.6). The server
// stores no reply for a names request.
func (f *fakeIngest) projectNames(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.namesPosts++
	fault := f.namesFaults[f.namesPosts]
	// namesThen's step runs before the answer goes, or the connection
	// drops, so the uploader never acts on the answer before it.
	then, ran := f.namesThen[f.namesPosts], false
	runThen := func() {
		if then != nil && !ran {
			ran = true
			then()
		}
	}
	defer runThen()
	raw, err := io.ReadAll(io.LimitReader(r.Body, 262_145))
	if err != nil || fault == "down" {
		drop(w)
		return
	}
	if fault == "404" {
		// A server without the endpoint, as an older pairtimed, which
		// reads nothing of the request: its ServeMux's own plain 404,
		// without X-Pairtime-Time.
		w.Header().Del("X-Pairtime-Time")
		http.NotFound(w, r)
		return
	}
	if !f.auth(w, r) {
		return
	}
	h := r.Header
	key, reqS, timeS, hash, sigS := h.Get("Idempotency-Key"), h.Get("X-Pairtime-Req"), h.Get("X-Pairtime-Time"), h.Get("X-Pairtime-Body-SHA256"), h.Get("X-Pairtime-Signature")
	switch {
	case !uuid7Form.MatchString(key):
		f.refuse(w, 400, "idempotency_key_malformed")
		return
	case !decimalForm.MatchString(reqS):
		f.refuse(w, 400, "counter_malformed")
		return
	case !decimalForm.MatchString(timeS):
		f.refuse(w, 400, "time_malformed")
		return
	case !hashForm.MatchString(hash):
		f.refuse(w, 400, "body_hash_malformed")
		return
	case len(sigS) != len("ed25519=")+88 || sigS[:8] != "ed25519=":
		f.refuse(w, 400, "signature_malformed")
		return
	case h.Get("Content-Type") != "application/json":
		f.refuse(w, 415, "content_type_unsupported")
		return
	case h.Get("Content-Encoding") != "gzip":
		f.refuse(w, 415, "encoding_unsupported")
		return
	case h.Get("User-Agent") == "":
		f.refuse(w, 400, "user_agent_missing")
		return
	}
	t, _ := strconv.ParseInt(timeS, 10, 64)
	if d := t - f.now().UnixMilli(); d > 300_000 || d < -300_000 {
		f.refuse(w, 401, "clock_skew")
		return
	}
	sum := sha256.Sum256(raw)
	if hex.EncodeToString(sum[:]) != hash {
		f.refuse(w, 400, "body_hash_mismatch")
		return
	}
	sig, err := base64.StdEncoding.DecodeString(sigS[8:])
	m := "pairtime-project-names-v1\n" + f.device + "\n" + reqS + "\n" + timeS + "\n" + hash
	if err != nil || !ed25519.Verify(f.devKey, []byte(m), sig) {
		f.refuse(w, 401, "signature_invalid")
		return
	}
	req, _ := strconv.ParseUint(reqS, 10, 64)
	f.counters = append(f.counters, req)
	if f.head == nil {
		f.namesEarly++
	}
	br := bytes.NewReader(raw)
	zr, err := gzip.NewReader(br)
	if err != nil {
		f.refuse(w, 400, "gzip_invalid")
		return
	}
	zr.Multistream(false)
	js, err := io.ReadAll(io.LimitReader(zr, 4_194_305))
	if err != nil || br.Len() != 0 {
		f.refuse(w, 400, "gzip_invalid")
		return
	}
	f.namesBodies = append(f.namesBodies, js)
	switch fault {
	case "503":
		f.refuse(w, 503, "unavailable")
		return
	case "429":
		w.Header().Set("Retry-After", "1")
		f.refuse(w, 429, "rate_limited")
		return
	case "html":
		// A proxy's page, as a firewall's block: no X-Pairtime-Time, no
		// JSON.
		w.Header().Del("X-Pairtime-Time")
		w.Header().Set("Content-Type", "text/html")
		w.WriteHeader(403)
		io.WriteString(w, "<html>blocked</html>")
		return
	case "stale":
		// Another computer with the same identity took higher counters.
		f.counter += 1000
		f.refuse(w, 409, "counter_stale")
		return
	case "label_invalid":
		f.refuse(w, 422, "label_invalid")
		return
	case "bodyhash":
		// The body changed on the way, as the server sees it: refused
		// before anything is stored.
		f.refuse(w, 400, "body_hash_mismatch")
		return
	}
	var top map[string]json.RawMessage
	if err := json.Unmarshal(js, &top); err != nil {
		f.refuse(w, 400, "json_invalid")
		return
	}
	var set []map[string]json.RawMessage
	var del []string
	for k, v := range top {
		var err error
		switch k {
		case "set":
			err = json.Unmarshal(v, &set)
		case "delete":
			err = json.Unmarshal(v, &del)
		default:
			f.refuse(w, 422, "body_shape")
			return
		}
		if err != nil {
			f.refuse(w, 422, "body_shape")
			return
		}
	}
	if len(set)+len(del) > 200 {
		f.refuse(w, 413, "too_many_names")
		return
	}
	seen := map[string]bool{}
	type item struct{ project, label string }
	var items []item
	for _, it := range set {
		var project, label string
		if len(it) != 2 || json.Unmarshal(it["project"], &project) != nil || json.Unmarshal(it["label"], &label) != nil {
			f.refuse(w, 422, "body_shape")
			return
		}
		items = append(items, item{project, label})
	}
	keys := slices.Clone(del)
	for _, it := range items {
		keys = append(keys, it.project)
	}
	for _, k := range keys {
		if !projectKeyForm.MatchString(k) {
			f.refuse(w, 422, "project_invalid")
			return
		}
		if seen[k] {
			f.refuse(w, 422, "project_repeated")
			return
		}
		seen[k] = true
	}
	for _, it := range items {
		if !projectname.ValidLabel(it.label) {
			f.refuse(w, 422, "label_invalid")
			return
		}
	}
	if req <= f.counter {
		f.refuse(w, 409, "counter_stale")
		return
	}
	for _, k := range del {
		delete(f.names, k)
		delete(f.namesHidden, k)
	}
	for _, it := range items {
		f.names[it.project] = it.label
		delete(f.namesHidden, it.project)
	}
	f.counter = req
	switch fault {
	case "lost":
		runThen()
		drop(w)
		return
	case "garbled":
		// pairtimed's 200, stored, whose body doesn't read.
		runThen()
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(200)
		io.WriteString(w, "{not json")
		return
	case "stored-502":
		// A proxy's 502 after pairtimed stored the request, as when its
		// connection to the origin drops before the answer: no
		// X-Pairtime-Time, and no resend by the transport, since an answer
		// came.
		runThen()
		w.Header().Del("X-Pairtime-Time")
		w.Header().Set("Content-Type", "text/html")
		w.WriteHeader(502)
		io.WriteString(w, "<html>bad gateway</html>")
		return
	}
	out := []map[string]string{}
	for _, k := range slices.Sorted(maps.Keys(f.names)) {
		out = append(out, map[string]string{"project": k, "label": f.names[k]})
	}
	hidden := slices.Sorted(maps.Keys(f.namesHidden))
	if hidden == nil {
		hidden = []string{}
	}
	codes := map[string]string{}
	maps.Copy(codes, f.codes)
	f.json(w, 200, map[string]any{"names": out, "hidden": hidden, "codes": codes})
}
