// SPDX-License-Identifier: AGPL-3.0-or-later

// Package redact finds the parts of a prompt or an agent message that must
// not leave the device: the teen's known values, secrets, and personal data
// that passes a checksum. It's the pipeline of DESIGN 7.5 step 6 and 8.7,
// after docs/research/trust-and-privacy.md (TP) B4.
//
// Redaction removes spans and never rewrites text. [Redactor.Find] returns
// byte ranges of the original text, each with a [Type] marker, and leaves
// the text alone, so a content commitment still proves every byte that is
// kept (TP B6). [Merge] turns the spans into a disjoint set, and [Render]
// draws them as markers for a person to read. For a ledger reveal, [Tokens]
// rounds the spans out to the whole tokens a reveal can remove, and
// [Type.Reveal] gives the coarse type the reveal shows: known, secret, pii,
// code or teen (docs/spec/ledger-v1.md 10.2 and 10.3, WP-LG3).
//
// # The pipeline
//
// [Redactor.Find] runs three steps over the original text, in this order:
//
//  1. Known values ([Known]): the OS account name, home directory,
//     hostname, git name and email, GitHub and HCA usernames, private repo
//     and folder names, and the teen's deny list. Matching ignores case,
//     needs a word edge where the value starts or ends with a letter or
//     digit, and also finds the spellings a raw capture file uses: JSON
//     escapes, once or twice for JSON inside JSON; URL escapes, as %20 or
//     in a query string; the name Claude Code gives a project folder,
//     with every character but an ASCII letter or digit turned into a
//     dash (C--Users-Maya-Chen-robot); and for a Windows home its
//     forward-slash, Git Bash and WSL forms and the path of a file URI,
//     VS Code's /c%3A/Users/... included. Right after a JSON escape such
//     as \n or \t is a word edge, since that's where an embedded line
//     starts. Short and generic names such as "user" or "app" are skipped
//     unless the teen put them on the deny list.
//  2. gitleaks' rules as data: rules/gitleaks.json.gz, converted from
//     gitleaks' default config at a pinned tag by rules/convert.py. The
//     engine follows gitleaks' detectRule: the keyword prefilter, the
//     secret group, the entropy floor and every allowlist.
//  3. Personal data with validation, ported from Presidio's pattern
//     recognizers: payment cards with Luhn, IBANs with mod-97 and the
//     country's format, US Social Security numbers with Presidio's
//     invalidation rules, UK National Insurance numbers, Aadhaar numbers
//     with Verhoeff, and email addresses. Like gitleaks' rules, they also
//     run over the JSON view of text with escapes, and an email address
//     never takes in the letter of an escape before it.
//
// [CodeBlocks] is separate: an agent message's fenced code blocks collapse
// to a marker with a line count by default, and the teen can add any back,
// so its spans are kept apart from Find's.
//
// # Where it differs from upstream
//
// From gitleaks: a rule limited to some file paths applies to all text,
// since a prompt has no path and a pasted .tf file is still a secret; a
// "gitleaks:allow" comment doesn't hide anything; nothing is base64- or
// percent-decoded first; and the rules also run over a copy of the text
// with sentence punctuation after a token, and every character outside
// ASCII, turned into spaces, because a token rule needs ASCII whitespace, a
// quote or a semicolon after the secret, and a key ends a sentence with a
// period, sits in smart quotes or before a no-break space, or runs into
// Chinese text with no space at all. Text with
// backslash escapes, which is what a raw JSON capture is, is read a second
// time through a copy with each escape blanked to spaces, and each \n to a
// line break, at the same offsets: there a key quoted as \"...\" is
// followed by a quote, a key at the start of an embedded line follows a
// line break, and a line-target allowlist sees that line rather than the
// whole JSON record. When two rules find the same secret, the span names a
// vendor's rule rather than generic-api-key. A span never ends inside an
// escape of a JSON string, so taking it out leaves the file parseable.
//
// Two secret rules are pairtime's own, run after gitleaks' over the same
// views: url-password takes the password out of a URL such as
// postgres://app:PASSWORD@localhost:5432, and authorization-header the
// credential out of an Authorization header with the Basic, Bearer or
// token scheme. gitleaks v8.30.1 finds a URL's password only after a
// keyword such as "password", and a header's credential only in a curl
// command, so a pasted DATABASE_URL or HTTP request kept both. And in a
// YAML document with kind: Secret, the value of every entry under data:
// and stringData: goes, under gitleaks' rule id kubernetes-secret-yaml:
// gitleaks' rule of that name matches one entry, so it took only the
// first value out of a pasted manifest, and it can't see stringData.
//
// From Presidio: a result needs a score of 0.4, Presidio's floor for a
// result with a context word, where Presidio keeps anything above zero; in
// practice only the very weak SSN shapes need a context word. The context
// window is the ten words before the match, standing in for five lemmas
// without stop words. Digits are ASCII digits. Email validation checks the
// top-level domain's shape instead of the public suffix list.
//
// TP B4 also lists phone numbers, IP and MAC addresses and dates of birth;
// none of them is in DESIGN 8.7's list, and phone numbers need
// libphonenumber, so they're left for later. GLiNER's name model is a v2
// option (DESIGN 8.7).
package redact
