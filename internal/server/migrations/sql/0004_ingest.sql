-- SPDX-License-Identifier: AGPL-3.0-or-later
--
-- Signed ingest (WP-SV4; DESIGN 3.6, 3.10, 4.3, 4.5; docs/spec/ingest-v1.md
-- section 6), written by package ingest through the writer. No table here
-- has a foreign key: retention jobs delete from most of them, and the
-- tables they would point at can lose their rows first (docs/retention.json).
-- account_id is a users id; device_id is the device's opaque key.
--
-- ingest_devices is ingest's own state per device: the request counter,
-- the head of the witnessed chain and the batch it started at, and the most
-- recent processed request's Idempotency-Key, body hash and response, which
-- a retry of that request gets back byte for byte (ingest-v1 2.10). WP-SV3's
-- devices table is never written here.
CREATE TABLE ingest_devices (
    device_id TEXT PRIMARY KEY,
    account_id TEXT NOT NULL,
    req BIGINT NOT NULL,
    head_b BIGINT,
    head_hash TEXT,
    head_received_at_ms BIGINT,
    chain_start BIGINT,
    last_key TEXT,
    last_body_sha256 TEXT,
    last_response TEXT,
    updated_at_ms BIGINT NOT NULL
);

CREATE INDEX ingest_devices_updated_at_ms ON ingest_devices (updated_at_ms);

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
-- JSON of pkg/event's MinuteBucket.Shareable() that ingest parsed, under
-- the device owner's account. earlier is a JSON array of the revisions it
-- replaced, oldest first, each {"rev", "received_at_ms", "bucket"}, for the
-- bundle's buckets.json (E-check-readings).
CREATE TABLE minute_buckets (
    device_id TEXT NOT NULL,
    minute_ms BIGINT NOT NULL,
    account_id TEXT NOT NULL,
    rev BIGINT NOT NULL,
    state TEXT NOT NULL,
    received_at_ms BIGINT NOT NULL,
    bucket TEXT NOT NULL,
    earlier TEXT NOT NULL,
    PRIMARY KEY (device_id, minute_ms)
);

CREATE INDEX minute_buckets_minute_ms ON minute_buckets (minute_ms);

CREATE INDEX minute_buckets_account_minute ON minute_buckets (account_id, minute_ms);

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
