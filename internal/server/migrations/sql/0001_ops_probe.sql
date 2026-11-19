-- SPDX-License-Identifier: AGPL-3.0-or-later
--
-- The row /readyz writes through the writer goroutine to show the database
-- takes writes (DESIGN 10.4). It holds one row, id 1, which
-- store.WriteProbe upserts.
CREATE TABLE ops_probe (
    id BIGINT PRIMARY KEY,
    written_at_ms BIGINT NOT NULL
);
