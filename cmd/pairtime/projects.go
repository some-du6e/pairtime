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
computer with it. A code on your dashboard can be a character longer than
here, when another of your computers has a project whose code starts the
same way.

  show-name <code> [label]  Shows the project's name on your dashboard:
                            the label, or else the one it showed before or
                            the name pairtime shows for it here. pairtime
                            prints the name before it saves it.
  hide-name <code>          Takes the name off your dashboard again.

<code> is a code pairtime projects lists, "Project <code>" as your dashboard
shows it, or the project's whole key. Both write the project's table in
config.toml, which the running agent reads again at once, and it sends the
change with its next upload. Hiding a name on your dashboard turns it off
here too, until you show it again.

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
	// linked, and config.toml says mode = "online".
	online, linked bool
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

// writeProjects prints the list: each project's title and code, its time
// today, and its name on the dashboard, then what couldn't be read and how
// to show a name.
func writeProjects(w io.Writer, v projectsView) {
	if len(v.list) == 0 {
		fmt.Fprintln(w, "pairtime knows no projects on this computer yet. A project shows up here once you work on it with an AI agent.")
	}
	for i, p := range v.list {
		if i > 0 {
			fmt.Fprintln(w)
		}
		title := projectname.CodeName(p.Code)
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
	fmt.Fprintln(w, "pairtime projects show-name <code> shows a project's name on your own pairtime dashboard, where only you see it, and hide-name takes it off.")
}

// nameWords says where p's name stands on the dashboard.
func nameWords(p projects.Project) string {
	label := `"` + text.Printable(p.Label) + `"`
	switch p.State {
	case projects.On:
		return "shows the name " + label
	case projects.Sending:
		return "shows the name " + label + " once pairtime uploads it"
	case projects.Removing:
		return "shows its name until pairtime uploads the change, then " + projectname.CodeName(p.Code)
	case projects.Hidden:
		return fmt.Sprintf("%s, since you hid its name there on %s; pairtime turned it off here, and show-name shows it again", projectname.CodeName(p.Code), text.Day(p.HiddenAt.In(viewZone())))
	}
	if p.Label == "" {
		return projectname.CodeName(p.Code) + "; it has no name on this computer, so show-name needs one"
	}
	return projectname.CodeName(p.Code) + "; show-name shows it as " + label
}

// notOnline says why no name goes to the dashboard from this computer now.
func notOnline(v projectsView) string {
	if !v.linked {
		return "This computer isn't linked to a pairtime server, so no name goes to your dashboard until pairtime login links it."
	}
	return "This computer is local-only, as config.toml says, so no name goes to your dashboard until it's online again."
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
// has it.
func findProject(v projectsView, code string, stderr io.Writer) (projects.Project, bool) {
	p, ok := projects.Find(v.list, code)
	if !ok {
		fmt.Fprintf(stderr, "pairtime projects: no project on this computer has the code %q; pairtime projects lists them\n", text.Printable(code))
	}
	return p, ok
}

func runShowName(args []string, stdout, stderr io.Writer) int {
	code, rest := projectArgs(args)
	if code == "" || code == "-h" || code == "--help" {
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
	label, err := projects.Label(p, strings.Join(rest, " "), len(rest) > 0)
	switch {
	case errors.Is(err, projects.ErrNoLabel):
		fmt.Fprintf(stderr, "pairtime projects: %s has no name on this computer, so say what your dashboard shows: pairtime projects show-name %s \"a name\"\n", projectname.CodeName(p.Code), p.Code)
		return 1
	case errors.Is(err, projects.ErrEmptyLabel):
		fmt.Fprintln(stderr, "pairtime projects: that name is empty once its spaces and invisible characters are taken out, so it can't show")
		return 1
	case err != nil:
		fmt.Fprintf(stderr, "pairtime projects: %v\n", err)
		return 1
	}
	fmt.Fprintf(stdout, "Your pairtime dashboard will show %s as \"%s\". Only you see it there, on your own dashboard while you're signed in, and nothing else about the project leaves this computer with it.\n",
		projectname.CodeName(p.Code), text.Printable(label))
	if err := projects.Show(v.dirs.ConfigFile(), p, label); err != nil {
		fmt.Fprintf(stderr, "pairtime projects: %v\n", err)
		return 1
	}
	if v.online {
		fmt.Fprintln(stdout, "Saved in config.toml. pairtime sends it with its next upload.")
	} else {
		fmt.Fprintln(stdout, "Saved in config.toml. "+notOnline(v))
	}
	return 0
}

func runHideName(args []string, stdout, stderr io.Writer) int {
	code, rest := projectArgs(args)
	if code == "" || code == "-h" || code == "--help" || len(rest) > 0 {
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
	switch p.State {
	case projects.On, projects.Removing:
		fmt.Fprintf(stdout, "Turned off in config.toml. Your pairtime dashboard calls it %s again once pairtime uploads the change.\n", projectname.CodeName(p.Code))
	case projects.Sending:
		fmt.Fprintf(stdout, "Turned off in config.toml before it was sent. Your pairtime dashboard calls it %s.\n", projectname.CodeName(p.Code))
	default:
		fmt.Fprintf(stdout, "Its name was off already. Your pairtime dashboard calls it %s.\n", projectname.CodeName(p.Code))
	}
	return 0
}
