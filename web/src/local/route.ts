// SPDX-License-Identifier: AGPL-3.0-or-later

// The dashboard's pages live in the URL's fragment, #/today and so on, so
// the agent serves one index.html and every page works after a reload.

export type Page = 'today' | 'projects' | 'privacy' | 'settings';

/** The span of days a page covers: this week, or the last 30 days. */
export type Range = 'week' | 'month';

export interface Route {
	page: Page;
	range: Range;
}

export const home: Route = { page: 'today', range: 'week' };

/** The pages in the order the name bar lists them. */
export const pages: { page: Page; label: string }[] = [
	{ page: 'today', label: 'Today' },
	{ page: 'projects', label: 'Projects' },
	{ page: 'privacy', label: 'Privacy' },
	{ page: 'settings', label: 'Settings' },
];

/** The fragment of a route. */
export function href(route: Route): string {
	if (route.page === 'projects' && route.range === 'month') {
		return '#/projects/30-days';
	}
	return `#/${route.page}`;
}

/**
 * The route a fragment names. An empty one is Today; one that names no page,
 * such as the skip link's #main, is undefined, so following it changes no
 * page.
 */
export function parseRoute(hash: string): Route | undefined {
	switch (hash) {
		case '':
		case '#':
		case '#/':
		case '#/today':
			return home;
		case '#/projects':
			return { page: 'projects', range: 'week' };
		case '#/projects/30-days':
			return { page: 'projects', range: 'month' };
		case '#/privacy':
			return { page: 'privacy', range: 'week' };
		case '#/settings':
			return { page: 'settings', range: 'week' };
		default:
			return undefined;
	}
}

/** The page's name, for the document title. */
export function titleOf(route: Route): string {
	return pages.find((p) => p.page === route.page)?.label ?? 'Today';
}
