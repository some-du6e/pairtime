-- SPDX-License-Identifier: AGPL-3.0-or-later
--
-- Linked devices and the device grant that links them, written by package
-- device (WP-SV3; DESIGN 3.10, 4.4; docs/spec/device-auth-v1.md).
--
-- devices is one row per installed pairtime app linked to an account.
-- device_id is the app's own ledger chain id, random and made on the
-- device, so the receipts and day leaves the server signs name the chain
-- its records came from (ledger-v1 5, 7). token_hash is the hex SHA-256 of
-- the device's ptd_ token; the token itself is never stored. public_key is
-- the 32-byte Ed25519 key that signs its uploads (DESIGN 4.5), NULL until
-- the app registers it. os_family is windows, macos, linux or other, and
-- client_build the app's version. removed_at_ms is set when the device is
-- removed, which stops its token at once; the retention job deletes the row
-- 90 days later (docs/retention.json server.devices). No table may have a
-- foreign key to this one, since that job deletes rows (AGENTS.md,
-- Retention).
CREATE TABLE devices (
    device_id TEXT PRIMARY KEY,
    user_id TEXT NOT NULL REFERENCES users (id),
    token_hash TEXT NOT NULL,
    public_key BYTEA,
    os_family TEXT NOT NULL,
    client_build TEXT NOT NULL,
    created_at_ms BIGINT NOT NULL,
    last_seen_at_ms BIGINT NOT NULL,
    key_registered_at_ms BIGINT,
    removed_at_ms BIGINT
);

CREATE UNIQUE INDEX devices_token_hash ON devices (token_hash);

CREATE INDEX devices_user_id ON devices (user_id);

CREATE INDEX devices_removed_at_ms ON devices (removed_at_ms);

-- device_codes is one row per device authorization request (RFC 8628 3.1).
-- device_code_hash is the hex SHA-256 of the device code the app polls
-- with, and user_code_hash an HMAC of the user code under a key that lives
-- only in the server's memory, so a copy of the database can't be searched
-- for a live user code. status is pending, approved, denied or redeemed;
-- user_id is the account that approved or denied it. interval_s starts at 5
-- and grows by 5 with each slow_down (RFC 8628 3.5). A row is deleted a day
-- after it expires (docs/retention.json server.device_codes).
CREATE TABLE device_codes (
    device_code_hash TEXT PRIMARY KEY,
    user_code_hash TEXT NOT NULL,
    device_id TEXT NOT NULL,
    os_family TEXT NOT NULL,
    client_build TEXT NOT NULL,
    status TEXT NOT NULL,
    user_id TEXT REFERENCES users (id),
    created_at_ms BIGINT NOT NULL,
    expires_at_ms BIGINT NOT NULL,
    interval_s BIGINT NOT NULL,
    last_poll_at_ms BIGINT,
    decided_at_ms BIGINT
);

CREATE UNIQUE INDEX device_codes_user_code_hash ON device_codes (user_code_hash);

CREATE INDEX device_codes_expires_at_ms ON device_codes (expires_at_ms);
