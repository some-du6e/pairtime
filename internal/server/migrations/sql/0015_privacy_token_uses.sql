-- SPDX-License-Identifier: AGPL-3.0-or-later
--
-- The privacy hub's single-use tokens (WP-SV13; DESIGN 8.9;
-- D-sv13-jti). privacy_token_uses holds the jti of each token pairtimed
-- has taken, recorded in a transaction that commits before the request
-- acts, so a replay is refused even when the first use failed and even
-- after pairtimed restarts inside the token's lifetime. exp_ms is the
-- token's exp, Unix ms. A row goes once its token can no longer verify,
-- 30 s past exp, in the transaction that records the next jti. A row is
-- a random token id and an expiry, with no account, subject, request_id
-- or action, so docs/retention.json lists the table under
-- no_personal_data.
CREATE TABLE privacy_token_uses (
    jti TEXT PRIMARY KEY,
    exp_ms BIGINT NOT NULL
);
