// SPDX-License-Identifier: AGPL-3.0-or-later

package install

import (
	"bytes"
	"errors"
	"fmt"
	"path/filepath"
	"slices"
	"strings"

	"github.com/some-du6e/pairtime/internal/install/jsonc"
)

// ItemClaudeCode is the ID of the item that adds pairtime's Claude Code
// hooks.
const ItemClaudeCode = "hooks/claude_code"

// claudeHarness is Claude Code's harness name in `pairtime hook --harness`
// (DESIGN 3.4).
const claudeHarness = "claude_code"

// claudeHooks are DESIGN 5.1's Claude Code hook events, each with the
// matcher pairtime installs it under. "" is no matcher, which matches
// everything.
var claudeHooks = []struct{ event, matcher string }{
	{"SessionStart", ""},
	{"UserPromptSubmit", ""},
	{"Stop", ""},
	{"StopFailure", ""},
	{"Notification", ""},
	{"PermissionRequest", ""},
	{"PostToolUse", "Edit|Write|Bash"},
	{"SubagentStart", ""},
	{"SubagentStop", ""},
	{"SessionEnd", ""},
}

// ClaudeSettings returns the path of Claude Code's user settings, which
// the CLI, the VS Code extension and the Desktop app's Code tab all read,
// and Cursor and Devin read too (HS 3.1, 4.1, 8.1): settings.json in
// $CLAUDE_CONFIG_DIR when that is an absolute path, which moves Claude
// Code's whole folder (HS 3.2), and in ~/.claude otherwise.
func ClaudeSettings(env Env) string {
	if d := env.getenv("CLAUDE_CONFIG_DIR"); filepath.IsAbs(d) {
		return filepath.Join(d, "settings.json")
	}
	return filepath.Join(env.Home, ".claude", "settings.json")
}

// ClaudeCode returns the item that adds pairtime's hooks to Claude Code's
// user settings (DESIGN 5.1). Each hook is a group of its own in exec form,
// the binary's absolute path as "command" and the rest as "args", which
// every program that reads the file runs (HS 16):
//
//	{"hooks": [{"type": "command", "command": "<exe>",
//	  "args": ["hook", "--harness", "claude_code", "--event", "<event>"]}]}
//
// with "matcher": "Edit|Write|Bash" for PostToolUse. The groups go after
// the ones already there, which stay as they are: git-ai rewrites its own
// entries in the same file every day (HS 13.3). A pairtime hook from an
// older install, such as one with another binary path, is replaced.
func ClaudeCode(env Env) (Item, error) {
	if err := env.check(); err != nil {
		return Item{}, err
	}
	return Item{
		ID:      ItemClaudeCode,
		Summary: "Add pairtime's hooks to Claude Code's settings, after the hooks already there",
		Edits:   []Edit{&claudeEdit{path: ClaudeSettings(env), exe: env.Exe}},
	}, nil
}

// kindClaudeHooks is the kind of the edit that adds pairtime's Claude Code
// hooks.
const kindClaudeHooks = "claude_code.hooks"

type claudeEdit struct {
	path string
	exe  string
}

func (e *claudeEdit) Target() Target { return File(e.path) }

func (e *claudeEdit) Kind() string { return kindClaudeHooks }

// Apply takes out pairtime hooks that aren't the ones wanted, then adds
// each wanted hook that isn't there, one edit at a time, so that each
// works on freshly parsed bytes.
func (e *claudeEdit) Apply(cur Content) (Content, error) {
	var src []byte
	if cur.Exists {
		src = cur.Data
	}
	src, err := removeClaudeHooks(src, func(event string, group, h *jsonc.Value) bool {
		return isPairtimeHook(h) && !e.isWanted(event, group, h)
	})
	if err != nil {
		return Content{}, err
	}
	for _, hk := range claudeHooks {
		if src, err = e.ensure(src, hk.event, hk.matcher); err != nil {
			return Content{}, err
		}
	}
	return Content{Exists: true, Data: src, Mode: cur.Mode}, nil
}

// ensure returns src with pairtime's hook for event added, unless it is
// there already.
func (e *claudeEdit) ensure(src []byte, event, matcher string) ([]byte, error) {
	root, err := parseSettings(src)
	if err != nil {
		return nil, err
	}
	group := e.group(event, matcher)
	if root == nil {
		st := jsonc.StyleOf(src, nil)
		text := jsonc.Render(jsonc.Obj{{Key: "hooks", Val: jsonc.Obj{{Key: event, Val: []any{group}}}}}, st, "") + st.Newline
		if len(src) > jsonc.BOMLen(src) && !bytes.HasSuffix(src, []byte("\n")) {
			text = st.Newline + text // after a last line, which may be a // comment
		}
		return append(append([]byte(nil), src...), text...), nil
	}
	st := jsonc.StyleOf(src, root)
	hooks := root.Get("hooks")
	if hooks == nil {
		return jsonc.AddMember(src, root, "hooks", jsonc.Obj{{Key: event, Val: []any{group}}}, st), nil
	}
	if hooks.Kind != jsonc.Object {
		return nil, errors.New(`has a "hooks" that isn't an object, so pairtime leaves it alone`)
	}
	list := hooks.Get(event)
	if list == nil {
		return jsonc.AddMember(src, hooks, event, []any{group}, st), nil
	}
	if list.Kind != jsonc.Array {
		return nil, fmt.Errorf(`has a "hooks.%s" that isn't a list, so pairtime leaves it alone`, event)
	}
	for _, g := range list.Items {
		hs := g.Value.Get("hooks")
		if hs == nil || hs.Kind != jsonc.Array {
			continue
		}
		for _, h := range hs.Items {
			if e.isWanted(event, g.Value, h.Value) {
				return src, nil
			}
		}
	}
	return jsonc.AppendElement(src, list, group, st), nil
}

// group is pairtime's hook group for event.
func (e *claudeEdit) group(event, matcher string) jsonc.Obj {
	handler := jsonc.Obj{
		{Key: "type", Val: "command"},
		{Key: "command", Val: e.exe},
		{Key: "args", Val: hookArgs(event)},
	}
	var g jsonc.Obj
	if matcher != "" {
		g = append(g, jsonc.Member{Key: "matcher", Val: matcher})
	}
	return append(g, jsonc.Member{Key: "hooks", Val: []any{handler}})
}

func hookArgs(event string) []string {
	return []string{"hook", "--harness", claudeHarness, "--event", event}
}

// isWanted reports whether h, a handler in group under event, is exactly
// one of the hooks this edit installs. Fields pairtime doesn't write, such
// as a timeout the teen added, don't matter.
func (e *claudeEdit) isWanted(event string, group, h *jsonc.Value) bool {
	i := slices.IndexFunc(claudeHooks, func(hk struct{ event, matcher string }) bool { return hk.event == event })
	if i < 0 || !sameMatcher(group, claudeHooks[i].matcher) {
		return false
	}
	typ, cmd := h.Get("type"), h.Get("command")
	return typ != nil && typ.Kind == jsonc.String && typ.Text == "command" &&
		cmd != nil && cmd.Kind == jsonc.String && cmd.Text == e.exe &&
		slices.Equal(strs(h.Get("args")), hookArgs(event))
}

// sameMatcher reports whether group's matcher is want. No matcher, "" and
// "*" all match everything.
func sameMatcher(group *jsonc.Value, want string) bool {
	got := ""
	if m := group.Get("matcher"); m != nil {
		if m.Kind != jsonc.String {
			return false
		}
		got = m.Text
	}
	if want == "" {
		return got == "" || got == "*"
	}
	return got == want
}

// isPairtimeHook reports whether h is a hook some pairtime install wrote
// for Claude Code: a command whose file name is pairtime or pairtime.exe,
// with arguments starting "hook" and naming --harness claude_code.
func isPairtimeHook(h *jsonc.Value) bool {
	cmd := h.Get("command")
	if cmd == nil || cmd.Kind != jsonc.String {
		return false
	}
	name := cmd.Text[strings.LastIndexAny(cmd.Text, `/\`)+1:]
	if !strings.EqualFold(name, "pairtime") && !strings.EqualFold(name, "pairtime.exe") {
		return false
	}
	args := strs(h.Get("args"))
	if len(args) < 3 || args[0] != "hook" {
		return false
	}
	for i := 1; i+1 < len(args); i++ {
		if args[i] == "--harness" && args[i+1] == claudeHarness {
			return true
		}
	}
	return false
}

// strs returns the strings of the array v, or nil when v isn't an array of
// strings.
func strs(v *jsonc.Value) []string {
	if v == nil || v.Kind != jsonc.Array {
		return nil
	}
	out := make([]string, 0, len(v.Items))
	for _, it := range v.Items {
		if it.Value.Kind != jsonc.String {
			return nil
		}
		out = append(out, it.Value.Text)
	}
	return out
}

// parseSettings parses a settings file. It returns nil for one that holds
// no value, such as an empty file, and an error for one that isn't JSON or
// whose top level isn't an object.
func parseSettings(src []byte) (*jsonc.Value, error) {
	root, err := jsonc.Parse(src)
	if errors.Is(err, jsonc.ErrEmpty) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("isn't valid JSON, so pairtime leaves it alone: %w", err)
	}
	if root.Kind != jsonc.Object {
		return nil, errors.New("doesn't hold a JSON object, so pairtime leaves it alone")
	}
	return root, nil
}

// maxRemovals bounds how many hooks one pass takes out, so a mistake can't
// loop forever.
const maxRemovals = 10000

// removeClaudeHooks takes out every hook handler match accepts, one at a
// time. A group whose handlers all match goes as a whole; otherwise only
// the matching handler goes, and the group keeps the others.
func removeClaudeHooks(src []byte, match func(event string, group, h *jsonc.Value) bool) ([]byte, error) {
	for range maxRemovals {
		root, err := parseSettings(src)
		if err != nil || root == nil {
			return src, err
		}
		hooks := root.Get("hooks")
		if hooks == nil || hooks.Kind != jsonc.Object {
			return src, nil
		}
		next := removeOneHook(src, hooks, match)
		if next == nil {
			return src, nil
		}
		src = next
	}
	return nil, errors.New("holds more pairtime hooks than pairtime ever writes")
}

// removeOneHook returns src without the first hook handler under hooks
// that match accepts, or nil when there is none.
func removeOneHook(src []byte, hooks *jsonc.Value, match func(event string, group, h *jsonc.Value) bool) []byte {
	for _, ev := range hooks.Items {
		list := ev.Value
		if list.Kind != jsonc.Array {
			continue
		}
		for gi, g := range list.Items {
			hs := g.Value.Get("hooks")
			if hs == nil || hs.Kind != jsonc.Array {
				continue
			}
			first, matched := -1, 0
			for hi, h := range hs.Items {
				if match(ev.Key, g.Value, h.Value) {
					matched++
					if first < 0 {
						first = hi
					}
				}
			}
			switch {
			case matched == 0:
				continue
			case matched == len(hs.Items):
				return jsonc.Remove(src, list, gi)
			default:
				return jsonc.Remove(src, hs, first)
			}
		}
	}
	return nil
}

// undoClaudeHooks takes every pairtime hook out of the settings cur holds,
// then the lists and the "hooks" object that pairtime's hooks left empty
// and that base, the file before pairtime, didn't have. A file left with
// only an empty object, where base had no value, becomes base again.
func undoClaudeHooks(cur, base Content) (Content, error) {
	if !cur.Exists {
		return cur, nil
	}
	src, err := removeClaudeHooks(cur.Data, func(_ string, _, h *jsonc.Value) bool { return isPairtimeHook(h) })
	if err != nil {
		return Content{}, err
	}
	var before *jsonc.Value // base's "hooks"
	if base.Exists {
		root, err := jsonc.Parse(base.Data)
		switch {
		case err == nil:
			before = root.Get("hooks")
		case !errors.Is(err, jsonc.ErrEmpty):
			// pairtime can't tell what base had, so it takes out nothing more.
			return Content{Exists: true, Data: src, Mode: cur.Mode}, nil
		}
	}
	if src, err = pruneHooks(src, before); err != nil {
		return Content{}, err
	}
	if root, err := jsonc.Parse(src); err == nil && root.Kind == jsonc.Object && emptyInside(src, root) && noValue(base) {
		return base, nil
	}
	return Content{Exists: true, Data: src, Mode: cur.Mode}, nil
}

// pruneHooks takes out of src's "hooks" each empty list that before,
// base's "hooks", doesn't have, and then "hooks" itself if it is empty and
// base had none.
func pruneHooks(src []byte, before *jsonc.Value) ([]byte, error) {
	for range maxRemovals {
		root, err := parseSettings(src)
		if err != nil || root == nil {
			return src, err
		}
		hooks := root.Get("hooks")
		if hooks == nil || hooks.Kind != jsonc.Object {
			return src, nil
		}
		gone := false
		for i := len(hooks.Items) - 1; i >= 0; i-- {
			it := hooks.Items[i]
			if emptyInside(src, it.Value) && before.Find(it.Key) < 0 {
				src = jsonc.Remove(src, hooks, i)
				gone = true
				break
			}
		}
		if gone {
			continue
		}
		if before == nil && emptyInside(src, hooks) {
			src = jsonc.Remove(src, root, root.Find("hooks"))
		}
		return src, nil
	}
	return nil, errors.New("has more empty hook lists than pairtime ever writes")
}

// emptyInside reports whether v is an object or array with nothing
// between its brackets but white space.
func emptyInside(src []byte, v *jsonc.Value) bool {
	return (v.Kind == jsonc.Object || v.Kind == jsonc.Array) && len(v.Items) == 0 && jsonc.IsBlank(src[v.Start+1:v.End-1])
}

// noValue reports whether c holds no JSON value: it isn't there, or holds
// only white space and comments.
func noValue(c Content) bool {
	if !c.Exists {
		return true
	}
	_, err := jsonc.Parse(c.Data)
	return errors.Is(err, jsonc.ErrEmpty)
}
