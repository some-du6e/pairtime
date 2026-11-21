// SPDX-License-Identifier: AGPL-3.0-or-later

package doctor

import (
	"fmt"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/some-du6e/pairtime/internal/coach/text"
)

// Reach says how asking the running agent for its status went.
type Reach int

const (
	// Reached is an agent that answered.
	Reached Reach = iota
	// Down is no agent: nothing listens on its socket, or port.json says it
	// stopped.
	Down
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
	// Now is when doctor ran.
	Now time.Time
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
		out = append(out, idleCheck(s.Idle), focusCheck(s.Focus))
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
	return out
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
// source, or "presence unknown on this desktop".
func idleCheck(s Signal) Check {
	if s.Via != "" {
		return Check{Name: "idle", Result: OK, Text: "idle detection working, from " + text.Printable(s.Via)}
	}
	return Check{Name: "idle", Result: Warn, Text: "presence unknown on this desktop: " + reason(s.Unknown)}
}

// focusCheck is DESIGN 5.9's focus line: focus detection working with its
// source, or "focus unknown on this desktop". It leaves out 5.9's ", so
// time here counts at level C" until WP-SC8 makes that true.
func focusCheck(s Signal) Check {
	if s.Via != "" {
		return Check{Name: "focus", Result: OK, Text: "focus detection working, from " + text.Printable(s.Via)}
	}
	return Check{Name: "focus", Result: Warn, Text: "focus unknown on this desktop: " + reason(s.Unknown)}
}

// loopCheck warns when the macOS notification loop runs off the main
// thread, where macOS doesn't deliver screen locks and app switches, or
// has dropped events.
func loopCheck(l Loop) Check {
	c := Check{Name: "macos-loop", Result: OK, Text: "the notification loop runs on the main thread, where macOS sends screen locks and app switches"}
	if !l.MainThread {
		c.Result = Warn
		c.Text = "the notification loop doesn't run on the main thread, so screen locks and app switches may not arrive. Report this at " + issues
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
		switch {
		case n.ParseFailures > 0:
			c.Result = Warn
			c.Text += fmt.Sprintf(", and %s didn't fully parse, the last: %s. pairtime may count less of this harness until it does, so report it at %s", count(n.ParseFailures, "call", "calls"), reason(n.LastFailure), issues)
		default:
			c.Text += ", with no parse failures"
		}
		if n.WriteFailures > 0 {
			c.Result = Warn
			c.Text += fmt.Sprintf("; the ledger refused %s, and the ledger line says why", count(n.WriteFailures, "record", "records"))
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

// promptWords says how many of the spooled calls were prompts.
func promptWords(s Spooled) string {
	if s.Calls == 1 {
		switch {
		case s.Unknown == 1:
			return "from a pairtime too old to record whether it was a prompt"
		case s.Prompts == 1:
			return "and it was a prompt"
		}
		return "and it wasn't a prompt"
	}
	w := count(s.Prompts, "of them a prompt", "of them prompts")
	if s.Unknown > 0 {
		w += fmt.Sprintf(", and %d from a pairtime too old to record whether they were", s.Unknown)
	}
	return w
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

// issues is where doctor asks for reports.
const issues = "https://github.com/some-du6e/pairtime/issues"

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
