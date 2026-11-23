// SPDX-License-Identifier: AGPL-3.0-or-later

package ingest

import (
	"bytes"
	"encoding/json"
	"fmt"
	"math"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/some-du6e/pairtime/pkg/event"
	"github.com/some-du6e/pairtime/pkg/ledger"
)

// itemErr is one item refused: its status, its code and, for a shape code
// or bad_rule, the field it names (ingest-v1 2.8, 2.9). field is a JSON
// path in DESIGN 4.3's names and never holds a string the request sent.
type itemErr struct {
	status int
	code   string
	field  string
	named  bool
}

func shapeErr(code, field string) *itemErr {
	return &itemErr{status: 422, code: code, field: field, named: true}
}

func refused(status int, code string) *itemErr { return &itemErr{status: status, code: code} }

// The members each item may have, required and optional (ingest-v1 2.3).
// TestAllowlist checks each against DESIGN 4.3's tier-1 list and ingest-v1
// 2.3's tables, and the bucket's and the slot's against
// event.Tier1BucketFields and event.Tier1SlotFields too, agent_only
// (D-bucket-agent-only) included.
var (
	batchRequired   = []string{"b", "n", "R_b", "H_b", "prev"}
	bucketRequired  = []string{"v", "params", "minute", "device", "slots", "credited_s", "agent_running_s", "state", "witness", "rev"}
	bucketOptional  = []string{"uncredited", "agent_only"}
	slotRequired    = []string{"cr"}
	slotOptional    = []string{"p", "why", "kind", "ev", "lbl"}
	commitRequired  = []string{"sha", "parents", "project", "observed_at", "author_time", "committer_time", "actor"}
	commitOptional  = []string{"files_changed", "insertions", "deletions", "path_hmacs", "credited_seconds_since_parent", "rewrites", "agent_trailer"}
	rewriteRequired = []string{"old_sha", "new_sha"}
	labelsRequired  = []string{"client_build", "scoring_version", "params", "harnesses", "typing_module", "vm"}
	harnessRequired = []string{"name"}
	harnessOptional = []string{"version"}
)

// The enum values each string may hold, pkg/event's own lists (ingest-v1
// 2.3), so a value pkg/event adds is one ingest takes.
var (
	activities   = event.Activities()
	evidences    = event.Evidences()
	slotLabels   = event.SlotLabels()
	bucketStates = event.BucketStates()
	witnesses    = event.Witnesses()
	actors       = event.Actors()
	// agentTrailers are the agents a commit's trailer or bot author can
	// credit, the values internal/adapters/git writes (its trailerAgents,
	// Replit's trailer and botAuthors). The list is closed, because the
	// value isn't keyed and a trailer can hold a person's name; a new
	// agent reaches the server first.
	agentTrailers = []string{"aider", "claude", "lovable", "replit"}
	// labelHarnesses are the harnesses a device's labels may name: every
	// one of pkg/event's but none, which marks events no harness made.
	labelHarnesses = slices.DeleteFunc(event.Harnesses(), func(h event.Harness) bool { return h == event.HarnessNone })
)

// Bounds of lists and numbers (ingest-v1 2.3).
const (
	maxKeyLen      = 128
	maxList        = 1024
	maxUncredited  = 64
	maxAgentOnly   = 64
	maxHarnesses   = 16
	maxSeconds     = 60
	maxSlotSeconds = 15
	maxCount       = math.MaxInt32
	maxRewrites    = 2000 // internal/adapters/git's maxRebase
	maxKinds       = 8
	minUnixMS      = 1_577_836_800_000
	maxUnixMS      = 4_102_444_800_000
	maxCredited    = 1e9
)

// jsonKind returns the JSON type of raw's value by its first byte: '{',
// '[', '"', 'b' for a boolean, 'n' for null, '0' for a number, or 0.
func jsonKind(raw []byte) byte {
	raw = bytes.TrimLeft(raw, " \t\r\n")
	if len(raw) == 0 {
		return 0
	}
	switch c := raw[0]; {
	case c == '{' || c == '[' || c == '"' || c == 'n':
		return c
	case c == 't' || c == 'f':
		return 'b'
	case c == '-' || '0' <= c && c <= '9':
		return '0'
	}
	return 0
}

// join names member name of the object at path.
func join(path, name string) string {
	if path == "" {
		return name
	}
	return path + "." + name
}

// index names element i of the list at path.
func index(path string, i int) string { return fmt.Sprintf("%s[%d]", path, i) }

// eachMember calls fn with each member of the JSON object raw, in the
// order they appear and a repeated name as often as it appears, until fn
// returns false. It reads raw as it goes, so a caller that stops early
// never pays for the members after that. It reports false when raw isn't
// an object.
func eachMember(raw json.RawMessage, fn func(name string, value json.RawMessage) bool) bool {
	if jsonKind(raw) != '{' {
		return false
	}
	dec := json.NewDecoder(bytes.NewReader(raw))
	if t, err := dec.Token(); err != nil || t != json.Delim('{') {
		return false
	}
	for dec.More() {
		t, err := dec.Token()
		if err != nil {
			return false
		}
		name, ok := t.(string)
		if !ok {
			return false
		}
		var v json.RawMessage
		if err := dec.Decode(&v); err != nil {
			return false
		}
		if !fn(name, v) {
			return true
		}
	}
	t, err := dec.Token()
	return err == nil && t == json.Delim('}')
}

// memberMap reads the JSON object raw, a map of at most max members. It
// stops at the first name that repeats, which ingest-v1 2.3 refuses
// (repeated), or at member max + 1 (over), so a map past its bound costs
// no more than max members. ok is false when raw isn't an object.
func memberMap(raw json.RawMessage, max int) (m map[string]json.RawMessage, repeated, over, ok bool) {
	m = map[string]json.RawMessage{}
	ok = eachMember(raw, func(name string, v json.RawMessage) bool {
		if _, seen := m[name]; seen {
			repeated = true
			return false
		}
		if len(m) == max {
			over = true
			return false
		}
		m[name] = v
		return true
	})
	return m, repeated, over, ok
}

// object reads the object at path and checks its members against the
// allowlist, in the order they appear: a member it doesn't name is
// unknown_field at path, and a name that repeats is bad_rule at path,
// neither naming the member. Either stops the reading there, so an object
// holds at most the allowlist's members however many it has. A missing
// required one is missing_field.
func object(raw json.RawMessage, path string, required, optional []string) (map[string]json.RawMessage, *itemErr) {
	if jsonKind(raw) != '{' {
		if path == "" {
			return nil, shapeErr(CodeNotObject, "")
		}
		return nil, shapeErr(CodeBadType, path)
	}
	m := make(map[string]json.RawMessage, len(required)+len(optional))
	var e *itemErr
	ok := eachMember(raw, func(name string, v json.RawMessage) bool {
		_, seen := m[name]
		switch {
		case !slices.Contains(required, name) && !slices.Contains(optional, name):
			e = shapeErr(CodeUnknownField, path)
		case seen:
			e = shapeErr(CodeBadRule, path)
		default:
			m[name] = v
			return true
		}
		return false
	})
	switch {
	case e != nil:
		return nil, e
	case !ok:
		return nil, shapeErr(CodeBadType, path)
	}
	for _, name := range required {
		if _, ok := m[name]; !ok {
			return nil, shapeErr(CodeMissingField, join(path, name))
		}
	}
	return m, nil
}

func str(raw json.RawMessage, field string) (string, *itemErr) {
	var s string
	if jsonKind(raw) != '"' || json.Unmarshal(raw, &s) != nil {
		return "", shapeErr(CodeBadType, field)
	}
	return s, nil
}

// pattern reads a string that ok must accept.
func pattern(raw json.RawMessage, field string, ok func(string) bool) (string, *itemErr) {
	s, e := str(raw, field)
	if e != nil {
		return "", e
	}
	if !ok(s) {
		return "", shapeErr(CodeBadString, field)
	}
	return s, nil
}

// enumOf reads a string that must be one of values.
func enumOf[T ~string](raw json.RawMessage, field string, values []T) (T, *itemErr) {
	s, e := pattern(raw, field, func(s string) bool { return slices.Contains(values, T(s)) })
	return T(s), e
}

// integer reads a JSON number with no fraction or exponent, from lo to hi.
func integer(raw json.RawMessage, field string, lo, hi int64) (int64, *itemErr) {
	if jsonKind(raw) != '0' {
		return 0, shapeErr(CodeBadType, field)
	}
	s := string(bytes.TrimSpace(raw))
	digits := strings.TrimPrefix(s, "-")
	if digits == "" || (len(digits) > 1 && digits[0] == '0') || strings.Trim(digits, "0123456789") != "" {
		return 0, shapeErr(CodeBadNumber, field)
	}
	v, err := strconv.ParseInt(s, 10, 64)
	if err != nil || v < lo || v > hi {
		return 0, shapeErr(CodeBadNumber, field)
	}
	return v, nil
}

// number reads a finite JSON number from lo to hi. A negative zero comes
// back as zero, so a bucket stores one spelling of it.
func number(raw json.RawMessage, field string, lo, hi float64) (float64, *itemErr) {
	if jsonKind(raw) != '0' {
		return 0, shapeErr(CodeBadType, field)
	}
	v, err := strconv.ParseFloat(string(bytes.TrimSpace(raw)), 64)
	if err != nil || math.IsNaN(v) || math.IsInf(v, 0) || v < lo || v > hi {
		return 0, shapeErr(CodeBadNumber, field)
	}
	return v + 0, nil
}

func boolean(raw json.RawMessage, field string) (bool, *itemErr) {
	switch string(bytes.TrimSpace(raw)) {
	case "true":
		return true, nil
	case "false":
		return false, nil
	}
	return false, shapeErr(CodeBadType, field)
}

// array reads a list of at most max entries. It stops at entry max + 1,
// so a list past its bound costs no more than max entries do.
func array(raw json.RawMessage, field string, max int) ([]json.RawMessage, *itemErr) {
	if jsonKind(raw) != '[' {
		return nil, shapeErr(CodeBadType, field)
	}
	dec := json.NewDecoder(bytes.NewReader(raw))
	if t, err := dec.Token(); err != nil || t != json.Delim('[') {
		return nil, shapeErr(CodeBadType, field)
	}
	var a []json.RawMessage
	for dec.More() {
		if len(a) == max {
			return nil, shapeErr(CodeTooMany, field)
		}
		var v json.RawMessage
		if err := dec.Decode(&v); err != nil {
			return nil, shapeErr(CodeBadType, field)
		}
		a = append(a, v)
	}
	return a, nil
}

// isKey reports whether s is an opaque key: 1 to 128 ASCII letters, digits,
// '_' or '-' (ledger-v1 1; pkg/event's isKey).
func isKey(s string) bool {
	if len(s) == 0 || len(s) > maxKeyLen {
		return false
	}
	for i := 0; i < len(s); i++ {
		c := s[i]
		if !('a' <= c && c <= 'z' || 'A' <= c && c <= 'Z' || '0' <= c && c <= '9' || c == '_' || c == '-') {
			return false
		}
	}
	return true
}

// isHex reports whether s is n lowercase hex digits.
func isHex(s string, n int) bool {
	if len(s) != n {
		return false
	}
	for i := 0; i < len(s); i++ {
		if c := s[i]; !('0' <= c && c <= '9' || 'a' <= c && c <= 'f') {
			return false
		}
	}
	return true
}

func isGitID(s string) bool { return isHex(s, 40) || isHex(s, 64) }

// isSemver reports whether s is MAJOR.MINOR.PATCH with 1 to 9 digits in
// each part, as pkg/event's scoring versions are.
func isSemver(s string) bool {
	// Three parts of nine digits and two dots: refused before Split, so a
	// long string of dots costs no slice of parts.
	if len(s) > 3*9+2 {
		return false
	}
	parts := strings.Split(s, ".")
	if len(parts) != 3 {
		return false
	}
	for _, p := range parts {
		if len(p) == 0 || len(p) > 9 || strings.Trim(p, "0123456789") != "" {
			return false
		}
	}
	return true
}

// isParams reports whether s is "sha256:" and 64 lowercase hex digits.
func isParams(s string) bool {
	h, ok := strings.CutPrefix(s, "sha256:")
	return ok && isHex(h, 64)
}

// isVersion reports whether s is a version such as 2.1.327, v0.1.0-alpha.1
// or 0.1.0+3dbf6d4, in D-version-grammar's form, which pkg/event checks
// for a harness_version too. A bare word isn't one, so no file, folder or
// host name reaches the labels.
func isVersion(s string) bool { return event.IsVersion(s) }

// minuteLayout is a bucket's minute, as encoding/json writes a whole UTC
// minute of a time.Time.
const minuteLayout = "2006-01-02T15:04:05Z"

// parseMinute reads YYYY-MM-DDTHH:MM:00Z, from 2020 to 2099.
func parseMinute(s string) (time.Time, bool) {
	if len(s) != len(minuteLayout) {
		return time.Time{}, false
	}
	t, err := time.Parse(minuteLayout, s)
	if err != nil || t.Format(minuteLayout) != s || t.Second() != 0 || t.Year() < 2020 || t.Year() > 2099 {
		return time.Time{}, false
	}
	return t.UTC(), true
}

// batchRoot is DESIGN 4.3's {b, n, R_b, H_b, prev}.
type batchRoot struct {
	b, n             uint64
	root, head, prev ledger.Hash
}

func hash(raw json.RawMessage, field string) (ledger.Hash, *itemErr) {
	var h ledger.Hash
	s, e := str(raw, field)
	if e != nil {
		return h, e
	}
	if h.UnmarshalText([]byte(s)) != nil {
		return h, shapeErr(CodeBadString, field)
	}
	return h, nil
}

// parseBatchRoot checks a batch root's forms (ingest-v1 2.3.1). Once b
// has passed its form, br.b holds it, even when another member fails.
func parseBatchRoot(raw json.RawMessage) (batchRoot, *itemErr) {
	var br batchRoot
	m, e := object(raw, "", batchRequired, nil)
	if e != nil {
		if raw, ok := objectMember(raw, "b"); ok {
			if b, e := integer(raw, "b", 1, math.MaxInt64); e == nil {
				br.b = uint64(b)
			}
		}
		return br, e
	}
	b, e := integer(m["b"], "b", 1, math.MaxInt64)
	if e != nil {
		return br, e
	}
	br.b = uint64(b)
	n, e := integer(m["n"], "n", 0, math.MaxInt64)
	if e != nil {
		return br, e
	}
	br.n = uint64(n)
	if br.root, e = hash(m["R_b"], "R_b"); e != nil {
		return br, e
	}
	if br.head, e = hash(m["H_b"], "H_b"); e != nil {
		return br, e
	}
	if br.prev, e = hash(m["prev"], "prev"); e != nil {
		return br, e
	}
	return br, nil
}

// parseBucket checks a minute bucket's forms (ingest-v1 2.3.2) and returns
// it as pkg/event's MinuteBucket, agent_only (D-bucket-agent-only)
// included. The caller runs Validate for the rules across fields.
func parseBucket(raw json.RawMessage) (event.MinuteBucket, *itemErr) {
	var rv event.MinuteBucket
	m, e := object(raw, "", bucketRequired, bucketOptional)
	if e != nil {
		return rv, e
	}
	if raw, ok := m["agent_only"]; ok {
		if rv.AgentOnly, e = parseAgentOnly(raw); e != nil {
			return rv, e
		}
	}
	mb := &rv
	if mb.V, e = pattern(m["v"], "v", isSemver); e != nil {
		return rv, e
	}
	if mb.Params, e = pattern(m["params"], "params", isParams); e != nil {
		return rv, e
	}
	s, e := str(m["minute"], "minute")
	if e != nil {
		return rv, e
	}
	var ok bool
	if mb.Minute, ok = parseMinute(s); !ok {
		return rv, shapeErr(CodeBadString, "minute")
	}
	if mb.Device, e = pattern(m["device"], "device", isKey); e != nil {
		return rv, e
	}
	slots, e := array(m["slots"], "slots", 4)
	if e != nil {
		return rv, e
	}
	mb.Slots = make([]event.BucketSlot, len(slots))
	for i, raw := range slots {
		if mb.Slots[i], e = parseSlot(raw, index("slots", i)); e != nil {
			return rv, e
		}
	}
	if mb.CreditedS, e = number(m["credited_s"], "credited_s", 0, maxSeconds); e != nil {
		return rv, e
	}
	if raw, ok := m["uncredited"]; ok {
		if mb.Uncredited, e = parseUncredited(raw); e != nil {
			return rv, e
		}
	}
	if mb.AgentRunningS, e = number(m["agent_running_s"], "agent_running_s", 0, maxSeconds); e != nil {
		return rv, e
	}
	if mb.State, e = enumOf(m["state"], "state", bucketStates); e != nil {
		return rv, e
	}
	if mb.Witness, e = enumOf(m["witness"], "witness", witnesses); e != nil {
		return rv, e
	}
	rev, e := integer(m["rev"], "rev", 0, math.MaxUint32)
	if e != nil {
		return rv, e
	}
	mb.Rev = uint32(rev)
	return rv, nil
}

// parseAgentOnly reads agent_only{p: s}: each member's name a project key,
// as slots[].p, and its value seconds, 0 to 60 in steps of 0.5
// (D-bucket-agent-only). Names are checked in sorted order, so the same
// bucket always fails the same way.
func parseAgentOnly(raw json.RawMessage) (map[string]float64, *itemErr) {
	m, repeated, over, ok := memberMap(raw, maxAgentOnly)
	switch {
	case !ok:
		return nil, shapeErr(CodeBadType, "agent_only")
	case repeated:
		return nil, shapeErr(CodeBadRule, "agent_only")
	case over:
		return nil, shapeErr(CodeTooMany, "agent_only")
	}
	names := make([]string, 0, len(m))
	for name := range m {
		names = append(names, name)
	}
	slices.Sort(names)
	out := make(map[string]float64, len(m))
	for _, name := range names {
		if !isKey(name) {
			return nil, shapeErr(CodeBadString, "agent_only")
		}
		v, e := number(m[name], "agent_only", 0, maxSeconds)
		if e != nil {
			return nil, e
		}
		if v*2 != math.Trunc(v*2) {
			return nil, shapeErr(CodeBadNumber, "agent_only")
		}
		out[name] = v
	}
	return out, nil
}

func parseSlot(raw json.RawMessage, path string) (event.BucketSlot, *itemErr) {
	var s event.BucketSlot
	m, e := object(raw, path, slotRequired, slotOptional)
	if e != nil {
		return s, e
	}
	if s.Credited, e = number(m["cr"], join(path, "cr"), 0, maxSlotSeconds); e != nil {
		return s, e
	}
	if s.Credited*2 != math.Trunc(s.Credited*2) {
		return s, shapeErr(CodeBadNumber, join(path, "cr"))
	}
	if raw, ok := m["p"]; ok {
		if s.Project, e = pattern(raw, join(path, "p"), isKey); e != nil {
			return s, e
		}
	}
	if raw, ok := m["why"]; ok {
		why, e := pattern(raw, join(path, "why"), func(v string) bool { return event.Reason(v).Credited() })
		if e != nil {
			return s, e
		}
		s.Why = event.Reason(why)
	}
	if raw, ok := m["kind"]; ok {
		if s.Kind, e = enumOf(raw, join(path, "kind"), activities); e != nil {
			return s, e
		}
	}
	if raw, ok := m["ev"]; ok {
		if s.Ev, e = enumOf(raw, join(path, "ev"), evidences); e != nil {
			return s, e
		}
	}
	if raw, ok := m["lbl"]; ok {
		field := join(path, "lbl")
		ls, e := array(raw, field, len(slotLabels))
		if e != nil {
			return s, e
		}
		for i, raw := range ls {
			l, e := enumOf(raw, index(field, i), slotLabels)
			if e != nil {
				return s, e
			}
			s.Lbl = append(s.Lbl, l)
		}
	}
	return s, nil
}

// parseUncredited reads uncredited{code: s}. Its member names are reason
// codes, checked in sorted order so the same bucket always fails the same
// way.
func parseUncredited(raw json.RawMessage) (map[event.Reason]float64, *itemErr) {
	m, repeated, over, ok := memberMap(raw, maxUncredited)
	switch {
	case !ok:
		return nil, shapeErr(CodeBadType, "uncredited")
	case repeated:
		return nil, shapeErr(CodeBadRule, "uncredited")
	case over:
		return nil, shapeErr(CodeTooMany, "uncredited")
	}
	out := make(map[event.Reason]float64, len(m))
	names := make([]string, 0, len(m))
	for name := range m {
		names = append(names, name)
	}
	slices.Sort(names)
	for _, name := range names {
		if !event.Reason(name).Uncredited() {
			return nil, shapeErr(CodeBadString, "uncredited")
		}
		v, e := number(m[name], "uncredited", 0, maxSeconds)
		if e != nil {
			return nil, e
		}
		out[event.Reason(name)] = v
	}
	return out, nil
}

// checkBucket checks a bucket's forms, then the rules across its fields
// that event.MinuteBucket.Validate holds, which fail as bad_rule
// (ingest-v1 2.3.2).
func checkBucket(raw json.RawMessage) (event.MinuteBucket, *itemErr) {
	rv, e := parseBucket(raw)
	if e != nil {
		return rv, e
	}
	if err := rv.Validate(); err != nil {
		return rv, shapeErr(CodeBadRule, ruleField(err))
	}
	return rv, nil
}

// mapFields are the bucket's members that are maps. pkg/event names a
// map's member field[key], as in agent_only[p_3f9a] or
// uncredited[X.absent].
var mapFields = []string{"uncredited", "agent_only"}

// ruleField returns the field pkg/event's first error names, or "" when
// it isn't a plain JSON path. Each of its errors reads "field: message",
// and the message, which can quote values, is dropped. A map's member is
// named by the map alone, so no result repeats a key the request sent
// (ingest-v1 2.8).
func ruleField(err error) string {
	msg, _, _ := strings.Cut(err.Error(), "\n")
	field, _, ok := strings.Cut(msg, ": ")
	if !ok {
		return ""
	}
	for _, m := range mapFields {
		if strings.HasPrefix(field, m+"[") {
			field = m
		}
	}
	if !isPath(field) {
		return ""
	}
	return field
}

// isPath reports whether s is a JSON path of lowercase names, dots and
// [N] or [] indexes, such as slots[2].lbl[0] or slots[].cr. An index holds
// digits only, so a path never carries a key.
func isPath(s string) bool {
	if s == "" || len(s) > 64 {
		return false
	}
	inIndex := false
	for i := 0; i < len(s); i++ {
		switch c := s[i]; {
		case c == '[' && !inIndex:
			inIndex = true
		case c == ']' && inIndex:
			inIndex = false
		case '0' <= c && c <= '9':
		case !inIndex && ('a' <= c && c <= 'z' || c == '_' || c == '.'):
		default:
			return false
		}
	}
	return !inIndex
}

// commitRecord is DESIGN 4.3's commit record as the server stores it, its
// members in that list's order (ingest-v1 2.3.3). The three counts are
// absent when the client couldn't read the commit's shortstat.
type commitRecord struct {
	SHA           string   `json:"sha"`
	Parents       []string `json:"parents"`
	Project       string   `json:"project"`
	ObservedAt    int64    `json:"observed_at"`
	AuthorTime    int64    `json:"author_time"`
	CommitterTime int64    `json:"committer_time"`
	FilesChanged  *int64   `json:"files_changed,omitempty"`
	Insertions    *int64   `json:"insertions,omitempty"`
	Deletions     *int64   `json:"deletions,omitempty"`
	PathHMACs     []string `json:"path_hmacs,omitempty"`
	// CreditedSecondsSinceParent is split by activity kind (DESIGN 5.8;
	// SM 5.3).
	CreditedSecondsSinceParent map[event.Activity]float64 `json:"credited_seconds_since_parent,omitempty"`
	Rewrites                   []rewrite                  `json:"rewrites,omitempty"`
	Actor                      string                     `json:"actor"`
	AgentTrailer               string                     `json:"agent_trailer,omitempty"`
}

type rewrite struct {
	OldSHA string `json:"old_sha"`
	NewSHA string `json:"new_sha"`
}

// keys reads a list of at most maxList distinct strings, each accepted by
// ok. A string that repeats is bad_rule, naming the list.
func keys(raw json.RawMessage, field string, ok func(string) bool) ([]string, *itemErr) {
	a, e := array(raw, field, maxList)
	if e != nil {
		return nil, e
	}
	out := make([]string, 0, len(a))
	for i, raw := range a {
		s, e := pattern(raw, index(field, i), ok)
		if e != nil {
			return nil, e
		}
		out = append(out, s)
	}
	if hasRepeat(out) {
		return nil, shapeErr(CodeBadRule, field)
	}
	return out, nil
}

// hasRepeat reports whether a value appears twice in vs.
func hasRepeat[T comparable](vs []T) bool {
	seen := make(map[T]bool, len(vs))
	for _, v := range vs {
		if seen[v] {
			return true
		}
		seen[v] = true
	}
	return false
}

// objectMember returns member name of the object raw, when raw is an
// object that has it exactly once.
func objectMember(raw json.RawMessage, name string) (json.RawMessage, bool) {
	var v json.RawMessage
	n := 0
	ok := eachMember(raw, func(k string, value json.RawMessage) bool {
		if k == name {
			v, n = value, n+1
		}
		return n < 2
	})
	return v, ok && n == 1
}

// optCount reads one of a commit's optional counts.
func optCount(m map[string]json.RawMessage, field string) (*int64, *itemErr) {
	raw, ok := m[field]
	if !ok {
		return nil, nil
	}
	v, e := integer(raw, field, 0, maxCount)
	if e != nil {
		return nil, e
	}
	return &v, nil
}

// parseKinds reads credited_seconds_since_parent{kind: s}: each member's
// name an activity kind of SM 5.1, and its value seconds, 0 to 1e9.
func parseKinds(raw json.RawMessage, field string) (map[event.Activity]float64, *itemErr) {
	m, repeated, over, ok := memberMap(raw, maxKinds)
	switch {
	case !ok:
		return nil, shapeErr(CodeBadType, field)
	case repeated:
		return nil, shapeErr(CodeBadRule, field)
	case over:
		return nil, shapeErr(CodeTooMany, field)
	}
	names := make([]string, 0, len(m))
	for name := range m {
		names = append(names, name)
	}
	slices.Sort(names)
	out := make(map[event.Activity]float64, len(m))
	for _, name := range names {
		if !slices.Contains(activities, event.Activity(name)) {
			return nil, shapeErr(CodeBadString, field)
		}
		v, e := number(m[name], field, 0, maxCredited)
		if e != nil {
			return nil, e
		}
		out[event.Activity(name)] = v
	}
	return out, nil
}

// parseCommit checks a commit record's forms (ingest-v1 2.3.3).
func parseCommit(raw json.RawMessage) (commitRecord, *itemErr) {
	var c commitRecord
	m, e := object(raw, "", commitRequired, commitOptional)
	if e != nil {
		return c, e
	}
	if c.SHA, e = pattern(m["sha"], "sha", isGitID); e != nil {
		return c, e
	}
	c.Parents = []string{}
	if jsonKind(m["parents"]) != 'n' {
		if c.Parents, e = keys(m["parents"], "parents", isGitID); e != nil {
			return c, e
		}
	}
	if c.Project, e = pattern(m["project"], "project", isKey); e != nil {
		return c, e
	}
	if c.ObservedAt, e = integer(m["observed_at"], "observed_at", minUnixMS, maxUnixMS-1); e != nil {
		return c, e
	}
	if c.AuthorTime, e = integer(m["author_time"], "author_time", 0, math.MaxInt64); e != nil {
		return c, e
	}
	if c.CommitterTime, e = integer(m["committer_time"], "committer_time", 0, math.MaxInt64); e != nil {
		return c, e
	}
	if c.FilesChanged, e = optCount(m, "files_changed"); e != nil {
		return c, e
	}
	if c.Insertions, e = optCount(m, "insertions"); e != nil {
		return c, e
	}
	if c.Deletions, e = optCount(m, "deletions"); e != nil {
		return c, e
	}
	if raw, ok := m["path_hmacs"]; ok {
		if c.PathHMACs, e = keys(raw, "path_hmacs", isKey); e != nil {
			return c, e
		}
	}
	if raw, ok := m["credited_seconds_since_parent"]; ok {
		if c.CreditedSecondsSinceParent, e = parseKinds(raw, "credited_seconds_since_parent"); e != nil {
			return c, e
		}
	}
	if raw, ok := m["rewrites"]; ok {
		a, e := array(raw, "rewrites", maxRewrites)
		if e != nil {
			return c, e
		}
		for i, raw := range a {
			path := index("rewrites", i)
			rm, e := object(raw, path, rewriteRequired, nil)
			if e != nil {
				return c, e
			}
			var r rewrite
			if r.OldSHA, e = pattern(rm["old_sha"], join(path, "old_sha"), isGitID); e != nil {
				return c, e
			}
			if r.NewSHA, e = pattern(rm["new_sha"], join(path, "new_sha"), isGitID); e != nil {
				return c, e
			}
			c.Rewrites = append(c.Rewrites, r)
		}
		if hasRepeat(c.Rewrites) {
			return c, shapeErr(CodeBadRule, "rewrites")
		}
	}
	actor, e := enumOf(m["actor"], "actor", actors)
	if e != nil {
		return c, e
	}
	c.Actor = string(actor)
	if raw, ok := m["agent_trailer"]; ok {
		if c.AgentTrailer, e = enumOf(raw, "agent_trailer", agentTrailers); e != nil {
			return c, e
		}
	}
	return c, nil
}

// labels is DESIGN 4.3's labels as the server stores them (ingest-v1
// 2.3.4).
type labels struct {
	ClientBuild    string         `json:"client_build"`
	ScoringVersion string         `json:"scoring_version"`
	Params         string         `json:"params"`
	Harnesses      []harnessLabel `json:"harnesses"`
	TypingModule   bool           `json:"typing_module"`
	VM             bool           `json:"vm"`
}

type harnessLabel struct {
	Name string `json:"name"`
	// Version is absent when the harness didn't say, or said it in a form
	// the labels don't take.
	Version string `json:"version,omitempty"`
}

// parseLabels checks labels' forms (ingest-v1 2.3.4).
func parseLabels(raw json.RawMessage) (labels, *itemErr) {
	var l labels
	m, e := object(raw, "", labelsRequired, nil)
	if e != nil {
		return l, e
	}
	if l.ClientBuild, e = pattern(m["client_build"], "client_build", isVersion); e != nil {
		return l, e
	}
	if l.ScoringVersion, e = pattern(m["scoring_version"], "scoring_version", isSemver); e != nil {
		return l, e
	}
	if l.Params, e = pattern(m["params"], "params", isParams); e != nil {
		return l, e
	}
	hs, e := array(m["harnesses"], "harnesses", maxHarnesses)
	if e != nil {
		return l, e
	}
	l.Harnesses = make([]harnessLabel, 0, len(hs))
	for i, raw := range hs {
		path := index("harnesses", i)
		hm, e := object(raw, path, harnessRequired, harnessOptional)
		if e != nil {
			return l, e
		}
		name, e := enumOf(hm["name"], join(path, "name"), labelHarnesses)
		if e != nil {
			return l, e
		}
		var version string
		if raw, ok := hm["version"]; ok {
			if version, e = pattern(raw, join(path, "version"), isVersion); e != nil {
				return l, e
			}
		}
		if slices.ContainsFunc(l.Harnesses, func(h harnessLabel) bool { return h.Name == string(name) }) {
			return l, &itemErr{status: 422, code: CodeBadRule, field: "harnesses", named: true}
		}
		l.Harnesses = append(l.Harnesses, harnessLabel{Name: string(name), Version: version})
	}
	if l.TypingModule, e = boolean(m["typing_module"], "typing_module"); e != nil {
		return l, e
	}
	if l.VM, e = boolean(m["vm"], "vm"); e != nil {
		return l, e
	}
	return l, nil
}
