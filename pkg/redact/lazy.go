// SPDX-License-Identifier: AGPL-3.0-or-later

package redact

import (
	"regexp"
	"sync"
)

// lazyRE is a regular expression compiled the first time it is used. The
// package's patterns are compiled that way rather than when the package is
// initialized, because every pairtime process links this package, and
// `pairtime hook` starts on every harness hook without ever redacting:
// compiling them all took about a millisecond of each hook's start on
// windows-latest (DESIGN 9.7's hook gate; footprint.yml).
type lazyRE struct {
	expr string
	once sync.Once
	re   *regexp.Regexp
}

// lazy returns expr, to be compiled with regexp.MustCompile on first use.
func lazy(expr string) *lazyRE { return &lazyRE{expr: expr} }

// get returns the compiled expression.
func (l *lazyRE) get() *regexp.Regexp {
	l.once.Do(func() { l.re = regexp.MustCompile(l.expr) })
	return l.re
}
