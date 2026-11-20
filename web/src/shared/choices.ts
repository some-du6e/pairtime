// SPDX-License-Identifier: AGPL-3.0-or-later

// The answers the shared decision components report. Callers store and act
// on them; the components only ask.

/**
 * A teen's answer on a consent card (DESIGN 8.4). "not_now" is a refusal,
 * which isn't asked again for a calendar year (WP-UI3).
 */
export type ConsentAnswer = 'yes' | 'not_now';

/**
 * How an instance asks about the public profile (DESIGN 8.3):
 * `profile_default = "public"`, the code default, shows two choices of equal
 * weight; "ask" makes "Private profile" the more prominent one.
 */
export type ProfileMode = 'public' | 'ask';

/** What the teen chose on the profile card. */
export type ProfileVisibility = 'public' | 'private';
