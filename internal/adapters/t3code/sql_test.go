// SPDX-License-Identifier: AGPL-3.0-or-later

package t3code

import (
	"context"
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

// TestGuestOpening holds the reader's two tests of a guest's opening to one
// reading (DESIGN 5.1): OpensAsGuest, which the agent applies to every copy
// of a prompt, and the SQL the reader's WHERE clauses apply inside sqlite3.
// Both skip white space, as Go's strings.TrimSpace reads it, and a leading
// Ultrathink: line with nothing else on it, as typed does. The texts are
// invented.
func TestGuestOpening(t *testing.T) {
	s := newStore(t)
	cases := []struct {
		name string
		text string
		want bool
	}{
		{"T3's opening of a guest's message is a guest's", "Message from guest Ada, sent by the owner:\n\nwhy not?", true},
		{"the opening after white space is a guest's", " \t\r\n Message from guest Ada, sent by the owner:", true},
		{"the opening after white space beyond ASCII that Go trims is a guest's", "\u00a0\u3000\u2028\u0085Message from guest Ada", true},
		{"the opening after an Ultrathink: line is a guest's", "Ultrathink:\nMessage from guest Ada, sent by the owner:", true},
		{"the opening after an Ultrathink: line in another case, with a space and a CRLF, is a guest's",
			"ULTRAthink: \r\n\tMessage from guest Ada", true},
		{"an Ultrathink: with words after it on its line opens no guest's message", "Ultrathink: Message from guest Ada", false},
		{"a second Ultrathink: line is text, so what follows it opens no guest's message",
			"Ultrathink:\nUltrathink:\nMessage from guest Ada", false},
		{"the words later in the text open nothing", "about the Message from guest Ada", false},
		{"the words in another case open nothing", "message from guest Ada", false},
		{"the words without their space open nothing", "Message from guestAda", false},
		{"a zero-width space isn't white space, so the words after it open nothing", "\u200bMessage from guest Ada", false},
		{"an Ultrathink: line alone opens nothing", "Ultrathink:", false},
		{"an empty text opens nothing", "", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := OpensAsGuest(tc.text); got != tc.want {
				t.Errorf("OpensAsGuest(%q) = %v, want %v", tc.text, got, tc.want)
			}
			sql := "SELECT " + tag("g") + ", " + hx("("+opensAsGuest+") IS 1") +
				" FROM (SELECT json_object('text', " + lit(tc.text) + ") AS payload_json) AS m;\n"
			rows, err := (&Runner{Path: s.sqlite3}).Query(context.Background(), s.path, sql)
			if err != nil {
				t.Fatal(err)
			}
			if len(rows) != 1 || len(rows[0]) != 2 {
				t.Fatalf("sqlite3 gave %+v", rows)
			}
			if got := rows[0][1].s == "1"; got != tc.want {
				t.Errorf("the SQL test of %q gives %v, want %v", tc.text, got, tc.want)
			}
		})
	}
}
