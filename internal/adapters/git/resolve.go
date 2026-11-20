// SPDX-License-Identifier: AGPL-3.0-or-later

package git

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"time"
)

// Kind says how a Location's project was identified.
type Kind string

const (
	// KindRepo is a git repo with commits: its project is its sorted set
	// of root commits (SP 4.2).
	KindRepo Kind = "repo"
	// KindUnborn is a repo with no commit yet. Until its first commit it
	// is identified by its folder, the same way the folder was before
	// `git init`, and that key becomes an alias of the root-commit key.
	KindUnborn Kind = "unborn"
	// KindShallow is a shallow clone. Its oldest commits are cut off, so
	// what git lists as roots are just where the clone stops, and they
	// change with every deeper fetch. It is identified by its folder until
	// it is unshallowed.
	KindShallow Kind = "shallow"
	// KindFolder is a folder outside git, or in a repo git refuses to
	// open (ErrUnsafeRepo): the folder itself is the project.
	KindFolder Kind = "folder"
)

// Location is what a folder resolves to. Everything but Project and Kind
// is local: paths and root commits never leave the device, apart from the
// root commits the uploader sends once to register a project (DESIGN 4.3).
type Location struct {
	// Project is the project key, an opaque key under the device's project
	// secret.
	Project string
	Kind    Kind
	// Dir is the folder the project is: the worktree's top level, the
	// main worktree's for a folder inside a git directory, or the folder
	// itself when there is no git.
	Dir string
	// GitDir is the worktree's own git directory, the one with its HEAD,
	// index and logs/HEAD: .git, .git/worktrees/<name> for a linked
	// worktree, .git/modules/<path> for a submodule. Empty without git.
	GitDir string
	// CommonDir is the git directory the worktrees of one repo share,
	// with the refs and objects.
	CommonDir string
	Bare      bool
	// Linked is a linked worktree (`git worktree add`).
	Linked bool
	// Submodule is a submodule's worktree. A submodule is a project of its
	// own, since it has root commits of its own.
	Submodule bool
	// Roots are the repo's root commits, sorted. Empty unless Kind is
	// KindRepo.
	Roots []string
}

// ErrIgnored means the folder is out of pairtime: it or a folder above it
// holds a .pairtime-ignore, or it matches an exclude pattern (DESIGN 8.8).
var ErrIgnored = errors.New("the folder is excluded from pairtime")

// ResolverOptions configure a Resolver.
type ResolverOptions struct {
	// Git runs git. Nil means a Runner for "git" on PATH.
	Git *Runner
	// Secret is the project secret, from ProjectSecret. Required.
	Secret []byte
	// Excludes keep folders out, with .pairtime-ignore files.
	Excludes Excludes
	// Now is the clock. Nil means time.Now.
	Now func() time.Time
	// Recheck is how long a resolved folder is trusted before its
	// .pairtime-ignore files and git directory are looked at again. Zero
	// means DefaultRecheck.
	Recheck time.Duration
}

// DefaultRecheck is how often a resolved folder is looked at again, with
// a few stats, never a git call.
const DefaultRecheck = time.Minute

// maxCachedDirs bounds the folder cache. Every harness session and editor
// project adds one folder, so it fills only on a machine with thousands
// of projects, and then it starts afresh.
const maxCachedDirs = 4096

// Resolver turns folders into Locations. It runs git once per folder and
// once per repo, and keeps the answers. It is safe for concurrent use.
type Resolver struct {
	git     *Runner
	secret  []byte
	ex      Excludes
	now     func() time.Time
	recheck time.Duration

	mu       sync.Mutex
	dirs     map[string]dirEntry // by foldPath of the folder
	repos    map[string]*RepoProject
	byRoot   map[string]string // root commit to project key
	inflight map[string]*flight
}

type dirEntry struct {
	loc     Location
	err     error
	checked time.Time
	// gitDir is the git directory's FileInfo when it was resolved, so a
	// repo deleted and made again at the same path is looked at afresh.
	gitDir os.FileInfo
}

type flight struct {
	done chan struct{}
	loc  Location
	err  error
}

// ProjectMap is the part of the resolver that state.json keeps (DESIGN
// 3.5), so a project keeps its key after a restart even when its root
// commits have changed since it was first seen. It holds local paths and
// root commits, so it never leaves the device.
type ProjectMap struct {
	Repos []RepoProject `json:"repos,omitempty"`
}

// RepoProject is one repo's identity, under its common git directory.
type RepoProject struct {
	CommonDir string   `json:"common_dir"`
	Key       string   `json:"key"`
	Kind      Kind     `json:"kind"`
	Roots     []string `json:"roots,omitempty"`
	// Aliases are keys the project had before: the folder key of a repo
	// before its first commit or before it was unshallowed.
	Aliases []string `json:"aliases,omitempty"`
}

// NewResolver returns a resolver that starts from m.
func NewResolver(opts ResolverOptions, m ProjectMap) (*Resolver, error) {
	if len(opts.Secret) < 16 {
		return nil, errors.New("the git adapter needs a project secret of at least 16 bytes")
	}
	r := &Resolver{
		git:      opts.Git,
		secret:   slices.Clone(opts.Secret),
		ex:       opts.Excludes,
		now:      opts.Now,
		recheck:  opts.Recheck,
		dirs:     make(map[string]dirEntry),
		repos:    make(map[string]*RepoProject),
		byRoot:   make(map[string]string),
		inflight: make(map[string]*flight),
	}
	if r.git == nil {
		r.git = &Runner{}
	}
	if r.now == nil {
		r.now = time.Now
	}
	if r.recheck <= 0 {
		r.recheck = DefaultRecheck
	}
	for _, p := range m.Repos {
		p.Roots = slices.Clone(p.Roots)
		p.Aliases = slices.Clone(p.Aliases)
		r.repos[foldPath(p.CommonDir)] = &p
	}
	// A root can be listed by several repos: a project that gained one,
	// through a merge of another project's history, lists it beside the
	// project it came from. Each root goes to the project its own roots
	// key first, then to the first that lists it, in the map's order.
	for _, owner := range []bool{true, false} {
		for _, p := range m.Repos {
			if len(p.Roots) == 0 || (rootsKey(r.secret, p.Roots) == p.Key) != owner {
				continue
			}
			for _, root := range p.Roots {
				if _, taken := r.byRoot[root]; !taken {
					r.byRoot[root] = p.Key
				}
			}
		}
	}
	return r, nil
}

// Map returns the project map to keep in state.json, sorted by common dir.
func (r *Resolver) Map() ProjectMap {
	r.mu.Lock()
	defer r.mu.Unlock()
	var m ProjectMap
	for _, p := range r.repos {
		c := *p
		c.Roots = slices.Clone(p.Roots)
		c.Aliases = slices.Clone(p.Aliases)
		m.Repos = append(m.Repos, c)
	}
	slices.SortFunc(m.Repos, func(a, b RepoProject) int { return strings.Compare(a.CommonDir, b.CommonDir) })
	return m
}

// Aliases returns the keys project had before its current one, sorted.
// Several repos can share a key, as a fresh clone shares the original's,
// and each brings the aliases it had.
func (r *Resolver) Aliases(project string) []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	var out []string
	for _, p := range r.repos {
		if p.Key == project {
			for _, a := range p.Aliases {
				out = appendNew(out, a)
			}
		}
	}
	slices.Sort(out)
	return out
}

// Resolve returns the Location of dir, a harness session's cwd or an
// editor's project folder. It returns ErrIgnored for a folder that is out
// of pairtime. A folder git can't open, or that git isn't installed to
// open, resolves to a KindFolder location.
func (r *Resolver) Resolve(ctx context.Context, dir string) (Location, error) {
	abs, err := filepath.Abs(dir)
	if err != nil {
		return Location{}, err
	}
	key := foldPath(abs)
	now := r.now()

	r.mu.Lock()
	e, cached := r.dirs[key]
	r.mu.Unlock()
	if cached {
		if now.Sub(e.checked) < r.recheck {
			return cloneLoc(e.loc), e.err
		}
		if r.stillValid(abs, e) {
			r.mu.Lock()
			e.checked = now
			r.dirs[key] = e
			r.mu.Unlock()
			return cloneLoc(e.loc), e.err
		}
	}

	loc, err := r.resolveOnce(ctx, key, abs)
	if err != nil && !errors.Is(err, ErrIgnored) {
		return Location{}, err // a missing folder or a git that timed out: try again next time
	}
	entry := dirEntry{loc: loc, err: err, checked: now}
	if loc.GitDir != "" {
		if fi, err := os.Stat(loc.GitDir); err == nil {
			// On Windows a FileInfo reads its file id lazily, from its path,
			// at the first SameFile. Reading it now keeps it this folder's,
			// whatever the path names later.
			_ = os.SameFile(fi, fi)
			entry.gitDir = fi
		}
	}
	r.mu.Lock()
	if len(r.dirs) >= maxCachedDirs {
		clear(r.dirs)
	}
	r.dirs[key] = entry
	r.mu.Unlock()
	return cloneLoc(loc), err
}

// Forget drops what the resolver remembers about folders, so the next
// Resolve of each looks again. The project map stays.
func (r *Resolver) Forget() {
	r.mu.Lock()
	clear(r.dirs)
	r.mu.Unlock()
}

// forgetRepo drops the folders that resolved to the repo whose common
// directory is commonDir.
func (r *Resolver) forgetRepo(commonDir string) {
	ck := foldPath(commonDir)
	r.mu.Lock()
	for k, e := range r.dirs {
		if e.loc.CommonDir != "" && foldPath(e.loc.CommonDir) == ck {
			delete(r.dirs, k)
		}
	}
	r.mu.Unlock()
}

// stillValid looks again, with stats only, at what a cached answer rests
// on: no .pairtime-ignore or exclude pattern has appeared, a repo's git
// directory is still the one that was resolved, and a folder without git
// hasn't gained one.
func (r *Resolver) stillValid(abs string, e dirEntry) bool {
	if errors.Is(e.err, ErrIgnored) {
		return r.ignored(abs)
	}
	if e.err != nil || r.ignored(abs) || r.ignoredLoc(e.loc) {
		return false
	}
	if e.loc.GitDir != "" {
		if _, err := os.Stat(filepath.Join(e.loc.GitDir, "HEAD")); err != nil {
			return false
		}
		fi, err := os.Stat(e.loc.GitDir)
		return err == nil && (e.gitDir == nil || os.SameFile(fi, e.gitDir))
	}
	return gitAbove(abs) == ""
}

// gitAbove returns the folder at or above dir that has a .git entry, or "".
func gitAbove(dir string) string {
	for d := filepath.Clean(dir); ; {
		if _, err := os.Lstat(filepath.Join(d, ".git")); err == nil {
			return d
		}
		parent := filepath.Dir(d)
		if parent == d {
			return ""
		}
		d = parent
	}
}

// ignored reports whether dir is out of pairtime.
func (r *Resolver) ignored(dir string) bool {
	return r.ex.Match(dir) || ignoreFileAbove(dir) != ""
}

// ignoredLoc reports whether a resolved location is out of pairtime now:
// its folder is, or, for a linked worktree, its repo's main worktree is.
// Agent tools put worktrees far from the repo (~/.cursor/worktrees/...,
// ../essay-feature), where the main folder's .pairtime-ignore and an
// exclude pattern for it don't reach. It costs a few stats, no git call.
func (r *Resolver) ignoredLoc(loc Location) bool {
	if loc.Dir != "" && r.ignored(loc.Dir) {
		return true
	}
	return loc.Linked && loc.CommonDir != "" && r.ignored(mainWorktree(loc.CommonDir))
}

// resolveOnce resolves abs, sharing the work with concurrent callers.
func (r *Resolver) resolveOnce(ctx context.Context, key, abs string) (Location, error) {
	r.mu.Lock()
	if f, ok := r.inflight[key]; ok {
		r.mu.Unlock()
		select {
		case <-f.done:
			return cloneLoc(f.loc), f.err
		case <-ctx.Done():
			return Location{}, ctx.Err()
		}
	}
	f := &flight{done: make(chan struct{})}
	r.inflight[key] = f
	r.mu.Unlock()

	f.loc, f.err = r.resolve(ctx, abs)
	r.mu.Lock()
	delete(r.inflight, key)
	r.mu.Unlock()
	close(f.done)
	return f.loc, f.err
}

func (r *Resolver) resolve(ctx context.Context, abs string) (Location, error) {
	if r.ignored(abs) {
		return Location{}, ErrIgnored
	}
	if fi, err := os.Stat(abs); err != nil {
		return Location{}, err
	} else if !fi.IsDir() {
		return Location{}, fmt.Errorf("%s isn't a folder", abs)
	}
	info, err := r.revParse(ctx, abs)
	switch {
	case errors.Is(err, ErrNotRepo), errors.Is(err, ErrUnsafeRepo), errors.Is(err, ErrNoGit):
		// The key comes from the folder's canonical path, the form git
		// prints for a top level: symlinks resolved, and on Windows short
		// names expanded. So `git init` in the folder keeps its key.
		dir := canonicalDir(abs)
		if r.ignored(dir) {
			return Location{}, ErrIgnored
		}
		return Location{Project: folderKey(r.secret, dir), Kind: KindFolder, Dir: dir}, nil
	case err != nil:
		return Location{}, err
	}
	loc := Location{
		GitDir:    info.gitDir,
		CommonDir: info.commonDir,
		Bare:      info.bare,
		Linked:    foldPath(info.gitDir) != foldPath(info.commonDir),
		Submodule: info.superproject != "",
		Dir:       info.toplevel,
	}
	if loc.Dir == "" {
		loc.Dir = mainWorktree(info.commonDir)
	}
	if r.ignoredLoc(loc) {
		return Location{}, ErrIgnored
	}
	p, err := r.identify(ctx, abs, info, loc.Dir)
	if err != nil {
		return Location{}, err
	}
	loc.Project, loc.Kind, loc.Roots = p.Key, p.Kind, slices.Clone(p.Roots)
	return loc, nil
}

// mainWorktree guesses the main worktree's folder from a common git
// directory: the folder holding .git, or the git directory itself for a
// bare repo or a separate git directory.
func mainWorktree(commonDir string) string {
	if filepath.Base(commonDir) == ".git" {
		return filepath.Dir(commonDir)
	}
	return commonDir
}

type revParseInfo struct {
	gitDir, commonDir string
	bare, inside      bool
	shallow           bool
	toplevel          string
	superproject      string
}

// revParse runs the one rev-parse that says where a folder's repo is.
// Inside a git directory or a bare repo, --show-toplevel fails after the
// other answers are printed, and those answers are still good.
func (r *Resolver) revParse(ctx context.Context, abs string) (revParseInfo, error) {
	out, err := r.git.Run(ctx, abs, "rev-parse",
		"--absolute-git-dir", "--git-common-dir", "--is-bare-repository",
		"--is-inside-work-tree", "--is-shallow-repository",
		"--show-toplevel", "--show-superproject-working-tree")
	var exit *ExitError
	if err != nil && !(errors.As(err, &exit) && strings.Contains(exit.Stderr, "must be run in a work tree")) {
		return revParseInfo{}, err
	}
	// --absolute-git-dir and --show-toplevel are canonical: symlinks
	// resolved, like macOS's /var to /private/var, and on Windows long names
	// instead of 8.3 ones like RUNNER~1. A main worktree's common dir comes
	// out relative to git's working directory, which git also reads from
	// the OS in canonical form. abs is the folder as the caller spelled it,
	// so the common dir is joined onto abs's canonical form and then takes
	// the git dir's spelling when the two are one folder. Repos are keyed
	// by their common dir, so every path to a repo has to give one spelling.
	info, err := parseRevParse(canonicalDir(abs), string(out))
	if err != nil {
		return revParseInfo{}, err
	}
	info.commonDir = commonSpelling(info.commonDir, info.gitDir)
	return info, nil
}

// canonicalDir returns dir with symlinks resolved and, on Windows, short
// names expanded, or dir itself when that fails.
func canonicalDir(dir string) string {
	if canon, err := filepath.EvalSymlinks(dir); err == nil {
		return canon
	}
	return dir
}

// commonSpelling returns commonDir as gitDir spells it when both are one
// folder, as in a main worktree, and otherwise in canonical form.
func commonSpelling(commonDir, gitDir string) string {
	if foldPath(commonDir) == foldPath(gitDir) {
		return gitDir
	}
	if a, err := os.Stat(commonDir); err == nil {
		if b, err := os.Stat(gitDir); err == nil && os.SameFile(a, b) {
			return gitDir
		}
	}
	return canonicalDir(commonDir)
}

// parseRevParse reads revParse's output: five lines always, then the top
// level and the superproject's top level when there are any. A relative
// common dir is joined onto abs, the folder git ran in.
func parseRevParse(abs, out string) (revParseInfo, error) {
	lines := strings.Split(strings.TrimRight(out, "\r\n"), "\n")
	for i := range lines {
		lines[i] = strings.TrimRight(lines[i], "\r")
	}
	if len(lines) < 5 {
		return revParseInfo{}, fmt.Errorf("unexpected rev-parse output: %d lines", len(lines))
	}
	info := revParseInfo{
		gitDir:  filepath.Clean(filepath.FromSlash(lines[0])),
		bare:    lines[2] == "true",
		inside:  lines[3] == "true",
		shallow: lines[4] == "true",
	}
	common := filepath.FromSlash(lines[1])
	if !filepath.IsAbs(common) {
		common = filepath.Join(abs, common)
	}
	info.commonDir = filepath.Clean(common)
	if len(lines) > 5 && info.inside {
		info.toplevel = filepath.Clean(filepath.FromSlash(lines[5]))
	}
	if len(lines) > 6 && lines[6] != "" {
		info.superproject = filepath.Clean(filepath.FromSlash(lines[6]))
	}
	if !filepath.IsAbs(info.gitDir) {
		return revParseInfo{}, fmt.Errorf("rev-parse gave a relative git directory")
	}
	return info, nil
}

// identify returns the identity of the repo whose common directory info
// names, working it out on first sight. dir is the folder that identifies
// it while it has no usable root commits.
func (r *Resolver) identify(ctx context.Context, abs string, info revParseInfo, dir string) (RepoProject, error) {
	ck := foldPath(info.commonDir)
	r.mu.Lock()
	known, ok := r.repos[ck]
	var was RepoProject
	if ok {
		was = *known
		was.Roots = slices.Clone(known.Roots)
	}
	r.mu.Unlock()
	if ok && was.Kind == KindShallow && info.shallow {
		return was, nil
	}
	replaced := false
	if ok && was.Kind == KindRepo {
		// Identities are kept by common dir path, and the repo there can be
		// another one now: the teen deleted a project and made a new one by
		// the same name. One of the known roots still being a commit here
		// says it is the same repo.
		if r.holdsAny(ctx, abs, was.Roots) {
			return was, nil
		}
		replaced = true
	}

	next := RepoProject{CommonDir: info.commonDir}
	if info.shallow {
		next.Kind, next.Key = KindShallow, folderKey(r.secret, dir)
	} else {
		roots, err := r.roots(ctx, abs)
		switch {
		case errors.Is(err, errUnborn):
			next.Kind, next.Key = KindUnborn, folderKey(r.secret, dir)
		case err != nil:
			return RepoProject{}, err
		default:
			next.Kind, next.Roots = KindRepo, roots
		}
	}

	// A history that joins two known projects, through git subtree add or
	// a merge of unrelated histories, stays with the project of its
	// first-parent chain.
	var firstParent string
	if next.Kind == KindRepo {
		r.mu.Lock()
		several := len(r.keysOf(next.Roots)) > 1
		r.mu.Unlock()
		if several {
			firstParent = r.firstParentRoot(ctx, abs)
		}
	}

	r.mu.Lock()
	defer r.mu.Unlock()
	if cur, ok := r.repos[ck]; ok && replaced && cur.Key == was.Key {
		delete(r.repos, ck) // another repo's identity, at this path before
	}
	if known, ok := r.repos[ck]; ok {
		if known.Kind == KindRepo || known.Kind == next.Kind {
			return *known, nil // someone else got there first, or nothing changed
		}
		next.Aliases = slices.Clone(known.Aliases)
		next.Aliases = appendNew(next.Aliases, known.Key)
	}
	if next.Kind == KindRepo {
		next.Key = r.keyForRoots(next.Roots, firstParent)
		for _, root := range next.Roots {
			if _, taken := r.byRoot[root]; !taken {
				r.byRoot[root] = next.Key // a root another project owns stays its
			}
		}
		if next.Key != rootsKey(r.secret, next.Roots) {
			// The roots overlap a project seen before, which keeps its key
			// and gains these roots.
			for _, p := range r.repos {
				if p.Key == next.Key {
					p.Roots = mergeSorted(p.Roots, next.Roots)
				}
			}
		}
	}
	next.Aliases = slices.DeleteFunc(next.Aliases, func(a string) bool { return a == next.Key })
	stored := next
	r.repos[ck] = &stored
	return next, nil
}

// keyForRoots returns the key of the project that already owns one of
// roots, or the key of roots themselves. So a repo whose history gained a
// root, say through `git subtree add`, stays one project across a fresh
// clone, which lists both roots. When the roots belong to several known
// projects, the one owning firstParent, the first-parent chain's root,
// wins, then the one owning the most roots, then the first. Called with
// r.mu held.
func (r *Resolver) keyForRoots(roots []string, firstParent string) string {
	keys := r.keysOf(roots)
	switch len(keys) {
	case 0:
		return rootsKey(r.secret, roots)
	case 1:
		return keys[0]
	}
	if k, ok := r.byRoot[firstParent]; ok && slices.Contains(keys, k) {
		return k
	}
	count := make(map[string]int)
	for _, root := range roots {
		count[r.byRoot[root]]++
	}
	best := keys[0]
	for _, k := range keys[1:] {
		if count[k] > count[best] {
			best = k
		}
	}
	return best
}

// keysOf returns the known projects that own any of roots, in the order
// of roots. Called with r.mu held.
func (r *Resolver) keysOf(roots []string) []string {
	var keys []string
	for _, root := range roots {
		if k, ok := r.byRoot[root]; ok {
			keys = appendNew(keys, k)
		}
	}
	return keys
}

// firstParentRoot returns the root of HEAD's first-parent chain, or "".
func (r *Resolver) firstParentRoot(ctx context.Context, worktree string) string {
	cctx, cancel := context.WithTimeout(ctx, rootsTimeout)
	defer cancel()
	out, err := r.git.Run(cctx, worktree, "rev-list", "--max-parents=0", "--first-parent", "HEAD")
	if err != nil {
		return ""
	}
	ids, err := parseIDs(string(out))
	if err != nil || len(ids) != 1 {
		return ""
	}
	return ids[0]
}

// holdsAny reports whether any of roots is a commit in the repo at dir, in
// one git call. When that can't be told, or asking could fetch a missing
// object from a partial clone's remote, it says yes, and the identity
// stays.
func (r *Resolver) holdsAny(ctx context.Context, dir string, roots []string) bool {
	if len(roots) == 0 {
		return true
	}
	if v, err := r.git.Version(); err == nil && !HonorsNoLazyFetch(v) && partialClone(ctx, r.git, dir) {
		return true
	}
	var in strings.Builder
	for _, root := range roots {
		in.WriteString(root)
		in.WriteString("\n")
	}
	out, err := r.git.RunInput(ctx, dir, []byte(in.String()), "cat-file", "--batch-check")
	if err != nil {
		return true
	}
	for _, line := range strings.Split(string(out), "\n") {
		if f := strings.Fields(line); len(f) >= 2 && f[1] == "commit" {
			return true
		}
	}
	return false
}

// Rekey looks again at the identity of loc's repo. The watcher calls it
// when a repo with no commit makes its first one, when a shallow clone
// loses its shallow file, and when an amend or a rebase rewrote one of a
// repo's root commits. It returns the location with the new key, and the
// old key is in Aliases; a repo whose root was rewritten keeps its key,
// with its new roots.
func (r *Resolver) Rekey(ctx context.Context, loc Location) (Location, error) {
	r.forgetRepo(loc.CommonDir)
	if loc.Kind == KindRepo {
		if err := r.reroot(ctx, loc); err != nil {
			return Location{}, err
		}
	}
	return r.Resolve(ctx, loc.Dir)
}

// reroot gives a known repo the roots its history has now, keeping its
// key, and points the new roots at that key, so a fresh clone of the
// rewritten history joins the project. The replaced roots keep pointing
// at it too, for clones made before the rewrite.
func (r *Resolver) reroot(ctx context.Context, loc Location) error {
	roots, err := r.roots(ctx, loc.Dir)
	if err != nil {
		return err
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	p, ok := r.repos[foldPath(loc.CommonDir)]
	if !ok || p.Kind != KindRepo {
		return nil
	}
	p.Roots = roots
	for _, root := range roots {
		if _, taken := r.byRoot[root]; !taken {
			r.byRoot[root] = p.Key
		}
	}
	return nil
}

var errUnborn = errors.New("the repo has no commit yet")

// rootsTimeout caps the root-commit walk, which reads the whole history.
// It runs once per repo, so a repo the size of Linux's is still fine.
const rootsTimeout = 2 * time.Minute

// roots lists the root commits of HEAD's history, sorted. When the walk
// over the whole history takes too long, the root of the first-parent
// chain stands in, which is what a fresh clone also finds first.
func (r *Resolver) roots(ctx context.Context, worktree string) ([]string, error) {
	walk := func(args ...string) ([]string, error) {
		cctx, cancel := context.WithTimeout(ctx, rootsTimeout)
		defer cancel()
		out, err := r.git.Run(cctx, worktree, append([]string{"rev-list", "--max-parents=0"}, args...)...)
		if err != nil {
			return nil, err
		}
		return parseIDs(string(out))
	}
	roots, err := walk("HEAD")
	if errors.Is(err, context.DeadlineExceeded) {
		roots, err = walk("--first-parent", "HEAD")
	}
	if err != nil {
		if exitCode(err) == 128 && r.unborn(ctx, worktree) {
			return nil, errUnborn
		}
		return nil, err
	}
	if len(roots) == 0 {
		return nil, errUnborn
	}
	slices.Sort(roots)
	return slices.Compact(roots), nil
}

// unborn reports whether HEAD names no commit yet.
func (r *Resolver) unborn(ctx context.Context, worktree string) bool {
	_, err := r.git.Run(ctx, worktree, "rev-parse", "--quiet", "--verify", "HEAD^{commit}")
	return exitCode(err) == 1
}

// parseIDs reads one object id per line.
func parseIDs(out string) ([]string, error) {
	var ids []string
	for _, line := range strings.Split(out, "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		if !isObjectID(line) {
			return nil, fmt.Errorf("unexpected object id %q", line)
		}
		ids = append(ids, line)
	}
	return ids, nil
}

// isObjectID reports whether s is a full SHA-1 or SHA-256 object id in
// lowercase hex, the form pkg/event accepts.
func isObjectID(s string) bool {
	if len(s) != 40 && len(s) != 64 {
		return false
	}
	for i := 0; i < len(s); i++ {
		if c := s[i]; !('0' <= c && c <= '9' || 'a' <= c && c <= 'f') {
			return false
		}
	}
	return true
}

func cloneLoc(l Location) Location {
	l.Roots = slices.Clone(l.Roots)
	return l
}

func appendNew(list []string, v string) []string {
	if v == "" || slices.Contains(list, v) {
		return list
	}
	return append(list, v)
}

func mergeSorted(a, b []string) []string {
	out := append(slices.Clone(a), b...)
	slices.Sort(out)
	return slices.Compact(out)
}
