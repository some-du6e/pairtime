// SPDX-License-Identifier: AGPL-3.0-or-later

// Package doctor works out `pairtime doctor`'s checks (DESIGN 5.9;
// docs/PLAN.md, WP-AG6) and writes them, one line per check or as the JSON
// that docs/schema/cli-doctor.schema.json describes.
//
// Checks is a function of Facts alone: what the running agent says of
// itself, which cmd/pairtime asks it over its socket (ipc.AskStatus), and
// what the CLI found on the computer, such as each installed harness's
// hook round trip. So the idle and focus lines say what the agent reads,
// never what the terminal doctor runs in can reach (D-design-5.9), and a
// test can give any status and read the lines it makes.
//
// Nothing here sends anything anywhere but GatherWakaTime, which asks
// wakatime-cli's two destinations whether they answer: the agent on
// 127.0.0.1, with pairtime's key, and the teen's WakaTime server, with no
// key. Nothing runs at package init: every hook process loads this
// package, and the hook's start-up has a thin margin (footprint.yml).
package doctor

import (
	"fmt"
	"io"
	"strings"
	"unicode/utf8"
)

// Result is a check's outcome.
type Result string

const (
	// OK means the check found what it looks for.
	OK Result = "ok"
	// Warn means it works with something the teen should know, or that
	// costs some of their time until it's fixed.
	Warn Result = "warn"
	// Fail means it doesn't work, and pairtime misses time until it does.
	Fail Result = "fail"
	// Skip means doctor couldn't check it, and the line says why.
	Skip Result = "skip"
)

// label is how a result reads in the text: FAIL in capitals, so it stands
// out, as in pairtimed doctor.
func (r Result) label() string {
	if r == Fail {
		return "FAIL"
	}
	return string(r)
}

// Check is one line of doctor.
type Check struct {
	// Name says what was checked, such as agent, idle or
	// hook.claude_code. A check of one harness ends in the harness's
	// name.
	Name   string `json:"name"`
	Result Result `json:"result"`
	// Text is what doctor found and, when something is wrong, the one
	// thing to do about it.
	Text string `json:"text"`
}

// Summary counts the checks by result.
type Summary struct {
	OK      int `json:"ok"`
	Warn    int `json:"warn"`
	Fail    int `json:"fail"`
	Skipped int `json:"skip"`
}

// Summarize counts checks by result.
func Summarize(checks []Check) Summary {
	var s Summary
	for _, c := range checks {
		switch c.Result {
		case OK:
			s.OK++
		case Warn:
			s.Warn++
		case Fail:
			s.Fail++
		default:
			s.Skipped++
		}
	}
	return s
}

// Write prints checks one per line, the result, the name and the text in
// columns, and then the summary line.
func Write(w io.Writer, checks []Check) error {
	width := 0
	for _, c := range checks {
		width = max(width, utf8.RuneCountInString(c.Name))
	}
	var b strings.Builder
	for _, c := range checks {
		fmt.Fprintf(&b, "%-4s  %s%s  %s\n", c.Result.label(), c.Name, strings.Repeat(" ", width-utf8.RuneCountInString(c.Name)), c.Text)
	}
	s := Summarize(checks)
	fmt.Fprintf(&b, "doctor: %d ok, %d %s, %d failed, %d skipped\n", s.OK, s.Warn, plural(s.Warn, "warning", "warnings"), s.Fail, s.Skipped)
	_, err := io.WriteString(w, b.String())
	return err
}

func plural(n int, one, many string) string {
	if n == 1 {
		return one
	}
	return many
}
