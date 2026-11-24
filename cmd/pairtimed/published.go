// SPDX-License-Identifier: AGPL-3.0-or-later

package main

import (
	"context"
	"time"

	"github.com/some-du6e/pairtime/internal/server/dashboard"
	"github.com/some-du6e/pairtime/internal/server/devlog"
	"github.com/some-du6e/pairtime/internal/server/pages"
	"github.com/some-du6e/pairtime/internal/server/rollup"
	"github.com/some-du6e/pairtime/internal/server/writer"
)

// publishedOf reads what the account published from its computers, for
// the list on its dashboard that takes each item down (WP-SV12 part 2):
// its devlogs, each with its project called as the dashboard calls it
// (dashboard.NameOf), and the badges it shows.
func publishedOf(ctx context.Context, q rollup.Querier, account string) (pages.Published, error) {
	devlogs, err := devlog.List(ctx, q, account)
	if err != nil {
		return pages.Published{}, err
	}
	badges, err := devlog.Badges(ctx, q, account)
	if err != nil {
		return pages.Published{}, err
	}
	names, err := dashboard.ProjectNames(ctx, q, account)
	if err != nil {
		return pages.Published{}, err
	}
	var p pages.Published
	for _, d := range devlogs {
		p.Devlogs = append(p.Devlogs, pages.PublishedDevlog{
			ID: d.ID, Project: dashboard.NameOf(names, d.Project), Markdown: d.Markdown, AIAssisted: d.AIAssisted, ReceivedAt: d.ReceivedAt,
		})
	}
	for _, b := range badges {
		p.Badges = append(p.Badges, pages.PublishedBadge{ID: b.ID, Code: b.Code})
	}
	return p, nil
}

// takeDownOp runs one of the dashboard's take-downs, devlog.DeleteHeld or
// devlog.RemoveHeld, of the account's id through the writer, at the time
// its op runs: the item's row and its removal record.
func takeDownOp(ctx context.Context, w rollup.Doer, op func(context.Context, writer.Tx, string, string, int64) (bool, error), account, id string) error {
	return w.Do(ctx, 2, func(ctx context.Context, tx writer.Tx) error {
		_, err := op(ctx, tx, account, id, time.Now().UnixMilli())
		return err
	})
}
