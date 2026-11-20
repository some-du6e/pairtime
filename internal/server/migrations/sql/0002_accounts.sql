-- SPDX-License-Identifier: AGPL-3.0-or-later
--
-- Accounts and web sessions, written by package accounts at Hack Club Auth
-- sign-in (WP-SV2; DESIGN 3.10, 4.4).
--
-- users is one row per person, keyed for sign-in by hca_sub, the HCA
-- subject (SP 3.3). The email is AES-256-GCM ciphertext, never plain text
-- (DESIGN 4.4), and email_index is an HMAC of the lowercased address under
-- a key derived from the same secret, so the privacy hub can find a row by
-- email without decrypting every row (DESIGN 8.9). ysws_eligible is 1, 0,
-- or NULL when HCA didn't say. There is no birthdate: HCA decides
-- eligibility (DESIGN 8.2).
CREATE TABLE users (
    id TEXT PRIMARY KEY,
    hca_sub TEXT NOT NULL,
    slack_id TEXT,
    email_ct BYTEA,
    email_index TEXT,
    verification_status TEXT NOT NULL,
    ysws_eligible BIGINT,
    created_at_ms BIGINT NOT NULL,
    updated_at_ms BIGINT NOT NULL,
    signed_in_at_ms BIGINT NOT NULL
);

CREATE UNIQUE INDEX users_hca_sub ON users (hca_sub);

CREATE INDEX users_slack_id ON users (slack_id);

CREATE INDEX users_email_index ON users (email_index);

-- sessions is one row per signed-in browser. The cookie holds a random
-- token and the row holds only its SHA-256, so a copy of the database
-- signs nobody in. auth_time_ms is the ID token's auth_time, NULL when HCA
-- didn't say; for an ordinary sign-in it can be days old.
-- fresh_login_at_ms is set only by a sign-in that asked HCA to
-- authenticate the person again (prompt=login) and got an auth_time from
-- after it started. A submission freeze reads it, never auth_time_ms
-- (DESIGN 7.5 step 2).
CREATE TABLE sessions (
    id_hash TEXT PRIMARY KEY,
    user_id TEXT NOT NULL REFERENCES users (id),
    created_at_ms BIGINT NOT NULL,
    expires_at_ms BIGINT NOT NULL,
    auth_time_ms BIGINT,
    fresh_login_at_ms BIGINT,
    revoked_at_ms BIGINT
);

CREATE INDEX sessions_user_id ON sessions (user_id);

CREATE INDEX sessions_expires_at_ms ON sessions (expires_at_ms);
