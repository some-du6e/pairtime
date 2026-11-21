-- SPDX-License-Identifier: AGPL-3.0-or-later
--
-- Signed ingest (WP-SV4; DESIGN 3.6, 3.10, 4.3, 4.5; docs/spec/ingest-v1.md
-- section 6), written by package ingest through the writer. No table here
-- has a foreign key: retention jobs delete from most of them, and the
-- tables they would point at can lose their rows first (docs/retention.json).
-- account_id is a users id; device_id is the device's opaque key. Every
-- row of a device names the account it was uploaded under, which no write
-- here changes, and ingest refuses a request whose device_id has rows
-- under another account (D-device-account-binding).
--
-- A device's request counter, chain head, chain start and local-only gaps
-- are on WP-SV3's devices row (0003), which ingest reads and writes only
-- through package device, so they go with the device
-- (D-device-row-chain-state).
--
-- ingest_replies is each device's most recent processed request: its
-- Idempotency-Key, the raw body's SHA-256 and the response, which a retry
-- of that request gets back byte for byte (ingest-v1 2.10). Without it a
-- retry is processed as a new request, which batch roots and bucket
-- revisions are idempotent for, so its rows may go on their own.
CREATE TABLE ingest_replies (
    device_id TEXT PRIMARY KEY,
    account_id TEXT NOT NULL,
    idempotency_key TEXT NOT NULL,
    body_sha256 TEXT NOT NULL,
    response TEXT NOT NULL,
    received_at_ms BIGINT NOT NULL
);

CREATE INDEX ingest_replies_received_at_ms ON ingest_replies (received_at_ms);

-- witness_rows is DESIGN 3.6's (device_id, b, n_b, R_b, H_b, received_at)
-- for each batch root ingest stored, with its prev and the receipt it
-- signed (ledger-v1 6, 7). Hashes and the signature are lowercase hex, and
-- receipt_key_id names the server key, as pairtimed keys show prints it.
CREATE TABLE witness_rows (
    device_id TEXT NOT NULL,
    b BIGINT NOT NULL,
    account_id TEXT NOT NULL,
    n BIGINT NOT NULL,
    root TEXT NOT NULL,
    head TEXT NOT NULL,
    prev TEXT NOT NULL,
    received_at_ms BIGINT NOT NULL,
    receipt_sig TEXT NOT NULL,
    receipt_key_id TEXT NOT NULL,
    PRIMARY KEY (device_id, b)
);

CREATE INDEX witness_rows_received_at_ms ON witness_rows (received_at_ms);

-- minute_buckets is each device's current revision of each minute, as the
-- JSON of the bucket ingest parsed (pkg/event's MinuteBucket.Shareable(),
-- agent_only included, D-bucket-agent-only), under the device owner's
-- account.
-- revisions counts the revisions of the minute ingest stored, this one
-- included, which ingest-v1 2.7 bounds.
CREATE TABLE minute_buckets (
    device_id TEXT NOT NULL,
    minute_ms BIGINT NOT NULL,
    account_id TEXT NOT NULL,
    rev BIGINT NOT NULL,
    state TEXT NOT NULL,
    received_at_ms BIGINT NOT NULL,
    bucket TEXT NOT NULL,
    revisions BIGINT NOT NULL,
    PRIMARY KEY (device_id, minute_ms)
);

CREATE INDEX minute_buckets_minute_ms ON minute_buckets (minute_ms);

CREATE INDEX minute_buckets_account_minute ON minute_buckets (account_id, minute_ms);

-- minute_bucket_revisions keeps every revision ingest stored, the current
-- one included, with its received_at, for the bundle's buckets.json (DESIGN
-- 7.8 as the decision E-check-readings words it). Each revision is a row of
-- its own, so storing one costs the same however many came before it.
CREATE TABLE minute_bucket_revisions (
    device_id TEXT NOT NULL,
    minute_ms BIGINT NOT NULL,
    rev BIGINT NOT NULL,
    account_id TEXT NOT NULL,
    received_at_ms BIGINT NOT NULL,
    bucket TEXT NOT NULL,
    PRIMARY KEY (device_id, minute_ms, rev)
);

CREATE INDEX minute_bucket_revisions_minute_ms ON minute_bucket_revisions (minute_ms);

-- commit_records is each device's commit records (DESIGN 4.3, 5.8) as
-- ingest parsed them, with when the server first had each one.
CREATE TABLE commit_records (
    device_id TEXT NOT NULL,
    sha TEXT NOT NULL,
    account_id TEXT NOT NULL,
    project TEXT NOT NULL,
    body TEXT NOT NULL,
    first_seen_at_ms BIGINT NOT NULL,
    updated_at_ms BIGINT NOT NULL,
    PRIMARY KEY (device_id, sha)
);

CREATE INDEX commit_records_account_id ON commit_records (account_id);

-- device_labels is each device's latest labels: client build, scoring
-- version, parameter hash, harness names and versions, and the typing
-- module and VM flags (DESIGN 4.3).
CREATE TABLE device_labels (
    device_id TEXT PRIMARY KEY,
    account_id TEXT NOT NULL,
    labels TEXT NOT NULL,
    received_at_ms BIGINT NOT NULL
);

CREATE INDEX device_labels_received_at_ms ON device_labels (received_at_ms);

-- request_origins is where each upload ingest processed came from: the IP
-- Cloudflare saw (CF-Connecting-IP) and its JA4 TLS fingerprint (CF-JA4)
-- when the plan sends one, for Fraud Squad's desk (DESIGN 3.10, 4.4; SP
-- 8.7). The retention job deletes each row 30 days after seen_at_ms.
CREATE TABLE request_origins (
    id TEXT PRIMARY KEY,
    device_id TEXT NOT NULL,
    account_id TEXT NOT NULL,
    ip TEXT NOT NULL,
    ja4 TEXT,
    seen_at_ms BIGINT NOT NULL
);

CREATE INDEX request_origins_seen_at_ms ON request_origins (seen_at_ms);

CREATE INDEX request_origins_device_id ON request_origins (device_id);
