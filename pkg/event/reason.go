// SPDX-License-Identifier: AGPL-3.0-or-later

package event

import "strings"

// Reason is a reason code (SM 5.2): why a second was credited, or why not.
// Uncredited reasons start with "X.". A credit reason may end in
// ReturnedSuffix, meaning the second was credited in a returned slot,
// with no input, under SM 2.1's return rule ("READ~r", "P~r"). It may
// also end in RestoredSuffix, after ReturnedSuffix if both apply
// ("READ~r+restored").
type Reason string

// ReturnedSuffix marks a credit reason earned in a returned slot.
const ReturnedSuffix = "~r"

// RestoredSuffix marks a credit reason whose half-weighted second a later
// progress event put back to weight 1 (SM 4.3).
const RestoredSuffix = "+restored"

// Credit reasons.
const (
	// ReasonPrompt is the lead of a substantive or error-paste prompt.
	ReasonPrompt Reason = "P"
	// ReasonAnswer is the lead of an answer to the agent's question.
	ReasonAnswer Reason = "ANS"
	// ReasonGoAhead is the lead of a go-ahead.
	ReasonGoAhead Reason = "GO"
	// ReasonQueued is the lead of a message queued while the agent worked.
	ReasonQueued Reason = "Q"
	// ReasonPermission is the wait and trail of a permission answer.
	ReasonPermission Reason = "PA"
	// ReasonInterrupt is the lead and trail of an interrupt.
	ReasonInterrupt Reason = "INT"
	// ReasonEdit is the lead of a typed edit.
	ReasonEdit Reason = "E"
	// ReasonNavigate is the lead of editor navigation.
	ReasonNavigate Reason = "NAV"
	// ReasonCommand is the lead, run and trail of a typed command.
	ReasonCommand Reason = "CMD"
	// ReasonTest is the lead, run and trail of a typed test or build command.
	ReasonTest Reason = "TEST"
	// ReasonCommit is the lead of a human commit.
	ReasonCommit Reason = "G"
	// ReasonSupervise is the reading bucket drained while the agent runs.
	ReasonSupervise Reason = "SUP"
	// ReasonRead is the reading bucket drained after the agent stopped.
	ReasonRead Reason = "READ"
	// ReasonLookup is a bracketed browser or chat-app run.
	ReasonLookup Reason = "LOOKUP"
)

// Uncredited reasons.
const (
	// ReasonAbsent is no presence: no input past the bridge and tail, a
	// lock, or sleep.
	ReasonAbsent Reason = "X.absent"
	// ReasonOtherFocus is input to an app outside the project.
	ReasonOtherFocus Reason = "X.other_focus"
	// ReasonAmbiguous is focus that couldn't be resolved to one project.
	ReasonAmbiguous Reason = "X.ambiguous"
	// ReasonNoAnchor is present and focused, but no window covered the
	// second. A mouse jiggler on an open terminal ends up here.
	ReasonNoAnchor Reason = "X.no_anchor"
	// ReasonAgentOnly is an agent turn running with nothing crediting the
	// second.
	ReasonAgentOnly Reason = "X.agent_only"
	// ReasonAutomated is a turn whose origin was scheduled, self_continue,
	// subagent, headless or cloud.
	ReasonAutomated Reason = "X.automated"
	// ReasonNoInput is a prompt with no input before it.
	ReasonNoInput Reason = "X.no_input"
	// ReasonReroll, ReasonRewriteLoop, ReasonSameError and ReasonAutopilot
	// are the dark-flow discounts (SM 4.2).
	ReasonReroll      Reason = "X.reroll"
	ReasonRewriteLoop Reason = "X.rewrite_loop"
	ReasonSameError   Reason = "X.same_error"
	ReasonAutopilot   Reason = "X.autopilot"
	// ReasonNoProgress is the half not credited under the no-progress rule
	// (SM 4.3).
	ReasonNoProgress Reason = "X.noprog"
	// ReasonLookupCap, ReasonBucketCap and ReasonDayCap are the caps
	// (SM 2.4, 2.5, 5.3).
	ReasonLookupCap Reason = "X.lookup_cap"
	ReasonBucketCap Reason = "X.bucket_cap"
	ReasonDayCap    Reason = "X.day_cap"
)

// creditReasons maps each credit reason to the activity its seconds count
// as, in SM 5.2 order.
var creditReasons = []struct {
	reason   Reason
	activity Activity
}{
	{ReasonPrompt, ActivityPrompting},
	{ReasonAnswer, ActivityPrompting},
	{ReasonGoAhead, ActivityPrompting},
	{ReasonQueued, ActivityPrompting},
	{ReasonPermission, ActivityReviewing},
	{ReasonInterrupt, ActivityReviewing},
	{ReasonEdit, ActivityHandEdit},
	{ReasonNavigate, ActivityReviewing},
	{ReasonCommand, ActivityDebugging},
	{ReasonTest, ActivityTesting},
	{ReasonCommit, ActivityReviewing},
	{ReasonSupervise, ActivitySupervising},
	{ReasonRead, ActivityReadingOutput},
	{ReasonLookup, ActivityLookup},
}

var uncreditedReasons = enum[Reason]{values: []Reason{
	ReasonAbsent, ReasonOtherFocus, ReasonAmbiguous, ReasonNoAnchor, ReasonAgentOnly,
	ReasonAutomated, ReasonNoInput, ReasonReroll, ReasonRewriteLoop, ReasonSameError,
	ReasonAutopilot, ReasonNoProgress, ReasonLookupCap, ReasonBucketCap, ReasonDayCap,
}}

// darkFlowReasons are SM 4.2's discounts, the reasons a rule applies to an
// anchor. The no-progress half rate isn't one: it weighs a project's
// seconds, not anchors (SM 4.3).
var darkFlowReasons = enum[Reason]{values: []Reason{
	ReasonReroll, ReasonRewriteLoop, ReasonSameError, ReasonAutopilot,
}}

// Base returns r without RestoredSuffix and ReturnedSuffix. Each may
// appear once, in that order, so "P+restored~r" has no base.
func (r Reason) Base() Reason {
	s := strings.TrimSuffix(string(r), RestoredSuffix)
	return Reason(strings.TrimSuffix(s, ReturnedSuffix))
}

// Activity returns the activity a credit reason's seconds count as, or ""
// if r isn't a credit reason.
func (r Reason) Activity() Activity {
	base := r.Base()
	for _, cr := range creditReasons {
		if cr.reason == base {
			return cr.activity
		}
	}
	return ""
}

// Credited reports whether r is a credit reason, with or without
// ReturnedSuffix and RestoredSuffix.
func (r Reason) Credited() bool { return r.Activity() != "" }

// Uncredited reports whether r is an uncredited reason.
func (r Reason) Uncredited() bool { return uncreditedReasons.has(r) }

// Valid reports whether r is a reason code in SM 5.2.
func (r Reason) Valid() bool { return r.Credited() || r.Uncredited() }
