-- SPDX-License-Identifier: AGPL-3.0-or-later
--
-- Rollups (WP-SV7; DESIGN 3.10; SM 3.4, 5.3), written by package rollup
-- inside the writer transaction that stores each minute-bucket revision,
-- and read by the owner's dashboard. Times are milliseconds: a slot credits
-- 0 to 15 seconds in steps of 0.5, which whole milliseconds hold exactly.
-- user_id is the account's users id.
--
-- rollup_projects gives each project key a device uploads the
-- account-level project id the rollups count it under
-- (D-account-project-id). A device's project keys are HMACs under its own
-- secret, so one repo has a different key on each computer. Until project
-- registration (WP-SV10) links the keys of one repo to one project_id,
-- each (device, key) has a project_id of its own, a UUIDv7. A row stays
-- as long as the account, as rollups_daily, which names its project_id,
-- does, so a key used again after a long gap keeps its project, and
-- registration can always link it. The commit records ingest keeps for
-- the account's lifetime hold the same pairs of device and key.
CREATE TABLE rollup_projects (
    user_id TEXT NOT NULL REFERENCES users (id),
    device_id TEXT NOT NULL,
    project_key TEXT NOT NULL,
    project_id TEXT NOT NULL,
    PRIMARY KEY (user_id, device_id, project_key)
);

-- rollup_minutes is each device's latest revision of each minute, as the
-- rollups applied it: its state, its four slots as JSON (project id,
-- credited milliseconds, activity kind, evidence level), the agent's time
-- on its own as a JSON object of milliseconds by project id (the bucket's
-- agent_only, D-bucket-agent-only), and the local day the minute counts
-- toward. Apply merges an account's devices from these rows, one human
-- minute per wall-clock minute (SM 3.4), so the rollups come out the same
-- whatever order the revisions arrive in. A row still pending
-- PENDING_WINDOW_S after its minute ended is settled as final by the
-- server's clock (D-server-settles-pending), and rollup_minutes_pending
-- finds those rows. Nothing stores concurrent_devices: a submission
-- computes it from these rows when it is frozen, over the submitted
-- window (D-no-ingest-flags). Its retention job deletes rows with the
-- minute buckets they mirror (docs/retention.json server.minute_buckets),
-- so no table points at it.
CREATE TABLE rollup_minutes (
    user_id TEXT NOT NULL REFERENCES users (id),
    minute_ms BIGINT NOT NULL,
    device_id TEXT NOT NULL,
    rev BIGINT NOT NULL,
    state TEXT NOT NULL,
    slots TEXT NOT NULL,
    agent_only TEXT NOT NULL,
    day TEXT NOT NULL,
    PRIMARY KEY (user_id, minute_ms, device_id)
);

CREATE INDEX rollup_minutes_minute_ms ON rollup_minutes (minute_ms);

CREATE INDEX rollup_minutes_pending ON rollup_minutes (minute_ms) WHERE state = 'pending';

-- rollups_daily is an account's time per project id, local day
-- (YYYY-MM-DD in the account's time zone) and activity kind: credited
-- milliseconds by evidence level, with pending minutes kept apart from
-- final ones. The row whose activity_kind is empty holds the agent's time
-- on its own in that project and day, the chess clock's other side (SM
-- 2.6), which has no kind or level. Apply and the settle job move it by
-- additive upserts; it stays as long as the account.
CREATE TABLE rollups_daily (
    user_id TEXT NOT NULL REFERENCES users (id),
    project_id TEXT NOT NULL,
    day TEXT NOT NULL,
    activity_kind TEXT NOT NULL,
    final_a_ms BIGINT NOT NULL DEFAULT 0,
    final_b_ms BIGINT NOT NULL DEFAULT 0,
    final_c_ms BIGINT NOT NULL DEFAULT 0,
    pending_a_ms BIGINT NOT NULL DEFAULT 0,
    pending_b_ms BIGINT NOT NULL DEFAULT 0,
    pending_c_ms BIGINT NOT NULL DEFAULT 0,
    agent_only_final_ms BIGINT NOT NULL DEFAULT 0,
    agent_only_pending_ms BIGINT NOT NULL DEFAULT 0,
    PRIMARY KEY (user_id, project_id, day, activity_kind)
);

CREATE INDEX rollups_daily_user_day ON rollups_daily (user_id, day);

-- tz is the account's IANA time zone, which decides where its days start.
-- It is UTC until the owner sets one on the dashboard.
ALTER TABLE users ADD COLUMN tz TEXT NOT NULL DEFAULT 'UTC';
