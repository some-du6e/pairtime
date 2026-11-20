// SPDX-License-Identifier: AGPL-3.0-or-later

package install

import (
	"bytes"
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
// HKEY_CURRENT_USER on Windows, a LaunchAgent on macOS, and an XDG
// autostart entry on Linux. The entry is pairtime's whole, so install
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
		e = ownedEdit{target: RegistryValue(RunKey, RunValue), data: []byte(cmd), typ: RegSZ}
	case "darwin":
		path := filepath.Join(env.Home, "Library", "LaunchAgents", LaunchAgentLabel+".plist")
		e = ownedEdit{target: File(path), data: launchAgent(env.Exe)}
	case "linux":
		data, err := desktopEntry(env.Exe)
		if err != nil {
			return Item{}, err
		}
		e = ownedEdit{target: File(filepath.Join(configHome(env), "autostart", DesktopFile)), data: data}
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

// runCommand is the Run value's data: the quoted binary path and the
// agent command.
func runCommand(exe string) (string, error) {
	if strings.Contains(exe, `"`) {
		return "", fmt.Errorf("the binary's path %q holds a double quote, which a Windows command line can't quote", exe)
	}
	cmd := `"` + exe + `" agent`
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

// execArg writes s as one argument of an Exec key. An argument with a
// reserved character goes in double quotes, inside which ", `, $ and \
// take a backslash; a literal % is written %%, which the spec doesn't
// allow inside quotes; and the whole value then takes the escapes of a
// string value, which double every backslash (Desktop Entry Specification
// 1.5, "The Exec key").
func execArg(s string) (string, error) {
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
}

func (e ownedEdit) Target() Target { return e.target }

func (e ownedEdit) Kind() string { return kindOwned }

func (e ownedEdit) Apply(cur Content) (Content, error) {
	next := Content{Exists: true, Data: e.data, Type: e.typ}
	if cur.Exists {
		next.Mode = cur.Mode
	}
	return next, nil
}

// undoOwned puts back what was there before pairtime, which for an entry
// pairtime created means nothing. An entry the teen deleted stays deleted.
func undoOwned(cur, base Content) (Content, error) {
	if !cur.Exists {
		return cur, nil
	}
	return base, nil
}
