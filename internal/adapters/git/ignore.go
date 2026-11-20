// SPDX-License-Identifier: AGPL-3.0-or-later

package git

import (
	"fmt"
	"os"
	"path"
	"path/filepath"
	"strings"
)

// IgnoreFile is the file that keeps the folder it is in, and every folder
// below it, out of pairtime (DESIGN 8.8). Its contents don't matter.
const IgnoreFile = ".pairtime-ignore"

// Excludes are the exclude patterns from config.toml (DESIGN 3.5, 8.8). A
// folder matching one is out of pairtime, and so is every folder below it.
//
// A pattern is a path whose elements may use path.Match's wildcards: '*'
// and '?' stay within one element, "[...]" is a character class, and an
// element of "**" matches any number of elements, none included. "~" at
// the start is the home folder. A pattern with no separator, such as
// "school-*", matches a folder of that name anywhere. A pattern with a
// separator that doesn't start at a root or at "~", such as "work/secret",
// matches that run of folders anywhere. Backslashes count as separators on
// Windows, so they can't escape a wildcard there. Matching ignores case
// where the OS does.
type Excludes struct {
	pats []exclude
}

type exclude struct {
	elems    []string
	anywhere bool // the pattern may start at any element of a path
}

// NewExcludes compiles patterns. home expands a leading "~". Patterns
// that don't compile are left out, and problems says which and why.
func NewExcludes(patterns []string, home string) (ex Excludes, problems []string) {
	for _, raw := range patterns {
		p := strings.TrimSpace(raw)
		if p == "" {
			continue
		}
		if p == "~" || strings.HasPrefix(p, "~/") || (os.PathSeparator == '\\' && strings.HasPrefix(p, `~\`)) {
			if home == "" {
				problems = append(problems, fmt.Sprintf("exclude pattern %q starts with ~, but the home folder is unknown", raw))
				continue
			}
			p = globLiteral(home) + p[1:]
		}
		if os.PathSeparator == '\\' {
			p = strings.ReplaceAll(p, `\`, "/")
		}
		if caseInsensitive {
			p = strings.ToLower(p)
		}
		var e exclude
		switch {
		case !strings.Contains(p, "/"):
			e = exclude{elems: []string{p}, anywhere: true}
		case filepath.IsAbs(filepath.FromSlash(p)) || strings.HasPrefix(p, "/"):
			e = exclude{elems: splitPath(p)}
		default:
			e = exclude{elems: splitPath(p), anywhere: true}
		}
		bad := false
		for _, el := range e.elems {
			if _, err := path.Match(el, ""); err != nil {
				problems = append(problems, fmt.Sprintf("exclude pattern %q doesn't compile: %v", raw, err))
				bad = true
				break
			}
		}
		if !bad && len(e.elems) > 0 {
			ex.pats = append(ex.pats, e)
		}
	}
	return ex, problems
}

// Match reports whether dir, an absolute path, or a folder above it
// matches a pattern.
func (ex Excludes) Match(dir string) bool {
	if len(ex.pats) == 0 {
		return false
	}
	elems := splitPath(foldPath(dir))
	for _, p := range ex.pats {
		if p.anywhere {
			for i := range elems {
				if matchPrefix(p.elems, elems[i:]) {
					return true
				}
			}
		} else if matchPrefix(p.elems, elems) {
			return true
		}
	}
	return false
}

// splitPath splits a slash-separated absolute path into elements. The
// root is the first element: "" for "/" and "c:" for "c:/"; a UNC path
// keeps "//server" as one element.
func splitPath(p string) []string {
	p = strings.TrimSuffix(p, "/")
	if strings.HasPrefix(p, "//") {
		rest := strings.TrimPrefix(p, "//")
		server, tail, _ := strings.Cut(rest, "/")
		elems := []string{"//" + server}
		if tail != "" {
			elems = append(elems, strings.Split(tail, "/")...)
		}
		return elems
	}
	if p == "" {
		return []string{""}
	}
	return strings.Split(p, "/")
}

// matchPrefix reports whether pat matches the first k elements of elems,
// for some k, which is what makes a pattern cover the folders below the
// ones it names. Each pair of positions is tried once, so a pattern with
// many "**" elements costs its length times the path's, squared at most.
func matchPrefix(pat, elems []string) bool {
	memo := make(map[[2]int]bool)
	var match func(i, j int) bool
	match = func(i, j int) bool {
		if i == len(pat) {
			return true
		}
		k := [2]int{i, j}
		if v, ok := memo[k]; ok {
			return v
		}
		ok := false
		if pat[i] == "**" {
			for jj := j; jj <= len(elems) && !ok; jj++ {
				ok = match(i+1, jj)
			}
		} else if j < len(elems) {
			if m, err := path.Match(pat[i], elems[j]); err == nil && m {
				ok = match(i+1, j+1)
			}
		}
		memo[k] = ok
		return ok
	}
	return match(0, 0)
}

// globLiteral quotes the wildcards in s, so path.Match reads them as
// plain characters: a home folder can be named "dev[1]". A backslash is a
// separator on Windows and git's escape elsewhere.
func globLiteral(s string) string {
	var b strings.Builder
	for _, c := range s {
		switch {
		case c == '*' || c == '?' || c == '[':
			b.WriteByte('[')
			b.WriteRune(c)
			b.WriteByte(']')
		case c == '\\' && os.PathSeparator != '\\':
			b.WriteString(`\\`)
		default:
			b.WriteRune(c)
		}
	}
	return b.String()
}

// ignoreFileAbove returns the folder that holds a .pairtime-ignore, looking
// in dir and every folder above it, or "" when none does. It costs one
// stat per folder.
func ignoreFileAbove(dir string) string {
	for d := filepath.Clean(dir); ; {
		if _, err := os.Lstat(filepath.Join(d, IgnoreFile)); err == nil {
			return d
		}
		parent := filepath.Dir(d)
		if parent == d {
			return ""
		}
		d = parent
	}
}
