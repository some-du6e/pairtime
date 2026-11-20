// SPDX-License-Identifier: AGPL-3.0-or-later

package main

// What `pairtime today`, `recap` and `statusline` share: a project's time
// as their --json prints it (docs/schema/cli-today.schema.json), built
// from the agent's tally (stores.go) with the words of
// internal/coach/text, and the text layout that prints it.

import (
	"cmp"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"slices"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/some-du6e/pairtime/internal/coach/text"
	"github.com/some-du6e/pairtime/pkg/event"
)

// jsonVersion is the "v" of every view's --json.
const jsonVersion = 1

// viewTimeout bounds how long recap waits for the agent's answer. A person
// is waiting, but the agent answers from what it already holds. Tests
// shorten it.
var viewTimeout = 3 * time.Second

// viewZone is the time zone the views give times of day and days in: the
// device's own, which the agent counts its days in too. Tests set it.
var viewZone = func() *time.Location { return time.Local }

// viewNow is the views' clock. Tests set it.
var viewNow = time.Now

// localToday is today's date in viewZone, as the agent names its day
// rollups.
func localToday() string { return viewNow().In(viewZone()).Format(time.DateOnly) }

// projectJSON is one project's time over a span.
type projectJSON struct {
	Project string `json:"project"`
	Name    string `json:"name"`
	// You is your time: the credited seconds, absent while display.shadow
	// is on (DESIGN 6.7), when the views show "calibrating".
	You *youJSON `json:"you,omitempty"`
	// Agents is each agent's time on its own, the most first. Other is the
	// rest of the uncredited time by reason, the most first, without the
	// agent's time (X.agent_only, X.automated), which Agents names. While
	// calibrating, Other holds SM 4.2's loops alone.
	Agents []agentJSON `json:"agents"`
	Other  []otherJSON `json:"other"`
}

type youJSON struct {
	S float64 `json:"s"`
	// PendingS is the part of S that may still change.
	PendingS float64 `json:"pending_s"`
	// Kinds splits S by activity kind, the most first.
	Kinds []kindJSON `json:"kinds"`
	// Levels splits S by evidence level, in the order A, B, C.
	Levels []levelJSON `json:"levels"`
}

type kindJSON struct {
	Kind  event.Activity `json:"kind"`
	Words string         `json:"words"`
	S     float64        `json:"s"`
}

type levelJSON struct {
	Level event.Evidence `json:"level"`
	Words string         `json:"words"`
	S     float64        `json:"s"`
	// Share is the level's part of your time in whole percent. The shares
	// add up to 100 (text.Percentages).
	Share int `json:"share"`
}

type agentJSON struct {
	Harness event.Harness `json:"harness"`
	Name    string        `json:"name"`
	S       float64       `json:"s"`
}

type otherJSON struct {
	Reason event.Reason `json:"reason"`
	Words  string       `json:"words"`
	S      float64      `json:"s"`
	// OnHold is set on the part of X.noprog that progress can still bring
	// back (SM 4.3; D5). The rest of X.noprog has an entry without it.
	OnHold bool `json:"on_hold,omitempty"`
}

// levels are the evidence levels in the order the views list them.
var levels = []event.Evidence{event.EvidenceA, event.EvidenceB, event.EvidenceC}

// buildProject turns the agent's tally for a project into what the views
// show. With calibrating set (display.shadow), your time is left out, and
// so is the other time but SM 4.2's loops: with what was left out, and
// what the day or session took, your hidden time could be worked out
// (DESIGN 6.7 step 2; SM 7.5). A code this build has no words for, from a
// newer agent, is left out too, so --json keeps to its schema and no raw
// code reaches the terminal; its seconds stay in your time and the agent's.
func buildProject(key, name string, t tally, calibrating bool) projectJSON {
	agent := agentOf(t.AgentOnlyByHarness)
	p := projectJSON{Project: key, Name: name, Agents: []agentJSON{}, Other: []otherJSON{}}
	if !calibrating {
		you := &youJSON{S: t.CreditedS, PendingS: t.PendingS, Kinds: []kindJSON{}, Levels: []levelJSON{}}
		for k, s := range t.CreditedByKind {
			if _, known := text.English().Kinds[k]; s > 0 && known {
				you.Kinds = append(you.Kinds, kindJSON{Kind: k, Words: text.Kind(k, agent), S: s})
			}
		}
		slices.SortFunc(you.Kinds, func(a, b kindJSON) int { return most(a.S, b.S, string(a.Kind), string(b.Kind)) })
		var parts []float64
		for _, l := range levels {
			if s := t.CreditedByEvidence[l]; s > 0 {
				you.Levels = append(you.Levels, levelJSON{Level: l, Words: text.Level(l), S: s})
				parts = append(parts, s)
			}
		}
		for i, share := range text.Percentages(parts) {
			you.Levels[i].Share = share
		}
		p.You = you
	}
	p.Agents = agentList(t.AgentOnlyByHarness)
	for r, s := range t.Uncredited {
		words := text.Other(r, agent)
		if s <= 0 || text.IsAgentTime(r) || words == "" || (calibrating && !r.DarkFlow()) {
			continue
		}
		if r != event.ReasonNoProgress {
			p.Other = append(p.Other, otherJSON{Reason: r, Words: words, S: s})
			continue
		}
		// The part progress can still bring back is on hold; the rest
		// can't come back any more, and says what it was.
		held := min(max(t.OnHoldS, 0), s)
		if held > 0 {
			p.Other = append(p.Other, otherJSON{Reason: r, Words: text.OnHold(), S: held, OnHold: true})
		}
		if s > held {
			p.Other = append(p.Other, otherJSON{Reason: r, Words: words, S: s - held})
		}
	}
	slices.SortFunc(p.Other, func(a, b otherJSON) int {
		if c := most(a.S, b.S, string(a.Reason), string(b.Reason)); c != 0 || a.OnHold == b.OnHold {
			return c
		}
		if a.OnHold {
			return -1
		}
		return 1
	})
	return p
}

// agentList lists each agent's time on its own, the most first. Time the
// agent files under no harness, or under one en.json has no name for, is
// "your agent"'s.
func agentList(byHarness map[event.Harness]float64) []agentJSON {
	sum := map[event.Harness]float64{}
	for h, s := range byHarness {
		if s > 0 {
			if _, named := text.English().Agents[h]; !named {
				h = event.HarnessOther
			}
			sum[h] += s
		}
	}
	out := make([]agentJSON, 0, len(sum))
	for h, s := range sum {
		out = append(out, agentJSON{Harness: h, Name: text.Agent(h), S: s})
	}
	slices.SortFunc(out, func(a, b agentJSON) int { return most(a.S, b.S, string(a.Harness), string(b.Harness)) })
	return out
}

// most orders by seconds, the most first, then by code.
func most(sa, sb float64, ca, cb string) int {
	if c := cmp.Compare(sb, sa); c != 0 {
		return c
	}
	return strings.Compare(ca, cb)
}

// agentOf names the agent whose answers a project's credited time went
// with: the one harness that had a session there, or "your agent" when
// there were none or several.
func agentOf(harnesses map[event.Harness]float64) string {
	if len(harnesses) == 1 {
		for h := range harnesses {
			return text.Agent(h)
		}
	}
	return text.Agent(event.HarnessOther)
}

// clock is a project's chess clock (text.Clock), with its level-C time.
func (p projectJSON) clock() string {
	agents := make([]text.AgentClock, len(p.Agents))
	for i, a := range p.Agents {
		agents[i] = text.AgentClock{Name: a.Name, S: a.S}
	}
	if p.You == nil {
		return text.Clock(0, 0, true, agents)
	}
	return text.Clock(p.You.S, p.You.levelC(), false, agents)
}

// levelC is the part of your time at evidence level C.
func (y *youJSON) levelC() float64 {
	for _, l := range y.Levels {
		if l.Level == event.EvidenceC {
			return l.S
		}
	}
	return 0
}

// title is what a project is called on screen: its name, or its key when
// the agent has no name for it, made Printable, since a name can come from
// a folder's.
func (p projectJSON) title() string {
	if p.Name != "" {
		return text.Printable(p.Name)
	}
	return text.Printable(p.Project)
}

// row is a line of a view: a label, and a value right-aligned in a column
// after the longest label. A row with no value is a note, printed as it
// is.
type row struct {
	indent int
	label  string
	value  string
}

// rows lays out a project's time: your time and what it went on, then each
// agent's time on its own, then the other time. Amounts under a second,
// which would read "0m", are left out.
func (p projectJSON) rows() []row {
	var out []row
	if p.You == nil {
		out = append(out, row{2, text.Phrase(text.KeyYourTime), text.Phrase(text.KeyCalibrating)})
	} else {
		out = append(out, row{2, text.Phrase(text.KeyYourTime), text.Duration(p.You.S)})
		for _, k := range p.You.Kinds {
			if k.S >= 1 {
				out = append(out, row{4, k.Words, text.Duration(k.S)})
			}
		}
		for _, l := range p.You.Levels {
			if l.Level == event.EvidenceC && l.S >= 1 {
				out = append(out, row{4, text.Phrase(text.KeyLevelCNote, "time", text.Duration(l.S)), ""})
			}
		}
		if p.You.PendingS >= 1 {
			out = append(out, row{4, text.Phrase(text.KeyPendingNote, "time", text.Duration(p.You.PendingS)), ""})
		}
	}
	for _, a := range p.Agents {
		if a.S >= 1 {
			out = append(out, row{2, text.Sentence(text.AgentTime(a.Name)), text.Duration(a.S)})
		}
	}
	for _, o := range p.Other {
		if o.S >= 1 {
			out = append(out, row{2, text.Sentence(o.Words), text.Duration(o.S)})
		}
	}
	return out
}

// writeRows prints rows with their values in one right-aligned column, two
// spaces after the longest label.
func writeRows(w io.Writer, rows []row) {
	labelW, valueW := 0, 0
	for _, r := range rows {
		if r.value != "" {
			labelW = max(labelW, r.indent+utf8.RuneCountInString(r.label))
			valueW = max(valueW, utf8.RuneCountInString(r.value))
		}
	}
	for _, r := range rows {
		line := strings.Repeat(" ", r.indent) + r.label
		if r.value != "" {
			gap := labelW - r.indent - utf8.RuneCountInString(r.label) + 2 + valueW - utf8.RuneCountInString(r.value)
			line += strings.Repeat(" ", gap) + r.value
		}
		fmt.Fprintln(w, line)
	}
}

// writeJSON prints v as indented JSON.
func writeJSON(w io.Writer, v any) error {
	b, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return err
	}
	_, err = w.Write(append(b, '\n'))
	return err
}

// dayTitle heads a day: "Tuesday, November 24", or the date, made
// Printable, if it doesn't parse.
func dayTitle(date string) string {
	d, err := time.Parse(time.DateOnly, date)
	if err != nil {
		return text.Printable(date)
	}
	return text.Day(d)
}

// sayAgentFailure says on stderr, in the views' words, why the agent
// couldn't give what a view asked for.
func sayAgentFailure(stderr io.Writer, err error) {
	switch {
	case errors.Is(err, errAgentDown):
		fmt.Fprintln(stderr, text.Phrase(text.KeyAgentDown))
	case errors.Is(err, errAgentOld):
		fmt.Fprintln(stderr, text.Phrase(text.KeyAgentOld))
	case errors.Is(err, errAgentSlow):
		fmt.Fprintln(stderr, text.Phrase(text.KeyAgentSlow))
	default:
		fmt.Fprintln(stderr, text.Phrase(text.KeyAgentError, "error", text.Printable(err.Error())))
	}
}

// sayStoreFailure says on stderr that what the agent stored couldn't be
// read, and why.
func sayStoreFailure(stderr io.Writer, err error) {
	fmt.Fprintln(stderr, text.Phrase(text.KeyStoreError, "error", text.Printable(err.Error())))
}
