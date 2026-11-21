-- SPDX-License-Identifier: AGPL-3.0-or-later
--
-- The keys this database was written with (#133): the server key's id and
-- a fingerprint of the pepper, which pairtimed serve records on its first
-- start and compares on every later one. A host rebuilt or moved with new
-- keys then refuses to serve the old database, instead of signing for it
-- with a key no receipt names and keying projects with another pepper. It
-- holds one row, id 1. A key rotation records the new id here.
CREATE TABLE server_keys (
    id BIGINT PRIMARY KEY,
    server_key_id TEXT NOT NULL,
    pepper_fingerprint TEXT NOT NULL,
    recorded_at_ms BIGINT NOT NULL
);
