// SPDX-License-Identifier: AGPL-3.0-or-later

// Package consent is the screen `pairtime install` shows before it changes
// anything (DESIGN 5.9 step 2; docs/PLAN.md, WP-IN2): every planned change
// as a diff, on one card per item, and the teen's say over each.
//
// Each card says what its item does, that saying no doesn't stop the teen
// from using pairtime, what saying no changes where it changes something,
// and how to turn the item off later, as DESIGN 8.4 asks of every consent
// card and in the words of the dashboard's cards (#72,
// web/src/shared/components/ConsentCard.svelte). Below that is the change
// itself, as install.Plan.Diff prints it: every string value and comment
// in a settings file that pairtime didn't write shows as ***, so the screen
// a teen screenshots for help holds none of their tokens.
//
// Unlike 8.4's cards, which ask for something beyond the service, these are
// the service the teen ran install for, so every card starts ticked and the
// teen unticks what they don't want (DESIGN 5.9). Nothing is written until
// they type y, and n, or the end of input, changes nothing.
package consent

import (
	"bufio"
	"fmt"
	"io"
	"strconv"
	"strings"
	"time"

	"github.com/some-du6e/pairtime/internal/install"
)

// Declined is the sentence every card carries (DESIGN 8.4; #72).
const Declined = "Saying no doesn't stop you from using pairtime."

// Card is one item the screen offers.
type Card struct {
	// ID is the install item's ID, such as install.ItemAutostart.
	ID string
	// Title names the item.
	Title string
	// What says what the item does.
	What string
	// Cost is the item's cost line, "" when it has none.
	Cost string
	// Notes are anything else the teen should know about the item.
	Notes []string
	// Declining says, in one plain and true sentence, what saying no
	// changes.
	Declining string
	// TurnOff says how to turn the item off later.
	TurnOff string
	// Diff is the change as install.Plan.Diff prints it, "" when nothing
	// changes because the item is set up already.
	Diff string
	// Ticked says whether install makes the change.
	Ticked bool
}

// HookTest is how the self-test of `pairtime hook` went, which decides
// whether install offers any hooks.
type HookTest struct {
	// OK says the hook command passed, and Took how long the test call
	// took, from start to exit.
	OK   bool
	Took time.Duration
	// Why says why install adds no hooks, when the test didn't pass.
	Why string
}

// Input is what install found and planned, from which New makes the
// screen.
type Input struct {
	// GOOS is the OS install runs on.
	GOOS string
	// Binary is where install puts pairtime's binary.
	Binary string
	// Harnesses are what install.Detect returned.
	Harnesses []install.Harness
	// Hook is how the hook self-test went.
	Hook HookTest
	// Diffs are the changes of the items install offers, by item ID, as
	// install.Plan.Diff prints them. An item missing from it isn't
	// offered.
	Diffs map[string]string
}

// Screen is the consent screen.
type Screen struct {
	in    Input
	Cards []Card
}

// order is the order the cards come in, which is the order install makes
// the changes: the login item and the agent first, so that the first hook
// call finds the agent up (DESIGN 5.9 steps 3 and 4).
var order = []string{install.ItemAutostart, install.ItemClaudeCode}

// New makes the screen for in, with every card ticked.
func New(in Input) *Screen {
	s := &Screen{in: in}
	for _, id := range order {
		diff, ok := in.Diffs[id]
		if !ok {
			continue
		}
		var c Card
		switch id {
		case install.ItemAutostart:
			c = autostartCard(in.GOOS)
		case install.ItemClaudeCode:
			c = claudeCard(in)
		}
		c.ID, c.Diff, c.Ticked = id, diff, true
		s.Cards = append(s.Cards, c)
	}
	return s
}

// Ticked returns the IDs of the ticked cards, in order.
func (s *Screen) Ticked() []string {
	var ids []string
	for _, c := range s.Cards {
		if c.Ticked {
			ids = append(ids, c.ID)
		}
	}
	return ids
}

// Untick unticks the card of the item id, and reports whether there is
// one. `--skip` uses it.
func (s *Screen) Untick(id string) bool {
	for i := range s.Cards {
		if s.Cards[i].ID == id {
			s.Cards[i].Ticked = false
			return true
		}
	}
	return false
}

func autostartCard(goos string) Card {
	c := Card{
		Title:     "Start pairtime when you log in",
		What:      "pairtime's agent starts in the background each time you log in, so it notices when you build without you starting it.",
		Declining: "Without it, pairtime starts again after you log in only when a hook starts it or you run pairtime agent, so it misses what comes before.",
	}
	switch goos {
	case "windows":
		c.TurnOff = "You can turn it off any time in Settings, under Apps > Startup, or with pairtime uninstall."
	case "darwin":
		c.TurnOff = "You can turn it off any time in System Settings, under General > Login Items & Extensions, or with pairtime uninstall."
	default:
		c.TurnOff = "You can turn it off any time in your desktop's Startup Applications, or with pairtime uninstall."
	}
	return c
}

func claudeCard(in Input) Card {
	c := Card{
		Title: "Add pairtime's hooks to Claude Code",
		What: "Claude Code runs pairtime hook when a session starts or ends, when you send a prompt, and when its agent stops, asks you something, edits a file, runs a command or starts a subagent, so pairtime knows when you prompt and when the agent works. " +
			"pairtime's hooks go after the hooks already there, which stay as they are.",
		Declining: "Without them, pairtime doesn't see your Claude Code prompts, and time it can't see isn't credited.",
		TurnOff:   "You can take them out any time with pairtime uninstall.",
	}
	if in.Hook.OK {
		c.Cost = fmt.Sprintf("Each hook call adds a moment to Claude Code's turn: a test call just now took %s.", millis(in.Hook.Took))
	}
	for _, h := range in.Harnesses {
		if h.Found() && h.ReadsClaudeHooks {
			c.Notes = append(c.Notes, h.Title+" runs these hooks too, without their arguments, and pairtime ignores those calls.")
		}
	}
	return c
}

// millis writes d in whole milliseconds, and as "under 1 ms" below that.
func millis(d time.Duration) string {
	if d < time.Millisecond {
		return "under 1 ms"
	}
	return strconv.FormatInt(d.Milliseconds(), 10) + " ms"
}

// Render writes the whole screen to w.
func (s *Screen) Render(w io.Writer) {
	b := &strings.Builder{}
	b.WriteString("pairtime install\n\n")
	b.WriteString(wrap("This is everything pairtime install changes on this computer. Nothing changes until you say yes, and pairtime uninstall takes it all back out.", ""))
	b.WriteString("\n")
	fmt.Fprintf(b, "pairtime goes in %s.\n", install.Printable(s.in.Binary))
	b.WriteString("It runs in local-only mode: what it records stays on this computer.\n\n")
	s.renderHarnesses(b)
	for i, c := range s.Cards {
		b.WriteString("\n")
		renderCard(b, i+1, c)
	}
	if len(s.Cards) == 0 {
		b.WriteString("\n")
		b.WriteString(wrap("There is nothing else to change here: pairtime only goes in place and starts.", ""))
	}
	io.WriteString(w, b.String())
}

func (s *Screen) renderHarnesses(b *strings.Builder) {
	b.WriteString("AI tools pairtime looked for:\n")
	col := 0
	for _, h := range s.in.Harnesses {
		col = max(col, len(h.Title))
	}
	for _, h := range s.in.Harnesses {
		var what string
		switch {
		case !h.Found():
			what = "not found"
		case h.Item == "":
			what = "found at " + install.Printable(h.Where) + "; pairtime can't add hooks to it yet"
		case h.Item == install.ItemClaudeCode && !s.in.Hook.OK:
			what = "found at " + install.Printable(h.Where) + "; " + s.in.Hook.Why
		default:
			what = "found at " + install.Printable(h.Where)
		}
		fmt.Fprintf(b, "  %-*s  %s\n", col, h.Title, what)
	}
}

func renderCard(b *strings.Builder, n int, c Card) {
	fmt.Fprintf(b, "%s %d. %s\n\n", mark(c.Ticked), n, c.Title)
	b.WriteString(wrap(c.What, "    "))
	if c.Cost != "" {
		b.WriteString(wrap(c.Cost, "    "))
	}
	for _, note := range c.Notes {
		b.WriteString(wrap(note, "    "))
	}
	b.WriteString("\n")
	b.WriteString(wrap(Declined, "    "))
	if c.Declining != "" {
		b.WriteString(wrap(c.Declining, "    "))
	}
	b.WriteString(wrap(c.TurnOff, "    "))
	b.WriteString("\n")
	if c.Diff == "" {
		b.WriteString("    It is set up already, so nothing changes.\n")
		return
	}
	for _, line := range strings.SplitAfter(strings.TrimSuffix(c.Diff, "\n"), "\n") {
		b.WriteString("    " + strings.TrimSuffix(line, "\n") + "\n")
	}
}

func mark(ticked bool) string {
	if ticked {
		return "[x]"
	}
	return "[ ]"
}

// lineWidth is the column the screen wraps its sentences at.
const lineWidth = 76

// wrap fills text into lines no longer than lineWidth, each starting with
// indent, and ends it with a newline. A word longer than a line, such as
// a path, gets a line of its own.
func wrap(text, indent string) string {
	var b strings.Builder
	line := indent
	for _, word := range strings.Fields(text) {
		if line != indent && len(line)+1+len(word) > lineWidth {
			b.WriteString(line + "\n")
			line = indent
		}
		if line != indent {
			line += " "
		}
		line += word
	}
	b.WriteString(line + "\n")
	return b.String()
}

// prompt is what Ask asks after the screen and after each change.
func (s *Screen) prompt() string {
	if len(s.Cards) == 0 {
		return "Type y to go ahead, or n to stop without changing anything: "
	}
	return fmt.Sprintf("Type y to install the ticked items, n to stop without changing anything, or a number from 1 to %d to tick or untick that item: ", len(s.Cards))
}

// Ask shows the screen on out and reads the teen's answers, one per line,
// from in until they say yes or no. A number ticks or unticks its card and
// shows the list again. It returns true for yes. The end of input, or a
// line that can't be read, is no.
func (s *Screen) Ask(in io.Reader, out io.Writer) bool {
	s.Render(out)
	sc := bufio.NewScanner(in)
	for {
		fmt.Fprintf(out, "\n%s", s.prompt())
		if !sc.Scan() {
			fmt.Fprintln(out)
			return false
		}
		answer := strings.ToLower(strings.TrimSpace(sc.Text()))
		switch answer {
		case "y", "yes":
			return true
		case "n", "no", "q", "quit":
			return false
		case "":
			continue
		}
		n, err := strconv.Atoi(answer)
		if err != nil || n < 1 || n > len(s.Cards) {
			if len(s.Cards) == 0 {
				fmt.Fprintln(out, "Type y or n.")
			} else {
				fmt.Fprintf(out, "Type y, n, or a number from 1 to %d.\n", len(s.Cards))
			}
			continue
		}
		s.Cards[n-1].Ticked = !s.Cards[n-1].Ticked
		fmt.Fprintln(out)
		for i, c := range s.Cards {
			fmt.Fprintf(out, "%s %d. %s\n", mark(c.Ticked), i+1, c.Title)
		}
	}
}
