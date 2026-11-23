// SPDX-License-Identifier: AGPL-3.0-or-later

package main

import (
	"errors"
	"fmt"
	"io"
	"io/fs"
	"strings"

	"github.com/some-du6e/pairtime/internal/agent/account"
	"github.com/some-du6e/pairtime/internal/agent/config"
	"github.com/some-du6e/pairtime/internal/agent/home"
	"github.com/some-du6e/pairtime/internal/agent/projects"
	"github.com/some-du6e/pairtime/internal/agent/uploader"
	"github.com/some-du6e/pairtime/internal/coach/text"
	"github.com/some-du6e/pairtime/pkg/projectname"
)

const projectsUsage = `Usage: pairtime projects
       pairtime projects show-name <code> [label]
       pairtime projects hide-name <code>

Lists every project pairtime knows on this computer: the name it shows for
the project here, the code your pairtime dashboard calls it by, its time
today, and whether its name shows on your dashboard.

Your dashboard doesn't know your projects' names. It calls each one by a
code, such as Project 7c41e9, unless you show its name there, one project
at a time, from this computer. Only you see the name, on your own dashboard
while you're signed in, and nothing else about the project leaves this
computer with it. The codes are your dashboard's own: pairtime reads them
from the server when the agent starts, and again once a day. A project
gets its code once its first minutes reach the server, and until pairtime
has read it, the list shows the project's whole key instead.

  show-name <code> [label]  Shows the project's name on your dashboard:
                            the label, cleaned of extra spaces and
                            invisible characters, or else the one the
                            listing shows, the one it showed before or the
                            name pairtime shows for it here. pairtime
                            prints the name it saved.
  hide-name <code>          Takes the name off your dashboard again.

<code> is a code pairtime projects lists, "Project <code>" as your dashboard
shows it, or the project's whole key, as pairtime projects lists a project
with no code yet. Both write the project's table in config.toml, which the
running agent reads again at once, and it sends the change with its next
upload. Hiding a name on your dashboard turns it off here too, until you
show it again, and so does the server dropping a name, or this computer
signing in to another server, which never showed it.

Exit status:
  0  done
  1  pairtime couldn't read what it needs or change config.toml, no
     project has that code, or the name can't be shown as given
  2  usage error
`

func init() {
	register(command{
		name:    "projects",
		summary: "List your projects, and show or hide their names on your dashboard",
		run:     runProjects,
	})
}

func runProjects(args []string, stdout, stderr io.Writer) int {
	if len(args) > 0 {
		switch args[0] {
		case "-h", "-help", "--help":
			fmt.Fprint(stdout, projectsUsage)
			return 0
		case "show-name":
			return runShowName(args[1:], stdout, stderr)
		case "hide-name":
			return runHideName(args[1:], stdout, stderr)
		}
		fmt.Fprintf(stderr, "pairtime projects: there's no %q; it takes show-name or hide-name, or nothing\n\n%s", args[0], projectsUsage)
		return 2
	}
	v, ok := readProjects(stderr)
	if !ok {
		return 1
	}
	writeProjects(stdout, v)
	return 0
}

// projectsView is what pairtime projects shows.
type projectsView struct {
	dirs home.Dirs
	list []projects.Project
	// clocks are each project's chess clock today, in pairtime today's
	// words, by key, and idle the clock of a project with no time today.
	clocks map[string]string
	idle   string
	// online says the names config.toml turns on go to the server: it is
	// linked, and config.toml says mode = "online". broken says config.toml
	// has an error, which keeps the computer local-only until it's fixed.
	online, linked, broken bool
	// server and device are the sign-in account.json holds, while linked.
	server, device string
	// notes say what couldn't be read, so the list may lack something.
	notes []string
}

// readProjects reads the projects as the running agent's files hold them,
// without changing any of them, and says on stderr why it can't.
func readProjects(stderr io.Writer) (projectsView, bool) {
	dirs, err := home.Default()
	if err != nil {
		fmt.Fprintf(stderr, "pairtime projects: %v\n", err)
		return projectsView{}, false
	}
	v := projectsView{dirs: dirs, clocks: map[string]string{}}
	cfg, problems, parsed, err := config.LoadParsed(dirs.ConfigFile())
	switch {
	case err != nil && !errors.Is(err, fs.ErrNotExist):
		fmt.Fprintf(stderr, "pairtime projects: config.toml doesn't read: %v\n", err)
		return projectsView{}, false
	case !parsed:
		v.broken = true
		v.notes = append(v.notes, "config.toml has an error, so every name is off until it's fixed: "+strings.Join(problems, "; "))
	}
	m, err := projects.ReadMap(dirs.StateFile())
	if err != nil {
		v.notes = append(v.notes, fmt.Sprintf("pairtime couldn't read its project map, so projects you didn't work on lately may be missing, and some names: %v", err))
	}
	days := dayStore{dirs.Days()}
	today := days.today()
	keys, err := projects.DayKeys(dirs.Days(), projects.Dates(today, projects.Days))
	if err != nil {
		v.notes = append(v.notes, fmt.Sprintf("pairtime couldn't read a day it stored, so a project may be missing: %v", err))
	}
	names, err := uploader.ReadNames(dirs.Data)
	if err != nil && !errors.Is(err, fs.ErrNotExist) {
		v.notes = append(v.notes, fmt.Sprintf("pairtime couldn't read which names your dashboard shows: %v", err))
	}
	in := projects.Input{Map: m, Days: keys, Config: cfg, Names: names}
	if l, err := account.Load(dirs.Data); err == nil {
		in.Server, in.Device, v.linked = l.Server, l.DeviceID, true
		v.server, v.device = l.Server, l.DeviceID
	}
	v.online = v.linked && parsed && cfg.Mode == config.Online
	v.list = projects.List(in)
	shadow := cfg.Display.Shadow
	v.idle = text.Clock(0, 0, shadow, nil)
	if d, _, err := days.day(today); err == nil {
		for _, p := range d.Projects {
			v.clocks[p.Project] = buildProject(p.Project, "", p.tally, shadow).clock()
		}
	} else {
		v.notes = append(v.notes, fmt.Sprintf("pairtime couldn't read today's time: %v", err))
	}
	return v, true
}

// noCode is what pairtime projects says after a list with a project the
// server has given no code yet (projects.Project.Code).
const noCode = "A project listed by its key has no code yet: your dashboard gives it one once the project's first minutes reach the server, and pairtime reads it back within a day, or when the agent next starts. show-name and hide-name take the key meanwhile."

// writeProjects prints the list: each project's title and the code the
// server gave it, or its key while it gave none, its time today, and its
// name on the dashboard, then what couldn't be read and how to show a name.
func writeProjects(w io.Writer, v projectsView) {
	if len(v.list) == 0 {
		fmt.Fprintln(w, "pairtime knows no projects on this computer yet. A project shows up here once you work on it with an AI agent.")
	}
	uncoded := false
	for i, p := range v.list {
		if i > 0 {
			fmt.Fprintln(w)
		}
		title := dashboardName(p, p.Key)
		uncoded = uncoded || p.Code == ""
		if p.LocalName != "" {
			title = text.Printable(p.LocalName) + " (" + title + ")"
		}
		fmt.Fprintln(w, title)
		clock, ok := v.clocks[p.Key]
		if !ok {
			clock = v.idle
		}
		fmt.Fprintln(w, "  Today: "+clock)
		fmt.Fprintln(w, "  On your dashboard: "+nameWords(p))
	}
	for _, n := range v.notes {
		fmt.Fprintln(w)
		fmt.Fprintln(w, text.Printable(n))
	}
	if len(v.list) == 0 {
		return
	}
	fmt.Fprintln(w)
	if !v.online {
		fmt.Fprintln(w, notOnline(v))
	}
	if uncoded {
		fmt.Fprintln(w, noCode)
	}
	fmt.Fprintln(w, "pairtime projects show-name <code> shows a project's name on your own pairtime dashboard, where only you see it, and hide-name takes it off.")
}

// dashboardName is what the owner's dashboard calls p in place of its
// name, "Project <code>" with the code the server gave it, or none, the
// words the sentence takes while the server has given it no code.
func dashboardName(p projects.Project, none string) string {
	if p.Code == "" {
		return none
	}
	return projectname.CodeName(p.Code)
}

// projectArg is what show-name and hide-name take for p: its code, or its
// whole key while it has none.
func projectArg(p projects.Project) string {
	if p.Code == "" {
		return p.Key
	}
	return p.Code
}

// nameWords says where p's name stands on the dashboard.
func nameWords(p projects.Project) string {
	label := `"` + text.Printable(p.Label) + `"`
	switch p.State {
	case projects.On:
		return "shows the name " + label
	case projects.Sending:
		if p.Shown != "" {
			return `shows the name "` + text.Printable(p.Shown) + `" until pairtime uploads ` + label
		}
		return "shows the name " + label + " once pairtime uploads it"
	case projects.Removing:
		return "shows its name until pairtime uploads the change, then " + dashboardName(p, "its code") + again(p)
	case projects.Hidden:
		day := text.Day(p.HiddenAt.In(viewZone()))
		var why string
		switch p.Why {
		case uploader.NoteDropped:
			why = fmt.Sprintf("%s, since the server stopped showing its name on %s, as after a long time signed out or a restore of its data", dashboardName(p, "its code"), day)
		case uploader.NoteSignedIn:
			why = fmt.Sprintf("%s, since this computer signed in to this server on %s, and only the server before it showed the name", dashboardName(p, "its code"), day)
		case uploader.NoteUnreadable:
			why = fmt.Sprintf("%s, since on %s pairtime couldn't read which sign-in the name was for", dashboardName(p, "its code"), day)
		default:
			why = fmt.Sprintf("%s, since you hid its name there on %s", dashboardName(p, "its code"), day)
		}
		if p.Pending {
			return why + "; pairtime couldn't turn it off in config.toml, so it doesn't send it, and to show it again set online = false under its table there, then run show-name"
		}
		return why + "; pairtime turned it off here" + again(p)
	}
	if label, ok := showLabel(p); ok {
		return dashboardName(p, "no code yet") + "; show-name shows it as " + label
	}
	return dashboardName(p, "no code yet") + "; it has no name on this computer, so show-name needs one"
}

// again says what show-name does for p, whose name is off here: shows it
// again with the label it would save, or needs one when there is none.
func again(p projects.Project) string {
	if label, ok := showLabel(p); ok {
		return ", and show-name shows it again as " + label
	}
	return "; it has no name on this computer, so show-name needs one"
}

// showLabel is the label show-name saves for p when it's given none, in
// quotes, and false when it would refuse for want of one (projects.Label).
func showLabel(p projects.Project) (string, bool) {
	label, err := projects.Label(p, "", false)
	if err != nil {
		return "", false
	}
	return `"` + text.Printable(label) + `"`, true
}

// notOnline says why no name goes to the dashboard from this computer now.
func notOnline(v projectsView) string {
	switch {
	case !v.linked:
		return "This computer isn't linked to a pairtime server, so no name goes to your dashboard until pairtime login links it."
	case v.broken:
		return "config.toml has an error, so no name goes to your dashboard until it's fixed."
	}
	return "This computer is local-only, as config.toml says, so no name goes to your dashboard until it's online again."
}

// recordSignIn writes the sign-in of a linked computer into a
// project-names.json that names none, so the names it turned on or off are
// this sign-in's: a sign-in to another server later turns them off there
// (uploader.RecordSignIn; project-names-v1 2).
func recordSignIn(v projectsView, stderr io.Writer) {
	if !v.linked {
		return
	}
	if err := uploader.RecordSignIn(v.dirs.Data, v.server, v.device); err != nil {
		fmt.Fprintf(stderr, "pairtime projects: project-names.json couldn't be written (%v); the agent writes which sign-in the names are for when it next uploads\n", err)
	}
}

// helpArg reports whether arg asks for the usage, as pairtime projects
// --help does.
func helpArg(arg string) bool {
	return arg == "-h" || arg == "-help" || arg == "--help"
}

// projectArgs reads a show-name or hide-name's arguments: the project, as
// one word or as "Project" and the code, and what follows it.
func projectArgs(args []string) (code string, rest []string) {
	if len(args) >= 2 && strings.EqualFold(args[0], "Project") {
		return args[0] + " " + args[1], args[2:]
	}
	if len(args) == 0 {
		return "", nil
	}
	return args[0], args[1:]
}

// findProject finds the project code names, and says on stderr when none
// has it. pairtime knows only the codes the server gave it, so a code it
// doesn't hold, as one the server gave since pairtime last read them,
// names nothing, and the whole key still does.
func findProject(v projectsView, code string, stderr io.Writer) (projects.Project, bool) {
	p, ok := projects.Find(v.list, code)
	if !ok {
		fmt.Fprintf(stderr, "pairtime projects: no project on this computer has the code %q; pairtime knows only the codes the server gave it, so give the project's whole key, as pairtime projects lists it\n", text.Printable(code))
	}
	return p, ok
}

func runShowName(args []string, stdout, stderr io.Writer) int {
	code, rest := projectArgs(args)
	if helpArg(code) {
		fmt.Fprint(stdout, projectsUsage)
		return 0
	}
	if code == "" {
		fmt.Fprint(stderr, projectsUsage)
		return 2
	}
	v, ok := readProjects(stderr)
	if !ok {
		return 1
	}
	p, ok := findProject(v, code, stderr)
	if !ok {
		return 1
	}
	called := dashboardName(p, "the project "+p.Key)
	label, err := projects.Label(p, strings.Join(rest, " "), len(rest) > 0)
	switch {
	case errors.Is(err, projects.ErrNoLabel):
		fmt.Fprintf(stderr, "pairtime projects: %s has no name on this computer, so say what your dashboard shows: pairtime projects show-name %s \"a name\"\n", called, projectArg(p))
		return 1
	case errors.Is(err, projects.ErrEmptyLabel):
		fmt.Fprintln(stderr, "pairtime projects: that name is empty once its spaces and invisible characters are taken out, so it can't show")
		return 1
	case err != nil:
		fmt.Fprintf(stderr, "pairtime projects: %v\n", err)
		return 1
	}
	if err := projects.Show(v.dirs.ConfigFile(), p, label); err != nil {
		fmt.Fprintf(stderr, "pairtime projects: %v\n", err)
		return 1
	}
	recordSignIn(v, stderr)
	fmt.Fprintf(stdout, "Your pairtime dashboard will show %s as \"%s\". Only you see it there, on your own dashboard while you're signed in, and nothing else about the project leaves this computer with it.\n",
		called, text.Printable(label))
	if v.online {
		fmt.Fprintln(stdout, "Saved in config.toml. pairtime sends it with its next upload.")
	} else {
		fmt.Fprintln(stdout, "Saved in config.toml. "+notOnline(v))
	}
	return 0
}

func runHideName(args []string, stdout, stderr io.Writer) int {
	code, rest := projectArgs(args)
	if helpArg(code) {
		fmt.Fprint(stdout, projectsUsage)
		return 0
	}
	if code == "" || len(rest) > 0 {
		fmt.Fprint(stderr, projectsUsage)
		return 2
	}
	v, ok := readProjects(stderr)
	if !ok {
		return 1
	}
	p, ok := findProject(v, code, stderr)
	if !ok {
		return 1
	}
	if err := projects.Hide(v.dirs.ConfigFile(), p); err != nil {
		fmt.Fprintf(stderr, "pairtime projects: %v\n", err)
		return 1
	}
	recordSignIn(v, stderr)
	called := dashboardName(p, "by its code")
	switch {
	case p.State == projects.Off || p.State == projects.Hidden:
		fmt.Fprintf(stdout, "Its name was off already. Your pairtime dashboard calls it %s.\n", called)
	case !v.linked:
		// The names a sign-in confirmed count for nothing once it's gone
		// (projects.Input), and a removed computer's names stay on the
		// server until its purge, which this computer can't change now.
		fmt.Fprintln(stdout, "Turned off in config.toml. This computer isn't linked to a pairtime server, so it can't change your dashboard: if your dashboard still shows the name, its Hide this name button takes it off at once.")
	case p.State == projects.Sending && p.Shown == "":
		fmt.Fprintf(stdout, "Turned off in config.toml before it was sent. Your pairtime dashboard calls it %s.\n", called)
	case !v.online:
		fmt.Fprintf(stdout, "Turned off in config.toml. Your pairtime dashboard shows the name until this computer uploads the change, then calls it %s; its Hide this name button takes it off at once meanwhile.\n%s\n", called, notOnline(v))
	default:
		fmt.Fprintf(stdout, "Turned off in config.toml. Your pairtime dashboard calls it %s again once pairtime uploads the change.\n", called)
	}
	return 0
}
