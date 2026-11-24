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
// they type y, and n, or the end of input, changes nothing. An item install
// can't offer, because the teen left it out with --skip or its planner
// won't edit the file it changes, has no card, and the screen says why.
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

// ItemCalibration is the card of pairtime install --calibration-volunteer:
// shadow = true under [display] in config.toml (DESIGN 6.7 step 2), which
// install makes through config's own edit, as pairtime login changes the
// file, not through the installer, so it isn't an install.Item.
const ItemCalibration = "calibration"

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
	// Left are items install doesn't offer, by item ID, each with why, in
	// words that follow "found at <folder>; " on a harness's line: the
	// teen left it out with --skip, or its planner won't edit the file,
	// such as a settings file that isn't JSON. A harness's line says why
	// its hooks stay out, and the screen says why autostart, the
	// calibration card and the WakaTime line do.
	Left map[string]string
	// Linked is the server pairtime login linked this computer to, while
	// config.toml says online and reads, which install leaves as they are:
	// the screen says so in place of local-only mode. It is empty for a
	// computer that isn't linked, or whose config.toml keeps it local.
	Linked string
	// WakaTimeNew says install makes ~/.wakatime.cfg, since there is none,
	// rather than adding pairtime's line to the teen's own (install.
	// WakaTime): the WakaTime card then says that pairtime is where
	// wakatime-cli sends, and how to get WakaTime's extension.
	WakaTimeNew bool
}

// Screen is the consent screen.
type Screen struct {
	in    Input
	Cards []Card
}

// order is the order the cards come in, which is the order install makes
// the changes: the login item and the agent first, so that the first hook
// call finds the agent up (DESIGN 5.9 steps 3 and 4), config.toml's shadow
// line before the agent starts, so that it starts with shadow on and finds
// the file there, which its first start would otherwise write, and
// ~/.wakatime.cfg after the hooks, once the agent is up (step 5).
var order = []string{install.ItemAutostart, ItemCalibration, install.ItemClaudeCode, install.ItemWakaTime}

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
		case ItemCalibration:
			c = calibrationCard()
		case install.ItemClaudeCode:
			c = claudeCard(in)
		case install.ItemWakaTime:
			c = wakatimeCard(in)
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

// calibrationCard is the card of --calibration-volunteer, which turns on
// display.shadow in config.toml (decision D-shadow-scope). Its diff names
// the file's path, which the sentences leave out, since the screen wraps
// them.
func calibrationCard() Card {
	return Card{
		Title: `Show your time as "calibrating" until pairtime's scoring is frozen`,
		What: `You volunteered to help calibrate pairtime, so until its scoring reaches version 1.0.0, when its rules are frozen, ` +
			`pairtime today, recap, statusline and the local dashboard show your time as "calibrating". ` +
			`They keep the agent's time, your moments and the loops pairtime caught, and leave out everything your time could be worked out from, ` +
			`so nobody learns to steer the rules while they are being calibrated. pairtime still counts all of your time, and its files on this computer keep every number.`,
		Declining: "Without it, pairtime shows your time as its rules count it now, while they are being calibrated.",
		TurnOff:   "You can turn it off any time with shadow = false under [display] in config.toml. It turns off by itself once pairtime's scoring reaches 1.0.0.",
	}
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

// proxyNote is the WakaTime cards' note on a proxy install can't see, one
// an editor sets: wakatime-cli v2.28.4 takes one its --proxy flag names, as
// WakaTime's plugin for JetBrains IDEs passes the IDE's, before the
// environment's, and sends every destination's heartbeats through it,
// pairtime's agent on 127.0.0.1 included, in a config pairtime made too
// (docs/errata/2026-11-24-editor-proxy.md). server says the card is for a
// config of the teen's, whose own server adds two clauses: a proxy the
// editors' environment sets for that server's address, which wakatime-cli
// takes for pairtime's line too, and the sends to that server a failed send
// to the agent holds back. A config pairtime makes has the agent on
// 127.0.0.1 as its only api_url, which no environment proxy reaches, and no
// server of the teen's to hold back.
func proxyNote(server bool) string {
	env, rest := "", ", and pairtime would get none of them"
	if server {
		env = ", or one their environment sets for your server's address"
		rest = ", pairtime would get none of them, and each send the proxy doesn't pass on would hold back what wakatime-cli sends your own server"
	}
	return "Your editors can use a proxy that install can't see: one in their settings, as WakaTime's plugin for JetBrains IDEs passes the IDE's to wakatime-cli" + env + ". " +
		"wakatime-cli would then send pairtime's copy of each heartbeat through it: the proxy would get your project and file names unencrypted" + rest + ". " +
		"So don't set a proxy for your editors while pairtime's line is in."
}

// wakatimeCard is the card of pairtime's line in ~/.wakatime.cfg (DESIGN
// 5.6), or of the config install makes when there is none (WakaTimeNew).
// Its sentences call the file ~/.wakatime.cfg, as WakaTime's own docs do,
// and leave its path to the diff, since the screen wraps them.
func wakatimeCard(in Input) Card {
	if in.WakaTimeNew {
		return Card{
			Title: "Count the time you spend in an editor, through WakaTime's plugins",
			What: "WakaTime's editor plugins run wakatime-cli, which sends a heartbeat each time you edit or move around in a file. " +
				"You have no ~/.wakatime.cfg, so pairtime makes one that has wakatime-cli send them to pairtime's agent on this computer, and nowhere else. " +
				"It also tells wakatime-cli to read your AI agents' transcripts only from now on, so that it doesn't send the ones from before as new time.",
			Notes: []string{
				"For VS Code, Cursor, Windsurf and other editors built on VS Code, install WakaTime's extension from the editor's extensions view, or with code --install-extension WakaTime.vscode-wakatime.",
				"Hack Club programs that review time through Robert read it from Hackatime, which this doesn't set up. If you join one, set up Hackatime too, then run pairtime install again: Hackatime's setup writes a new ~/.wakatime.cfg, and install adds pairtime's line to it.",
				proxyNote(false),
			},
			Declining: "Without it, pairtime doesn't see the time you spend in an editor.",
			TurnOff: "You can take it out any time with pairtime uninstall. It takes pairtime's settings out of the config it made, and deletes the file when nothing else is in it. " +
				"If you gave that config a key of your own and it still sends to pairtime's agent, uninstall leaves the file to you, and says so. " +
				"wakatime-cli's own wakatime-internal.cfg stays once wakatime-cli has written to it, since without it wakatime-cli would send your AI agents' old transcripts as new time to the next server you set up.",
		}
	}
	return Card{
		Title: "Send your editor time to pairtime as well as to your WakaTime server",
		What: "WakaTime's editor plugins run wakatime-cli, which sends a heartbeat each time you edit or move around in a file to the server in your ~/.wakatime.cfg, such as Hackatime. " +
			"pairtime adds one line under [api_urls] there, so that wakatime-cli also sends every heartbeat to pairtime's agent on this computer. " +
			"Nothing else in the file changes. While pairtime's agent doesn't take what wakatime-cli sends it, as while the agent isn't running, wakatime-cli holds back what it sends your server too, and sends it once the agent takes it again.",
		Notes:     []string{proxyNote(true)},
		Declining: "Without it, pairtime doesn't see the time you spend in an editor.",
		TurnOff:   "You can take the line out any time with pairtime uninstall, which leaves the rest of the file as it was.",
	}
}

// millis writes d in whole milliseconds, and as "under 1 ms" below that.
func millis(d time.Duration) string {
	if d < time.Millisecond {
		return "under 1 ms"
	}
	return strconv.FormatInt(d.Milliseconds(), 10) + " ms"
}

// Render writes the whole screen to w, and returns the error writing it
// gave.
func (s *Screen) Render(w io.Writer) error {
	b := &strings.Builder{}
	b.WriteString("pairtime install\n\n")
	b.WriteString(wrap("This is everything pairtime install changes on this computer. Nothing changes until you say yes, and pairtime uninstall takes it all back out.", ""))
	b.WriteString("\n")
	fmt.Fprintf(b, "pairtime goes in %s.\n", install.Printable(s.in.Binary))
	if s.in.Linked != "" {
		b.WriteString(wrap("This computer is linked to "+install.Printable(s.in.Linked)+", and config.toml lets pairtime upload your hours' numbers there.", ""))
		b.WriteString("\n")
	} else {
		b.WriteString("It runs in local-only mode: what it records stays on this computer.\n\n")
	}
	s.renderHarnesses(b)
	if why, ok := s.in.Left[install.ItemAutostart]; ok {
		b.WriteString("\n")
		b.WriteString(wrap("pairtime won't start when you log in: "+install.Printable(why)+".", ""))
	}
	if why, ok := s.in.Left[ItemCalibration]; ok {
		b.WriteString("\n")
		b.WriteString(wrap(`Your time won't show as "calibrating": `+install.Printable(why)+".", ""))
	}
	if why, ok := s.in.Left[install.ItemWakaTime]; ok {
		b.WriteString("\n")
		b.WriteString(wrap("pairtime won't get your editor time from WakaTime's plugins: "+install.Printable(why)+".", ""))
	}
	for i, c := range s.Cards {
		b.WriteString("\n")
		renderCard(b, i+1, c)
	}
	if len(s.Cards) == 0 {
		b.WriteString("\n")
		b.WriteString(wrap("There is nothing else to change here: pairtime only goes in place and starts.", ""))
	}
	_, err := io.WriteString(w, b.String())
	return err
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
		case s.in.Left[h.Item] != "":
			what = "found at " + install.Printable(h.Where) + "; " + install.Printable(s.in.Left[h.Item])
		case h.Item == install.ItemClaudeCode && !s.in.Hook.OK:
			what = "found at " + install.Printable(h.Where) + "; " + install.Printable(s.in.Hook.Why)
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
// line that can't be read, is no, and so is a yes to a screen or a
// question that couldn't be written: the teen can only agree to what they
// saw.
func (s *Screen) Ask(in io.Reader, out io.Writer) bool {
	if s.Render(out) != nil {
		return false
	}
	sc := bufio.NewScanner(in)
	for {
		if _, err := fmt.Fprintf(out, "\n%s", s.prompt()); err != nil {
			return false
		}
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
			if _, err := fmt.Fprintf(out, "%s %d. %s\n", mark(c.Ticked), i+1, c.Title); err != nil {
				return false
			}
		}
	}
}
