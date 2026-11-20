// SPDX-License-Identifier: AGPL-3.0-or-later

package retention

import (
	"context"
	"fmt"
	"os"
	"reflect"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/some-du6e/pairtime/docs"
	"github.com/some-du6e/pairtime/internal/server/migrations"
	"github.com/some-du6e/pairtime/internal/server/store"
	"github.com/some-du6e/pairtime/internal/server/store/storetest"
	"github.com/some-du6e/pairtime/internal/server/writer"
)

const day = 24 * time.Hour

// validDoc is a small schedule that loads. TestLoad's cases edit it.
const validDoc = `{"about": "test", "version": 1, "source": "DESIGN 4.4",
"spot_check_window": {"months": 12},
"no_personal_data": ["schema_migrations"],
"entries": [
{"id": "server.ip_ja4", "where": "server", "data": "IPs", "kept": "{days}", "days": 30,
 "jobs": [{"table": "request_origins", "key": ["id"], "time_column": "seen_at_ms"}]}
]}`

func TestLoad(t *testing.T) {
	tests := []struct {
		name     string
		data     string // the schedule; "" means validDoc with old replaced by new
		old, new string
		wantErr  string // substring; "" means Load must succeed
	}{
		{name: "the embedded docs/retention.json loads", data: string(docs.RetentionJSON)},
		{name: "a valid schedule loads"},
		{name: "an unknown field is refused, so a misspelt key can't drop a rule", old: `"days": 30`, new: `"dayz": 30`, wantErr: `unknown field "dayz"`},
		{name: "anything after the schedule is refused", data: validDoc + "{}", wantErr: "more after"},
		{name: "another format version is refused", old: `"version": 1`, new: `"version": 2`, wantErr: "version is 2"},
		{name: "a spot-check window under a month is refused", old: `"months": 12`, new: `"months": 0`, wantErr: "spot_check_window.months is 0"},
		{name: "an unknown placeholder is refused", old: `"kept": "{days}"`, new: `"kept": "{days} or {hours}"`, wantErr: "kept has {hours}"},
		{name: "days must appear in kept as {days}", old: `"kept": "{days}"`, new: `"kept": "a month"`, wantErr: "kept doesn't say {days}"},
		{name: "{days} needs days", old: `"days": 30,`, new: ``, wantErr: "days isn't set"},
		{name: "a job by days needs days", old: `"days": 30,`, new: ``, wantErr: "a job by days needs the entry's days"},
		{name: "a job by the spot-check window needs {window} in kept", old: `"time_column": "seen_at_ms"`, new: `"time_column": "seen_at_ms", "period": "window"`, wantErr: "needs kept to say {window}"},
		{name: "an unknown period is refused", old: `"time_column": "seen_at_ms"`, new: `"time_column": "seen_at_ms", "period": "weeks"`, wantErr: `period is "weeks"`},
		{
			name: "an evidence entry may have jobs, since the evidence store's index rows are in the server's database",
			data: strings.NewReplacer(
				`"id": "server.ip_ja4", "where": "server", "data": "IPs", "kept": "{days}", "days": 30,`, `"id": "evidence.revealed", "where": "evidence", "data": "Reveals", "kept": "Until the window closes ({window})",`,
				`"time_column": "seen_at_ms"`, `"time_column": "decided_at_ms", "period": "window"`,
			).Replace(validDoc),
		},
		{
			name: "a hold's table needs a job by the window, or its holds would outlive the window",
			data: strings.NewReplacer(
				`"kept": "{days}"`, `"kept": "{days}, or until its window closes ({window})"`,
				`"time_column": "seen_at_ms"}`, `"time_column": "seen_at_ms", "hold": {"table": "holds", "decided_column": "decided_at_ms"}}`,
			).Replace(validDoc),
			wantErr: "nothing deletes the holds in holds",
		},
		{
			name: "a job by days on a hold's table doesn't count, since a hold lasts until its window closes",
			data: strings.NewReplacer(
				`"kept": "{days}"`, `"kept": "{days}, or until its window closes ({window})"`,
				`"time_column": "seen_at_ms"}`, `"time_column": "seen_at_ms", "hold": {"table": "holds", "decided_column": "decided_at_ms"}}, {"table": "holds", "key": ["submission_id", "id"], "time_column": "decided_at_ms"}`,
			).Replace(validDoc),
			wantErr: "nothing deletes the holds in holds",
		},
		{
			name: "a hold whose table a job by the window deletes from loads",
			data: strings.NewReplacer(
				`"kept": "{days}"`, `"kept": "{days}, or until its window closes ({window})"`,
				`"time_column": "seen_at_ms"}`, `"time_column": "seen_at_ms", "hold": {"table": "holds", "decided_column": "decided_at_ms"}}, {"table": "holds", "key": ["submission_id", "id"], "time_column": "decided_at_ms", "period": "window"}`,
			).Replace(validDoc),
		},
		{name: "kept may not write a number of its own beside {days}", old: `"kept": "{days}"`, new: `"kept": "{days}, or 31 days"`, wantErr: "a number besides {days}"},
		{name: "a device entry has no jobs, since its data isn't in the server's database", old: `"id": "server.ip_ja4", "where": "server"`, new: `"id": "device.ip_ja4", "where": "device"`, wantErr: "only server and evidence entries have tables and jobs"},
		{name: "an entry with a period can't list a table no job deletes", old: `"days": 30,`, new: `"days": 30, "tables": ["request_log"],`, wantErr: "nothing would delete table request_log"},
		{name: "lifetime_tables is for an entry with a period", old: `"kept": "{days}", "days": 30,`, new: `"kept": "forever", "lifetime_tables": ["rollups"],`, wantErr: "lifetime_tables is for an entry with a period"},
		{name: "a table in lifetime_tables can't also be a job's", old: `"days": 30,`, new: `"days": 30, "lifetime_tables": ["request_origins"],`, wantErr: "is in lifetime_tables, but the job deletes its rows"},
		{name: "two jobs of one entry can't delete from one table", old: `"jobs": [{"table": "request_origins", "key": ["id"], "time_column": "seen_at_ms"}]`,
			new: `"jobs": [{"table": "request_origins", "key": ["id"], "time_column": "seen_at_ms"}, {"table": "request_origins", "key": ["id"], "time_column": "seen_at_ms"}]`, wantErr: "another job of the entry deletes from request_origins"},
		{name: "an unknown place is refused", old: `"where": "server"`, new: `"where": "cloud"`, wantErr: `where is "cloud"`},
		{name: "an id must start with its place", old: `"id": "server.ip_ja4"`, new: `"id": "ip_ja4"`, wantErr: "id must be where.name"},
		{name: "an id is used once", old: "\n]}", new: ",\n" + `{"id": "server.ip_ja4", "where": "server", "data": "x", "kept": "forever"}` + "\n]}", wantErr: "the id is used twice"},
		{name: "a table belongs to one entry", old: `"no_personal_data": ["schema_migrations"]`, new: `"no_personal_data": ["request_origins"]`, wantErr: "table request_origins belongs to both"},
		{name: "a column name can't carry SQL", old: `"time_column": "seen_at_ms"`, new: `"time_column": "seen_at_ms; DROP TABLE devices"`, wantErr: "isn't a lowercase SQL name"},
		{name: "a key column is listed once", old: `"key": ["id"]`, new: `"key": ["id", "id"]`, wantErr: "key lists id twice"},
		{name: "data can't be empty", old: `"data": "IPs"`, new: `"data": " "`, wantErr: "data is empty"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			data := tt.data
			if data == "" {
				data = validDoc
				if tt.old != "" {
					if !strings.Contains(data, tt.old) {
						t.Fatalf("validDoc has no %q to replace", tt.old)
					}
					data = strings.Replace(data, tt.old, tt.new, 1)
				}
			}
			_, err := Load([]byte(data))
			switch {
			case tt.wantErr == "" && err != nil:
				t.Errorf("Load: %v", err)
			case tt.wantErr != "" && err == nil:
				t.Errorf("Load succeeded, want an error containing %q", tt.wantErr)
			case tt.wantErr != "" && !strings.Contains(err.Error(), tt.wantErr):
				t.Errorf("Load error = %q, want it to contain %q", err, tt.wantErr)
			}
		})
	}
}

func TestKept(t *testing.T) {
	tests := []struct {
		name   string
		window Window
		entry  Entry
		want   string
	}{
		{name: "{days} becomes the entry's days", window: Window{Months: 12}, entry: Entry{Kept: "{days}, then deleted", Days: 30}, want: "30 days, then deleted"},
		{name: "one day is singular", window: Window{Months: 12}, entry: Entry{Kept: "{days}", Days: 1}, want: "1 day"},
		{name: "{window} counts months after the decision", window: Window{Months: 12}, entry: Entry{Kept: "until ({window})"}, want: "until (12 months after the decision)"},
		{name: "a provisional window says it is a default", window: Window{Months: 12, Provisional: "DESIGN 13.2"}, entry: Entry{Kept: "until ({window})"},
			want: "until (12 months after the decision, a default until Hack Club sets the window)"},
		{name: "one month is singular", window: Window{Months: 1}, entry: Entry{Kept: "{window}"}, want: "1 month after the decision"},
		{name: "text without placeholders is unchanged", window: Window{Months: 12}, entry: Entry{Kept: "While your account exists"}, want: "While your account exists"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			p := &Policy{SpotCheckWindow: tt.window}
			if got := p.Kept(tt.entry); got != tt.want {
				t.Errorf("Kept = %q, want %q", got, tt.want)
			}
		})
	}
}

// The DESIGN sections whose tables the periods come from.
const (
	design44  = "4.4 Data inventory and retention"
	design103 = "10.3 Backups and the restore drill"
)

// designRows returns the rows of the table in the DESIGN section with the
// given heading, each split into its cells. In 4.4 they are where, data
// and kept; in 10.3, layer, mechanism, and RPO and RTO.
func designRows(t *testing.T, heading string) [][]string {
	t.Helper()
	data, err := os.ReadFile("../../../docs/DESIGN.md")
	if err != nil {
		t.Fatal(err)
	}
	_, section, ok := strings.Cut(string(data), "\n### "+heading+"\n")
	if !ok {
		t.Fatalf("docs/DESIGN.md has no section %s", heading)
	}
	section, _, _ = strings.Cut(section, "\n### ")
	var rows [][]string
	for _, line := range strings.Split(section, "\n") {
		if strings.HasPrefix(line, "| ") {
			rows = append(rows, strings.Split(strings.Trim(line, "| "), " | "))
		}
	}
	return rows
}

// TestDesignPeriods checks every period docs/retention.json states against
// the oracle, DESIGN 4.4's own text, or for the server's database backups,
// which 4.4 doesn't list, DESIGN 10.3's: each entry's days, and each number
// that an entry without days writes in its kept text, such as the access
// log's 2 years. The sessions entry's numbers are package accounts'
// constants, which TestSessionRetention there checks.
func TestDesignPeriods(t *testing.T) {
	p, err := Default()
	if err != nil {
		t.Fatal(err)
	}
	tests := []struct {
		name    string
		id      string // the entry in docs/retention.json
		section string // the DESIGN section; "" means 4.4
		where   string // the row's first cell
		data    string // the start of its second cell
		re      string // captures the period in its third cell, or for 10.3 in its second
		hours   bool   // the period is in hours, a whole number of days
		kept    string // for an entry without days, captures the number its kept text writes
	}{
		{name: "prompt text is kept 90 days on the device", id: "device.prompt_text", where: "Device", data: "Prompt text, final agent messages", re: `^(\d+) days, or until a submission covering them is decided, whichever is later`},
		{name: "file paths and tool commands are kept 30 days on the device", id: "device.paths_commands", where: "Device", data: "File paths in the vault; tool commands", re: `^(\d+) days$`},
		{name: "presence, focus, typing aggregates and sketches are kept 90 days on the device", id: "device.activity", where: "Device", data: "Presence and focus slots", re: `^(\d+) days$`},
		{name: "ledger records are kept 180 days on the device, or longer for a submission", id: "device.ledger", where: "Device", data: "Ledger records and batch roots", re: `^(\d+) days, or longer while a submission using them is inside its spot-check window$`},
		{name: "minute buckets are kept 180 days on the device", id: "device.buckets", where: "Device", data: "Minute buckets", re: `^(\d+) days; day rollups and coach records until the teen deletes them$`},
		{name: "device rows are kept 90 days past the device's lifetime", id: "server.devices", where: "Server", data: "`device_id`, OS family", re: `^Device lifetime plus (\d+) days$`},
		{name: "IP addresses and JA4 are kept 30 days, then deleted", id: "server.ip_ja4", where: "Server", data: "Uploading IP address and JA4", re: `^(\d+) days, then deleted, not truncated`},
		{name: "per-minute buckets are kept 180 days, then daily totals", id: "server.minute_buckets", where: "Server", data: "Minute buckets", re: `^Per-minute detail for (\d+) days, the submission horizon, then daily totals per project`},
		{name: "witness rows are kept 180 days unless a submission uses them", id: "server.witness", where: "Server", data: "Witness rows and batch roots", re: `^(\d+) days; rows a submission uses stay until its spot-check window closes$`},
		{name: "backup segments older than 180 days are deleted", id: "server.backups", where: "Server", data: "Wrapped `K_dev` and encrypted backups", re: `; backup segments older than (\d+) days are deleted$`},
		{name: "deleted rows stay in the continuous replica for its 72-hour retention", id: "server.db_replica", section: design103, where: "Continuous", data: "Litestream to object storage", re: `snapshots every \d+ h, retention (\d+) h \(`, hours: true},
		{name: "deleted rows stay in the nightly snapshots for their 30-day lifecycle", id: "server.db_snapshots", section: design103, where: "Nightly", data: "`VACUUM INTO` a snapshot", re: `uploaded to a second bucket with a (\d+)-day lifecycle\.`},
		{name: "the evidence access log is kept 2 years", id: "server.access_log", where: "Server", data: "Evidence access log", re: `^(\d+) years, visible to the teen$`, kept: `^(\d+) years$`},
		{name: "Claude Code keeps its transcripts 30 days by default", id: "device.transcripts", where: "Device", data: "Transcripts and agent output", re: `\(Claude Code: (\d+) days by default; TP B8\)`, kept: `\(Claude Code keeps them for (\d+) days unless you change it\)$`},
	}
	checked := make(map[string]bool)
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			checked[tt.id] = true
			e, ok := p.Find(tt.id)
			if !ok {
				t.Fatalf("docs/retention.json has no entry %s", tt.id)
			}
			section, cell := design44, 2
			if tt.section != "" {
				section, cell = tt.section, 1
			}
			var kept string
			for _, r := range designRows(t, section) {
				if len(r) == 3 && r[0] == tt.where && strings.HasPrefix(r[1], tt.data) {
					kept = r[cell]
					break
				}
			}
			if kept == "" {
				t.Fatalf("DESIGN %s has no row %q, %q...", section, tt.where, tt.data)
			}
			m := regexp.MustCompile(tt.re).FindStringSubmatch(kept)
			if m == nil {
				t.Fatalf("DESIGN %s's %q row says %q, which doesn't match %s", section, tt.data, kept, tt.re)
			}
			want, _ := strconv.Atoi(m[1])
			if tt.hours {
				if want%24 != 0 {
					t.Fatalf("DESIGN %s says %d h, which isn't a whole number of days", section, want)
				}
				want /= 24
			}
			if tt.kept != "" {
				if e.Days != 0 {
					t.Fatalf("%s has days %d, so its kept can't write a number of its own; compare its days", tt.id, e.Days)
				}
				km := regexp.MustCompile(tt.kept).FindStringSubmatch(e.Kept)
				if km == nil {
					t.Fatalf("%s's kept is %q, which doesn't match %s", tt.id, e.Kept, tt.kept)
				}
				if got, _ := strconv.Atoi(km[1]); got != want {
					t.Errorf("%s's kept says %d (%q); DESIGN %s says %d (%q)", tt.id, got, e.Kept, section, want, kept)
				}
				return
			}
			if e.Days != want {
				t.Errorf("%s has days %d; DESIGN %s says %d (%q)", tt.id, e.Days, section, want, kept)
			}
		})
	}
	t.Run("every entry with days is checked against DESIGN", func(t *testing.T) {
		for _, e := range p.Entries {
			if e.Days > 0 && !checked[e.ID] {
				t.Errorf("%s has days %d and no case in TestDesignPeriods", e.ID, e.Days)
			}
		}
	})
	t.Run("every number written in a kept text is checked", func(t *testing.T) {
		// Load refuses a number beside {days}, so these are entries
		// without days.
		elsewhere := map[string]bool{"server.sessions": true} // accounts' TestSessionRetention
		for _, e := range p.Entries {
			if strings.ContainsAny(e.Kept, "0123456789") && !checked[e.ID] && !elsewhere[e.ID] {
				t.Errorf("%s's kept says %q, and no case in TestDesignPeriods checks its number", e.ID, e.Kept)
			}
		}
	})
	t.Run("the spot-check window is DESIGN 4.4's default, marked provisional", func(t *testing.T) {
		var kept string
		for _, r := range designRows(t, design44) {
			if len(r) == 3 && r[0] == "Evidence store" {
				kept = r[2]
			}
		}
		m := regexp.MustCompile(`(\d+) months after the decision until HQ sets a window`).FindStringSubmatch(kept)
		if m == nil {
			t.Fatalf("DESIGN 4.4's evidence store row says %q", kept)
		}
		if want, _ := strconv.Atoi(m[1]); p.SpotCheckWindow.Months != want {
			t.Errorf("spot_check_window.months = %d; DESIGN 4.4 says %d", p.SpotCheckWindow.Months, want)
		}
		if p.SpotCheckWindow.Provisional == "" {
			t.Error("spot_check_window.provisional is empty, but HQ hasn't set the window (DESIGN 13.2 item 4)")
		}
	})
}

func TestJobs(t *testing.T) {
	tests := []struct {
		name     string
		edit     func(p *Policy)
		wantJobs []string // ids, and "+hold" when the job has its hold
		wantOff  []string
	}{
		{
			name:     "every job waits while its table doesn't exist, and the rollups' jobs, whose tables exist, run",
			edit:     func(*Policy) {},
			wantJobs: []string{"server.minute_buckets/rollup_minutes", "server.minute_buckets/rollup_flags"},
			wantOff: []string{
				"server.devices/devices awaits WP-SV3", "server.ip_ja4/request_origins awaits WP-SV4",
				"server.minute_buckets/minute_buckets awaits WP-SV7", "server.minute_buckets/submission_minutes awaits WP-SB4",
				"server.witness/witness_rows awaits WP-SV4", "server.witness/submission_batches awaits WP-SB4",
			},
		},
		{
			name: "a job whose table exists runs, without a hold whose table doesn't",
			edit: func(p *Policy) {
				for _, e := range p.Entries {
					for k := range e.Jobs {
						e.Jobs[k].Awaits = ""
					}
				}
			},
			wantJobs: []string{
				"server.devices/devices", "server.ip_ja4/request_origins",
				"server.minute_buckets/minute_buckets", "server.minute_buckets/submission_minutes",
				"server.minute_buckets/rollup_minutes", "server.minute_buckets/rollup_flags",
				"server.witness/witness_rows", "server.witness/submission_batches",
			},
			wantOff: []string{"server.minute_buckets/minute_buckets hold awaits WP-SB4", "server.witness/witness_rows hold awaits WP-SB4"},
		},
		{
			name: "a job runs with its hold once both tables exist",
			edit: func(p *Policy) {
				for _, e := range p.Entries {
					for k := range e.Jobs {
						e.Jobs[k].Awaits = ""
						if h := e.Jobs[k].Hold; h != nil {
							h.Awaits = ""
						}
					}
				}
			},
			wantJobs: []string{
				"server.devices/devices", "server.ip_ja4/request_origins",
				"server.minute_buckets/minute_buckets+hold", "server.minute_buckets/submission_minutes",
				"server.minute_buckets/rollup_minutes", "server.minute_buckets/rollup_flags",
				"server.witness/witness_rows+hold", "server.witness/submission_batches",
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			p, err := Default()
			if err != nil {
				t.Fatal(err)
			}
			tt.edit(p)
			jobs, off := p.Jobs()
			var got []string
			for _, j := range jobs {
				id := j.ID
				if j.Hold != nil {
					id += "+hold"
				}
				got = append(got, id)
				e, _ := p.Find(j.Entry)
				switch {
				case j.WindowMonths > 0:
					if j.WindowMonths != p.SpotCheckWindow.Months || j.MaxAge != 0 {
						t.Errorf("%s runs by %d months and MaxAge %v, want the spot-check window's %d months alone", j.ID, j.WindowMonths, j.MaxAge, p.SpotCheckWindow.Months)
					}
				case j.MaxAge != time.Duration(e.Days)*day:
					t.Errorf("%s MaxAge = %v, want the entry's %d days", j.ID, j.MaxAge, e.Days)
				}
				if j.Hold != nil && j.Hold.WindowMonths != p.SpotCheckWindow.Months {
					t.Errorf("%s hold window = %d months, want the schedule's %d", j.ID, j.Hold.WindowMonths, p.SpotCheckWindow.Months)
				}
			}
			if !slices.Equal(got, tt.wantJobs) {
				t.Errorf("jobs = %q, want %q", got, tt.wantJobs)
			}
			if !slices.Equal(off, tt.wantOff) {
				t.Errorf("off = %q, want %q", off, tt.wantOff)
			}
		})
	}
}

func TestDeleteSQL(t *testing.T) {
	now := time.Date(2027, 7, 1, 12, 0, 0, 0, time.UTC)
	tests := []struct {
		name     string
		job      Job
		wantSQL  string
		wantArgs []any
	}{
		{
			name:     "a one-column key deletes by that column, in batches",
			job:      Job{ID: "server.ip_ja4", Table: "request_origins", Key: []string{"id"}, TimeColumn: "seen_at_ms", MaxAge: 30 * day},
			wantSQL:  "DELETE FROM request_origins WHERE id IN (SELECT t.id FROM request_origins t WHERE t.seen_at_ms <= $1 LIMIT $2)",
			wantArgs: []any{now.Add(-30 * day).UnixMilli(), 500},
		},
		{
			name: "a hold keeps the rows an open or recently decided submission uses",
			job: Job{ID: "server.witness", Table: "witness_rows", Key: []string{"device_id", "b"}, TimeColumn: "received_at_ms", MaxAge: 180 * day,
				Hold: &Hold{Table: "submission_batches", DecidedColumn: "decided_at_ms", WindowMonths: 12}},
			wantSQL: "DELETE FROM witness_rows WHERE (device_id, b) IN (SELECT t.device_id, t.b FROM witness_rows t WHERE t.received_at_ms <= $1" +
				" AND NOT EXISTS (SELECT 1 FROM submission_batches h WHERE h.device_id = t.device_id AND h.b = t.b" +
				" AND (h.decided_at_ms IS NULL OR h.decided_at_ms > $3)) LIMIT $2)",
			wantArgs: []any{now.Add(-180 * day).UnixMilli(), 500, time.Date(2026, 7, 1, 12, 0, 0, 0, time.UTC).UnixMilli()},
		},
		{
			name:     "a job by the window deletes rows whose decision's window has closed",
			job:      Job{ID: "evidence.revealed/evidence_index", Table: "evidence_index", Key: []string{"blob"}, TimeColumn: "decided_at_ms", WindowMonths: 12},
			wantSQL:  "DELETE FROM evidence_index WHERE blob IN (SELECT t.blob FROM evidence_index t WHERE t.decided_at_ms <= $1 LIMIT $2)",
			wantArgs: []any{time.Date(2026, 7, 1, 12, 0, 0, 0, time.UTC).UnixMilli(), 500},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			gotSQL, gotArgs := tt.job.deleteSQL(now, 500)
			if gotSQL != tt.wantSQL {
				t.Errorf("SQL =\n%s\nwant\n%s", gotSQL, tt.wantSQL)
			}
			if !reflect.DeepEqual(gotArgs, tt.wantArgs) {
				t.Errorf("args = %v, want %v", gotArgs, tt.wantArgs)
			}
		})
	}
}

// utc is a UTC time to the minute, for writing test dates as literals.
func utc(y int, m time.Month, d, h, min int) time.Time {
	return time.Date(y, m, d, h, min, 0, 0, time.UTC)
}

// TestWindow checks when a spot-check window closes, and that the hold
// cutoff the jobs compute is its inverse, with dates written out rather
// than computed the way the code computes them.
func TestWindow(t *testing.T) {
	ms := time.Millisecond
	closes := []struct {
		name    string
		months  int
		decided time.Time
		want    time.Time
	}{
		{name: "a window closes the same day and time, months later", months: 12, decided: utc(2026, 7, 1, 12, 0), want: utc(2027, 7, 1, 12, 0)},
		{name: "a window into a shorter month stays open to its end", months: 1, decided: utc(2027, 3, 31, 10, 0), want: utc(2027, 5, 1, 0, 0)},
		{name: "a window from a leap day closes as March starts", months: 12, decided: utc(2028, 2, 29, 6, 0), want: utc(2029, 3, 1, 0, 0)},
		{name: "a window counts months in UTC, whatever zone the decision is in", months: 12, decided: time.Date(2026, 7, 1, 7, 0, 0, 0, time.FixedZone("UTC-5", -5*3600)), want: utc(2027, 7, 1, 12, 0)},
	}
	for _, tt := range closes {
		t.Run(tt.name, func(t *testing.T) {
			if got := (Window{Months: tt.months}).Closes(tt.decided); !got.Equal(tt.want) {
				t.Errorf("Closes(%v) = %v, want %v", tt.decided, got, tt.want)
			}
		})
	}
	cutoffs := []struct {
		name   string
		months int
		now    time.Time
		want   time.Time
	}{
		{name: "the cutoff is months before now when that date exists", months: 12, now: utc(2027, 7, 1, 12, 0), want: utc(2026, 7, 1, 12, 0)},
		{name: "on 2028-02-29 every decision of February 2027 has closed and none of 1 March", months: 12, now: utc(2028, 2, 29, 6, 0), want: utc(2027, 3, 1, 0, 0).Add(-ms)},
		{name: "18 months before 2028-08-29 is past February 2027 too", months: 18, now: utc(2028, 8, 29, 6, 0), want: utc(2027, 3, 1, 0, 0).Add(-ms)},
		{name: "one month before 31 March is the end of February", months: 1, now: utc(2027, 3, 31, 10, 0), want: utc(2027, 3, 1, 0, 0).Add(-ms)},
		{name: "the cutoff counts in UTC, whatever zone the clock is in", months: 12, now: time.Date(2027, 7, 1, 7, 0, 0, 0, time.FixedZone("UTC-5", -5*3600)), want: utc(2026, 7, 1, 12, 0)},
	}
	for _, tt := range cutoffs {
		t.Run(tt.name, func(t *testing.T) {
			if got := lastClosed(tt.now, tt.months); !got.Equal(tt.want) {
				t.Errorf("lastClosed(%v, %d) = %v, want %v", tt.now, tt.months, got, tt.want)
			}
		})
	}
	t.Run("a hold is released exactly when its window closes, every six hours for two years", func(t *testing.T) {
		// Closes never decreases as the decision gets later, so the
		// boundary decides it: the cutoff's own window has closed, and the
		// window of a decision a millisecond later hasn't.
		for _, months := range []int{1, 12, 18} {
			w := Window{Months: months}
			for now := utc(2027, 1, 1, 0, 0); now.Before(utc(2029, 1, 1, 0, 0)); now = now.Add(6 * time.Hour) {
				cut := lastClosed(now, months)
				if c := w.Closes(cut); c.After(now) {
					t.Fatalf("%d months, now %v: the cutoff %v closes at %v, after now, so its hold is released early", months, now, cut, c)
				}
				if c := w.Closes(cut.Add(ms)); !c.After(now) {
					t.Fatalf("%d months, now %v: a decision at %v closed at %v, but its hold stays", months, now, cut.Add(ms), c)
				}
			}
		}
	})
}

// TestSweep runs the server jobs under a fake clock against tables shaped
// as their bindings say, on SQLite and, in postgres.yml, Postgres. The
// ages and dates are written from DESIGN 4.4 here rather than read from
// the schedule, so with TestDesignPeriods it checks that the jobs delete
// exactly DESIGN 4.4's rows. A case's hold is a row of its job's hold
// table, and the hold table's own job deletes it once its window has
// closed. Each sweep runs on a database of its own.
func TestSweep(t *testing.T) {
	p, err := Default()
	if err != nil {
		t.Fatal(err)
	}
	type hold struct {
		open    bool      // the submission isn't decided
		decided time.Time // when it was decided, unless open
		other   bool      // the hold names a different row
		gone    bool      // the hold itself is deleted, because its window has closed
	}
	type sweepCase struct {
		name string
		job  string
		age  time.Duration // how long before the sweep the row's time is
		null bool          // the row's time is NULL
		hold *hold
		kept bool
	}
	sweeps := []struct {
		name  string
		at    time.Time
		cases []sweepCase
	}{
		{
			name: "a sweep on 2027-07-01",
			at:   utc(2027, 7, 1, 12, 0),
			cases: []sweepCase{
				{name: "an IP seen 30 days ago is deleted", job: "server.ip_ja4/request_origins", age: 30 * day},
				{name: "an IP seen a millisecond less than 30 days ago stays", job: "server.ip_ja4/request_origins", age: 30*day - time.Millisecond, kept: true},
				{name: "an IP seen an hour ago stays", job: "server.ip_ja4/request_origins", age: time.Hour, kept: true},
				{name: "an IP seen a year ago is deleted", job: "server.ip_ja4/request_origins", age: 365 * day},
				{name: "a minute bucket 180 days old loses its per-minute row", job: "server.minute_buckets/minute_buckets", age: 180 * day},
				{name: "a minute bucket a minute younger than 180 days keeps its row", job: "server.minute_buckets/minute_buckets", age: 180*day - time.Minute, kept: true},
				{name: "an old minute bucket an open submission uses keeps its row and its hold", job: "server.minute_buckets/minute_buckets", age: 400 * day, hold: &hold{open: true}, kept: true},
				{name: "an old minute bucket keeps its row and its hold a minute before its submission's window closes", job: "server.minute_buckets/minute_buckets", age: 400 * day, hold: &hold{decided: utc(2026, 7, 1, 12, 1)}, kept: true},
				{name: "an old minute bucket loses its row, and its hold goes, when its submission's window closes", job: "server.minute_buckets/minute_buckets", age: 400 * day, hold: &hold{decided: utc(2026, 7, 1, 12, 0), gone: true}},
				{name: "a device removed 90 days ago is deleted", job: "server.devices/devices", age: 90 * day},
				{name: "a device removed 89 days ago stays", job: "server.devices/devices", age: 89 * day, kept: true},
				{name: "a device that was never removed stays", job: "server.devices/devices", null: true, kept: true},
				{name: "a witness row received 180 days ago is deleted", job: "server.witness/witness_rows", age: 180 * day},
				{name: "a witness row received 179 days ago stays", job: "server.witness/witness_rows", age: 179 * day, kept: true},
				{name: "an old witness row an open submission uses stays, and so does its hold", job: "server.witness/witness_rows", age: 400 * day, hold: &hold{open: true}, kept: true},
				{name: "an old witness row and its hold stay 11 months after the submission's decision", job: "server.witness/witness_rows", age: 400 * day, hold: &hold{decided: utc(2026, 8, 1, 12, 0)}, kept: true},
				{name: "an old witness row and its hold stay a minute before the spot-check window closes", job: "server.witness/witness_rows", age: 400 * day, hold: &hold{decided: utc(2026, 7, 1, 12, 1)}, kept: true},
				{name: "an old witness row is deleted when the window closes, 12 months after the decision, and its hold goes too", job: "server.witness/witness_rows", age: 400 * day, hold: &hold{decided: utc(2026, 7, 1, 12, 0), gone: true}},
				{name: "a hold on a different batch keeps nothing, and stays while its submission is open", job: "server.witness/witness_rows", age: 400 * day, hold: &hold{open: true, other: true}},
			},
		},
		{
			name: "a sweep at noon on 2028-02-29, a date February 2027 lacks",
			at:   utc(2028, 2, 29, 12, 0),
			cases: []sweepCase{
				{name: "a decision at noon on 2027-02-28, whose window closed at noon on 2028-02-28, holds nothing and goes", job: "server.witness/witness_rows", age: 400 * day, hold: &hold{decided: utc(2027, 2, 28, 12, 0), gone: true}},
				{name: "a decision in the last minute of 2027-02-28 holds nothing and goes", job: "server.witness/witness_rows", age: 400 * day, hold: &hold{decided: utc(2027, 2, 28, 23, 59), gone: true}},
				{name: "a decision at midnight starting 2027-03-01 still holds its witness row", job: "server.witness/witness_rows", age: 400 * day, hold: &hold{decided: utc(2027, 3, 1, 0, 0)}, kept: true},
				{name: "a decision at noon on 2027-03-01, whose window closes at noon on 2028-03-01, still holds its witness row", job: "server.witness/witness_rows", age: 400 * day, hold: &hold{decided: utc(2027, 3, 1, 12, 0)}, kept: true},
				{name: "a minute bucket held by a decision at noon on 2027-02-28 loses its row, and the hold goes", job: "server.minute_buckets/minute_buckets", age: 400 * day, hold: &hold{decided: utc(2027, 2, 28, 12, 0), gone: true}},
				{name: "a minute bucket held by a decision at midnight starting 2027-03-01 keeps its row and its hold", job: "server.minute_buckets/minute_buckets", age: 400 * day, hold: &hold{decided: utc(2027, 3, 1, 0, 0)}, kept: true},
			},
		},
	}

	jobs := p.AllJobs()
	byID := make(map[string]Job)
	byTable := make(map[string]Job)
	for _, j := range jobs {
		byID[j.ID] = j
		byTable[j.Table] = j
	}
	for _, sw := range sweeps {
		for _, c := range sw.cases {
			j, ok := byID[c.job]
			switch {
			case !ok:
				t.Fatalf("%s: docs/retention.json has no job %s", c.name, c.job)
			case c.hold != nil && j.Hold == nil:
				t.Fatalf("%s: job %s has no hold", c.name, c.job)
			case c.hold != nil && byTable[j.Hold.Table].ID == "":
				t.Fatalf("%s: no job deletes the holds in %s", c.name, j.Hold.Table)
			}
		}
	}
	// holdKey returns the key of the hold that case i's hold names: the
	// held row's key, for row hi, and the rest of the hold job's key, such
	// as the submission, for case i.
	holdKey := func(j Job, i, hi int, at any) ([]string, []any) {
		cols, vals := keyValues(j, hi, at)
		for _, k := range byTable[j.Hold.Table].Key {
			if !slices.Contains(cols, k) {
				cols = append(cols, k)
				vals = append(vals, fmt.Sprintf("%s-%d", k, i))
			}
		}
		return cols, vals
	}
	// heldRow is the row case i's hold names.
	heldRow := func(i int, h *hold) int {
		if h.other {
			return 1000 + i
		}
		return i
	}

	for _, engine := range storetest.Engines() {
		t.Run(string(engine), func(t *testing.T) {
			for _, sw := range sweeps {
				t.Run(sw.name, func(t *testing.T) {
					ctx := context.Background()
					db := storetest.Open(t, engine)
					createTables(t, db, jobs)
					wantDeleted := make(map[string]int64)
					rowTime := func(c sweepCase) any {
						if c.null {
							return nil
						}
						return sw.at.Add(-c.age).UnixMilli()
					}
					for i, c := range sw.cases {
						j := byID[c.job]
						if !c.kept {
							wantDeleted[c.job]++
						}
						cols, vals := rowValues(j, i, rowTime(c))
						insert(t, db, j.Table, cols, vals)
						if h := c.hold; h != nil {
							if h.gone {
								wantDeleted[byTable[j.Hold.Table].ID]++
							}
							hcols, hvals := holdKey(j, i, heldRow(i, h), rowTime(c))
							var decided any
							if !h.open {
								decided = h.decided.UnixMilli()
							}
							insert(t, db, j.Hold.Table, append(hcols, j.Hold.DecidedColumn), append(hvals, decided))
						}
					}

					w := writer.Start(db.Write, writer.Options{})
					// A batch of 2 makes the jobs that delete two rows or
					// more loop.
					results := Sweep(ctx, w, jobs, sw.at, 2)
					again := Sweep(ctx, w, jobs, sw.at, 2)
					w.Close()

					if len(results) != len(jobs) {
						t.Fatalf("Sweep returned %d results for %d jobs", len(results), len(jobs))
					}
					for _, r := range results {
						if r.Err != nil {
							t.Errorf("%s: %v", r.ID, r.Err)
						} else if r.Deleted != wantDeleted[r.ID] {
							t.Errorf("%s deleted %d rows, want %d", r.ID, r.Deleted, wantDeleted[r.ID])
						}
					}
					for _, r := range again {
						if r.Err != nil || r.Deleted != 0 {
							t.Errorf("a second sweep at the same time: %s deleted %d rows (error %v), want 0", r.ID, r.Deleted, r.Err)
						}
					}
					for i, c := range sw.cases {
						t.Run(c.name, func(t *testing.T) {
							j := byID[c.job]
							cols, vals := keyValues(j, i, rowTime(c))
							if got := exists(t, db, j.Table, cols, vals); got != c.kept {
								t.Errorf("row kept = %v, want %v", got, c.kept)
							}
							if h := c.hold; h != nil {
								hcols, hvals := holdKey(j, i, heldRow(i, h), rowTime(c))
								if got := exists(t, db, j.Hold.Table, hcols, hvals); got != !h.gone {
									t.Errorf("hold kept = %v, want %v", got, !h.gone)
								}
							}
						})
					}
				})
			}
			// The cases above run on tables shaped from the bindings. This
			// runs the jobs that are on against the tables the migrations
			// really create, so a binding that doesn't fit them fails here.
			t.Run("every job that is on runs against the migrated schema", func(t *testing.T) {
				ctx := context.Background()
				db := storetest.Open(t, engine)
				ms, err := migrations.Embedded()
				if err != nil {
					t.Fatal(err)
				}
				if _, err := migrations.Apply(ctx, db, ms, time.Now); err != nil {
					t.Fatal(err)
				}
				on, _ := p.Jobs()
				w := writer.Start(db.Write, writer.Options{})
				results := Sweep(ctx, w, on, utc(2027, 7, 1, 12, 0), 2)
				w.Close()
				for _, r := range results {
					if r.Err != nil {
						t.Errorf("%s: %v", r.ID, r.Err)
					}
				}
				t.Logf("%d of the schedule's %d jobs are on", len(on), len(jobs))
			})
		})
	}
}

// createTables creates each job's table and each hold table, with just the
// columns the bindings name. A column that some binding uses as a time, a
// job's time column or a hold's decided column, is BIGINT, and any other
// is TEXT, so a hold's columns compare with the rows it names on both
// engines. A table that is both a hold and a job's, as every hold table
// is, is created once, with the columns of both and the job's key.
func createTables(t *testing.T, db *store.DB, jobs []Job) {
	t.Helper()
	times := make(map[string]bool)
	for _, j := range jobs {
		times[j.TimeColumn] = true
		if j.Hold != nil {
			times[j.Hold.DecidedColumn] = true
		}
	}
	type table struct {
		name string
		cols []string
		key  []string
	}
	var tables []*table
	add := func(name string, cols, key []string) {
		i := slices.IndexFunc(tables, func(tb *table) bool { return tb.name == name })
		if i < 0 {
			tables = append(tables, &table{name: name})
			i = len(tables) - 1
		}
		tb := tables[i]
		for _, c := range cols {
			if !slices.Contains(tb.cols, c) {
				tb.cols = append(tb.cols, c)
			}
		}
		if key != nil {
			tb.key = key
		}
	}
	for _, j := range jobs {
		add(j.Table, append(slices.Clone(j.Key), j.TimeColumn), j.Key)
		if h := j.Hold; h != nil {
			add(h.Table, append(slices.Clone(j.Key), h.DecidedColumn), nil)
		}
	}
	for _, tb := range tables {
		var defs []string
		for _, c := range tb.cols {
			def := c + " TEXT"
			if times[c] {
				def = c + " BIGINT"
			}
			if slices.Contains(tb.key, c) {
				def += " NOT NULL"
			}
			defs = append(defs, def)
		}
		if len(tb.key) > 0 {
			defs = append(defs, "PRIMARY KEY ("+strings.Join(tb.key, ", ")+")")
		}
		q := fmt.Sprintf("CREATE TABLE %s (%s)", tb.name, strings.Join(defs, ", "))
		if _, err := db.Write.ExecContext(context.Background(), q); err != nil {
			t.Fatalf("%s: %v", q, err)
		}
	}
}

// keyValues returns the key columns of row i of j's table and their values.
func keyValues(j Job, i int, at any) ([]string, []any) {
	var cols []string
	var vals []any
	for _, k := range j.Key {
		cols = append(cols, k)
		if k == j.TimeColumn {
			vals = append(vals, at)
		} else {
			vals = append(vals, fmt.Sprintf("%s-%d", k, i))
		}
	}
	return cols, vals
}

// rowValues returns every column of row i of j's table and their values.
func rowValues(j Job, i int, at any) ([]string, []any) {
	cols, vals := keyValues(j, i, at)
	if !slices.Contains(j.Key, j.TimeColumn) {
		cols = append(cols, j.TimeColumn)
		vals = append(vals, at)
	}
	return cols, vals
}

func insert(t *testing.T, db *store.DB, table string, cols []string, vals []any) {
	t.Helper()
	ph := make([]string, len(cols))
	for i := range cols {
		ph[i] = "$" + strconv.Itoa(i+1)
	}
	q := fmt.Sprintf("INSERT INTO %s (%s) VALUES (%s)", table, strings.Join(cols, ", "), strings.Join(ph, ", "))
	if _, err := db.Write.ExecContext(context.Background(), q, vals...); err != nil {
		t.Fatalf("%s: %v", q, err)
	}
}

// exists reports whether table still has a row with these column values.
func exists(t *testing.T, db *store.DB, table string, cols []string, vals []any) bool {
	t.Helper()
	conds := make([]string, len(cols))
	for k, c := range cols {
		conds[k] = c + " = $" + strconv.Itoa(k+1)
	}
	q := "SELECT count(*) FROM " + table + " WHERE " + strings.Join(conds, " AND ")
	var n int
	if err := db.Read.QueryRowContext(context.Background(), q, vals...).Scan(&n); err != nil {
		t.Fatalf("%s: %v", q, err)
	}
	return n > 0
}

// TestTables checks docs/retention.json against the tables pairtimed's
// migrations create, so no table holds data without a published period.
func TestTables(t *testing.T) {
	p, err := Default()
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	db := storetest.Open(t, store.SQLite)
	ms, err := migrations.Embedded()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := migrations.Apply(ctx, db, ms, time.Now); err != nil {
		t.Fatal(err)
	}
	columns := func(table string) []string {
		rows, err := db.Read.QueryContext(ctx, `SELECT name FROM pragma_table_info($1)`, table)
		if err != nil {
			t.Fatal(err)
		}
		defer rows.Close()
		var cols []string
		for rows.Next() {
			var c string
			if err := rows.Scan(&c); err != nil {
				t.Fatal(err)
			}
			cols = append(cols, c)
		}
		if err := rows.Err(); err != nil {
			t.Fatal(err)
		}
		return cols
	}
	var tables []string
	rows, err := db.Read.QueryContext(ctx, `SELECT name FROM sqlite_master WHERE type = 'table' AND name NOT LIKE 'sqlite_%' ORDER BY name`)
	if err != nil {
		t.Fatal(err)
	}
	for rows.Next() {
		var n string
		if err := rows.Scan(&n); err != nil {
			t.Fatal(err)
		}
		tables = append(tables, n)
	}
	rows.Close()
	if len(tables) == 0 {
		t.Fatal("the migrations created no tables")
	}

	claimed := make(map[string]bool)
	for _, n := range p.NoPersonalData {
		claimed[n] = true
	}
	for _, e := range p.Entries {
		for _, n := range slices.Concat(e.Tables, e.LifetimeTables) {
			claimed[n] = true
		}
		for _, j := range e.Jobs {
			claimed[j.Table] = true
			if j.Hold != nil {
				claimed[j.Hold.Table] = true
			}
		}
	}

	// bound checks one job or hold table: it exists with cols when on,
	// and doesn't exist yet when it awaits its work package.
	bound := func(t *testing.T, id, table, awaits string, cols []string) {
		t.Helper()
		have := columns(table)
		switch {
		case awaits != "" && len(have) > 0:
			t.Errorf("%s: table %s exists now; remove its awaits %q in docs/retention.json, so its job runs", id, table, awaits)
		case awaits == "" && len(have) == 0:
			t.Errorf("%s: table %s doesn't exist; set awaits to the work package that creates it", id, table)
		case awaits == "":
			for _, c := range cols {
				if !slices.Contains(have, c) {
					t.Errorf("%s: table %s has no column %s", id, table, c)
				}
			}
		}
	}
	tests := []struct {
		name  string
		check func(t *testing.T)
	}{
		{
			name: "every table the migrations create is in docs/retention.json",
			check: func(t *testing.T) {
				for _, n := range tables {
					if !claimed[n] {
						t.Errorf("table %s isn't in docs/retention.json; add it to the entry for the data it holds, as a job's table, in lifetime_tables or, for an entry without a period, in tables; or add it to no_personal_data", n)
					}
				}
			},
		},
		{
			name: "no foreign key points at a table a job deletes from",
			check: func(t *testing.T) {
				// A delete would fail, or cascade into rows that must stay
				// longer, such as the witness rows of a device removed 90
				// days ago.
				deletes := make(map[string]string)
				for _, j := range p.AllJobs() {
					deletes[j.Table] = j.ID
				}
				for _, n := range tables {
					rows, err := db.Read.QueryContext(ctx, `SELECT "table" FROM pragma_foreign_key_list($1)`, n)
					if err != nil {
						t.Fatal(err)
					}
					var refs []string
					for rows.Next() {
						var ref string
						if err := rows.Scan(&ref); err != nil {
							t.Fatal(err)
						}
						refs = append(refs, ref)
					}
					if err := rows.Err(); err != nil {
						t.Fatal(err)
					}
					rows.Close()
					for _, ref := range refs {
						if job, ok := deletes[ref]; ok {
							t.Errorf("table %s has a foreign key to %s, whose rows %s deletes; drop the foreign key (AGENTS.md, Retention)", n, ref, job)
						}
					}
				}
			},
		},
		{
			name: "a job runs only on a table that exists, with the columns it names",
			check: func(t *testing.T) {
				for _, e := range p.Entries {
					for _, j := range e.Jobs {
						bound(t, e.ID, j.Table, j.Awaits, append(slices.Clone(j.Key), j.TimeColumn))
					}
				}
			},
		},
		{
			name: "a hold is used only once its table exists, with the columns it names",
			check: func(t *testing.T) {
				for _, e := range p.Entries {
					for _, j := range e.Jobs {
						if h := j.Hold; h != nil {
							bound(t, e.ID+" hold", h.Table, h.Awaits, append(slices.Clone(j.Key), h.DecidedColumn))
						}
					}
				}
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, tt.check)
	}
}
