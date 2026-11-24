// SPDX-License-Identifier: AGPL-3.0-or-later

package dashboard

import (
	"context"

	"github.com/some-du6e/pairtime/internal/server/projectnames"
	"github.com/some-du6e/pairtime/internal/server/rollup"
	"github.com/some-du6e/pairtime/pkg/projectname"
)

// ProjectNames returns what the dashboard calls each of the account's
// projects that has a device key, by account-level project id
// (D-account-project-id), as View calls the projects it lists: the name its
// owner turned on (D-owner-names), or else projectname.CodeName of its
// code, which is unique among all the account's projects. The owner's list
// of devlogs names their projects by it, a devlog's project with no time in
// the last MonthDays days included. NameOf names an id it lacks.
func ProjectNames(ctx context.Context, q rollup.Querier, account string) (map[string]string, error) {
	keys, err := rollup.Projects(ctx, q, account)
	if err != nil {
		return nil, err
	}
	labels, err := projectnames.OfAccount(ctx, q, account)
	if err != nil {
		return nil, err
	}
	names := make(map[string]string, len(keys))
	for id, code := range projectname.ProjectCodes(keys) {
		names[id] = projectname.CodeName(code)
	}
	for id, label := range labels {
		if label != "" {
			names[id] = label
		}
	}
	return names, nil
}

// NameOf returns what names, ProjectNames' answer, calls project id, and
// for an id it lacks what View calls it: No project for none, and
// otherwise Project and the id's own code, as View calls a project nothing
// maps.
func NameOf(names map[string]string, id string) string {
	switch {
	case names[id] != "":
		return names[id]
	case id == "":
		return noProject
	}
	return projectname.CodeName(projectname.ShortCodes([]string{id})[id])
}
