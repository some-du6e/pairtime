// SPDX-License-Identifier: AGPL-3.0-or-later
// The owner's dashboard, live (WP-SV8; SP 6.4). The page works without
// this script; with it, the page follows GET /api/v1/stream and, on every
// event, fetches the dashboard again and puts the server's new rendering
// of #live in place, so the numbers and the faded pending minutes are
// always the server's own. When the stream says nothing for 45 s, not even
// a keepalive, as when a proxy holds it back, the page polls the snapshot
// every 30 s with If-None-Match until the stream speaks again. It also
// fetches the dashboard again at the owner's next midnight, so a page left
// open turns its day.
'use strict';
(() => {
	if (!document.getElementById('live') || !window.EventSource || !window.fetch || !window.DOMParser) {
		return;
	}
	// The dashboard's own path: the page may be the time zone form's answer
	// at /dashboard/zone, which takes only a POST.
	const page = '/dashboard';
	const stream = '/api/v1/stream?topics=user:me';
	const snapshot = '/api/v1/users/me/dashboard';
	const silence = 45000;
	const every = 30000;
	// A refetch after a reconnect or a resync waits up to spread, and the
	// one at midnight up to midnightSpread, so the tabs that a restarted
	// server dropped, or that one midnight turns, don't all ask at once.
	const spread = 5000;
	const midnightSpread = 60000;
	// A stream the server refused, or that couldn't connect, as while a
	// restarted server comes back behind its proxy, is tried again after a
	// wait that starts at retryFirst and doubles up to retryMost, with as
	// much again at random.
	const retryFirst = 5000;
	const retryMost = 150000;

	const live = () => document.getElementById('live');
	const mark = (state) => {
		live().dataset.stream = state;
	};

	let source = null;
	let heard = Date.now();
	let opens = 0;
	let polling = null;
	let etag = live().dataset.etag || '';
	let busy = false;
	let again = false;
	let failed = false;
	let held = null;
	let stopped = false;
	let retry = 0;
	let retryTimer = null;
	let midnight = null;

	// stop ends everything once the page is signed out: the page itself
	// then sends the owner to sign in.
	const stop = () => {
		stopped = true;
		if (source) {
			source.close();
		}
		if (polling !== null) {
			clearInterval(polling);
			polling = null;
		}
		clearTimeout(retryTimer);
		clearTimeout(midnight);
		mark('stopped');
	};

	// atMidnight fetches the page again when the day #live shows ends in
	// the owner's time zone, as #live says.
	const atMidnight = () => {
		clearTimeout(midnight);
		const next = Number(live().dataset.nextDay);
		if (!stopped && next > 0) {
			midnight = setTimeout(() => {
				refresh();
			}, Math.max(0, next - Date.now()) + Math.random() * midnightSpread);
		}
	};

	// put swaps fresh, the new #live, in place, keeping which details
	// were open, and takes its ETag, the one of the View it shows. While
	// focus is inside #live, as on a button, it waits for focus to leave,
	// so nothing moves under the owner's hand.
	const put = (fresh) => {
		const current = live();
		if (current.contains(document.activeElement)) {
			held = fresh;
			return;
		}
		held = null;
		const open = Array.from(current.querySelectorAll('details'), (d) => d.open);
		fresh.querySelectorAll('details').forEach((d, i) => {
			d.open = Boolean(open[i]);
		});
		fresh.dataset.stream = current.dataset.stream || '';
		current.replaceWith(document.adoptNode(fresh));
		etag = fresh.dataset.etag || etag;
		atMidnight();
	};
	document.addEventListener('focusout', (e) => {
		if (held && !live().contains(e.relatedTarget)) {
			put(held);
		}
	});

	// refresh fetches the dashboard again and puts its #live in place.
	// Calls while one runs make one more run after it. A fetch that fails
	// is tried once more a few seconds later.
	const refresh = async () => {
		if (stopped) {
			return;
		}
		if (busy) {
			again = true;
			return;
		}
		busy = true;
		let ok = false;
		try {
			do {
				again = false;
				ok = false;
				const r = await fetch(page, {
					credentials: 'same-origin',
					cache: 'no-store',
					redirect: 'manual',
					headers: { Accept: 'text/html' },
				});
				if (r.type === 'opaqueredirect' || r.status === 401) {
					stop();
					return;
				}
				if (!r.ok) {
					break;
				}
				const fresh = new DOMParser().parseFromString(await r.text(), 'text/html').getElementById('live');
				if (fresh) {
					put(fresh);
				}
				ok = true;
			} while (again && !stopped);
		} catch {
			// The network failed.
		} finally {
			busy = false;
		}
		if (ok) {
			failed = false;
		} else if (!failed && !stopped) {
			failed = true;
			refreshSoon(2000 + Math.random() * 3000);
		}
	};
	// refreshSoon runs refresh after wait, unless one is already due by
	// then: a burst of events, or an open and the gap it replays, makes
	// one fetch, and one more at most if events come while it runs.
	let due = null;
	let timer = null;
	const refreshSoon = (wait) => {
		const at = Date.now() + wait;
		if (timer !== null && due <= at) {
			return;
		}
		clearTimeout(timer);
		due = at;
		timer = setTimeout(() => {
			timer = null;
			refresh();
		}, wait);
	};

	// poll asks the snapshot whether anything changed since the View the
	// page shows, by its ETag, and refreshes the page when it did. The ETag
	// changes only when the page is redrawn, so a refetch that fails makes
	// the next poll ask again.
	const poll = async () => {
		try {
			const headers = { Accept: 'application/json' };
			if (etag) {
				headers['If-None-Match'] = etag;
			}
			const r = await fetch(snapshot, { credentials: 'same-origin', cache: 'no-store', headers });
			if (r.status === 304) {
				return;
			}
			if (r.status === 401) {
				stop();
				return;
			}
			if (r.ok) {
				refresh();
			}
		} catch {
			// The next poll tries again.
		}
	};

	// heardFrom notes that the stream spoke, an event or a keepalive,
	// which ends any polling. An open alone doesn't: a proxy that holds the
	// stream back passes its headers on all the same.
	const heardFrom = () => {
		heard = Date.now();
		if (polling !== null) {
			clearInterval(polling);
			polling = null;
		}
		mark('open');
	};

	const connect = () => {
		clearTimeout(retryTimer);
		if (source) {
			source.close();
		}
		const es = new EventSource(stream);
		source = es;
		es.addEventListener('open', () => {
			retry = 0;
			opens++;
			if (polling !== null) {
				// Polling goes on until the stream says something.
				return;
			}
			heard = Date.now();
			mark('open');
			// The first open fetches what changed since the page was
			// drawn; a reconnect, what changed while it was away.
			refreshSoon(opens === 1 ? 0 : Math.random() * spread);
		});
		es.addEventListener('credited', () => {
			heardFrom();
			refreshSoon(0);
		});
		es.addEventListener('resync', () => {
			heardFrom();
			refreshSoon(Math.random() * spread);
		});
		es.addEventListener('keepalive', heardFrom);
		es.addEventListener('error', () => {
			if (es !== source || stopped || es.readyState !== EventSource.CLOSED) {
				// EventSource tries again itself after a dropped connection.
				return;
			}
			// A refused answer, such as a proxy's 502 while the server
			// restarts, ends EventSource for good: try again after a while.
			mark('closed');
			retry = retry ? Math.min(retry * 2, retryMost) : retryFirst;
			retryTimer = setTimeout(connect, retry + Math.random() * retry);
		});
	};

	setInterval(() => {
		if (stopped || polling !== null || Date.now() - heard < silence) {
			return;
		}
		mark('polling');
		poll();
		polling = setInterval(poll, every);
		// A stream that went quiet may have died without a word: a new one
		// ends the polling as soon as it says something.
		connect();
	}, 5000);
	atMidnight();
	connect();
})();
