// SPDX-License-Identifier: AGPL-3.0-or-later
// The owner's dashboard, live (WP-SV8; SP 6.4). The page works without
// this script; with it, the page follows GET /api/v1/stream and, on every
// event, fetches itself again and puts the server's new rendering of #live
// in place, so the numbers and the faded pending minutes are always the
// server's own. When the stream says nothing for 45 s, not even a
// keepalive, as when a proxy holds it back, the page polls the snapshot
// every 30 s with If-None-Match until the stream speaks again.
'use strict';
(() => {
	if (!document.getElementById('live') || !window.EventSource || !window.fetch || !window.DOMParser) {
		return;
	}
	const stream = '/api/v1/stream?topics=user:me';
	const snapshot = '/api/v1/users/me/dashboard';
	const silence = 45000;
	const every = 30000;
	// A refetch after a reconnect or a resync waits up to this long, so
	// every tab that a restarted server dropped doesn't ask at once.
	const spread = 5000;

	const live = () => document.getElementById('live');
	const mark = (state) => {
		live().dataset.stream = state;
	};

	let source = null;
	let heard = Date.now();
	let opens = 0;
	let polling = null;
	let polls = 0;
	let etag = live().dataset.etag || '';
	let busy = false;
	let again = false;
	let held = null;
	let stopped = false;

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
		mark('stopped');
	};

	// put swaps fresh, the new #live, in place, keeping which details
	// were open. While focus is inside #live, as on a button, it waits for
	// focus to leave, so nothing moves under the owner's hand.
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
	};
	document.addEventListener('focusout', (e) => {
		if (held && !live().contains(e.relatedTarget)) {
			put(held);
		}
	});

	// refresh fetches the page again and puts its #live in place. Calls
	// while one runs make one more run after it.
	const refresh = async () => {
		if (stopped) {
			return;
		}
		if (busy) {
			again = true;
			return;
		}
		busy = true;
		try {
			do {
				again = false;
				const r = await fetch(location.pathname, {
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
					return;
				}
				const fresh = new DOMParser().parseFromString(await r.text(), 'text/html').getElementById('live');
				if (fresh) {
					put(fresh);
				}
			} while (again && !stopped);
		} catch {
			// The network failed; the next event or poll tries again.
		} finally {
			busy = false;
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

	// poll asks the snapshot whether anything changed since the page's
	// ETag, and refreshes the page when it did.
	const poll = async () => {
		polls++;
		if (source && source.readyState === EventSource.CLOSED && polls % 10 === 0) {
			// A stream the server refused, such as past a cap, gets a new
			// try every five minutes.
			connect();
		}
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
				etag = r.headers.get('ETag') || etag;
				refresh();
			}
		} catch {
			// The next poll tries again.
		}
	};

	// heardFrom notes that the stream spoke, which ends any polling.
	const heardFrom = () => {
		heard = Date.now();
		if (polling !== null) {
			clearInterval(polling);
			polling = null;
		}
		mark('open');
	};

	const connect = () => {
		if (source) {
			source.close();
		}
		const es = new EventSource(stream);
		source = es;
		es.addEventListener('open', () => {
			heardFrom();
			// The first open fetches what changed since the page was
			// drawn; a reconnect, what changed while it was away.
			opens++;
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
			if (es === source && es.readyState === EventSource.CLOSED) {
				mark('closed');
			}
		});
	};

	setInterval(() => {
		if (stopped || polling !== null || Date.now() - heard < silence) {
			return;
		}
		mark('polling');
		polls = 0;
		poll();
		polling = setInterval(poll, every);
		// A stream that went quiet may have died without a word: a new
		// one ends the polling as soon as it gets through.
		connect();
	}, 5000);
	connect();
})();
