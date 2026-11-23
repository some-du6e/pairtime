// SPDX-License-Identifier: AGPL-3.0-or-later

package ingest

import (
	"encoding/json"
	"fmt"
	"os"
	"runtime"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/some-du6e/pairtime/internal/adapters/git"
	"github.com/some-du6e/pairtime/pkg/event"
)

// sampleBucket is a bucket the scorer could write, with every member of
// the upload set, anchors included, which Shareable drops.
func sampleBucket(device string, minute time.Time, rev uint32) event.MinuteBucket {
	return event.MinuteBucket{
		V:      "1.0.0",
		Params: "sha256:" + strings.Repeat("ab", 32),
		Minute: minute,
		Device: device,
		Slots: []event.BucketSlot{
			{Project: "p_one", Credited: 12.5, Why: event.ReasonPrompt, Kind: event.ActivityPrompting, Ev: event.EvidenceA, Lbl: []event.Label{event.LabelViaTrack}},
			{Project: "p_one", Credited: 15, Why: event.ReasonRead, Kind: event.ActivityReadingOutput, Ev: event.EvidenceB},
			{},
			{},
		},
		CreditedS:     27.5,
		Uncredited:    map[event.Reason]float64{event.ReasonAbsent: 15, event.ReasonAgentOnly: 2.5},
		AgentRunningS: 30,
		Anchors:       []string{"0190f4b2-5a1c-7d3e-8f00-123456789abc"},
		State:         event.StateFinal,
		Witness:       event.Witnessed,
		Rev:           rev,
	}
}

var sampleMinute = time.Date(2026, 11, 19, 12, 0, 0, 0, time.UTC)

// shareableJSON is what the uploader sends for mb: encoding/json's form of
// its Shareable copy.
func shareableJSON(t testing.TB, mb event.MinuteBucket) []byte {
	t.Helper()
	b, err := json.Marshal(mb.Shareable())
	if err != nil {
		t.Fatal(err)
	}
	return b
}

// asMap decodes JSON into maps and slices, for a case to edit.
func asMap(t testing.TB, b []byte) map[string]any {
	t.Helper()
	var m map[string]any
	if err := json.Unmarshal(b, &m); err != nil {
		t.Fatal(err)
	}
	return m
}

func marshal(t testing.TB, v any) []byte {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

// setPath sets the member at path, such as slots[0].lbl[0], with at most
// one index per step.
func setPath(t testing.TB, root map[string]any, path string, v any) {
	t.Helper()
	var cur any = root
	steps := strings.Split(path, ".")
	for k, step := range steps {
		name, idx := step, -1
		if i := strings.IndexByte(step, '['); i >= 0 {
			n, err := strconv.Atoi(strings.TrimSuffix(step[i+1:], "]"))
			if err != nil {
				t.Fatalf("bad path %q", path)
			}
			name, idx = step[:i], n
		}
		last := k == len(steps)-1
		m, ok := cur.(map[string]any)
		if !ok {
			t.Fatalf("%q: %s isn't an object", path, name)
		}
		if idx < 0 {
			if last {
				m[name] = v
				return
			}
			cur = m[name]
			continue
		}
		a, ok := m[name].([]any)
		if !ok || idx >= len(a) {
			t.Fatalf("%q: %s has no element %d", path, name, idx)
		}
		if last {
			a[idx] = v
			return
		}
		cur = a[idx]
	}
}

// slot returns slot i of a bucket map.
func slot(m map[string]any, i int) map[string]any { return m["slots"].([]any)[i].(map[string]any) }

// shapeResult is an item check's outcome, as a case states it.
type shapeResult struct {
	code  string // "" when the item passes
	field string
}

func outcomeOf(e *itemErr) shapeResult {
	if e == nil {
		return shapeResult{}
	}
	return shapeResult{code: e.code, field: e.field}
}

// TestEnums checks the server's enum lists against pkg/event's and the
// event schema's, so a value pkg/event adds is one ingest takes.
func TestEnums(t *testing.T) {
	schema := schemaEnums(t)
	var unshaped []string
	for _, r := range event.CreditReasons() {
		if !slices.Contains(activities, r.Activity()) {
			unshaped = append(unshaped, string(r))
		}
	}
	tests := []struct {
		name      string
		got, want []string
	}{
		{"activities are pkg/event's", names(activities), names(event.Activities())},
		{"evidence levels are pkg/event's", names(evidences), names(event.Evidences())},
		{"slot labels are pkg/event's", names(slotLabels), names(event.SlotLabels())},
		{"bucket states are pkg/event's", names(bucketStates), names(event.BucketStates())},
		{"witness labels are pkg/event's", names(witnesses), names(event.Witnesses())},
		{"actors are the event schema's", names(actors), schema["actor"]},
		{"label harnesses are the event schema's harnesses but none", names(labelHarnesses), slices.DeleteFunc(schema["harness"], func(h string) bool { return h == "none" })},
		{"every credit reason's activity is one the shape allows", unshaped, nil},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if !slices.Equal(tt.got, tt.want) {
				t.Errorf("got %q, want %q", tt.got, tt.want)
			}
		})
	}
}

// schemaEnums returns docs/schema/event.schema.json's enum lists by their
// $defs names.
func schemaEnums(t *testing.T) map[string][]string {
	t.Helper()
	data, err := os.ReadFile("../../../docs/schema/event.schema.json")
	if err != nil {
		t.Fatal(err)
	}
	var schema struct {
		Defs map[string]struct {
			Enum []string `json:"enum"`
		} `json:"$defs"`
	}
	if err := json.Unmarshal(data, &schema); err != nil {
		t.Fatal(err)
	}
	out := map[string][]string{}
	for name, def := range schema.Defs {
		if def.Enum != nil {
			out[name] = def.Enum
		}
	}
	return out
}

// names turns a list of enum values into strings.
func names[T ~string](vs []T) []string {
	out := make([]string, len(vs))
	for i, v := range vs {
		out[i] = string(v)
	}
	return out
}

func TestBucketShape(t *testing.T) {
	type edit func(m map[string]any)
	tests := []struct {
		name string
		edit edit
		want shapeResult
	}{
		{"a bucket as MinuteBucket.Shareable writes it passes", func(map[string]any) {}, shapeResult{}},
		{"a bucket without uncredited passes", func(m map[string]any) {
			delete(m, "uncredited")
		}, shapeResult{}},
		{"a bucket with anchors, the event ids that stay on the device, is refused", func(m map[string]any) {
			m["anchors"] = []any{"0190f4b2-5a1c-7d3e-8f00-123456789abc"}
		}, shapeResult{CodeUnknownField, ""}},
		{"an unknown member is refused without naming it", func(m map[string]any) { m["note"] = "hi" }, shapeResult{CodeUnknownField, ""}},
		{"an unknown member of a slot names the slot", func(m map[string]any) { slot(m, 1)["note"] = "hi" }, shapeResult{CodeUnknownField, "slots[1]"}},
		{"a missing rev is missing_field", func(m map[string]any) { delete(m, "rev") }, shapeResult{CodeMissingField, "rev"}},
		{"a slot without cr is missing_field", func(m map[string]any) { delete(slot(m, 2), "cr") }, shapeResult{CodeMissingField, "slots[2].cr"}},
		{"a rev in a string is bad_type", func(m map[string]any) { m["rev"] = "2" }, shapeResult{CodeBadType, "rev"}},
		{"a null credited_s is bad_type", func(m map[string]any) { m["credited_s"] = nil }, shapeResult{CodeBadType, "credited_s"}},
		{"a slot that isn't an object is bad_type", func(m map[string]any) { m["slots"].([]any)[3] = 0 }, shapeResult{CodeBadType, "slots[3]"}},
		{"a fractional rev is bad_number", func(m map[string]any) { m["rev"] = 2.5 }, shapeResult{CodeBadNumber, "rev"}},
		{"a rev past uint32 is bad_number", func(m map[string]any) { m["rev"] = 4294967296 }, shapeResult{CodeBadNumber, "rev"}},
		{"a negative rev is bad_number", func(m map[string]any) { m["rev"] = -1 }, shapeResult{CodeBadNumber, "rev"}},
		{"credited_s over a minute is bad_number", func(m map[string]any) { m["credited_s"] = 60.5 }, shapeResult{CodeBadNumber, "credited_s"}},
		{"a cr off its half-second steps is bad_number", func(m map[string]any) { slot(m, 0)["cr"] = 12.25 }, shapeResult{CodeBadNumber, "slots[0].cr"}},
		{"a cr over 15 is bad_number", func(m map[string]any) { slot(m, 0)["cr"] = 15.5 }, shapeResult{CodeBadNumber, "slots[0].cr"}},
		{"an uncredited value over a minute is bad_number", func(m map[string]any) {
			m["uncredited"] = map[string]any{"X.absent": 61}
		}, shapeResult{CodeBadNumber, "uncredited"}},
		{"five slots are too_many", func(m map[string]any) {
			m["slots"] = append(m["slots"].([]any), map[string]any{"cr": 0})
		}, shapeResult{CodeTooMany, "slots"}},
		{"three slots break MinuteBucket.Validate's four", func(m map[string]any) {
			m["slots"] = m["slots"].([]any)[:3]
		}, shapeResult{CodeBadRule, "slots"}},
		{"slots that don't add up to credited_s break a rule", func(m map[string]any) { m["credited_s"] = 30 }, shapeResult{CodeBadRule, "slots[].cr"}},
		{"a credited slot without a project breaks a rule", func(m map[string]any) { delete(slot(m, 0), "p") }, shapeResult{CodeBadRule, "slots[0].p"}},
		{"a credited slot whose kind isn't its reason's breaks a rule", func(m map[string]any) { slot(m, 0)["kind"] = "testing" }, shapeResult{CodeBadRule, "slots[0].kind"}},
		{"side-door labels out of order break a rule", func(m map[string]any) {
			slot(m, 0)["lbl"] = []any{"via_bridge", "via_track"}
		}, shapeResult{CodeBadRule, "slots[0].lbl"}},
		{"focus_unknown after a slot's side-door labels, at level C, passes: a value of lbl and not a field (SM 2.5)", func(m map[string]any) {
			slot(m, 0)["ev"] = "C"
			slot(m, 0)["lbl"] = []any{"via_track", "via_plugin", "via_bridge", "focus_unknown"}
		}, shapeResult{}},
		{"focus_unknown before a side-door label breaks a rule", func(m map[string]any) {
			slot(m, 0)["ev"] = "C"
			slot(m, 0)["lbl"] = []any{"focus_unknown", "via_track"}
		}, shapeResult{CodeBadRule, "slots[0].lbl"}},
		{"a slot labeled focus_unknown above level C breaks MinuteBucket.Validate's rule, since its weakest second is level C", func(m map[string]any) {
			slot(m, 0)["lbl"] = []any{"focus_unknown"}
		}, shapeResult{CodeBadRule, "slots[0].ev"}},
		{"via_relay at level C passes: a value of lbl and not a field (DESIGN 4.1 E-14)", func(m map[string]any) {
			slot(m, 0)["ev"] = "C"
			slot(m, 0)["lbl"] = []any{"via_relay"}
		}, shapeResult{}},
		{"via_relay after focus_unknown, the last of the five labels, passes", func(m map[string]any) {
			slot(m, 0)["ev"] = "C"
			slot(m, 0)["lbl"] = []any{"via_track", "via_plugin", "via_bridge", "focus_unknown", "via_relay"}
		}, shapeResult{}},
		{"via_relay before focus_unknown breaks a rule", func(m map[string]any) {
			slot(m, 0)["ev"] = "C"
			slot(m, 0)["lbl"] = []any{"via_relay", "focus_unknown"}
		}, shapeResult{CodeBadRule, "slots[0].lbl"}},
		{"a slot labeled via_relay above level C breaks MinuteBucket.Validate's rule, since every relayed second is level C", func(m map[string]any) {
			slot(m, 0)["lbl"] = []any{"via_relay"}
		}, shapeResult{CodeBadRule, "slots[0].ev"}},
		{"six labels on a slot are too_many", func(m map[string]any) {
			slot(m, 0)["lbl"] = []any{"via_track", "via_plugin", "via_bridge", "focus_unknown", "via_relay", "via_track"}
		}, shapeResult{CodeTooMany, "slots[0].lbl"}},
		{"a backfilled minute credited above level C breaks a rule", func(m map[string]any) { m["witness"] = "backfilled" }, shapeResult{CodeBadRule, "slots[0].ev"}},
		{"a minute with seconds is bad_string", func(m map[string]any) { m["minute"] = "2026-11-19T12:00:05Z" }, shapeResult{CodeBadString, "minute"}},
		{"a minute with an offset is bad_string", func(m map[string]any) { m["minute"] = "2026-11-19T12:00:00+00:00" }, shapeResult{CodeBadString, "minute"}},
		{"a minute with fractional seconds is bad_string", func(m map[string]any) { m["minute"] = "2026-11-19T12:00:00.000Z" }, shapeResult{CodeBadString, "minute"}},
		{"a minute before 2020 is bad_string", func(m map[string]any) { m["minute"] = "2019-12-31T23:59:00Z" }, shapeResult{CodeBadString, "minute"}},
		{"a minute on 30 February is bad_string", func(m map[string]any) { m["minute"] = "2026-02-30T12:00:00Z" }, shapeResult{CodeBadString, "minute"}},
		{"a minute at hour 24 is bad_string", func(m map[string]any) { m["minute"] = "2026-11-19T24:00:00Z" }, shapeResult{CodeBadString, "minute"}},
		{"a reason returned twice is bad_string", func(m map[string]any) { slot(m, 1)["why"] = "READ~r~r" }, shapeResult{CodeBadString, "slots[1].why"}},
		{"a reason's suffixes in the wrong order are bad_string", func(m map[string]any) { slot(m, 0)["why"] = "P+restored~r" }, shapeResult{CodeBadString, "slots[0].why"}},
		{"a credit reason as an uncredited member is bad_string", func(m map[string]any) {
			m["uncredited"] = map[string]any{"P": 3}
		}, shapeResult{CodeBadString, "uncredited"}},
		{"an uncredited reason as a slot's why is bad_string", func(m map[string]any) { slot(m, 0)["why"] = "X.absent" }, shapeResult{CodeBadString, "slots[0].why"}},
		{"typing_module, an anchor's label, on a slot is bad_string", func(m map[string]any) {
			slot(m, 0)["lbl"] = []any{"typing_module"}
		}, shapeResult{CodeBadString, "slots[0].lbl[0]"}},
		{"a scoring version that isn't N.N.N is bad_string", func(m map[string]any) { m["v"] = "1.0" }, shapeResult{CodeBadString, "v"}},
		{"a scoring version of three nine-digit parts passes", func(m map[string]any) { m["v"] = "123456789.123456789.123456789" }, shapeResult{}},
		{"a scoring version longer than three nine-digit parts is bad_string", func(m map[string]any) { m["v"] = strings.Repeat(".", 100_000) }, shapeResult{CodeBadString, "v"}},
		{"params without sha256: is bad_string", func(m map[string]any) { m["params"] = strings.Repeat("ab", 32) }, shapeResult{CodeBadString, "params"}},
		{"agent_only by project passes", func(m map[string]any) {
			m["agent_only"] = map[string]any{"p_one": 12.5, "p_two": 0.5}
		}, shapeResult{}},
		{"an empty agent_only passes", func(m map[string]any) { m["agent_only"] = map[string]any{} }, shapeResult{}},
		{"agent_only named by a path is bad_string", func(m map[string]any) {
			m["agent_only"] = map[string]any{"/home/teen/game": 3}
		}, shapeResult{CodeBadString, "agent_only"}},
		{"agent_only off its half-second steps is bad_number", func(m map[string]any) {
			m["agent_only"] = map[string]any{"p_one": 1.25}
		}, shapeResult{CodeBadNumber, "agent_only"}},
		{"agent_only over a minute is bad_number", func(m map[string]any) {
			m["agent_only"] = map[string]any{"p_one": 60.5}
		}, shapeResult{CodeBadNumber, "agent_only"}},
		{"negative agent_only is bad_number", func(m map[string]any) {
			m["agent_only"] = map[string]any{"p_one": -0.5}
		}, shapeResult{CodeBadNumber, "agent_only"}},
		{"agent_only seconds in a string are bad_type", func(m map[string]any) {
			m["agent_only"] = map[string]any{"p_one": "3"}
		}, shapeResult{CodeBadType, "agent_only"}},
		{"a null agent_only is bad_type", func(m map[string]any) { m["agent_only"] = nil }, shapeResult{CodeBadType, "agent_only"}},
		{"agent_only above agent_running_s breaks MinuteBucket.Validate's rule, named by agent_only alone", func(m map[string]any) {
			m["agent_only"] = map[string]any{"p_one": 45}
		}, shapeResult{CodeBadRule, "agent_only"}},
		{"agent_only for 65 projects is too_many", func(m map[string]any) {
			ao := map[string]any{}
			for i := range maxAgentOnly + 1 {
				ao["p_"+strconv.Itoa(i)] = 1
			}
			m["agent_only"] = ao
		}, shapeResult{CodeTooMany, "agent_only"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			m := asMap(t, shareableJSON(t, sampleBucket("d_test", sampleMinute, 1)))
			tt.edit(m)
			_, e := checkBucket(marshal(t, m))
			if got := outcomeOf(e); got != tt.want {
				t.Errorf("got %+v, want %+v", got, tt.want)
			}
		})
	}
	t.Run("a bucket that passes comes back as the bucket Shareable gave", func(t *testing.T) {
		mb := sampleBucket("d_test", sampleMinute, 7)
		got, e := checkBucket(shareableJSON(t, mb))
		if e != nil {
			t.Fatalf("refused: %+v", *e)
		}
		if want := mb.Shareable(); string(marshal(t, got)) != string(marshal(t, want)) {
			t.Errorf("got  %s\nwant %s", marshal(t, got), marshal(t, want))
		}
	})
}

// sampleCommit is a commit record with every member set.
func sampleCommit() map[string]any {
	return map[string]any{
		"sha": strings.Repeat("a", 40), "parents": []any{strings.Repeat("b", 40)}, "project": "p_one",
		"observed_at": 1795089660250, "author_time": 1795089600, "committer_time": 1795089600,
		"files_changed": 3, "insertions": 10, "deletions": 2, "path_hmacs": []any{"k_one"},
		"credited_seconds_since_parent": map[string]any{"prompting": 100, "testing": 20.5},
		"rewrites":                      []any{map[string]any{"old_sha": strings.Repeat("c", 40), "new_sha": strings.Repeat("a", 40)}},
		"actor":                         "human", "agent_trailer": "claude",
	}
}

func TestCommitShape(t *testing.T) {
	fromAdapter := func(t *testing.T) []byte {
		return marshal(t, git.Commit{
			SHA: strings.Repeat("d", 40), Project: "p_one", ObservedAt: 1795089660250, AuthorTime: 1795089600, CommitterTime: 1795089600,
			FilesChanged: 1, Insertions: 2, Deletions: 3, Actor: event.ActorAgent, AgentTrailer: "claude",
			Local: git.CommitLocal{Op: event.GitCommit, Origin: git.MadeHere, AuthorTZ: "-0600", CommitterTZ: "-0600"},
		})
	}
	tests := []struct {
		name string
		raw  func(t *testing.T) []byte
		want shapeResult
	}{
		{"a commit record with every member passes", func(t *testing.T) []byte { return marshal(t, sampleCommit()) }, shapeResult{}},
		{"a root commit as internal/adapters/git writes it, with null parents, passes", fromAdapter, shapeResult{}},
		{"a 64-digit SHA-256 commit id passes", func(t *testing.T) []byte {
			m := sampleCommit()
			m["sha"] = strings.Repeat("e", 64)
			return marshal(t, m)
		}, shapeResult{}},
		{"a missing actor is missing_field", func(t *testing.T) []byte {
			m := sampleCommit()
			delete(m, "actor")
			return marshal(t, m)
		}, shapeResult{CodeMissingField, "actor"}},
		{"a commit's message is unknown_field", func(t *testing.T) []byte {
			m := sampleCommit()
			m["message"] = "fix the bug"
			return marshal(t, m)
		}, shapeResult{CodeUnknownField, ""}},
		{"a rewrite with an unknown member names the rewrite", func(t *testing.T) []byte {
			m := sampleCommit()
			m["rewrites"].([]any)[0].(map[string]any)["why"] = "amend"
			return marshal(t, m)
		}, shapeResult{CodeUnknownField, "rewrites[0]"}},
		{"an observed_at in seconds is bad_number", func(t *testing.T) []byte {
			m := sampleCommit()
			m["observed_at"] = 1795089660
			return marshal(t, m)
		}, shapeResult{CodeBadNumber, "observed_at"}},
		{"a negative insertion count is bad_number", func(t *testing.T) []byte {
			m := sampleCommit()
			m["insertions"] = -1
			return marshal(t, m)
		}, shapeResult{CodeBadNumber, "insertions"}},
		{"a commit dated after 2099 passes, since git takes any date", func(t *testing.T) []byte {
			m := sampleCommit()
			m["author_time"], m["committer_time"] = 5_000_000_000, 5_000_000_000
			return marshal(t, m)
		}, shapeResult{}},
		{"a commit without its counts passes, as the client sends one whose shortstat it couldn't read", func(t *testing.T) []byte {
			m := sampleCommit()
			delete(m, "files_changed")
			delete(m, "insertions")
			delete(m, "deletions")
			return marshal(t, m)
		}, shapeResult{}},
		{"credited seconds as one number, not split by kind, are bad_type", func(t *testing.T) []byte {
			m := sampleCommit()
			m["credited_seconds_since_parent"] = 120.5
			return marshal(t, m)
		}, shapeResult{CodeBadType, "credited_seconds_since_parent"}},
		{"credited seconds under a name that isn't an activity kind are bad_string", func(t *testing.T) []byte {
			m := sampleCommit()
			m["credited_seconds_since_parent"] = map[string]any{"coding": 5}
			return marshal(t, m)
		}, shapeResult{CodeBadString, "credited_seconds_since_parent"}},
		{"a parent listed twice breaks a rule", func(t *testing.T) []byte {
			m := sampleCommit()
			m["parents"] = []any{strings.Repeat("b", 40), strings.Repeat("b", 40)}
			return marshal(t, m)
		}, shapeResult{CodeBadRule, "parents"}},
		{"a path key listed twice breaks a rule", func(t *testing.T) []byte {
			m := sampleCommit()
			m["path_hmacs"] = []any{"k_one", "k_one"}
			return marshal(t, m)
		}, shapeResult{CodeBadRule, "path_hmacs"}},
		{"an agent trailer outside the adapter's list is bad_string, since a trailer can hold a name", func(t *testing.T) []byte {
			m := sampleCommit()
			m["agent_trailer"] = "Jane_Doe"
			return marshal(t, m)
		}, shapeResult{CodeBadString, "agent_trailer"}},
		{"2,000 rewrites, a squash of as many commits as a rebase maps, pass", func(t *testing.T) []byte {
			m := sampleCommit()
			rw := make([]any, maxRewrites)
			for i := range rw {
				rw[i] = map[string]any{"old_sha": fmt.Sprintf("%040x", i+1), "new_sha": strings.Repeat("a", 40)}
			}
			m["rewrites"] = rw
			return marshal(t, m)
		}, shapeResult{}},
		{"1,025 parents are too_many", func(t *testing.T) []byte {
			m := sampleCommit()
			ps := make([]any, maxList+1)
			for i := range ps {
				ps[i] = strings.Repeat("b", 40)
			}
			m["parents"] = ps
			return marshal(t, m)
		}, shapeResult{CodeTooMany, "parents"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, e := parseCommit(tt.raw(t))
			if got := outcomeOf(e); got != tt.want {
				t.Errorf("got %+v, want %+v", got, tt.want)
			}
		})
	}
}

// sampleLabels are labels with one harness.
func sampleLabels() map[string]any {
	return map[string]any{
		"client_build": "v0.1.0", "scoring_version": "1.0.0", "params": "sha256:" + strings.Repeat("cd", 32),
		"harnesses":     []any{map[string]any{"name": "claude_code", "version": "2.1.327"}},
		"typing_module": false, "vm": true,
	}
}

func TestLabelsShape(t *testing.T) {
	tests := []struct {
		name string
		edit func(m map[string]any)
		want shapeResult
	}{
		{"labels with one harness pass", func(map[string]any) {}, shapeResult{}},
		{"labels with no harness pass", func(m map[string]any) { m["harnesses"] = []any{} }, shapeResult{}},
		{"a Go pseudo-version as the build passes", func(m map[string]any) { m["client_build"] = "v0.0.0-20261119104900-1a2b3c4d5e6f" }, shapeResult{}},
		{"a harness without a version passes, as a hook that names none", func(m map[string]any) {
			m["harnesses"] = []any{map[string]any{"name": "codex"}}
		}, shapeResult{}},
		{"a hostname is unknown_field", func(m map[string]any) { m["hostname"] = "teens-laptop" }, shapeResult{CodeUnknownField, ""}},
		{"a missing vm flag is missing_field", func(m map[string]any) { delete(m, "vm") }, shapeResult{CodeMissingField, "vm"}},
		{"a vm flag in a string is bad_type", func(m map[string]any) { m["vm"] = "true" }, shapeResult{CodeBadType, "vm"}},
		{"the harness none is bad_string", func(m map[string]any) {
			m["harnesses"] = []any{map[string]any{"name": "none", "version": "1"}}
		}, shapeResult{CodeBadString, "harnesses[0].name"}},
		{"one harness twice breaks a rule", func(m map[string]any) {
			h := map[string]any{"name": "codex", "version": "1.2.3"}
			m["harnesses"] = []any{h, h}
		}, shapeResult{CodeBadRule, "harnesses"}},
		{"17 harnesses are too_many", func(m map[string]any) {
			m["harnesses"] = make([]any, maxHarnesses+1)
		}, shapeResult{CodeTooMany, "harnesses"}},
		{"a build starting with a dot is bad_string", func(m map[string]any) { m["client_build"] = ".1" }, shapeResult{CodeBadString, "client_build"}},
		{"a build of 65 characters is bad_string", func(m map[string]any) { m["client_build"] = strings.Repeat("1", 65) }, shapeResult{CodeBadString, "client_build"}},
		{"a dev build is v0.0.0-dev, which passes", func(m map[string]any) { m["client_build"] = "v0.0.0-dev" }, shapeResult{}},
		{"a build with a pre-release suffix passes", func(m map[string]any) { m["client_build"] = "v0.1.0-alpha.1" }, shapeResult{}},
		{"a bare word as the build is bad_string, as dev is", func(m map[string]any) { m["client_build"] = "dev" }, shapeResult{CodeBadString, "client_build"}},
		{"a folder name as the build is bad_string", func(m map[string]any) { m["client_build"] = "my-secret-startup" }, shapeResult{CodeBadString, "client_build"}},
		{"a hostname as a harness's version is bad_string", func(m map[string]any) {
			m["harnesses"] = []any{map[string]any{"name": "codex", "version": "teens-mbp.local"}}
		}, shapeResult{CodeBadString, "harnesses[0].version"}},
		{"a file name as a harness's version is bad_string", func(m map[string]any) {
			m["harnesses"] = []any{map[string]any{"name": "codex", "version": "main.go"}}
		}, shapeResult{CodeBadString, "harnesses[0].version"}},
		{"a build suffix on a harness's version passes", func(m map[string]any) {
			m["harnesses"] = []any{map[string]any{"name": "codex", "version": "0.1.0+3dbf6d4"}}
		}, shapeResult{}},
		{"a Go pseudo-version after a pre-release passes as the build", func(m map[string]any) {
			m["client_build"] = "v0.1.0-alpha.5.0.20261123010542-8f0e2c5fba57"
		}, shapeResult{}},
		{"a hostname after the build's number is bad_string in tier 1's form (ingest-v1 2.3.4)", func(m map[string]any) {
			m["client_build"] = "1.0.0-teens-mbp.local"
		}, shapeResult{CodeBadString, "client_build"}},
		{"a word joined to a number in the build's suffix is bad_string", func(m map[string]any) { m["client_build"] = "v0.1.0-rc1" }, shapeResult{CodeBadString, "client_build"}},
		{"a branch name after a harness's version number is bad_string", func(m map[string]any) {
			m["harnesses"] = []any{map[string]any{"name": "codex", "version": "2.1.327-fix-login"}}
		}, shapeResult{CodeBadString, "harnesses[0].version"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			m := sampleLabels()
			tt.edit(m)
			_, e := parseLabels(marshal(t, m))
			if got := outcomeOf(e); got != tt.want {
				t.Errorf("got %+v, want %+v", got, tt.want)
			}
		})
	}
}

// sampleRoot is a batch root's members, not necessarily on any chain.
func sampleRoot() map[string]any {
	return map[string]any{"b": 7, "n": 12, "R_b": strings.Repeat("1", 64), "H_b": strings.Repeat("2", 64), "prev": strings.Repeat("3", 64)}
}

func TestBatchRootShape(t *testing.T) {
	tests := []struct {
		name string
		edit func(m map[string]any)
		want shapeResult
	}{
		{"a batch root with its five members passes", func(map[string]any) {}, shapeResult{}},
		{"a batch root that isn't an object is not_object", nil, shapeResult{CodeNotObject, ""}},
		{"a sixth member is unknown_field", func(m map[string]any) { m["sealed_at"] = 1 }, shapeResult{CodeUnknownField, ""}},
		{"a missing prev is missing_field", func(m map[string]any) { delete(m, "prev") }, shapeResult{CodeMissingField, "prev"}},
		{"b = 0 is bad_number", func(m map[string]any) { m["b"] = 0 }, shapeResult{CodeBadNumber, "b"}},
		{"n = 0 has the right form; the chain refuses it as batch_empty", func(m map[string]any) { m["n"] = 0 }, shapeResult{}},
		{"b in exponent form is bad_number", func(m map[string]any) { m["b"] = json.Number("7e0") }, shapeResult{CodeBadNumber, "b"}},
		{"an uppercase root is bad_string", func(m map[string]any) { m["R_b"] = strings.Repeat("A", 64) }, shapeResult{CodeBadString, "R_b"}},
		{"a 63-digit head is bad_string", func(m map[string]any) { m["H_b"] = strings.Repeat("2", 63) }, shapeResult{CodeBadString, "H_b"}},
		{"a prev as a number is bad_type", func(m map[string]any) { m["prev"] = 3 }, shapeResult{CodeBadType, "prev"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			raw := []byte(`[1]`)
			if tt.edit != nil {
				m := sampleRoot()
				tt.edit(m)
				raw = marshal(t, m)
			}
			_, e := parseBatchRoot(raw)
			if got := outcomeOf(e); got != tt.want {
				t.Errorf("got %+v, want %+v", got, tt.want)
			}
		})
	}
}

// replaceOnce replaces old, which must appear exactly once in s.
func replaceOnce(t *testing.T, s []byte, old, new string) []byte {
	t.Helper()
	if n := strings.Count(string(s), old); n != 1 {
		t.Fatalf("%q appears %d times in %s", old, n, s)
	}
	return []byte(strings.Replace(string(s), old, new, 1))
}

// TestRepeatedNames checks that an item whose object repeats a member name
// is refused as bad_rule naming that object, never the member, where
// encoding/json alone would keep the last value (ingest-v1 2.3).
func TestRepeatedNames(t *testing.T) {
	bucket := func(t *testing.T) []byte { return shareableJSON(t, sampleBucket("d_test", sampleMinute, 1)) }
	checks := map[string]func(raw []byte) *itemErr{
		"root":   func(raw []byte) *itemErr { _, e := parseBatchRoot(raw); return e },
		"bucket": func(raw []byte) *itemErr { _, e := checkBucket(raw); return e },
		"commit": func(raw []byte) *itemErr { _, e := parseCommit(raw); return e },
		"labels": func(raw []byte) *itemErr { _, e := parseLabels(raw); return e },
	}
	tests := []struct {
		name string
		kind string
		raw  func(t *testing.T) []byte
		want shapeResult
	}{
		{"a batch root's b twice is bad_rule", "root", func(t *testing.T) []byte {
			return replaceOnce(t, marshal(t, sampleRoot()), `"b":7`, `"b":7,"b":8`)
		}, shapeResult{CodeBadRule, ""}},
		{"a bucket's rev twice is bad_rule", "bucket", func(t *testing.T) []byte {
			return replaceOnce(t, bucket(t), `"rev":1`, `"rev":1,"rev":2`)
		}, shapeResult{CodeBadRule, ""}},
		{"free text in a first v, with a valid v after it, is refused", "bucket", func(t *testing.T) []byte {
			return replaceOnce(t, bucket(t), `"v":"1.0.0"`, `"v":"fix the login bug","v":"1.0.0"`)
		}, shapeResult{CodeBadRule, ""}},
		{"a name spelled once plainly and once with an escape repeats", "bucket", func(t *testing.T) []byte {
			return replaceOnce(t, bucket(t), `"rev":1`, `"rev":1,"\u0072ev":2`)
		}, shapeResult{CodeBadRule, ""}},
		{"a slot's cr twice names the slot", "bucket", func(t *testing.T) []byte {
			return replaceOnce(t, bucket(t), `"cr":12.5`, `"cr":12.5,"cr":12.5`)
		}, shapeResult{CodeBadRule, "slots[0]"}},
		{"an uncredited reason twice names uncredited", "bucket", func(t *testing.T) []byte {
			return replaceOnce(t, bucket(t), `"X.absent":15`, `"X.absent":15,"X.absent":45`)
		}, shapeResult{CodeBadRule, "uncredited"}},
		{"an agent_only project twice names agent_only", "bucket", func(t *testing.T) []byte {
			return replaceOnce(t, bucket(t), `"agent_running_s":30`, `"agent_running_s":30,"agent_only":{"p_one":1,"p_one":2}`)
		}, shapeResult{CodeBadRule, "agent_only"}},
		{"an unknown member twice is unknown_field, the first failure in order", "bucket", func(t *testing.T) []byte {
			return replaceOnce(t, bucket(t), `"rev":1`, `"rev":1,"note":"a","note":"b"`)
		}, shapeResult{CodeUnknownField, ""}},
		{"a commit's actor twice is bad_rule", "commit", func(t *testing.T) []byte {
			return replaceOnce(t, marshal(t, sampleCommit()), `"actor":"human"`, `"actor":"human","actor":"agent"`)
		}, shapeResult{CodeBadRule, ""}},
		{"an activity twice in credited_seconds_since_parent names the map", "commit", func(t *testing.T) []byte {
			return replaceOnce(t, marshal(t, sampleCommit()), `"prompting":100`, `"prompting":100,"prompting":5`)
		}, shapeResult{CodeBadRule, "credited_seconds_since_parent"}},
		{"a rewrite's old_sha twice names the rewrite", "commit", func(t *testing.T) []byte {
			old := `"old_sha":"` + strings.Repeat("c", 40) + `"`
			return replaceOnce(t, marshal(t, sampleCommit()), old, old+","+old)
		}, shapeResult{CodeBadRule, "rewrites[0]"}},
		{"the labels' vm twice is bad_rule", "labels", func(t *testing.T) []byte {
			return replaceOnce(t, marshal(t, sampleLabels()), `"vm":true`, `"vm":true,"vm":false`)
		}, shapeResult{CodeBadRule, ""}},
		{"a harness's name twice names the harness", "labels", func(t *testing.T) []byte {
			return replaceOnce(t, marshal(t, sampleLabels()), `"name":"claude_code"`, `"name":"claude_code","name":"codex"`)
		}, shapeResult{CodeBadRule, "harnesses[0]"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := outcomeOf(checks[tt.kind](tt.raw(t))); got != tt.want {
				t.Errorf("got %+v, want %+v", got, tt.want)
			}
		})
	}
	t.Run("a batch root whose b repeats names no b in its result", func(t *testing.T) {
		br, e := parseBatchRoot(replaceOnce(t, marshal(t, sampleRoot()), `"b":7`, `"b":7,"b":8`))
		if e == nil || br.b != 0 {
			t.Errorf("b = %d with %+v, want no b and a refusal", br.b, e)
		}
	})
}

// TestObjectsStopEarly checks that an object of hundreds of thousands of
// members, which a few KiB of gzip inflate to, is refused at the first
// member that fails, without a slice or map entry per member: an item
// allocates a few copies of its own bytes at most (ingest-v1 2.4).
func TestObjectsStopEarly(t *testing.T) {
	const n = 300_000
	many := func(prefix string) string {
		var b strings.Builder
		b.WriteByte('{')
		for i := range n {
			if i > 0 {
				b.WriteByte(',')
			}
			fmt.Fprintf(&b, `"%s%d":0`, prefix, i)
		}
		b.WriteByte('}')
		return b.String()
	}
	bucket := string(shareableJSON(t, sampleBucket("d_test", sampleMinute, 1)))
	tests := []struct {
		name string
		raw  []byte
		want shapeResult
	}{
		{"a bucket of unknown members is refused at the first", []byte(strings.TrimSuffix(bucket, "}") + "," + many("zz")[1:]), shapeResult{CodeUnknownField, ""}},
		{"an uncredited map past its bound is refused at the member after it", []byte(strings.Replace(bucket, `"uncredited":{"X.absent":15,"X.agent_only":2.5}`, `"uncredited":`+many("X.a"), 1)), shapeResult{CodeTooMany, "uncredited"}},
		{"an agent_only map past its bound is refused at the member after it", []byte(strings.Replace(bucket, `"agent_running_s":30`, `"agent_running_s":30,"agent_only":`+many("p_"), 1)), shapeResult{CodeTooMany, "agent_only"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if len(tt.raw) < n*5 {
				t.Fatalf("the item is %d bytes, so it holds no big object and tests nothing", len(tt.raw))
			}
			var before, after runtime.MemStats
			runtime.GC()
			runtime.ReadMemStats(&before)
			_, e := checkBucket(tt.raw)
			runtime.ReadMemStats(&after)
			if got := outcomeOf(e); got != tt.want {
				t.Fatalf("got %+v, want %+v", got, tt.want)
			}
			if grew, limit := after.TotalAlloc-before.TotalAlloc, uint64(8*len(tt.raw)+1<<20); grew > limit {
				t.Errorf("checking %d bytes allocated %d, more than %d: it kept a member past the one that failed", len(tt.raw), grew, limit)
			}
		})
	}
}

func TestRuleField(t *testing.T) {
	tests := []struct {
		name, err, want string
	}{
		{"a field path is kept and the message dropped", "slots[0].p: must be 1-128 letters, got \"/home/teen\"", "slots[0].p"},
		{"only the first error counts", "slots[].cr: sums to 3\ncredited_s: bad", "slots[].cr"},
		{"a field that isn't a plain path is dropped", "/home/teen: oops", ""},
		{"an agent_only member is named by agent_only alone, never by its project key", "agent_only[p_3f9a]: is 3, more than agent_running_s (2)", "agent_only"},
		{"an uncredited member is named by uncredited alone", "uncredited[X.absent]: must be 0 to 60 seconds", "uncredited"},
		{"a numbered index is kept", "slots[2].lbl[0]: bad", "slots[2].lbl[0]"},
		{"an index that isn't a number is no path", "slots[p_one].cr: bad", ""},
		{"an unclosed index is no path", "slots[2: bad", ""},
		{"an error without a field gives none", "oops", ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := ruleField(errString(tt.err)); got != tt.want {
				t.Errorf("ruleField = %q, want %q", got, tt.want)
			}
		})
	}
}

type errString string

func (e errString) Error() string { return string(e) }
