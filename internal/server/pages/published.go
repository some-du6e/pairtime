// SPDX-License-Identifier: AGPL-3.0-or-later

package pages

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/some-du6e/pairtime/internal/server/dashboard"
	"github.com/some-du6e/pairtime/internal/server/rollup"
	"github.com/some-du6e/pairtime/pkg/projectname"
)

// The forms of the owner's list of what they published, each of which
// takes one item down (WP-SV12 part 2): a devlog's delete and a badge's
// removal.
const (
	DeleteDevlogPath = "/dashboard/devlogs/delete"
	RemoveBadgePath  = "/dashboard/badges/remove"
)

// Published is what an account published from its computers, for the
// list on the owner's dashboard.
type Published struct {
	// Devlogs are the account's devlogs, in the order the server received
	// them.
	Devlogs []PublishedDevlog
	// Badges are the badges it shows.
	Badges []PublishedBadge
}

// PublishedDevlog is a devlog on the owner's list.
type PublishedDevlog struct {
	ID string
	// Project is what the dashboard calls the devlog's project
	// (dashboard.NameOf).
	Project    string
	Markdown   string
	AIAssisted bool
	ReceivedAt time.Time
}

// PublishedBadge is a badge on the owner's list.
type PublishedBadge struct{ ID, Code string }

// itemsPart is the owner's list: each devlog and badge with the button
// that takes it down, and how long the server keeps the id of what was
// taken down.
type itemsPart struct {
	DeletePath, RemovePath string
	Devlogs                []devlogRow
	Badges                 []badgeRow
	KeptFor                string
}

// devlogRow is a devlog on the list: its title, its project's name, the
// date it reached the server in the account's time zone, and whether the
// teen's agent drafted it.
type devlogRow struct {
	ID, Title, Project, Date string
	AI                       bool
}

type badgeRow struct{ ID, Name string }

// BadgeName is what the owner's list calls a badge code: DESIGN 3.8's
// moment kind in words (docs/spec/teen-layer-v1.md 4), or the code itself
// for one this release has no words for, which a later version may add.
func BadgeName(code string) string {
	if n := badgeNames[code]; n != "" {
		return n
	}
	return code
}

// badgeNames are BadgeName's words for the badge codes.
var badgeNames = map[string]string{
	"first_commit":        "First commit",
	"first_green_test":    "First passing test",
	"first_test_written":  "First test written",
	"rejected_agent_edit": "Turned down an agent's edit",
	"interrupt":           "Stopped the agent to steer it",
	"fixed_by_hand":       "Fixed it by hand",
	"long_fight":          "Won a long fight",
	"loop_caught":         "Caught a loop",
	"comeback":            "Comeback",
	"new_language":        "New language",
	"new_tool":            "New tool",
}

// untitled is what the list calls a devlog with no text.
const untitled = "Untitled devlog"

// devlogTitle is what the list calls a devlog: its first line that holds
// any text, without a heading's # marks, cleaned as a project's label is
// (projectname.CleanLabel), so no control, invisible or bidirectional
// character reaches the page and at most projectname.MaxLabel characters
// do.
func devlogTitle(md string) string {
	for line := range strings.Lines(md) {
		if t := projectname.CleanLabel(strings.TrimLeft(strings.TrimSpace(line), "#")); t != "" {
			return t
		}
	}
	return untitled
}

// newItemsPart makes the owner's list of p, with each devlog's date in the
// account's time zone tz, and keptFor how long a removal is kept.
func newItemsPart(p Published, tz, keptFor string) *itemsPart {
	loc, err := rollup.LoadZone(tz)
	if err != nil {
		loc = time.UTC
	}
	part := &itemsPart{DeletePath: DeleteDevlogPath, RemovePath: RemoveBadgePath, KeptFor: keptFor}
	for _, d := range p.Devlogs {
		part.Devlogs = append(part.Devlogs, devlogRow{
			ID: d.ID, Title: devlogTitle(d.Markdown), Project: d.Project,
			Date: d.ReceivedAt.In(loc).Format("2 January 2006"), AI: d.AIAssisted,
		})
	}
	for _, b := range p.Badges {
		part.Badges = append(part.Badges, badgeRow{ID: b.ID, Name: BadgeName(b.Code)})
	}
	return part
}

// items reads the owner's list of account, whose days are in tz, or nil
// when d offers none: without Published, DeleteDevlog and RemoveBadge all
// three.
func (s *Site) items(ctx context.Context, d Dashboard, account, tz string) (*itemsPart, error) {
	if d.Published == nil || d.DeleteDevlog == nil || d.RemoveBadge == nil {
		return nil, nil
	}
	p, err := d.Published(ctx, account)
	if err != nil {
		return nil, err
	}
	return newItemsPart(p, tz, s.removedFor), nil
}

// registerTakeDown adds the list's two forms, POST DeleteDevlogPath and
// POST RemoveBadgePath. Each takes down one item of the signed-in account
// while the account holds it, as the zone and hide forms change their
// settings: only from this server's own pages, for the session's account,
// and back to the list after. It needs no fresh sign-in, since a form only
// takes down what the account published, and an id the account doesn't
// hold keeps nothing, so a form can't fill the account's removal records
// (orchestrator, 2026-11-24).
func registerTakeDown(mux *http.ServeMux, in SignIn, d Dashboard) {
	takeDown(mux, in, d, DeleteDevlogPath, d.DeleteDevlog, "devlog",
		"Deleting a devlog has to start from your dashboard.", "The devlog didn't delete. Try again in a minute.")
	takeDown(mux, in, d, RemoveBadgePath, d.RemoveBadge, "badge",
		"Removing a badge has to start from your dashboard.", "The badge didn't go. Try again in a minute.")
}

// takeDown adds POST path, which takes the form's id down with take.
// foreign is the refusal of a form from another site, and failed the
// answer when take fails.
func takeDown(mux *http.ServeMux, in SignIn, d Dashboard, path string, take func(ctx context.Context, account, id string) error, what, foreign, failed string) {
	mux.HandleFunc("POST "+path, func(w http.ResponseWriter, r *http.Request) {
		if !in.Enabled || d.Account == nil || take == nil {
			http.Redirect(w, r, DashboardPath, http.StatusSeeOther)
			return
		}
		if d.SameOrigin == nil || !d.SameOrigin(r) {
			http.Error(w, foreign, http.StatusForbidden)
			return
		}
		account, err := d.Account(r)
		switch {
		case errors.Is(err, dashboard.ErrNotSignedIn):
			http.Redirect(w, r, LoginURL(in.LoginPath, DashboardPath), http.StatusSeeOther)
			return
		case err != nil:
			d.Logger.Error("reading a session for the dashboard", "err", err)
			http.Error(w, failed, http.StatusInternalServerError)
			return
		}
		r.Body = http.MaxBytesReader(w, r.Body, zoneFormMax)
		if err := r.ParseForm(); err != nil {
			http.Error(w, "That form didn't arrive whole.", http.StatusBadRequest)
			return
		}
		id := r.PostForm.Get("id")
		if id == "" || len(id) > 128 {
			http.Error(w, "That form names no "+what+".", http.StatusBadRequest)
			return
		}
		// An id the account no longer holds, as when one of its computers
		// took the item down first, is already gone from the list.
		if err := take(r.Context(), account, id); err != nil {
			d.Logger.Error("taking down a "+what, "user", account, "err", err)
			http.Error(w, failed, http.StatusInternalServerError)
			return
		}
		http.Redirect(w, r, DashboardPath+"#published", http.StatusSeeOther)
	})
}
