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
-- the 32-byte Ed25519 key that signs its uploads (DESIGN 4.5), which the
-- app sent with the code its token came from, so a token works only with
-- it (D-key-at-grant). os_family is windows, macos, linux or other, and
-- client_build the app's version. linked_at_ms is when its token was
-- handed out. link_generation counts its links: 1 for the first, and one
-- more each time a login links it again with a new token (D-local-gap).
--
-- The ingest_ columns are ingest's state for the device, which goes with
-- the row (AGENTS.md, Retention; D-device-row-chain-state). Only package
-- ingest changes them, through device.Store's LoadState and SaveState
-- inside its own writer transaction. ingest_req is the counter of the last
-- request ingest accepted. ingest_head_b and ingest_head_hash are the b and
-- hex H_b of the chain's head, the last batch root ingest stored,
-- ingest_head_generation the link generation it was stored under, and
-- ingest_head_received_at_ms when it arrived. ingest_chain_start is the b
-- the chain started at, and ingest_gaps the batches the device sealed in
-- local-only mode between two links, which ingest never takes, as JSON
-- (D-local-gap). All of them are NULL until ingest first accepts a request
-- from the device.
--
-- Removing a device stops its token at once. A device ingest has nothing
-- from loses its row, which frees its device_id. Any other stays as a
-- tombstone that keeps its device_id with its account
-- (D-device-account-binding): removed_at_ms is set, and token_hash,
-- public_key, os_family, client_build, linked_at_ms and last_seen_at_ms go
-- NULL, so only the account, the removal time, the link generation and
-- ingest's state stay. The retention job deletes a tombstone 90 days after
-- the removal, and only once ingest's tables hold no row for its device_id
-- (docs/retention.json server.devices, kept_by). A new link sets them all
-- again. No table may have a foreign key to this one, since that job
-- deletes rows (AGENTS.md, Retention).
CREATE TABLE devices (
    device_id TEXT PRIMARY KEY,
    user_id TEXT NOT NULL REFERENCES users (id),
    token_hash TEXT,
    public_key BYTEA,
    os_family TEXT,
    client_build TEXT,
    linked_at_ms BIGINT,
    last_seen_at_ms BIGINT,
    removed_at_ms BIGINT,
    link_generation BIGINT NOT NULL,
    ingest_req BIGINT,
    ingest_head_b BIGINT,
    ingest_head_hash TEXT,
    ingest_head_generation BIGINT,
    ingest_head_received_at_ms BIGINT,
    ingest_chain_start BIGINT,
    ingest_gaps TEXT
);

CREATE UNIQUE INDEX devices_token_hash ON devices (token_hash);

CREATE INDEX devices_user_id ON devices (user_id);

CREATE INDEX devices_removed_at_ms ON devices (removed_at_ms);

-- device_codes is one row per device authorization request (RFC 8628 3.1).
-- device_code_hash is the hex SHA-256 of the device code the app polls
-- with, and user_code_hash an HMAC of the user code under a key that lives
-- only in the server's memory, so a copy of the database can't be searched
-- for a live user code. run_id names the pairtimed process that made the
-- code: another process can't use it, since that key died with its maker.
-- public_key is the device key the app sent, which the token will name
-- (D-key-at-grant). status is pending, approved, denied, taken or
-- redeemed; taken means an account other than the one the device id
-- belongs to tried to approve it. user_id is the account that approved,
-- denied or tried. interval_s starts at 5 and grows by 5 with each
-- slow_down (RFC 8628 3.5). A row is deleted a day after it expires
-- (docs/retention.json server.device_codes).
CREATE TABLE device_codes (
    device_code_hash TEXT PRIMARY KEY,
    user_code_hash TEXT NOT NULL,
    run_id TEXT NOT NULL,
    device_id TEXT NOT NULL,
    os_family TEXT NOT NULL,
    client_build TEXT NOT NULL,
    public_key BYTEA NOT NULL,
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
