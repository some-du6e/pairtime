// SPDX-License-Identifier: AGPL-3.0-or-later

// How an element looks, read from computed styles, so the checks go by what
// the teen sees rather than by class names or attributes.

export type RGB = [number, number, number];

export interface Look {
	text: string;
	/** The element's own background, composited over everything behind it. */
	fill: RGB;
	/** What the element sits on: its parent's background, composited the same way. */
	behind: RGB;
	/** The text color, composited over the fill. */
	color: RGB;
	/** The computed styles that make up a button's visual weight. */
	styles: Record<string, string>;
	width: number;
	height: number;
	outline: { style: string; width: number };
}

/**
 * Reads el's look. It runs in the page through locator.evaluate, so it uses
 * nothing from outside its own body.
 */
export function look(el: Element): Look {
	const weight = [
		'background-color',
		'color',
		'border-top-color',
		'border-right-color',
		'border-bottom-color',
		'border-left-color',
		'border-top-style',
		'border-top-width',
		'border-right-width',
		'border-bottom-width',
		'border-left-width',
		'border-top-left-radius',
		'border-bottom-right-radius',
		'box-shadow',
		'filter',
		'opacity',
		'font-family',
		'font-size',
		'font-style',
		'font-weight',
		'letter-spacing',
		'line-height',
		'text-decoration-line',
		'text-transform',
		'padding-top',
		'padding-right',
		'padding-bottom',
		'padding-left',
	];
	type RGBA = [number, number, number, number];
	const rgba = (color: string): RGBA => {
		const m = /^rgba?\(([^)]*)\)$/.exec(color.trim());
		if (!m || m[1] === undefined) {
			throw new Error(`can't read the color ${color}`);
		}
		const p = m[1]
			.split(/[\s,/]+/)
			.filter((s) => s !== '')
			.map(Number);
		return [p[0] ?? 0, p[1] ?? 0, p[2] ?? 0, p[3] ?? 1];
	};
	const over = (top: RGBA, under: RGB): RGB => [
		top[0] * top[3] + under[0] * (1 - top[3]),
		top[1] * top[3] + under[1] * (1 - top[3]),
		top[2] * top[3] + under[2] * (1 - top[3]),
	];
	const fillOf = (node: Element | null): RGB => {
		const layers: RGBA[] = [];
		for (let n = node; n; n = n.parentElement) {
			layers.push(rgba(getComputedStyle(n).backgroundColor));
		}
		// The page under <html> is white; every pairtime page paints <body>.
		let rgb: RGB = [255, 255, 255];
		for (let i = layers.length - 1; i >= 0; i--) {
			rgb = over(layers[i] as RGBA, rgb);
		}
		return rgb;
	};
	const s = getComputedStyle(el);
	const styles: Record<string, string> = {};
	for (const p of weight) {
		styles[p] = s.getPropertyValue(p);
	}
	const fill = fillOf(el);
	const box = el.getBoundingClientRect();
	return {
		text: (el.textContent ?? '').trim(),
		fill,
		behind: fillOf(el.parentElement),
		color: over(rgba(s.color), fill),
		styles,
		width: box.width,
		height: box.height,
		outline: { style: s.outlineStyle, width: parseFloat(s.outlineWidth) },
	};
}

/** WCAG 2.2's contrast ratio between two colors. */
export function contrast(a: RGB, b: RGB): number {
	const channel = (c: number) => {
		const v = c / 255;
		return v <= 0.04045 ? v / 12.92 : ((v + 0.055) / 1.055) ** 2.4;
	};
	const luminance = ([r, g, b]: RGB) => 0.2126 * channel(r) + 0.7152 * channel(g) + 0.0722 * channel(b);
	const [x, y] = [luminance(a), luminance(b)];
	return (Math.max(x, y) + 0.05) / (Math.min(x, y) + 0.05);
}

/**
 * How far a button's fill stands out from what it sits on. The
 * primary-styled button of a group is the one that scores highest; a
 * transparent button scores 1.
 */
export function prominence(l: Look): number {
	return contrast(l.fill, l.behind);
}
