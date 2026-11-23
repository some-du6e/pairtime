// SPDX-License-Identifier: AGPL-3.0-or-later

package doctor

import (
	"fmt"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/some-du6e/pairtime/internal/coach/text"
)

// Reach says how asking the running agent for its status went.
type Reach int

const (
	// Reached is an agent that answered.
	Reached Reach = iota
	// Down is no agent: nothing answers on its socket, and nothing holds
	// the data folder's lock.
	Down
	// Deaf is an agent that holds the data folder's lock but answers
	// nothing on its socket, so every hook call goes to the spool.
	Deaf
	// Old is an agent from before doctor, which doesn't answer the status
	// question.
	Old
	// Slow is an agent that didn't answer in time.
	Slow
	// Unreached is any other failure, which Facts.AgentError says.
	Unreached
)

// Facts is everything doctor's checks are worked out from.
type Facts struct {
	// Reach says whether the agent answered, and Agent is its status when
	// it did. AgentError says what went wrong when Reach is Unreached.
	Reach      Reach
	Agent      *Status
	AgentError string
	// AskWait is how long doctor waited for the agent's answer.
	AskWait time.Duration
	// Version is this pairtime's own version, which the agent's may differ
	// from until it restarts.
	Version string
	// AskTook is how long the agent took to answer, when it did.
	AskTook time.Duration
	// Adapters are the harnesses this pairtime has an adapter for, sorted.
	Adapters []string
	// Hooks are the harnesses pairtime can hook into, found or not, with
	// what their settings hold of pairtime's hooks and the round trips of a
	// test call of the hook they run.
	Hooks []Hooks
	// Keystore is where the device key is, from keystore.json.
	Keystore Keystore
	// Signing is what this binary is and how it is signed.
	Signing Signing
	// Homes are the home folder's spellings (HomeSpellings), which the
	// lines show as ~, so that a line someone shares names fewer of their
	// folders.
	Homes []string
}

// Checks works out doctor's lines from f, in the order DESIGN 5.9 lists
// them, and then each problem the agent reports, in its own words.
func Checks(f Facts) []Check {
	var out []Check
	out = append(out, agentCheck(f))
	if f.Reach != Reached || f.Agent == nil {
		why := notAsked(f)
		out = append(out,
			Check{Name: "idle", Result: Skip, Text: why},
			Check{Name: "focus", Result: Skip, Text: why})
	} else {
		s := f.Agent.Signals
		out = append(out, idleCheck(s), focusCheck(s))
		if s.Loop != nil {
			out = append(out, loopCheck(*s.Loop))
		}
	}
	for _, h := range f.Hooks {
		out = append(out, hookChecks(h)...)
	}
	out = append(out, adapterChecks(f)...)
	out = append(out, signingCheck(f.Signing), keystoreCheck(f.Keystore), spoolCheck(f))
	out = append(out, problemChecks(f)...)
	for i := range out {
		out[i].Text = tilde(out[i].Text, f.Homes)
	}
	return out
}

// tilde writes each of homes as ~ in s, in both its slashes' spellings
// and in any case: Windows and macOS read paths that way, so the same
// folder can come back in another case. Each pattern is compiled here,
// when doctor runs, rather than at package init. A home that isn't UTF-8,
// as a Linux path can be, is passed over: regexp refuses it, and the
// lines, which text.Printable has made UTF-8, can't hold it.
func tilde(s string, homes []string) string {
	for _, home := range homes {
		if len(home) < 3 || !utf8.ValidString(home) {
			continue
		}
		for _, h := range []string{home, strings.ReplaceAll(home, `\`, "/"), strings.ReplaceAll(home, "/", `\`)} {
			s = regexp.MustCompile("(?i)"+regexp.QuoteMeta(h)).ReplaceAllLiteralString(s, "~")
		}
	}
	return s
}

// notAsked says why a line that reads the agent's status was skipped.
func notAsked(f Facts) string {
	if f.Reach == Down {
		return "the agent isn't running, and doctor reads this from the agent, not from the terminal it runs in"
	}
	return "the agent didn't say, and doctor reads this from the agent, not from the terminal it runs in"
}

func agentCheck(f Facts) Check {
	c := Check{Name: "agent"}
	switch f.Reach {
	case Down:
		c.Result, c.Text = Fail, "isn't running, so pairtime tracks nothing. Logging in starts it, or run: pairtime agent --detach"
	case Deaf:
		c.Result, c.Text = Fail, "runs, but nothing answers on its socket, so every hook call goes to the spool and counts for less. Log out and back in to restart it"
	case Old:
		c.Result, c.Text = Warn, "runs an older pairtime that can't answer doctor, so the lines that need it are skipped. Run pairtime install with this one to move the agent to it"
	case Slow:
		c.Result, c.Text = Fail, fmt.Sprintf("didn't answer within %s, so it may be stuck. Try again in a moment", seconds(f.AskWait))
	case Unreached:
		c.Result, c.Text = Fail, "couldn't be asked: "+text.Printable(f.AgentError)
	default:
		a := f.Agent
		if a == nil {
			c.Result, c.Text = Fail, "answered with no status"
			return c
		}
		c.Result = OK
		c.Text = fmt.Sprintf("running pairtime %s for %s, %s; it answered in %s", text.Printable(a.Version.Version),
			text.Duration(float64(a.UptimeS)), modeWords(a.Mode), Millis(f.AskTook))
		// The agent answered over the socket or pipe that hooks use, so
		// they reach it too.
		if a.IPC.Via == "pipe" {
			c.Text += ", and hooks reach it over the named pipe Windows falls back to"
		}
		if f.Version != "" && a.Version.Version != f.Version {
			if c.Result == OK {
				c.Result = Warn
			}
			c.Text += fmt.Sprintf(". This pairtime is %s, so the agent's lines here are of another version; run pairtime install with this one to move the agent to it", text.Printable(f.Version))
		}
	}
	return c
}

// modeWords says what the agent's mode uploads (DESIGN 4.2).
func modeWords(mode string) string {
	switch mode {
	case "local":
		return "in local mode, which uploads nothing"
	case "online":
		return "signed in, so it uploads your hours' numbers"
	}
	return "in mode " + strconv.Quote(mode)
}

// idleCheck is DESIGN 5.9's idle line: idle detection working with its
// source, or "presence unknown on this desktop". With presence unknown no
// slot is present (pkg/score), so pairtime counts none of the teen's time
// there: the line fails, and says what to do.
func idleCheck(s Signals) Check {
	if s.Idle.Via != "" {
		return Check{Name: "idle", Result: OK, Text: "idle detection working, from " + text.Printable(s.Idle.Via)}
	}
	return Check{Name: "idle", Result: Fail,
		Text: "presence unknown on this desktop: " + reason(s.Idle.Unknown) + unknownCost(s, s.Idle.Unknown, false)}
}

// focusCheck is DESIGN 5.9's focus line: focus detection working with its
// source, or "focus unknown on this desktop". With focus unknown the agent
// records an os.focus with app_class unknown (SM 2.5), whose slots the
// scorer credits nothing until WP-SC8: the line fails, and says what to
// do. WP-SC8, which credits such time at level C, makes it a warning with
// 5.9's ", so time here counts at level C".
func focusCheck(s Signals) Check {
	if s.Focus.Via != "" {
		return Check{Name: "focus", Result: OK, Text: "focus detection working, from " + text.Printable(s.Focus.Via)}
	}
	return Check{Name: "focus", Result: Fail,
		Text: "focus unknown on this desktop: " + reason(s.Focus.Unknown) + unknownCost(s, s.Focus.Unknown, true)}
}

// unknownCost is what an unknown signal costs, and the one thing to do,
// from the session and the cause (internal/platform/linux names both):
// on Wayland, whose desktops tell other programs neither signal, an X11
// session; on X11, a window manager that keeps _NET_ACTIVE_WINDOW for
// focus and the X server's MIT-SCREEN-SAVER extension for idle time,
// unless the agent is only reconnecting to the X server; and with no
// desktop session, or a macOS or Windows source that didn't start, a new
// login, which starts the agent in the desktop's session.
func unknownCost(s Signals, why string, focus bool) string {
	const cost = ". pairtime counts none of your time here yet; "
	sees := "when you're at the keyboard"
	if focus {
		sees = "the window in front"
	}
	switch {
	case s.Session == "wayland":
		return cost + "an X11 session, where your login screen offers one, lets it see " + sees
	case s.Session == "x11" && strings.Contains(why, "reconnecting"):
		return cost + "the agent is reconnecting to the X server, so run pairtime doctor again in a minute"
	case s.Session == "x11" && focus:
		return cost + "a window manager that keeps _NET_ACTIVE_WINDOW, as most do, lets it see " + sees
	case s.Session == "x11":
		return cost + "an X server with the MIT-SCREEN-SAVER extension, as most have, lets it see " + sees
	}
	return cost + "logging out and back in starts the agent in your desktop's session, where it can see " + sees
}

// loopCheck warns when the macOS notification loop runs off the main
// thread, where macOS doesn't deliver screen locks and app switches, or
// has dropped events.
func loopCheck(l Loop) Check {
	c := Check{Name: "macos-loop", Result: OK, Text: "the notification loop runs on the main thread, where macOS sends screen locks and app switches"}
	if !l.MainThread {
		c.Result = Warn
		c.Text = "the notification loop doesn't run on the main thread, so screen locks and app switches may not arrive. " + reportIt
	}
	if l.Dropped > 0 {
		c.Result = Warn
		c.Text += fmt.Sprintf("; it dropped %s because the agent fell behind", count(l.Dropped, "event", "events"))
	}
	return c
}

// adapterChecks are DESIGN 5.9's adapters line, one per adapter this
// pairtime has, with the calls each read since the agent started and its
// parse failures, and one for calls of harnesses it has none for.
func adapterChecks(f Facts) []Check {
	var a Adapters
	if f.Agent != nil {
		a = f.Agent.Adapters
	}
	names := slices.Clone(f.Adapters)
	for h := range a.Harnesses {
		if !slices.Contains(names, h) {
			names = append(names, h)
		}
	}
	slices.Sort(names)
	var out []Check
	for _, h := range names {
		c := Check{Name: "adapter." + h}
		if f.Reach != Reached || f.Agent == nil {
			c.Result, c.Text = Skip, "found; "+notAsked(f)
			out = append(out, c)
			continue
		}
		n := a.Harnesses[h]
		c.Result = OK
		c.Text = fmt.Sprintf("found; since the agent started, %s made %s", count(n.Calls, "call", "calls"), count(n.Records, "record", "records"))
		// The agent keeps one last failure for both kinds, so it is named
		// for neither.
		var trouble []string
		if n.ParseFailures > 0 {
			trouble = append(trouble, count(n.ParseFailures, "call", "calls")+" didn't fully parse")
		}
		if n.WriteFailures > 0 {
			trouble = append(trouble, "the ledger refused "+count(n.WriteFailures, "record", "records"))
		}
		switch {
		case n.Calls == 0 && len(trouble) == 0:
			c.Text = "found; no hook call has come in since the agent started"
		case len(trouble) == 0:
			c.Text += ", with no parse failures"
		default:
			c.Result = Warn
			c.Text += ", but " + strings.Join(trouble, " and ")
			if n.LastFailure != "" {
				c.Text += "; the last failure: " + reason(n.LastFailure)
			}
			if n.ParseFailures > 0 {
				c.Text += ". pairtime may count less of this harness until it reads them. " + reportIt
			}
		}
		if !slices.Contains(f.Adapters, h) {
			c.Text += "; this pairtime has no adapter for it, but the agent does"
		}
		out = append(out, c)
	}
	if f.Reach == Reached && f.Agent != nil && a.Other.Calls > 0 {
		out = append(out, Check{Name: "adapter.other", Result: Warn,
			Text: fmt.Sprintf("%s came from harnesses pairtime has no adapter for, so they count for nothing yet", count(a.Other.Calls, "hook call", "hook calls"))})
	}
	return out
}

// spoolCheck is the line DESIGN 5.2's spool rule asks for: the hook calls
// of the last week that only the spool delivered, and how many were
// prompts. Their events get via = plugin, so they earn side-door credit
// and the scorer counts a transcript entry of their turns as one with no
// hook record (CC1); the line measures what the rule costs.
func spoolCheck(f Facts) Check {
	c := Check{Name: "spool"}
	if f.Reach != Reached || f.Agent == nil {
		c.Result, c.Text = Skip, notAsked(f)
		return c
	}
	s := f.Agent.IPC.Spooled
	if s.Calls == 0 {
		c.Result, c.Text = OK, "every hook call of the last 7 days reached the agent directly, none only through the spool"
		return c
	}
	c.Result = Warn
	c.Text = fmt.Sprintf("%s of the last 7 days reached the agent only through the spool, %s", count(s.Calls, "hook call", "hook calls"), promptWords(s))
	c.Text += ", so pairtime trusts them as little as a plugin's and credits less of their time. Calls go to the spool while the agent isn't running or answering, and logging in starts it"
	return c
}

// promptWords says how many of the spooled calls were prompts, of those
// whose journal lines say: the lines an older version wrote don't.
func promptWords(s Spooled) string {
	unknown := min(s.Unknown, s.Calls)
	known := s.Calls - unknown
	switch {
	case known == 0 && s.Calls == 1:
		return "from a pairtime too old to record whether it was a prompt"
	case known == 0:
		return "all from a pairtime too old to record whether they were prompts"
	case s.Calls == 1 && s.Prompts > 0:
		return "and it was a prompt"
	case s.Calls == 1:
		return "and it wasn't a prompt"
	case unknown == 0:
		return count(s.Prompts, "of them a prompt", "of them prompts")
	}
	were := "were prompts"
	if s.Prompts == 1 {
		were = "was a prompt"
	}
	them := "they were"
	if unknown == 1 {
		them = "it was"
	}
	return fmt.Sprintf("%d of the %d it can tell %s, and %d from a pairtime too old to record whether %s", s.Prompts, known, were, unknown, them)
}

// problemChecks are the problems the agent reports, each in its own
// words, by where it comes from. The keystore's warning is left out,
// since the keystore line gives it.
func problemChecks(f Facts) []Check {
	if f.Agent == nil {
		return nil
	}
	keys := make([]string, 0, len(f.Agent.Problems))
	for k, v := range f.Agent.Problems {
		if strings.TrimSpace(v) != "" {
			keys = append(keys, k)
		}
	}
	slices.Sort(keys)
	keys = slices.DeleteFunc(keys, func(k string) bool { return k == "keystore" })
	out := make([]Check, 0, len(keys))
	for _, k := range keys {
		out = append(out, Check{Name: "problem." + checkName(k), Result: Warn, Text: text.Printable(f.Agent.Problems[k])})
	}
	return out
}

// checkName makes s, a problem's source, the last part of a check's name,
// in the characters the agent's sources use: lowercase letters, digits,
// '_' and '-', with '.' between parts (docs/schema/cli-doctor.schema.json).
func checkName(s string) string {
	var b strings.Builder
	for _, r := range strings.ToLower(s) {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9', r == '_', r == '-', r == '.':
			b.WriteRune(r)
		default:
			b.WriteByte('-')
		}
	}
	n := b.String()
	for strings.Contains(n, "..") {
		n = strings.ReplaceAll(n, "..", ".")
	}
	if n = strings.Trim(n, "."); n == "" {
		return "other"
	}
	return n
}

// reportIt is where and what doctor asks the teen to report. The issues
// are public, and doctor's other lines name their folders.
const reportIt = "Report this line, and pairtime doctor --report if it helps, at https://github.com/some-du6e/pairtime/issues, but not the rest of doctor's output, which names your folders"

// reason is an unknown signal's or a failure's reason, made printable, or
// "the agent doesn't say why" when there is none.
func reason(s string) string {
	if strings.TrimSpace(s) == "" {
		return "the agent doesn't say why"
	}
	return text.Printable(s)
}

// count says n things: "1 call", "3 calls".
func count(n uint64, one, many string) string {
	if n == 1 {
		return "1 " + one
	}
	return strconv.FormatUint(n, 10) + " " + many
}

// Millis says d in milliseconds, as install's consent screen does: "under
// 1 ms", or "23 ms".
func Millis(d time.Duration) string {
	if d < time.Millisecond {
		return "under 1 ms"
	}
	return strconv.FormatInt(d.Round(time.Millisecond).Milliseconds(), 10) + " ms"
}

// seconds says d in seconds, such as "3 s".
func seconds(d time.Duration) string {
	return strconv.FormatFloat(d.Seconds(), 'f', -1, 64) + " s"
}
