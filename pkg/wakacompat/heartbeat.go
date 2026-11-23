// SPDX-License-Identifier: AGPL-3.0-or-later

package wakacompat

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"math"
	"strconv"
	"strings"
)

// Heartbeat is one heartbeat: the fields wakatime-cli v2.28.4 writes (its
// pkg/heartbeat.Heartbeat), and line_additions and line_deletions, which
// Hackatime keeps too (HEARTBEAT_KEYS, hackatime_controller.rb lines
// 7-12). A field the sender left out is empty, zero or nil, and the
// pointer fields keep left out apart from 0, as wakatime-cli's do. The
// JSON names are the wire's, and the field order is the one Key hashes.
type Heartbeat struct {
	Entity   string `json:"entity"`
	Type     string `json:"type,omitempty"`
	Category string `json:"category,omitempty"`
	// Time is Unix epoch seconds with fractions, as Seconds reads it.
	Time             float64  `json:"time"`
	Project          *string  `json:"project,omitempty"`
	Branch           *string  `json:"branch,omitempty"`
	Language         *string  `json:"language,omitempty"`
	Dependencies     []string `json:"dependencies,omitempty"`
	Lines            *int64   `json:"lines,omitempty"`
	LineNo           *int64   `json:"lineno,omitempty"`
	CursorPos        *int64   `json:"cursorpos,omitempty"`
	LineAdditions    *int64   `json:"line_additions,omitempty"`
	LineDeletions    *int64   `json:"line_deletions,omitempty"`
	ProjectRootCount *int64   `json:"project_root_count,omitempty"`
	IsWrite          *bool    `json:"is_write,omitempty"`
	UserAgent        string   `json:"user_agent,omitempty"`
	// AISession and the fields after it come from wakatime-cli's AI sync,
	// which makes heartbeats from an AI agent's transcripts and sets
	// AISession on every one (HC 2.2;
	// docs/errata/2026-11-23-wakatime-cli-2.28.4.md). A plugin can set
	// AILineChanges and HumanLineChanges through wakatime-cli, and none of
	// the others.
	AISession           string `json:"ai_session,omitempty"`
	AISubscriptionPlan  string `json:"ai_subscription_plan,omitempty"`
	AIPromptLength      int64  `json:"ai_prompt_length,omitempty"`
	AILineChanges       *int64 `json:"ai_line_changes,omitempty"`
	HumanLineChanges    *int64 `json:"human_line_changes,omitempty"`
	AIInputTokens       int64  `json:"ai_input_tokens,omitempty"`
	AIOutputTokens      int64  `json:"ai_output_tokens,omitempty"`
	AICachedInputTokens int64  `json:"ai_cached_input_tokens,omitempty"`
}

// Refusal says why a heartbeat can't be taken, in the two parts of
// Hackatime's answer for a refused one (heartbeat_error.schema.json):
// Type names the kind of problem, as Hackatime's names its exception's
// class, and Message says what was wrong. Neither repeats anything the
// heartbeat holds.
type Refusal struct {
	Type    string
	Message string
}

func (r *Refusal) Error() string { return "wakacompat: " + r.Type + ": " + r.Message }

// The kinds of Refusal.
const (
	// InvalidHeartbeat is an entry that isn't a JSON object, or a
	// heartbeat with no entity.
	InvalidHeartbeat = "InvalidHeartbeat"
	// InvalidHeartbeatTime is a heartbeat whose time Seconds can't read,
	// as Hackatime's InvalidHeartbeatTime is (heartbeat_ingest.rb lines
	// 364-395).
	InvalidHeartbeatTime = "InvalidHeartbeatTime"
)

// maxInt bounds the integer fields: 2^53, past which a JSON number can't
// hold every integer, so no real count comes near it.
const maxInt = 1 << 53

// Parse reads one heartbeat from a JSON value. It reads as leniently as
// Hackatime's type casts do: a number may be a JSON number or a string of
// one, an integer field takes a number with no fraction, a boolean may be
// the string "true" or "false", dependencies may be an array of strings or
// one comma-separated string, user_agent falls back to plugin, as
// Hackatime's does (heartbeat_ingest.rb line 112), and a field it can't
// read is left out instead of refusing the heartbeat. Unknown fields are
// ignored. It refuses, with a *Refusal, only what can't be a heartbeat: a
// value that isn't an object, a heartbeat with no entity, and a time
// Seconds can't read.
func Parse(raw []byte) (Heartbeat, error) {
	var f map[string]json.RawMessage
	if err := json.Unmarshal(raw, &f); err != nil || f == nil {
		return Heartbeat{}, &Refusal{InvalidHeartbeat, "a heartbeat is a JSON object"}
	}
	var h Heartbeat
	h.Entity, _ = stringOf(f["entity"])
	if h.Entity == "" {
		return Heartbeat{}, &Refusal{InvalidHeartbeat, "entity is missing"}
	}
	t, ok := numberOf(f["time"])
	if !ok {
		return Heartbeat{}, &Refusal{InvalidHeartbeatTime, "time must be a Unix epoch timestamp"}
	}
	if h.Time, ok = Seconds(t); !ok {
		return Heartbeat{}, &Refusal{InvalidHeartbeatTime,
			fmt.Sprintf("time must be Unix epoch seconds between %d and %d", saneMin, saneMax-1)}
	}
	h.Type, _ = stringOf(f["type"])
	h.Category, _ = stringOf(f["category"])
	h.Project = stringPtr(f["project"])
	h.Branch = stringPtr(f["branch"])
	h.Language = stringPtr(f["language"])
	h.Dependencies = dependenciesOf(f["dependencies"])
	h.Lines = intPtr(f["lines"])
	h.LineNo = intPtr(f["lineno"])
	h.CursorPos = intPtr(f["cursorpos"])
	h.LineAdditions = intPtr(f["line_additions"])
	h.LineDeletions = intPtr(f["line_deletions"])
	h.ProjectRootCount = intPtr(f["project_root_count"])
	h.IsWrite = boolPtr(f["is_write"])
	h.UserAgent, _ = stringOf(f["user_agent"])
	if h.UserAgent == "" {
		h.UserAgent, _ = stringOf(f["plugin"])
	}
	h.AISession, _ = stringOf(f["ai_session"])
	h.AISubscriptionPlan, _ = stringOf(f["ai_subscription_plan"])
	h.AIPromptLength = intOf(f["ai_prompt_length"])
	h.AILineChanges = intPtr(f["ai_line_changes"])
	h.HumanLineChanges = intPtr(f["human_line_changes"])
	h.AIInputTokens = intOf(f["ai_input_tokens"])
	h.AIOutputTokens = intOf(f["ai_output_tokens"])
	h.AICachedInputTokens = intOf(f["ai_cached_input_tokens"])
	return h, nil
}

// stringOf reads a JSON string. Anything else, null included, reads as
// none.
func stringOf(raw json.RawMessage) (string, bool) {
	var s string
	if len(raw) == 0 || raw[0] != '"' || json.Unmarshal(raw, &s) != nil {
		return "", false
	}
	return s, true
}

func stringPtr(raw json.RawMessage) *string {
	if s, ok := stringOf(raw); ok {
		return &s
	}
	return nil
}

// numberOf reads a JSON number, or a string that holds one. NaN and the
// infinities, which strconv reads, are no numbers here.
func numberOf(raw json.RawMessage) (float64, bool) {
	var v float64
	if s, ok := stringOf(raw); ok {
		x, err := strconv.ParseFloat(strings.TrimSpace(s), 64)
		if err != nil {
			return 0, false
		}
		v = x
	} else if len(raw) == 0 || json.Unmarshal(raw, &v) != nil {
		return 0, false
	}
	if math.IsNaN(v) || math.IsInf(v, 0) {
		return 0, false
	}
	return v, true
}

// intPtr reads a whole number of at most 2^53 in either direction.
func intPtr(raw json.RawMessage) *int64 {
	v, ok := numberOf(raw)
	if !ok || v != math.Trunc(v) || math.Abs(v) > maxInt {
		return nil
	}
	n := int64(v)
	return &n
}

func intOf(raw json.RawMessage) int64 {
	if p := intPtr(raw); p != nil {
		return *p
	}
	return 0
}

func boolPtr(raw json.RawMessage) *bool {
	var b bool
	if s, ok := stringOf(raw); ok {
		switch s {
		case "true":
			b = true
		case "false":
		default:
			return nil
		}
		return &b
	}
	if len(raw) == 0 || json.Unmarshal(raw, &b) != nil {
		return nil
	}
	return &b
}

// dependenciesOf reads an array of strings, keeping the strings in it, or
// a comma-separated string, as WakaTime documents the field (HC 2.1).
func dependenciesOf(raw json.RawMessage) []string {
	if s, ok := stringOf(raw); ok {
		var out []string
		for _, d := range strings.Split(s, ",") {
			if d = strings.TrimSpace(d); d != "" {
				out = append(out, d)
			}
		}
		return out
	}
	var items []json.RawMessage
	if len(raw) == 0 || json.Unmarshal(raw, &items) != nil {
		return nil
	}
	var out []string
	for _, it := range items {
		if s, ok := stringOf(it); ok {
			out = append(out, s)
		}
	}
	return out
}

// The window of epoch seconds Hackatime takes, and the gap between the
// Windows epoch, 1601-01-01, and the Unix one (heartbeat_ingest.rb lines
// 8-13).
const (
	saneMin            = 1_000_000_000
	saneMax            = 2_000_000_000
	windowsEpochOffset = 11_644_473_600
)

// Seconds reads a heartbeat's time as Hackatime does (heartbeat_ingest.rb
// lines 364-383): Unix epoch seconds from 2001-09-09 up to 2033-05-18, a
// time in milliseconds, microseconds or nanoseconds scaled down into that
// window, or seconds counted from 1601 moved to 1970. It reports false for
// anything else.
func Seconds(t float64) (float64, bool) {
	sane := func(s float64) bool { return s >= saneMin && s < saneMax }
	if math.IsNaN(t) || math.IsInf(t, 0) {
		return 0, false
	}
	if sane(t) {
		return t, true
	}
	for _, scale := range []float64{1e3, 1e6, 1e9} {
		if s := t / scale; sane(s) {
			return s, true
		}
	}
	if s := t - windowsEpochOffset; sane(s) {
		return s, true
	}
	return 0, false
}

// Key names a heartbeat by everything Parse read of it: the first 16
// bytes of the SHA-256 of its canonical form, which is its JSON with the
// fields in Heartbeat's order. Two heartbeats Parse read alike have the
// same Key however their JSON was spelled, in any key order, with a number
// written as a number or a string, and with an unknown field or not. A
// heartbeat that differs in any field it read has another. wakatime-cli
// sends a heartbeat it replays from its offline queue byte for byte as it
// first sent it (testdata/fixtures/wakatime/2.28.4/linux/replay), so a
// replay has its first send's Key.
//
// Hackatime's fields_hash names a heartbeat by a list of fields too
// (heartbeat.rb lines 41-56), but by the values it stores, after filling
// some in from the request, so the two can differ for one heartbeat.
type Key [16]byte

// keyDomain tells this hash from any other SHA-256 of the same bytes.
const keyDomain = "pairtime wakacompat heartbeat key v1\n"

// Key returns h's Key.
func (h Heartbeat) Key() Key {
	if math.IsNaN(h.Time) || math.IsInf(h.Time, 0) {
		// JSON has no such numbers. Parse never returns them, and a
		// heartbeat built by hand keys as if its time were 0.
		h.Time = 0
	}
	b, err := json.Marshal(h)
	if err != nil {
		// Only strings, finite numbers and booleans are left, so Marshal
		// can't fail.
		panic("wakacompat: " + err.Error())
	}
	sum := sha256.Sum256(append([]byte(keyDomain), b...))
	var k Key
	copy(k[:], sum[:len(k)])
	return k
}

func (k Key) String() string { return hex.EncodeToString(k[:]) }
