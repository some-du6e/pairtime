-- SPDX-License-Identifier: AGPL-3.0-or-later
--
-- The server's own witness label for each minute-bucket revision ingest
-- stores (WP-SV6; DESIGN 7.4; ingest-v1 7 item 13): witnessed, offline or
-- backfilled, which package witness gives from when the revision arrived.
-- The bucket's JSON keeps the client's reading as sent. A release from
-- before this migration stores a revision without a label, NULL, and when
-- it runs on this database after a rollback it leaves the replaced
-- revision's label on the minute's row. So ingest reads a revision's
-- label from its minute_bucket_revisions row alone.
ALTER TABLE minute_buckets ADD COLUMN server_witness TEXT;

ALTER TABLE minute_bucket_revisions ADD COLUMN server_witness TEXT;

-- Package witness looks up the first batch the server received from a
-- device after a minute ended, to tell whether a late minute came in the
-- catch-up after an outage.
CREATE INDEX witness_rows_device_received_at ON witness_rows (device_id, received_at_ms, b);
