-- SPDX-License-Identifier: AGPL-3.0-or-later
--
-- The display name of a sign-in (D-display-name; DESIGN 4.4), written by
-- package accounts: the ID token's given_name, or its nickname without
-- one, cleaned by package auth and at most 64 characters, or NULL when HCA
-- sent neither. The pages show it as who is signed in. It is kept with the
-- session's row only, never in users, so it goes when that row does, a day
-- after the session ends, or at once when HCA turns the account ineligible.
ALTER TABLE sessions ADD COLUMN display_name TEXT;
