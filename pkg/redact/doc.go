// SPDX-License-Identifier: AGPL-3.0-or-later

// Package redact finds the parts of a prompt or an agent message that must
// not leave the device: the teen's known values, secrets, and personal data
// with a checksum. It's the pipeline of DESIGN 7.5 step 6 and 8.7, after
// docs/research/trust-and-privacy.md (TP) B4.
//
// Redaction removes spans and never rewrites text. [Redactor.Find] returns
// byte ranges of the original text, each with a [Type] marker, and leaves
// the text alone, so a content commitment still proves every byte that is
// kept (TP B6). The ledger maps the spans onto its tokens for a reveal
// (WP-LG4); [Render] draws them as markers for a person to read.
package redact
