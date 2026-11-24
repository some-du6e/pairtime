-- SPDX-License-Identifier: AGPL-3.0-or-later
--
-- The privacy hub's single-use tokens (WP-SV13; DESIGN 8.9;
-- D-sv13-jti). privacy_token_uses holds the SHA-256 of the jti of each
-- token pairtimed has taken, recorded in a transaction that commits
-- before the request acts, so a replay is refused even when the first
-- use failed and even after pairtimed restarts inside the token's
-- lifetime. jti_sha256 is the hex SHA-256 of the jti: the hub's jti
-- starts with the request_id and names the action, and its hash keeps
-- neither. pairtimed has held the jti to 1 to 128 bytes of printable
-- ASCII before it hashes it. exp_ms is the token's exp, Unix ms. A row
-- stays at least an hour past exp plus the 30 s leeway, so no request's
-- clock can prune the row of a token another request has just checked,
-- and goes in the transaction that records the next request's jti. A
-- row is a hash of the privacy hub's token id and its expiry, with no
-- account, subject, request_id or action, kept at least an hour past
-- exp plus the 30 s leeway, until the next request, so
-- docs/retention.json lists the table under no_personal_data.
CREATE TABLE privacy_token_uses (
    jti_sha256 TEXT PRIMARY KEY,
    exp_ms BIGINT NOT NULL
);
