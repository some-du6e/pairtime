// SPDX-License-Identifier: AGPL-3.0-or-later

package agent

import (
	"encoding/json"
	"fmt"
	"path/filepath"
	"reflect"
	"testing"
	"time"

	"github.com/some-du6e/pairtime/internal/agent/doctor"
	"github.com/some-du6e/pairtime/internal/agent/inbox"
	"github.com/some-du6e/pairtime/internal/platform"
	"github.com/some-du6e/pairtime/internal/platform/clock"
	"github.com/some-du6e/pairtime/internal/version"
	"github.com/some-du6e/pairtime/pkg/event"
)

// fullStatus is a status reply with every field set, so that a field
// doctor reads under a name the agent doesn't write comes back empty.
func fullStatus() statusReply {
	at := time.Date(2026, 11, 24, 9, 0, 0, 0, time.UTC)
	both := signalReply{Via: "a source", Unknown: "a reason"}
	stats := harnessStats{Calls: 1, Records: 2, ParseFailures: 3, WriteFailures: 4, LateLocal: 9, Ignored: 5, LastRecord: 6, LastFailure: "Stop: unknown field",
		Lines: 7, LineFailures: 8}
	return statusReply{
		Version: version.Info{Version: "v0.1.0", Commit: "0123", Date: "2026-11-24T09:00:00Z", Modified: true, GoVersion: "go1.26.0", Platform: "linux/amd64"},
		PID:     4242, Started: at, UptimeS: 60, Mode: "local", Port: 7247, Wakeups: 7,
		Jobs: []jobReply{{Name: "drain", EveryS: 60, Runs: 1}},
		IPC: ipcReply{Via: inbox.ViaSocket, Stored: 1, Duplicates: 2, Refused: 3,
			Spooled: spooledReply{Since: at.Add(-spooledWindow), Calls: 4, Prompts: 5, Unknown: 6}},
		Signals: signalsReply{Idle: both, Focus: both, Power: both, Lock: both, Session: "x11",
			Loop: &loopReply{MainThread: true, Dropped: 1}},
		Adapters: adaptersReply{Harnesses: map[event.Harness]harnessStats{event.HarnessClaudeCode: stats}, Other: stats,
			Waiting: 1, Dropped: 2, Git: gitReply{Watched: 1, Changes: 2}},
		Footprint: footprintReply{RSSBytes: 1, PeakRSSBytes: 2, CPUS: 3},
		Problems:  map[string]string{"ledger": "the ledger can't open"},
	}
}

// TestDoctorReadsTheStatus fails when doctor reads a field of the status
// under a name or a type the agent doesn't write it with: every field of
// doctor.Status, all the way down, comes back from a full reply set, and
// with the agent's own value.
func TestDoctorReadsTheStatus(t *testing.T) {
	b, err := json.Marshal(fullStatus())
	if err != nil {
		t.Fatal(err)
	}
	var d doctor.Status
	if err := json.Unmarshal(b, &d); err != nil {
		t.Fatalf("doctor can't read the agent's status: %v", err)
	}
	tests := []struct {
		name  string
		check func(t *testing.T)
	}{
		{"every field doctor reads is one the agent writes", func(t *testing.T) {
			for _, path := range zeroFields(reflect.ValueOf(d), "status") {
				t.Errorf("%s is empty after reading a full status: the agent writes no field of that name", path)
			}
		}},
		{"each value doctor reads is the one the agent wrote", func(t *testing.T) {
			back, err := json.Marshal(d)
			if err != nil {
				t.Fatal(err)
			}
			var agentSide, doctorSide map[string]any
			if err := json.Unmarshal(b, &agentSide); err != nil {
				t.Fatal(err)
			}
			if err := json.Unmarshal(back, &doctorSide); err != nil {
				t.Fatal(err)
			}
			for _, diff := range sameValues(doctorSide, agentSide, "status") {
				t.Error(diff)
			}
		}},
	}
	for _, tt := range tests {
		t.Run(tt.name, tt.check)
	}
}

// zeroFields lists the paths of the empty values in v, a struct, through
// structs, pointers, maps and slices.
func zeroFields(v reflect.Value, path string) []string {
	switch v.Kind() {
	case reflect.Pointer:
		if v.IsNil() {
			return []string{path}
		}
		return zeroFields(v.Elem(), path)
	case reflect.Struct:
		if v.Type() == reflect.TypeFor[time.Time]() {
			if v.Interface().(time.Time).IsZero() {
				return []string{path}
			}
			return nil
		}
		var out []string
		for i := range v.NumField() {
			out = append(out, zeroFields(v.Field(i), path+"."+v.Type().Field(i).Name)...)
		}
		return out
	case reflect.Map, reflect.Slice:
		if v.Len() == 0 {
			return []string{path}
		}
		var out []string
		if v.Kind() == reflect.Map {
			for _, k := range v.MapKeys() {
				out = append(out, zeroFields(v.MapIndex(k), fmt.Sprintf("%s[%v]", path, k))...)
			}
		}
		return out
	}
	if v.IsZero() {
		return []string{path}
	}
	return nil
}

// sameValues lists where got, doctor's view as JSON, holds a value want,
// the agent's, doesn't.
func sameValues(got, want any, path string) []string {
	switch g := got.(type) {
	case map[string]any:
		w, ok := want.(map[string]any)
		if !ok {
			return []string{fmt.Sprintf("%s is an object to doctor and %v to the agent", path, want)}
		}
		var out []string
		for k, v := range g {
			out = append(out, sameValues(v, w[k], path+"."+k)...)
		}
		return out
	}
	if !reflect.DeepEqual(got, want) {
		return []string{fmt.Sprintf("%s is %v to doctor and %v to the agent", path, got, want)}
	}
	return nil
}

func TestStatusCountsSpooledCalls(t *testing.T) {
	now := time.Now().UTC()
	call := func(name string, via inbox.Via, sent time.Time) inbox.Delivery {
		self := inbox.Proc{PID: 400, Name: "pairtime", Start: sent}
		d := inbox.Delivery{Message: hookMessage(t, name, `{"session_id":"s1"}`, self, sent), Via: via, Received: clock.Stamp{Wall: now}}
		if via != inbox.ViaInbox {
			d.PeerPID = 400
		}
		return d
	}
	tests := []struct {
		name                                string
		calls                               []inbox.Delivery
		wantCalls, wantPrompts, wantUnknown uint64
	}{
		{
			name: "the status counts the week's hook calls only the spool delivered, and how many were prompts, and none the socket did",
			calls: []inbox.Delivery{
				call("UserPromptSubmit", inbox.ViaInbox, now.Add(-time.Hour)),
				call("Stop", inbox.ViaInbox, now.Add(-time.Hour)),
				call("UserPromptSubmit", inbox.ViaSocket, now.Add(-time.Hour)),
			},
			wantCalls: 2, wantPrompts: 1,
		},
		{
			name: "a spooled call made before the week doesn't count",
			calls: []inbox.Delivery{
				call("UserPromptSubmit", inbox.ViaInbox, now.Add(-spooledWindow-time.Hour)),
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			j, err := inbox.OpenJournal(filepath.Join(t.TempDir(), "received"), now)
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { j.Close() })
			sink := newHookSink(j, platform.Linux, newHookTies(), hookSteps{})
			for _, d := range tt.calls {
				if err := sink.Deliver(d); err != nil {
					t.Fatal(err)
				}
			}
			a := &agent{journal: j}
			got := a.ipcStatus().Spooled
			if got.Calls != tt.wantCalls || got.Prompts != tt.wantPrompts || got.Unknown != tt.wantUnknown {
				t.Errorf("the status's spooled is %+v, want %d calls, %d prompts and %d unknown", got, tt.wantCalls, tt.wantPrompts, tt.wantUnknown)
			}
			if d := now.Sub(got.Since); d < spooledWindow-time.Minute || d > spooledWindow+time.Minute {
				t.Errorf("the status counts since %v, %v before now, want a week", got.Since, d)
			}
		})
	}
}

func TestFootprint(t *testing.T) {
	tests := []struct {
		name  string
		check func(t *testing.T, f footprintReply)
	}{
		{"the CPU time this process has used is more than none", func(t *testing.T, f footprintReply) {
			if f.CPUS <= 0 {
				t.Errorf("cpu_s = %v", f.CPUS)
			}
		}},
		{"the resident set, where the OS gives it, is never above its peak", func(t *testing.T, f footprintReply) {
			if f.RSSBytes > 0 && f.PeakRSSBytes > 0 && f.RSSBytes > f.PeakRSSBytes {
				t.Errorf("rss %d is above its peak %d", f.RSSBytes, f.PeakRSSBytes)
			}
		}},
		{"the peak resident set is known and more than a megabyte", func(t *testing.T, f footprintReply) {
			if f.PeakRSSBytes < 1<<20 {
				t.Errorf("peak rss = %d bytes", f.PeakRSSBytes)
			}
		}},
	}
	f := footprintNow()
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) { tt.check(t, f) })
	}
}
