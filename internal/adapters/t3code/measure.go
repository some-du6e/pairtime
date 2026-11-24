// SPDX-License-Identifier: AGPL-3.0-or-later

package t3code

import (
	"net/url"
	"regexp"
	"strings"
	"sync"
	"unicode/utf8"

	"github.com/some-du6e/pairtime/pkg/event"
	"github.com/some-du6e/pairtime/pkg/params"
	"github.com/some-du6e/pairtime/pkg/score"
)

// attachmentOnly is the text T3 stores for a message that has attachments
// and no text of its own: 121 characters no person typed (T3 d8f3feff,
// packages/shared/src/composerContextReferences.ts,
// ATTACHMENT_ONLY_BOOTSTRAP_PROMPT).
const attachmentOnly = "[User attached one or more files without additional text. Respond using the conversation context and the attached files.]"

// ultrathink opens the line T3's web client puts before a message's text at
// Claude's ultrathink effort (packages/shared/src/model.ts).
const ultrathink = "Ultrathink:"

// guestOpening is how T3 Code opens the text it sends on for a guest's
// message, "Message from guest <name>, sent by the owner:" and then the
// guest's words (T3 d8f3feff,
// apps/server/src/orchestration-v2/GuestShareDecider.ts:217).
const guestOpening = "Message from guest "

// The limits T3 puts on a context reference's parts
// (composerContextReferences.ts): a label of at most 512 characters and a
// reference of at most 200 after its prefix.
const (
	contextPrefix   = "t3-context://v1/"
	maxContextLabel = 512
	maxContextRef   = 200
	citationPrefix  = "t3-citation://v1/"
)

// The patterns of T3's machine strings, compiled on first use since every
// client package's init runs in every hook call.
var (
	// contextLink is a context reference: [label](t3-context://v1/kind/id),
	// with a ! before it for an image.
	contextLink = sync.OnceValue(func() *regexp.Regexp {
		return regexp.MustCompile(`!?\[([^\]\n]*)\]\((t3-context://v1/[^\s)]+)\)`)
	})
	// citationLink is a quote of an earlier answer, [Assistant quote](...),
	// whose link holds the quoted words and the person's comment on them.
	citationLink = sync.OnceValue(func() *regexp.Regexp {
		return regexp.MustCompile(`\[Assistant quote\]\((t3-citation://v1/[^\s)]+)\)`)
	})
	// envelope is the block T3 appends for the provider, with what each
	// context reference names, should a stored text carry one.
	envelope = sync.OnceValue(func() *regexp.Regexp {
		return regexp.MustCompile(`(?s)\s*<t3_context version="1">.*</t3_context>\s*$`)
	})
	contextKind = sync.OnceValue(func() *regexp.Regexp { return regexp.MustCompile(`^[a-z][a-z0-9-]{0,39}$`) })
	contextID   = sync.OnceValue(func() *regexp.Regexp { return regexp.MustCompile(`^[A-Za-z0-9_-]{1,128}$`) })
)

// typed returns what a person typed of a message's stored text, as far as
// the text shows it: the text without T3's machine strings (SM 2.2 rule 9).
// They are a leading Ultrathink: line, which T3's web client puts before
// the text at that effort; the placeholder T3 stores for a message with
// attachments and no text; and the tokens of its context references, with
// the block T3 appends for them. A quote of an earlier answer counts as the
// person's comment on it, since the quoted words were the agent's. The
// result is trimmed. It is the caller's to measure and drop.
func typed(text string) string {
	text = unthink(text)
	if text == attachmentOnly {
		return ""
	}
	text = envelope().ReplaceAllString(text, "")
	text = contextLink().ReplaceAllStringFunc(text, func(link string) string {
		m := contextLink().FindStringSubmatch(link)
		if !isContextRef(m[1], m[2]) {
			return link
		}
		return ""
	})
	text = citationLink().ReplaceAllStringFunc(text, func(link string) string {
		m := citationLink().FindStringSubmatch(link)
		u, err := url.Parse(m[1])
		if err != nil {
			return ""
		}
		return u.Query().Get("comment")
	})
	return strings.TrimSpace(text)
}

// unthink returns text trimmed, and without a leading Ultrathink: line, in
// any case, with nothing else on it.
func unthink(text string) string {
	text = strings.TrimSpace(text)
	if len(text) < len(ultrathink) || !strings.EqualFold(text[:len(ultrathink)], ultrathink) {
		return text
	}
	line, after, found := strings.Cut(text[len(ultrathink):], "\n")
	switch {
	case strings.TrimSpace(line) != "":
		return text
	case !found:
		return ""
	}
	return strings.TrimSpace(after)
}

// OpensAsGuest reports whether text opens as T3 Code opens a guest's
// message when it sends it on: "Message from guest ", after any white space
// and a leading Ultrathink: line, as the reader reads a message's text. The
// reader's SQL makes the same test inside sqlite3 (sql.go's opensAsGuest),
// and the agent makes it on every copy of a prompt from any harness, so a
// prompt that carries a guest's words keeps none of them whatever path it
// took (DESIGN 5.1).
func OpensAsGuest(text string) bool { return strings.HasPrefix(unthink(text), guestOpening) }

// isContextRef reports whether label and ref make a context reference T3
// itself would read as one: a label within its limit, and a reference of a
// kind and an id of T3's forms, within its limit.
func isContextRef(label, ref string) bool {
	if utf8.RuneCountInString(label) > maxContextLabel {
		return false
	}
	rest := strings.TrimPrefix(ref, contextPrefix)
	if len(rest) > maxContextRef {
		return false
	}
	kind, id, ok := strings.Cut(rest, "/")
	return ok && !strings.Contains(id, "/") && contextKind().MatchString(kind) && contextID().MatchString(id)
}

// measurement is all the reader keeps of a message's text (SM 1.2, 2.2):
// the length of what was typed, an upper bound since T3 keeps no paste
// flag, and the go-ahead test.
type measurement struct {
	lenChars  int
	lowEffort bool
}

// measure measures a message's text, of which read is the start the text
// query read and total its length in characters, as SQLite counts them.
// Characters past the start count by number, and a text that long is no
// go-ahead.
func measure(read string, total int) measurement {
	t := typed(read)
	n := utf8.RuneCountInString(t)
	cut := total - utf8.RuneCountInString(read)
	if cut > 0 {
		return measurement{lenChars: n + cut}
	}
	return measurement{lenChars: n, lowEffort: score.LowEffort(t)}
}

// class is SM 2.2's class of the app's own text, from its length and the
// go-ahead test, as far as the reader can tell it. The reader can't know
// whether the agent's last output asked a question, which makes a message
// an answer, so the ledger writer, which follows each session's questions,
// gives the record its f.pc (ledger-v1 10.7) by the same rule with the
// session's questions (score.RelayClass, score.Questions).
func (m measurement) class(t *params.Table) event.PromptClass {
	return score.RelayClass(t, &event.RelayMessage{LenChars: m.lenChars, LowEffort: m.lowEffort}, false)
}
