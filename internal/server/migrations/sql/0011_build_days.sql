-- SPDX-License-Identifier: AGPL-3.0-or-later
--
-- Build days, devlogs and badges (WP-SV12; DESIGN 3.10, 4.4, 6.2 D10 and
-- D13; docs/errata/2026-11-23-build-day-lag.md and
-- docs/errata/2026-11-23-build-days-devlogs-retention.md).
--
-- build_days is each local day the build-day job (package buildday) has
-- decided, by its date in the profile's time zone: its credited
-- milliseconds at levels A and B as D10 decides them, from the minute-bucket
-- revisions the server itself labeled witnessed or offline, whether that
-- made it a build day (counted, 1 or 0), and when that was decided. A day is
-- stored the moment it counts, or at its deadline when it doesn't, and
-- nothing changes a row once it is there. The rows stay as long as the
-- account (server.build_days), after the minute buckets they were read from
-- have gone at 180 days.
CREATE TABLE build_days (
    user_id TEXT NOT NULL REFERENCES users (id),
    day TEXT NOT NULL,
    a_ms BIGINT NOT NULL,
    b_ms BIGINT NOT NULL,
    counted BIGINT NOT NULL,
    decided_at_ms BIGINT NOT NULL,
    PRIMARY KEY (user_id, day)
);

-- build_goals is an account's weekly goal and its freezes, from its first
-- build day on: the goal in build days a week, the build days counted toward
-- the next freeze, the freezes held and the freezes earned so far, and
-- next_week, the first date (a Monday) of the earliest week the job hasn't
-- decided. It moves on one week at each week's deadline, so every week
-- before it without a build_weeks row was missed.
CREATE TABLE build_goals (
    user_id TEXT PRIMARY KEY REFERENCES users (id),
    goal BIGINT NOT NULL,
    freeze_days BIGINT NOT NULL,
    freezes_held BIGINT NOT NULL,
    freezes_earned BIGINT NOT NULL,
    next_week TEXT
);

-- The build-day job finds the accounts with a week to decide.
CREATE INDEX build_goals_next_week ON build_goals (next_week);

-- build_weeks is each week's result, by its first date, a Monday in the
-- profile's time zone: the goal in force when it was decided, its build
-- days then, and met or frozen (a held freeze covered it). A week is met the
-- moment its build days reach the goal, and missed, spending a held freeze
-- if there is one, only once its last day's deadline has passed. A week
-- missed with no freeze to spend has no row, and ends the run of weeks.
CREATE TABLE build_weeks (
    user_id TEXT NOT NULL REFERENCES users (id),
    week TEXT NOT NULL,
    goal BIGINT NOT NULL,
    build_days BIGINT NOT NULL,
    result TEXT NOT NULL,
    decided_at_ms BIGINT NOT NULL,
    PRIMARY KEY (user_id, week)
);

-- devlogs is each devlog the teen published (package devlog): the random id
-- the client gave it, the device that published it and the project key it
-- names, as project_names names a project, which reads resolve through
-- rollup_projects, when the server received it, which bounds D13's windows,
-- the Markdown byte for byte as previewed, and the ai_assisted label (1 or
-- 0). A delete deletes the row at once and leaves only the id in
-- removed_items (server.devlogs).
CREATE TABLE devlogs (
    user_id TEXT NOT NULL REFERENCES users (id),
    id TEXT NOT NULL,
    device_id TEXT NOT NULL,
    project_key TEXT NOT NULL,
    received_at_ms BIGINT NOT NULL,
    markdown BYTEA NOT NULL,
    ai_assisted BIGINT NOT NULL,
    PRIMARY KEY (user_id, id)
);

-- Reads list an account's devlogs in the order the server received them.
CREATE INDEX devlogs_user_received ON devlogs (user_id, received_at_ms);

-- badges is each badge the teen chose to show: the random id the client gave
-- it and its code, from the fixed list package devlog keeps (server.badges).
CREATE TABLE badges (
    user_id TEXT NOT NULL REFERENCES users (id),
    id TEXT NOT NULL,
    code TEXT NOT NULL,
    PRIMARY KEY (user_id, id)
);

-- An account shows each code at most once: DESIGN 4.4 keeps the badges the
-- teen chose to show as codes from a fixed list (#283).
CREATE UNIQUE INDEX badges_user_code ON badges (user_id, code);

-- removed_items is the id of each devlog the teen deleted and each badge the
-- teen removed, with when, and nothing else, so a publish of the id sent
-- again from a computer that missed the removal gets 410 and stores nothing.
-- Its retention job (server.removed_items) deletes a row 30 days after the
-- removal, by removed_at_ms, so no table points at it.
CREATE TABLE removed_items (
    user_id TEXT NOT NULL REFERENCES users (id),
    id TEXT NOT NULL,
    removed_at_ms BIGINT NOT NULL,
    PRIMARY KEY (user_id, id)
);

CREATE INDEX removed_items_removed_at_ms ON removed_items (removed_at_ms);

-- zone_changes is every change of the profile's time zone (users.tz): when
-- it was made and the zone it replaced, which package rollup allows at most
-- once per 30 days. A minute counts toward its date in the zone in force at
-- its own time: the zone the first change after it replaced, or users.tz
-- when no change came after it (#278). It stays as long as the account,
-- under server.account with users.
CREATE TABLE zone_changes (
    user_id TEXT NOT NULL REFERENCES users (id),
    changed_at_ms BIGINT NOT NULL,
    tz_before TEXT NOT NULL,
    PRIMARY KEY (user_id, changed_at_ms)
);
