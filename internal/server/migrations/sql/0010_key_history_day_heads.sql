-- SPDX-License-Identifier: AGPL-3.0-or-later
--
-- What the day roots need (WP-SV17): which server key was valid when, and
-- the day heads the publisher signs. Neither holds personal data.
--
-- key_history is every server key this database was written with and the
-- times it was valid, which /.well-known/pairtime-keys.json publishes
-- (DESIGN 10.2; docs/spec/keys-v1.md). server_keys (0007) holds only the
-- current key's id, in one row a rotation overwrites, so it can't say
-- which key signed a receipt or a day head. kind is "server" for
-- server.ed25519's keys; a later work package may add other kinds. key_id
-- is the key's id, as pairtimed keys show prints it, and public_key the
-- key in lowercase hex. valid_from_ms and valid_to_ms are the first and
-- the last millisecond the key is valid, Unix ms, and valid_to_ms is NULL
-- while the key is current. A row is never deleted: old keys stay
-- published so old receipts and day roots verify.
CREATE TABLE key_history (
    kind TEXT NOT NULL,
    key_id TEXT NOT NULL,
    public_key TEXT NOT NULL,
    valid_from_ms BIGINT NOT NULL,
    valid_to_ms BIGINT,
    recorded_at_ms BIGINT NOT NULL,
    PRIMARY KEY (kind, key_id)
);

-- day_heads is each UTC day's signed head (ledger-v1 8): day is d, the
-- days since 1970-01-01; n is count_d, the number of the day's witness
-- rows; root is T_d, prev is G_(d-1) and head is G_d, each lowercase hex;
-- signature is the Ed25519 signature over D_d in hex, by the server key
-- key_id names, the one valid at the day's last millisecond; and
-- written_at_ms is when the publisher stored the row. The publisher
-- resumes the day chain from here, never from the files under /witness,
-- which nothing backs up, and writes each day's file from its row.
CREATE TABLE day_heads (
    day BIGINT PRIMARY KEY,
    n BIGINT NOT NULL,
    root TEXT NOT NULL,
    prev TEXT NOT NULL,
    head TEXT NOT NULL,
    signature TEXT NOT NULL,
    key_id TEXT NOT NULL,
    written_at_ms BIGINT NOT NULL
);
