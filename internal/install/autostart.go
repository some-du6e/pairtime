// SPDX-License-Identifier: AGPL-3.0-or-later

package install

import (
	"bytes"
	"encoding/json"
	"encoding/xml"
	"fmt"
	"path/filepath"
	"strings"
)

// ItemAutostart is the ID of the item that starts the agent at login.
const ItemAutostart = "autostart"

// The autostart entries, one per OS (CP 2.1 and 7). None needs admin
// rights, and each shows up where the teen looks for startup apps: Task
// Manager's Startup apps on Windows, Login Items on macOS, and the
// desktop's Startup Applications on Linux.
const (
	// RunKey and RunValue are the Run value on Windows.
	RunKey   = `Software\Microsoft\Windows\CurrentVersion\Run`
	RunValue = "pairtime"
	// LaunchAgentLabel is the LaunchAgent's label on macOS, which names its
	// file in ~/Library/LaunchAgents. CP 7 calls it a placeholder; it
	// changes only together with uninstall's knowledge of the old one.
	LaunchAgentLabel = "dev.pairtime.agent"
	// DesktopFile is the XDG autostart entry's file name on Linux.
	DesktopFile = "pairtime.desktop"
)

// runCommandMax is the longest command the Run key starts: "no longer than
// 260 characters" (CP 2.1, citing Microsoft's Run and RunOnce keys page).
const runCommandMax = 260

// Autostart returns the item that starts `pairtime agent` when the teen
// logs in, without admin rights (DESIGN 3.2; CP 2.1): the Run value under
// HKEY_CURRENT_USER on Windows, which runs `pairtime agent --detach` so
// that no console window stays open (runArgs), a LaunchAgent on macOS, and
// an XDG autostart entry on Linux. The entry is pairtime's whole, so install
// writes all of it and uninstall takes all of it away. Starting the agent
// now, rather than at the next login, is the caller's job.
func Autostart(env Env) (Item, error) {
	if err := env.check(); err != nil {
		return Item{}, err
	}
	var e Edit
	switch env.GOOS {
	case "windows":
		cmd, err := runCommand(env.Exe)
		if err != nil {
			return Item{}, err
		}
		e = ownedEdit{target: RegistryValue(RunKey, RunValue), data: []byte(cmd), typ: RegSZ, entry: entryRunValue}
	case "darwin":
		path := filepath.Join(env.Home, "Library", "LaunchAgents", LaunchAgentLabel+".plist")
		e = ownedEdit{target: File(path), data: launchAgent(env.Exe), entry: entryLaunchAgent}
	case "linux":
		data, err := desktopEntry(env.Exe)
		if err != nil {
			return Item{}, err
		}
		e = ownedEdit{target: File(filepath.Join(configHome(env), "autostart", DesktopFile)), data: data, entry: entryDesktop, keep: keepOffSwitch}
	default:
		return Item{}, fmt.Errorf("pairtime can't start itself at login on %s", env.GOOS)
	}
	return Item{
		ID:      ItemAutostart,
		Summary: "Start pairtime when you log in, so it tracks without you starting it",
		Edits:   []Edit{e},
	}, nil
}

// configHome is $XDG_CONFIG_HOME, or ~/.config when that isn't an
// absolute path (XDG Base Directory Specification).
func configHome(env Env) string {
	if v := env.getenv("XDG_CONFIG_HOME"); filepath.IsAbs(v) {
		return v
	}
	return filepath.Join(env.Home, ".config")
}

// runArgs are what the Run value passes the binary. pairtime.exe is a
// console program, and Explorer, which runs the Run key at login, has no
// console to share, so Windows opens a console window for it. `pairtime
// agent` would keep that window open, full of the agent's log, for as long
// as the agent runs, and closing it would stop the agent. `agent --detach`
// starts the agent in a process of its own with no console window and
// exits, so the window closes at once (cmd/pairtime/agent.go).
const runArgs = "agent --detach"

// runCommand is the Run value's data: the quoted binary path and runArgs.
func runCommand(exe string) (string, error) {
	if strings.Contains(exe, `"`) {
		return "", fmt.Errorf("the binary's path %q holds a double quote, which a Windows command line can't quote", exe)
	}
	cmd := `"` + exe + `" ` + runArgs
	n := 0 // in UTF-16 code units, as Windows counts
	for _, r := range cmd {
		n++
		if r > 0xFFFF {
			n++
		}
	}
	if n > runCommandMax {
		return "", fmt.Errorf("the login command %s is %d characters long, and Windows starts only commands of up to %d from the Run key", cmd, n, runCommandMax)
	}
	return cmd, nil
}

// launchAgent is the LaunchAgent's property list (launchd.plist(5)):
// launchd runs the agent at login (RunAtLoad), with the Background process
// type, which DESIGN 3.2 asks for on macOS.
func launchAgent(exe string) []byte {
	var b bytes.Buffer
	b.WriteString(`<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>
	<key>Label</key>
	<string>` + LaunchAgentLabel + `</string>
	<key>ProgramArguments</key>
	<array>
		<string>`)
	xml.EscapeText(&b, []byte(exe)) // a bytes.Buffer write never fails
	b.WriteString(`</string>
		<string>agent</string>
	</array>
	<key>RunAtLoad</key>
	<true/>
	<key>ProcessType</key>
	<string>Background</string>
</dict>
</plist>
`)
	return b.Bytes()
}

// desktopEntry is the XDG autostart entry (Desktop Application Autostart
// Specification 0.5; Desktop Entry Specification 1.5). TryExec makes the
// desktop skip the entry if the binary is gone. The entry has no
// NoDisplay, which would hide it from the desktop's Startup Applications,
// where the teen can turn it off.
func desktopEntry(exe string) ([]byte, error) {
	arg, err := execArg(exe)
	if err != nil {
		return nil, err
	}
	return []byte("[Desktop Entry]\n" +
		"Type=Application\n" +
		"Name=pairtime\n" +
		"Comment=Counts the time you spend building with AI\n" +
		"Exec=" + arg + " agent\n" +
		"TryExec=" + desktopString(exe) + "\n" +
		"Terminal=false\n"), nil
}

// execArg writes s, the program's path, as the first argument of an Exec
// key. An argument with a reserved character goes in double quotes, inside
// which ", `, $ and \ take a backslash; a literal % is written %%, which
// the spec doesn't allow inside quotes; and the whole value then takes the
// escapes of a string value, which double every backslash. "The name or
// path of the executable program may not contain the equal sign", quoted
// or not, so such a path is refused (Desktop Entry Specification 1.5, "The
// Exec key").
func execArg(s string) (string, error) {
	if strings.Contains(s, "=") {
		return "", fmt.Errorf("the binary's path %q holds an equals sign, which a desktop entry can't carry in the program it starts", s)
	}
	const reserved = " \t\n\"'\\><~|&;$*?#()`"
	if !strings.ContainsAny(s, reserved) {
		return desktopString(strings.ReplaceAll(s, "%", "%%")), nil
	}
	if strings.Contains(s, "%") {
		return "", fmt.Errorf("the binary's path %q holds both a %% sign and a character that needs quoting, which a desktop entry can't carry", s)
	}
	var b strings.Builder
	b.WriteByte('"')
	for _, r := range s {
		if r == '"' || r == '`' || r == '$' || r == '\\' {
			b.WriteByte('\\')
		}
		b.WriteRune(r)
	}
	b.WriteByte('"')
	return desktopString(b.String()), nil
}

// desktopString escapes s as a desktop entry's string value: a backslash
// is written \\. s holds no control character (Env.check).
func desktopString(s string) string {
	return strings.ReplaceAll(s, `\`, `\\`)
}

// kindOwned is the kind of an edit that makes a target hold exactly what
// pairtime wants: an entry pairtime owns whole, such as its autostart.
const kindOwned = "owned"

type ownedEdit struct {
	target Target
	data   []byte
	typ    uint32 // a registry value's type; 0 for a file
	// entry is the kind of entry the edit writes, which picks the function
	// that recognizes an entry some pairtime wrote (agentEntries). Every
	// owned edit names one: with "", claims knows only the entry this edit
	// writes, and undoOwned, which has only the manifest, knows none, so it
	// leaves an entry that changed since install alone.
	entry string
	// keep, when set, returns data with what the teen set in cur, an entry
	// pairtime wrote, carried over, such as autostart turned off.
	keep func(cur, data []byte) []byte
}

// The kinds of entry an owned edit writes. The manifest records the kind
// (ownedData), so that uninstall, working from the manifest alone, can
// tell an entry some pairtime wrote from one that took its place.
const (
	entryRunValue    = "run_value"
	entryLaunchAgent = "launch_agent"
	entryDesktop     = "desktop_entry"
)

// agentEntries recognize, for each kind of entry, one that starts the agent
// of a binary called pairtime, at any path, turned off or not.
var agentEntries = map[string]func(Content) bool{
	entryRunValue:    isAgentRunValue,
	entryLaunchAgent: isAgentLaunchAgent,
	entryDesktop:     isAgentDesktopEntry,
}

// isAgentEntry reports whether c, an entry of the kind entry, is one some
// pairtime wrote.
func isAgentEntry(entry string, c Content) bool {
	f := agentEntries[entry]
	return f != nil && f(c)
}

// ownedData is what an owned edit records for its undo.
type ownedData struct {
	Entry string `json:"entry"`
}

func (e ownedEdit) Target() Target { return e.target }

func (e ownedEdit) Kind() string { return kindOwned }

// Record makes ownedEdit a Recorder of the kind of entry it writes, which
// undoOwned needs and neither the target nor what it held before tells.
func (e ownedEdit) Record() json.RawMessage {
	if e.entry == "" {
		return nil
	}
	data, _ := json.Marshal(ownedData{Entry: e.entry}) // a string field always marshals
	return data
}

// claim reports whether cur is an entry some pairtime wrote, such as the
// entry of an install whose binary was somewhere else.
func (e ownedEdit) claim(cur Content) bool { return isAgentEntry(e.entry, cur) }

// claims makes ownedEdit a claimer: an entry is pairtime's when it is what
// this edit writes, or what claim recognizes.
func (e ownedEdit) claims(cur Content) bool {
	if next, err := e.Apply(cur); err == nil && next.Same(cur) {
		return true
	}
	return e.claim(cur)
}

func (e ownedEdit) Apply(cur Content) (Content, error) {
	next := Content{Exists: true, Data: e.data, Type: e.typ}
	if cur.Exists {
		next.Mode = cur.Mode
		if e.keep != nil && e.claim(cur) {
			next.Data = e.keep(cur.Data, e.data)
		}
	}
	return next, nil
}

// undoOwned takes an owned edit out of cur, a target that changed since
// pairtime wrote it. An entry that is still recognizably pairtime's, such
// as its autostart turned off, gives way to base, what was there before
// pairtime, which for an entry pairtime created means nothing. Anything
// else at the name stays, such as another program's entry that took
// pairtime's place, and so does an entry whose kind data doesn't name. An
// entry the teen deleted stays deleted.
func undoOwned(cur, base Content, data json.RawMessage) (Content, error) {
	var d ownedData
	if !cur.Exists || json.Unmarshal(data, &d) != nil || !isAgentEntry(d.Entry, cur) {
		return cur, nil
	}
	return base, nil
}

// isPairtimeBinary reports whether path names a binary called pairtime,
// pairtime.exe on Windows, whatever folder it is in.
func isPairtimeBinary(path string) bool {
	name := path[strings.LastIndexAny(path, `/\`)+1:]
	return strings.EqualFold(name, "pairtime") || strings.EqualFold(name, "pairtime.exe")
}

// startsAgent reports whether args, a program and its arguments, start a
// pairtime binary's agent.
func startsAgent(args []string) bool {
	return len(args) >= 2 && isPairtimeBinary(args[0]) && args[1] == "agent"
}

// isAgentRunValue reports whether c, a Run value, starts a pairtime agent.
// The program is the first token of the command line, in double quotes or
// up to the first space or tab, as CreateProcess reads it, and may hold
// environment variables in a REG_EXPAND_SZ value.
func isAgentRunValue(c Content) bool {
	if c.Type != RegSZ && c.Type != RegExpandSZ {
		return false
	}
	cmd := strings.TrimLeft(string(c.Data), " \t")
	var prog, rest string
	if strings.HasPrefix(cmd, `"`) {
		end := strings.IndexByte(cmd[1:], '"')
		if end < 0 {
			return false
		}
		prog, rest = cmd[1:1+end], cmd[2+end:]
	} else {
		prog, rest = cmd, ""
		if i := strings.IndexAny(cmd, " \t"); i >= 0 {
			prog, rest = cmd[:i], cmd[i:]
		}
	}
	return startsAgent(append([]string{prog}, strings.Fields(rest)...))
}

// isAgentLaunchAgent reports whether c, a LaunchAgent property list in XML,
// starts a pairtime agent through its ProgramArguments. A binary property
// list, or one that doesn't parse, isn't pairtime's.
func isAgentLaunchAgent(c Content) bool {
	var doc struct {
		Dict struct {
			// Items are the dict's keys and values, in order.
			Items []struct {
				XMLName xml.Name
				Text    string   `xml:",chardata"`
				Strings []string `xml:"string"`
			} `xml:",any"`
		} `xml:"dict"`
	}
	if xml.Unmarshal(c.Data, &doc) != nil {
		return false
	}
	items := doc.Dict.Items
	for i := 0; i+1 < len(items); i++ {
		if items[i].XMLName.Local == "key" && items[i].Text == "ProgramArguments" && items[i+1].XMLName.Local == "array" {
			return startsAgent(items[i+1].Strings)
		}
	}
	return false
}

// isAgentDesktopEntry reports whether c, an XDG desktop entry, starts a
// pairtime agent through the Exec key of its [Desktop Entry] group.
func isAgentDesktopEntry(c Content) bool {
	group := ""
	for _, line := range strings.Split(string(c.Data), "\n") {
		line = strings.TrimSpace(line)
		switch {
		case line == "" || strings.HasPrefix(line, "#"):
		case strings.HasPrefix(line, "["):
			group = line
		case group == "[Desktop Entry]":
			key, value, ok := strings.Cut(line, "=")
			if ok && strings.TrimSpace(key) == "Exec" {
				return startsAgent(execArgs(strings.TrimSpace(value)))
			}
		}
	}
	return false
}

// keepOffSwitch returns entry, pairtime's desktop entry, with the lines
// that turned cur, an entry some pairtime wrote, off. On Linux the off
// switch lives in the entry itself: Hidden=true, which the Autostart
// specification defines and XFCE and KDE write (CP 2.1), and
// X-GNOME-Autostart-enabled=false, which GNOME and Cinnamon write, as
// MATE writes X-MATE-Autostart-enabled=false. A reinstall that wrote
// pairtime's entry without them would turn autostart back on.
func keepOffSwitch(cur, entry []byte) []byte {
	var off []string
	group := ""
	for _, line := range strings.Split(string(cur), "\n") {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "[") {
			group = line
			continue
		}
		key, value, ok := strings.Cut(line, "=")
		if ok && group == "[Desktop Entry]" && isOffSwitch(strings.TrimSpace(key), strings.TrimSpace(value)) {
			off = append(off, line)
		}
	}
	if len(off) == 0 {
		return entry
	}
	// pairtime's entry is one [Desktop Entry] group ending in a newline,
	// so the lines join that group.
	return append(append([]byte(nil), entry...), strings.Join(off, "\n")+"\n"...)
}

// isOffSwitch reports whether key=value, in a [Desktop Entry] group, turns
// an autostart entry off.
func isOffSwitch(key, value string) bool {
	if key == "Hidden" {
		return value == "true"
	}
	return strings.HasPrefix(key, "X-") && strings.HasSuffix(key, "-Autostart-enabled") && value == "false"
}

// execArgs splits an Exec value, as written in the file, into its program
// and arguments, undoing what execArg and desktopString do: the string
// escapes first, then double quotes, inside which a backslash escapes the
// next character, then %% (Desktop Entry Specification 1.5, "The Exec
// key").
func execArgs(value string) []string {
	var s strings.Builder
	for i := 0; i < len(value); i++ {
		c := value[i]
		if c == '\\' && i+1 < len(value) {
			i++
			switch value[i] {
			case 's':
				c = ' '
			case 'n':
				c = '\n'
			case 't':
				c = '\t'
			case 'r':
				c = '\r'
			case '\\':
			default:
				s.WriteByte('\\') // an escape of the quoting, below
				c = value[i]
			}
		}
		s.WriteByte(c)
	}
	var args []string
	var arg strings.Builder
	open, quoted := false, false
	u := s.String()
	for i := 0; i < len(u); i++ {
		switch c := u[i]; {
		case quoted && c == '\\' && i+1 < len(u):
			i++
			arg.WriteByte(u[i])
		case c == '"':
			quoted, open = !quoted, true
		case !quoted && (c == ' ' || c == '\t'):
			if open {
				args = append(args, strings.ReplaceAll(arg.String(), "%%", "%"))
				arg.Reset()
				open = false
			}
		default:
			arg.WriteByte(c)
			open = true
		}
	}
	if open {
		args = append(args, strings.ReplaceAll(arg.String(), "%%", "%"))
	}
	return args
}
