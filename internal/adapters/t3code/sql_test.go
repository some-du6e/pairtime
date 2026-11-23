// SPDX-License-Identifier: AGPL-3.0-or-later

package t3code

import (
	"strings"
	"testing"
)

// TestSQL holds the reader's SQL to DESIGN 5.1's privacy rules: no query
// touches T3's search table or a column that holds text but a message's
// text, which only the text query reads, by primary key, behind the guest,
// author, source and folder tests.
func TestSQL(t *testing.T) {
	all := map[string]string{
		"probe":    probeSQL(),
		"start":    startSQL(),
		"window":   windowSQL(7, []int64{3, 5}),
		"items":    itemSQL([]int64{3}),
		"decision": decideSQL(7, 9, []int64{2}, []string{"/work/alpha"}),
		"text":     textSQL(textReq{id: "m1", created: at(0), updated: at(0), folder: "/work/alpha"}),
		"resync":   resyncSQL(at(0)),
	}
	never := []string{"message_search", "search_text", "title", "guestShare", "$.context.guest.", "$.linkedSender.",
		"$.notification.", "payload_json FROM " + tThreads + " WHERE rowid = (SELECT max(rowid) FROM " + tThreads + ")) AS j;"}
	for name, sql := range all {
		t.Run(name+" touches no search table and no column of text", func(t *testing.T) {
			for _, n := range never {
				if strings.Contains(sql, n) {
					t.Errorf("it names %q", n)
				}
			}
		})
		t.Run(name+" puts no folder or id in the SQL as plain text", func(t *testing.T) {
			if strings.Contains(sql, "/work/alpha") || strings.Contains(sql, "'m1'") {
				t.Error("it holds a plain string")
			}
		})
	}
	t.Run("only the text query reads a message's text, by primary key, behind the guest, author, source and folder tests", func(t *testing.T) {
		for name, sql := range all {
			reads := strings.Contains(sql, "json_extract(m.payload_json, '$.text'), 1, 65536)")
			if reads != (name == "text") {
				t.Errorf("%s reads the text: %v", name, reads)
			}
		}
		sql := all["text"]
		for _, want := range []string{"m.message_id = " + lit("m1"), notGuest, byPerson, folderOf + " = " + lit("/work/alpha")} {
			if !strings.Contains(sql, want) {
				t.Errorf("the text query lacks %q", want)
			}
		}
	})
	t.Run("the decision tests the guest, author, source and folder in its WHERE clause", func(t *testing.T) {
		sql := all["decision"]
		_, where, _ := strings.Cut(sql, " WHERE ")
		for _, want := range []string{notGuest, byPerson, folderOf + " IN (" + lit("/work/alpha") + ")"} {
			if !strings.Contains(where, want) {
				t.Errorf("the WHERE clause lacks %q", want)
			}
		}
	})
	t.Run("a literal is hex, so a quote in an id can't end it", func(t *testing.T) {
		if got, want := lit("a'b"), "CAST(X'612762' AS TEXT)"; got != want {
			t.Errorf("lit = %s, want %s", got, want)
		}
	})
}
