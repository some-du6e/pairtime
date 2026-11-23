// SPDX-License-Identifier: AGPL-3.0-or-later

package t3code

import (
	"fmt"
	"slices"
	"strconv"
	"strings"
)

// Migration 55, OrchestrationV2, is the base of the schema the reader reads,
// and 85, TurnItemCounts, the newest head it was tested against (T3
// d8f3feff).
const (
	baseMigration   = 55
	baseName        = "OrchestrationV2"
	testedMigration = 85
)

// needColumns are the columns the reader's SQL names, by table.
var needColumns = []struct {
	short, table string
	columns      []string
}{
	{"m", tMessages, []string{"message_id", "thread_id", "run_id", "role", "created_at", "updated_at", "payload_json"}},
	{"r", tRuns, []string{"run_id", "status", "requested_at", "completed_at", "provider_thread_id", "payload_json"}},
	{"a", tAttempts, []string{"run_id", "attempt_ordinal", "status", "provider_thread_id", "payload_json"}},
	{"pt", tProviderThreads, []string{"provider_thread_id", "driver", "payload_json"}},
	{"t", tThreads, []string{"thread_id", "project_id", "payload_json"}},
	{"p", tProjects, []string{"project_id", "workspace_root"}},
}

// needKeys are the payload keys the reader reads, by sample: a recent user
// message's, the newest run's, the newest provider thread's
// nativeThreadRef and the newest thread's.
var needKeys = []struct {
	tag, what string
	keys      []string
}{
	{"mkey", "a message", []string{"createdBy", "creationSource", "id", "threadId", "runId", "role", "text", "createdAt", "updatedAt"}},
	{"rkey", "a run", []string{"userMessageId", "activeAttemptId", "providerThreadId", "status", "requestedAt", "startedAt"}},
	{"ptkey", "a provider thread's nativeThreadRef", []string{"driver", "nativeId"}},
	{"tkey", "a thread", []string{"projectId", "worktreePath"}},
}

// probeSQL is the schema probe (DESIGN 5.1): that sqlite3 has JSON
// functions, migration 55 and the head, the columns the reader names, the
// index the resync walks, and the payload keys of a sample row of each
// table it reads JSON from. json_each gives the keys alone, never a value.
// A message is sampled among the last 500 rows, so a store whose newest
// rows are all an agent's can have none to sample yet.
func probeSQL() string {
	var b strings.Builder
	b.WriteString("SELECT " + tag("json") + ", " + hx(`json_type('{"a":{}}', '$.a')`) + ";\n")
	b.WriteString("SELECT " + tag("mig") + ", " + hx("migration_id") + ", " + hx("name") + " FROM " + tMigrations +
		" WHERE migration_id = " + num(baseMigration) + " OR migration_id = (SELECT max(migration_id) FROM " + tMigrations + ");\n")
	for _, n := range needColumns {
		b.WriteString("SELECT " + tag("col") + ", " + tag(n.short) + ", " + hx("name") +
			" FROM pragma_table_info('" + n.table + "');\n")
	}
	b.WriteString("SELECT " + tag("ix") + ", " + hx("seqno") + ", " + hx("name") +
		" FROM pragma_index_info('" + iThreadCreated + "');\n")
	newest := func(table string) string {
		return "(SELECT payload_json FROM " + table + " WHERE rowid = (SELECT max(rowid) FROM " + table + "))"
	}
	b.WriteString("SELECT " + tag("mkey") + ", " + hx("j.key") + " FROM json_each((SELECT payload_json FROM " + tMessages +
		" WHERE rowid > (SELECT max(rowid) FROM " + tMessages + ") - 500 AND role = 'user' ORDER BY rowid DESC LIMIT 1)) AS j;\n")
	b.WriteString("SELECT " + tag("rkey") + ", " + hx("j.key") + " FROM json_each(" + newest(tRuns) + ") AS j;\n")
	b.WriteString("SELECT " + tag("ptkey") + ", " + hx("j.key") + " FROM json_each(" + newest(tProviderThreads) + ", '$.nativeThreadRef') AS j;\n")
	b.WriteString("SELECT " + tag("tkey") + ", " + hx("j.key") + " FROM json_each(" + newest(tThreads) +
		") AS j WHERE j.key IN ('projectId', 'worktreePath');\n")
	return b.String()
}

// schema is what the probe found.
type schema struct {
	// head is the newest migration recorded.
	head int
	// unsampled lists the samples the store had no row for yet, whose keys
	// the next probe checks.
	unsampled []string
}

// newer reports whether T3's schema is newer than the one the reader was
// tested against, which passes with a doctor line (DESIGN 5.1).
func (s schema) newer() bool { return s.head > testedMigration }

// refusal says why the probe turned the store down, naming no path.
type refusal struct{ why string }

func (r *refusal) Error() string { return r.why }

// checkProbe reads the probe's rows, or says why the reader must not read
// the store.
func checkProbe(rows []row) (schema, error) {
	var s schema
	hasJSON := false
	var base string
	head := -1
	cols := map[string][]string{}
	type ixCol struct {
		seq  int
		name string
	}
	var ix []ixCol
	keys := map[string][]string{}
	for _, r := range rows {
		if len(r) == 0 || r[0].null {
			return s, ErrOutput
		}
		switch t := r[0].s; {
		case t == "json" && len(r) == 2:
			hasJSON = !r[1].null && r[1].s == "object"
		case t == "mig" && len(r) == 3:
			id, err := strconv.Atoi(r[1].s)
			if r[1].null || err != nil {
				return s, ErrOutput
			}
			if id == baseMigration {
				base = r[2].s
			}
			head = max(head, id)
		case t == "col" && len(r) == 3:
			cols[r[1].s] = append(cols[r[1].s], r[2].s)
		case t == "ix" && len(r) == 3:
			seq, err := strconv.Atoi(r[1].s)
			if err != nil {
				return s, ErrOutput
			}
			ix = append(ix, ixCol{seq, r[2].s})
		case strings.HasSuffix(t, "key") && len(r) == 2:
			if !r[1].null {
				keys[t] = append(keys[t], r[1].s)
			}
		default:
			return s, ErrOutput
		}
	}
	if !hasJSON {
		return s, &refusal{"this sqlite3 has no JSON functions"}
	}
	if base != baseName {
		return s, &refusal{fmt.Sprintf("T3 Code's store has no migration %d (%s), the base of the schema pairtime reads", baseMigration, baseName)}
	}
	s.head = head
	for _, n := range needColumns {
		for _, c := range n.columns {
			if !slices.Contains(cols[n.short], c) {
				return s, &refusal{fmt.Sprintf("T3 Code's table %s has no column %s", n.table, c)}
			}
		}
	}
	slices.SortFunc(ix, func(a, b ixCol) int { return a.seq - b.seq })
	var names []string
	for _, c := range ix {
		names = append(names, c.name)
	}
	if !slices.Equal(names, []string{"thread_id", "created_at", "message_id"}) {
		return s, &refusal{fmt.Sprintf("T3 Code's index %s isn't on (thread_id, created_at, message_id)", iThreadCreated)}
	}
	for _, n := range needKeys {
		got := keys[n.tag]
		if len(got) == 0 {
			s.unsampled = append(s.unsampled, n.tag)
			continue
		}
		for _, k := range n.keys {
			if !slices.Contains(got, k) {
				return s, &refusal{fmt.Sprintf("%s in T3 Code's store has no payload key %s", n.what, k)}
			}
		}
	}
	return s, nil
}
