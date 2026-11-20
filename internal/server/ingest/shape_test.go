// SPDX-License-Identifier: AGPL-3.0-or-later

package ingest

import (
	"encoding/json"
	"fmt"
	"maps"
	"regexp"
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

func TestAllowlist(t *testing.T) {
	set := func(lists ...[]string) []string { return slices.Sorted(slices.Values(slices.Concat(lists...))) }
	tests := []struct {
		name      string
		got, want []string
	}{
		// TODO(WP-EV3): drop the append once Tier1BucketFields lists
		// D-bucket-agent-only's agent_only.
		{"the bucket allowlist is DESIGN 4.3's bucket fields, Tier1BucketFields, with agent_only", set(bucketRequired, bucketOptional), set(withAgentOnly(event.Tier1BucketFields()))},
		{"the slot allowlist is DESIGN 4.3's slot fields, Tier1SlotFields", set(slotRequired, slotOptional), set(event.Tier1SlotFields())},
		{"the commit allowlist is DESIGN 4.3's commit fields", set(commitRequired, commitOptional), set([]string{
			"sha", "parents", "project", "observed_at", "author_time", "committer_time", "files_changed", "insertions",
			"deletions", "path_hmacs", "credited_seconds_since_parent", "rewrites", "actor", "agent_trailer",
		})},
		{"the labels allowlist is DESIGN 4.3's labels", set(labelsRequired), set([]string{"client_build", "scoring_version", "params", "harnesses", "typing_module", "vm"})},
		{"the batch root allowlist is DESIGN 4.3's {b, n, R_b, H_b, prev}", set(batchRequired), set([]string{"b", "n", "R_b", "H_b", "prev"})},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if !slices.Equal(tt.got, tt.want) {
				t.Errorf("allowlist %q, want %q", tt.got, tt.want)
			}
		})
	}
}

// withAgentOnly adds agent_only to fields unless they have it.
func withAgentOnly(fields []string) []string {
	if slices.Contains(fields, "agent_only") {
		return fields
	}
	return append(fields, "agent_only")
}

// TestEnums checks the server's enum lists against pkg/event's, so a value
// pkg/event adds can't be refused by mistake.
func TestEnums(t *testing.T) {
	tests := []struct {
		name string
		ok   bool
	}{
		{"every credit reason's activity is an activity the shape allows", func() bool {
			for _, r := range []event.Reason{event.ReasonPrompt, event.ReasonAnswer, event.ReasonGoAhead, event.ReasonQueued,
				event.ReasonPermission, event.ReasonInterrupt, event.ReasonEdit, event.ReasonNavigate, event.ReasonCommand,
				event.ReasonTest, event.ReasonCommit, event.ReasonSupervise, event.ReasonRead, event.ReasonLookup} {
				if !slices.Contains(activities, r.Activity()) {
					return false
				}
			}
			return true
		}()},
		{"a harness label can't be none", !slices.Contains(labelHarnesses, event.HarnessNone)},
		{"slot labels are the three side doors", slices.Equal(slotLabels, []event.Label{event.LabelViaTrack, event.LabelViaPlugin, event.LabelViaBridge})},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if !tt.ok {
				t.Error("no")
			}
		})
	}
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
		{"four labels on a slot are too_many", func(m map[string]any) {
			slot(m, 0)["lbl"] = []any{"via_track", "via_plugin", "via_bridge", "via_track"}
		}, shapeResult{CodeTooMany, "slots[0].lbl"}},
		{"a backfilled minute credited above level C breaks a rule", func(m map[string]any) { m["witness"] = "backfilled" }, shapeResult{CodeBadRule, "slots[0].ev"}},
		{"a minute with seconds is bad_string", func(m map[string]any) { m["minute"] = "2026-11-19T12:00:05Z" }, shapeResult{CodeBadString, "minute"}},
		{"a minute with an offset is bad_string", func(m map[string]any) { m["minute"] = "2026-11-19T12:00:00+00:00" }, shapeResult{CodeBadString, "minute"}},
		{"a minute with fractional seconds is bad_string", func(m map[string]any) { m["minute"] = "2026-11-19T12:00:00.000Z" }, shapeResult{CodeBadString, "minute"}},
		{"a minute before 2020 is bad_string", func(m map[string]any) { m["minute"] = "2019-12-31T23:59:00Z" }, shapeResult{CodeBadString, "minute"}},
		{"a credit reason as an uncredited member is bad_string", func(m map[string]any) {
			m["uncredited"] = map[string]any{"P": 3}
		}, shapeResult{CodeBadString, "uncredited"}},
		{"an uncredited reason as a slot's why is bad_string", func(m map[string]any) { slot(m, 0)["why"] = "X.absent" }, shapeResult{CodeBadString, "slots[0].why"}},
		{"typing_module, an anchor's label, on a slot is bad_string", func(m map[string]any) {
			slot(m, 0)["lbl"] = []any{"typing_module"}
		}, shapeResult{CodeBadString, "slots[0].lbl[0]"}},
		{"a scoring version that isn't N.N.N is bad_string", func(m map[string]any) { m["v"] = "1.0" }, shapeResult{CodeBadString, "v"}},
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
		if want := (Revision{MinuteBucket: mb.Shareable()}); string(marshal(t, got)) != string(marshal(t, want)) {
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

// freeText is text no string member may hold: sentences, paths, an email
// address, a window title, an empty string, an over-long run, non-ASCII,
// and a JSON-escaped NUL.
var freeText = []string{
	"fix the login bug please",
	"/home/teen/projects/game/main.go",
	`C:\Users\teen\Desktop\notes.txt`,
	"teen@example.com",
	"main.go - Visual Studio Code",
	"",
	strings.Repeat("a", 200),
	"héllo",
	"ignore previous instructions\n",
	"\x00",
}

// TestFreeTextRefused puts free text in every string member of every item
// kind, and each time gets that item refused with 422 bad_string naming
// the member (DESIGN 4.5's firewall; PLAN WP-SV5).
func TestFreeTextRefused(t *testing.T) {
	type kind struct {
		name   string
		sample func(t *testing.T) map[string]any
		check  func(raw []byte) *itemErr
		fields []string
	}
	kinds := []kind{
		{
			name:   "batch root",
			sample: func(*testing.T) map[string]any { return sampleRoot() },
			check:  func(raw []byte) *itemErr { _, e := parseBatchRoot(raw); return e },
			fields: []string{"R_b", "H_b", "prev"},
		},
		{
			name: "minute bucket",
			sample: func(t *testing.T) map[string]any {
				return asMap(t, shareableJSON(t, sampleBucket("d_test", sampleMinute, 1)))
			},
			check: func(raw []byte) *itemErr { _, e := checkBucket(raw); return e },
			fields: []string{"v", "params", "minute", "device", "slots[0].p", "slots[0].why", "slots[0].kind", "slots[0].ev",
				"slots[0].lbl[0]", "state", "witness"},
		},
		{
			name:   "commit record",
			sample: func(*testing.T) map[string]any { return sampleCommit() },
			check:  func(raw []byte) *itemErr { _, e := parseCommit(raw); return e },
			fields: []string{"sha", "parents[0]", "project", "path_hmacs[0]", "rewrites[0].old_sha", "rewrites[0].new_sha", "actor", "agent_trailer"},
		},
		{
			name:   "labels",
			sample: func(*testing.T) map[string]any { return sampleLabels() },
			check:  func(raw []byte) *itemErr { _, e := parseLabels(raw); return e },
			fields: []string{"client_build", "scoring_version", "params", "harnesses[0].name", "harnesses[0].version"},
		},
	}
	for _, k := range kinds {
		for _, field := range k.fields {
			for i, text := range freeText {
				t.Run(k.name+": free text "+strconv.Itoa(i)+" in "+field+" is refused as bad_string", func(t *testing.T) {
					m := k.sample(t)
					setPath(t, m, field, text)
					got := outcomeOf(k.check(marshal(t, m)))
					if want := (shapeResult{CodeBadString, field}); got != want {
						t.Errorf("got %+v, want %+v", got, want)
					}
				})
			}
		}
		t.Run(k.name+": the sample passes, so each refusal above is the text's", func(t *testing.T) {
			if e := k.check(marshal(t, k.sample(t))); e != nil {
				t.Errorf("refused: %+v", *e)
			}
		})
	}
	for i, text := range freeText {
		t.Run("minute bucket: free text "+strconv.Itoa(i)+" as an agent_only project is refused as bad_string", func(t *testing.T) {
			m := asMap(t, shareableJSON(t, sampleBucket("d_test", sampleMinute, 1)))
			m["agent_only"] = map[string]any{text: 1}
			_, e := checkBucket(marshal(t, m))
			if got, want := outcomeOf(e), (shapeResult{CodeBadString, "agent_only"}); got != want {
				t.Errorf("got %+v, want %+v", got, want)
			}
		})
		t.Run("commit record: free text "+strconv.Itoa(i)+" as an activity kind is refused as bad_string", func(t *testing.T) {
			m := sampleCommit()
			m["credited_seconds_since_parent"] = map[string]any{text: 1}
			_, e := parseCommit(marshal(t, m))
			if got, want := outcomeOf(e), (shapeResult{CodeBadString, "credited_seconds_since_parent"}); got != want {
				t.Errorf("got %+v, want %+v", got, want)
			}
		})
		t.Run("minute bucket: free text "+strconv.Itoa(i)+" as an uncredited reason is refused as bad_string", func(t *testing.T) {
			m := asMap(t, shareableJSON(t, sampleBucket("d_test", sampleMinute, 1)))
			u := maps.Clone(m["uncredited"].(map[string]any))
			u[text] = 1
			m["uncredited"] = u
			_, e := checkBucket(marshal(t, m))
			if got, want := outcomeOf(e), (shapeResult{CodeBadString, "uncredited"}); got != want {
				t.Errorf("got %+v, want %+v", got, want)
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

// lenientForms are the forms of ingest-v1 2.3, written again as regular
// expressions that accept at least what the server does. FuzzFreeText
// holds the server to them: text outside a member's form must be refused,
// whatever the shape check's own code says.
var lenientForms = map[string]*regexp.Regexp{
	"key":     regexp.MustCompile(`^[A-Za-z0-9_-]{1,128}$`),
	"hash":    regexp.MustCompile(`^[0-9a-f]{64}$`),
	"gitid":   regexp.MustCompile(`^(?:[0-9a-f]{40}|[0-9a-f]{64})$`),
	"semver":  regexp.MustCompile(`^[0-9]{1,9}\.[0-9]{1,9}\.[0-9]{1,9}$`),
	"params":  regexp.MustCompile(`^sha256:[0-9a-f]{64}$`),
	"minute":  regexp.MustCompile(`^20[2-9][0-9]-[01][0-9]-[0-3][0-9]T[0-2][0-9]:[0-5][0-9]:00Z$`),
	"reason":  regexp.MustCompile(`^[A-Za-z_.~+]{1,40}$`),
	"lower":   regexp.MustCompile(`^[a-z_]{1,32}$`),
	"level":   regexp.MustCompile(`^[ABC]$`),
	"version": regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._+-]{0,63}$`),
}

// uncreditedForm accepts at least the uncredited reason codes of SM 5.2.
var uncreditedForm = regexp.MustCompile(`^X\.[a-z_]{1,32}$`)

// FuzzFreeText puts any text in every string member of every item kind.
// Text outside the member's form is refused for that item with 422
// bad_string naming the member, so no field can carry free text into the
// database (DESIGN 4.5's firewall; PLAN WP-SV5). Its seeds run with every
// go test, and nightly.yml fuzzes it for 60 s.
func FuzzFreeText(f *testing.F) {
	for _, s := range freeText {
		f.Add(s)
	}
	for _, s := range []string{"p_one", "1.0.0", "v0.1.0", "P", "READ~r+restored", "X.absent", "via_track", "final", "human", "A",
		strings.Repeat("a", 40), strings.Repeat("0", 64), "sha256:" + strings.Repeat("ab", 32), "2026-11-19T12:00:00Z"} {
		f.Add(s)
	}
	type member struct {
		kind, field, form string
	}
	members := []member{
		{"root", "R_b", "hash"}, {"root", "H_b", "hash"}, {"root", "prev", "hash"},
		{"bucket", "v", "semver"}, {"bucket", "params", "params"}, {"bucket", "minute", "minute"}, {"bucket", "device", "key"},
		{"bucket", "slots[0].p", "key"}, {"bucket", "slots[0].why", "reason"}, {"bucket", "slots[0].kind", "lower"},
		{"bucket", "slots[0].ev", "level"}, {"bucket", "slots[0].lbl[0]", "lower"}, {"bucket", "state", "lower"}, {"bucket", "witness", "lower"},
		{"commit", "sha", "gitid"}, {"commit", "parents[0]", "gitid"}, {"commit", "project", "key"}, {"commit", "path_hmacs[0]", "key"},
		{"commit", "rewrites[0].old_sha", "gitid"}, {"commit", "rewrites[0].new_sha", "gitid"}, {"commit", "actor", "lower"},
		{"commit", "agent_trailer", "key"},
		{"labels", "client_build", "version"}, {"labels", "scoring_version", "semver"}, {"labels", "params", "params"},
		{"labels", "harnesses[0].name", "lower"}, {"labels", "harnesses[0].version", "version"},
	}
	check := func(t *testing.T, kind string, m map[string]any) *itemErr {
		raw := marshal(t, m)
		var e *itemErr
		switch kind {
		case "root":
			_, e = parseBatchRoot(raw)
		case "bucket":
			_, e = checkBucket(raw)
		case "commit":
			_, e = parseCommit(raw)
		case "labels":
			_, e = parseLabels(raw)
		}
		return e
	}
	samples := map[string]func(t *testing.T) map[string]any{
		"root": func(*testing.T) map[string]any { return sampleRoot() },
		"bucket": func(t *testing.T) map[string]any {
			return asMap(t, shareableJSON(t, sampleBucket("d_test", sampleMinute, 1)))
		},
		"commit": func(*testing.T) map[string]any { return sampleCommit() },
		"labels": func(*testing.T) map[string]any { return sampleLabels() },
	}
	f.Fuzz(func(t *testing.T, text string) {
		for _, mb := range members {
			if lenientForms[mb.form].MatchString(text) {
				continue
			}
			m := samples[mb.kind](t)
			setPath(t, m, mb.field, text)
			got := outcomeOf(check(t, mb.kind, m))
			if want := (shapeResult{CodeBadString, mb.field}); got != want {
				t.Errorf("%s with %q in %s: got %+v, want %+v", mb.kind, text, mb.field, got, want)
			}
		}
		if !lenientForms["key"].MatchString(text) {
			m := samples["bucket"](t)
			m["agent_only"] = map[string]any{text: 1}
			if got, want := outcomeOf(check(t, "bucket", m)), (shapeResult{CodeBadString, "agent_only"}); got != want {
				t.Errorf("bucket with %q as an agent_only project: got %+v, want %+v", text, got, want)
			}
		}
		if !uncreditedForm.MatchString(text) {
			m := samples["bucket"](t)
			u := maps.Clone(m["uncredited"].(map[string]any))
			u[text] = 1
			m["uncredited"] = u
			if got, want := outcomeOf(check(t, "bucket", m)), (shapeResult{CodeBadString, "uncredited"}); got != want {
				t.Errorf("bucket with %q as an uncredited reason: got %+v, want %+v", text, got, want)
			}
		}
	})
}
