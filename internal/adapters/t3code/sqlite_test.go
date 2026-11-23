// SPDX-License-Identifier: AGPL-3.0-or-later

package t3code

import (
	"context"
	"errors"
	"os"
	"reflect"
	"strings"
	"testing"
	"time"
)

// TestRunner holds the sqlite3 runner to DESIGN 5.1: read-only, with no
// .sqliterc, its SQL on stdin, every field hex behind a fixed separator,
// under a timeout.
func TestRunner(t *testing.T) {
	s := newStore(t)
	cases := []struct {
		name string
		run  func(t *testing.T)
	}{
		{"every field comes back decoded, NULL kept apart from the empty string and a separator inside a field kept whole",
			func(t *testing.T) {
				rows, err := (&Runner{Path: s.sqlite3}).Query(context.Background(), s.path,
					"SELECT "+tag("a")+", "+hx("NULL")+", "+hx("''")+", "+hx("'x|y'")+", "+hx("'line\none'")+";\n")
				if err != nil {
					t.Fatal(err)
				}
				want := []row{{{s: "a"}, {null: true}, {s: ""}, {s: "x|y"}, {s: "line\none"}}}
				if !reflect.DeepEqual(rows, want) {
					t.Errorf("rows %+v, want %+v", rows, want)
				}
			}},
		{"a run that fails says why, without the SQL's literals, which can name folders",
			func(t *testing.T) {
				_, err := (&Runner{Path: s.sqlite3}).Query(context.Background(), s.path,
					"SELECT "+lit("/home/someone/secret-project")+" FROM no_such_table;\n")
				var qe *QueryError
				if !errors.As(err, &qe) {
					t.Fatalf("got %v, want a QueryError", err)
				}
				if !strings.Contains(qe.Detail, "no such table") || strings.Contains(qe.Error(), lit("/home/someone/secret-project")[5:20]) {
					t.Errorf("the error is %q, want one that says no such table and holds no literal", qe.Error())
				}
			}},
		{"output that isn't the hex fields the reader asked for is refused",
			func(t *testing.T) {
				_, err := (&Runner{Path: s.sqlite3}).Query(context.Background(), s.path, "SELECT 'zz';\n")
				if !errors.Is(err, ErrOutput) {
					t.Errorf("got %v, want ErrOutput", err)
				}
			}},
		{"the SQL goes to sqlite3 on stdin, and its arguments are the read-only flags, no .sqliterc and the store",
			func(t *testing.T) {
				sp := newSpy(t, s.sqlite3)
				if _, err := (&Runner{Path: sp.path}).Query(context.Background(), s.path, "SELECT "+tag("secret")+";\n"); err != nil {
					t.Fatal(err)
				}
				runs := sp.runs(t)
				if len(runs) != 1 {
					t.Fatalf("the spy saw %d runs, want 1", len(runs))
				}
				args, sql, _ := strings.Cut(runs[0], "\n")
				if want := "-- args: -readonly -batch -bail -init " + os.DevNull + " " + s.path; args != want {
					t.Errorf("the arguments are %q, want %q", args, want)
				}
				if !strings.Contains(sql, "hex('secret')") || strings.Contains(args, "secret") {
					t.Errorf("the SQL wasn't on stdin alone: args %q, stdin %q", args, sql)
				}
			}},
		{"with no sqlite3 on PATH the runner says so",
			func(t *testing.T) {
				_, err := (&Runner{Path: "sqlite3-that-isnt-there"}).Query(context.Background(), s.path, "SELECT 1;\n")
				if !errors.Is(err, ErrNoSQLite3) {
					t.Errorf("got %v, want ErrNoSQLite3", err)
				}
			}},
		{"a run that outlasts its timeout is stopped",
			func(t *testing.T) {
				start := time.Now()
				_, err := (&Runner{Path: s.sqlite3, Timeout: 200 * time.Millisecond}).Query(context.Background(), s.path,
					"WITH RECURSIVE n(i) AS (SELECT 1 UNION ALL SELECT i + 1 FROM n) SELECT count(*) FROM n;\n")
				if !errors.Is(err, context.DeadlineExceeded) {
					t.Errorf("got %v, want the deadline", err)
				}
				if d := time.Since(start); d > 5*time.Second {
					t.Errorf("the run took %v", d)
				}
			}},
		{"a read-only run writes nothing to the store",
			func(t *testing.T) {
				_, err := (&Runner{Path: s.sqlite3}).Query(context.Background(), s.path,
					"INSERT INTO effect_sql_migrations (migration_id, name) VALUES (99, 'x');\n")
				if err == nil {
					t.Error("the insert worked")
				}
			}},
	}
	for _, tc := range cases {
		t.Run(tc.name, tc.run)
	}
}

// TestParseRows holds the output parser to the fields hx writes.
func TestParseRows(t *testing.T) {
	cases := []struct {
		name string
		out  string
		want []row
		err  error
	}{
		{"hex fields and N", "6869|N|\n", []row{{{s: "hi"}, {null: true}, {s: ""}}}, nil},
		{"a Windows line end is one", "6869\r\n", []row{{{s: "hi"}}}, nil},
		{"blank lines are skipped", "\n6869\n\n", []row{{{s: "hi"}}}, nil},
		{"a field that isn't hex is refused", "6869|hi\n", nil, ErrOutput},
		{"an odd number of hex digits is refused", "686\n", nil, ErrOutput},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := parseRows([]byte(tc.out))
			if !errors.Is(err, tc.err) || !reflect.DeepEqual(got, tc.want) {
				t.Errorf("parseRows = %+v, %v; want %+v, %v", got, err, tc.want, tc.err)
			}
		})
	}
}
