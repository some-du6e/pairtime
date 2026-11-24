-- SPDX-License-Identifier: AGPL-3.0-or-later
--
-- Submissions (WP-SB1; DESIGN 7.5 steps 1 and 2, SP 10.2), written by
-- package submission. Times are milliseconds and ids are UUIDv7s as TEXT.
--
-- submissions is each submission: the program it goes to and the
-- external_id the create is idempotent on, with the SHA-256 of the create
-- request it was made from, so a repeat is told from a reuse; who made it
-- (a signed-in session, a device or a program) and from which device; its
-- mode (one window, or a list of devlog windows, D13); its state and the
-- time of each change the spot-check window can open at (DESIGN 4.4); and,
-- from the freeze, the scoring version and parameter hash it froze, the
-- 32-byte r and its SHA-256, and whether the teen's eligibility was still
-- to confirm. No read selects r before the metadata reveal is stored.
-- device_id names its device with no foreign key, since a retention job
-- deletes devices.
CREATE TABLE submissions (
    id TEXT PRIMARY KEY,
    user_id TEXT NOT NULL REFERENCES users (id),
    program TEXT NOT NULL,
    external_id TEXT NOT NULL,
    request_sha256 TEXT NOT NULL,
    created_by TEXT NOT NULL,
    device_id TEXT,
    mode TEXT NOT NULL,
    state TEXT NOT NULL,
    scoring_version TEXT,
    params TEXT,
    r BYTEA,
    r_sha256 TEXT,
    eligibility_to_confirm BIGINT NOT NULL,
    created_at_ms BIGINT NOT NULL,
    updated_at_ms BIGINT NOT NULL,
    frozen_at_ms BIGINT,
    metadata_revealed_at_ms BIGINT,
    decided_at_ms BIGINT,
    withdrawn_at_ms BIGINT,
    revoked_at_ms BIGINT
);

-- A create is idempotent on (program, external_id).
CREATE UNIQUE INDEX submissions_program_external_id ON submissions (program, external_id);

CREATE INDEX submissions_user_created ON submissions (user_id, created_at_ms);

-- A program lists its submissions by when they last changed.
CREATE INDEX submissions_program_updated ON submissions (program, updated_at_ms, id);

-- submission_projects is the projects P the teen picked, by their
-- account-level project ids.
CREATE TABLE submission_projects (
    submission_id TEXT NOT NULL REFERENCES submissions (id),
    project_id TEXT NOT NULL,
    PRIMARY KEY (submission_id, project_id)
);

-- submission_keys is the devices' project keys P named at the freeze,
-- through rollup_projects, kept as they were, since project registration
-- (WP-SV10) can link keys to another project id later.
CREATE TABLE submission_keys (
    submission_id TEXT NOT NULL REFERENCES submissions (id),
    user_id TEXT NOT NULL,
    device_id TEXT NOT NULL,
    project_key TEXT NOT NULL,
    PRIMARY KEY (submission_id, device_id, project_key)
);

CREATE INDEX submission_keys_user_key ON submission_keys (user_id, device_id, project_key);

-- submission_windows is each frozen window's start and end: the window W,
-- or each devlog window, and never its devlogs (DESIGN 7.5 step 2).
CREATE TABLE submission_windows (
    submission_id TEXT NOT NULL REFERENCES submissions (id),
    start_ms BIGINT NOT NULL,
    end_ms BIGINT NOT NULL,
    PRIMARY KEY (submission_id, start_ms)
);

CREATE INDEX submission_windows_start ON submission_windows (start_ms);

-- submission_events is each change of a submission's state, in order.
CREATE TABLE submission_events (
    submission_id TEXT NOT NULL REFERENCES submissions (id),
    seq BIGINT NOT NULL,
    from_state TEXT NOT NULL,
    to_state TEXT NOT NULL,
    at_ms BIGINT NOT NULL,
    PRIMARY KEY (submission_id, seq)
);

CREATE INDEX submission_events_at ON submission_events (at_ms);

-- submission_flags is the risk flags the freeze raised for reviewers, such
-- as concurrent_devices, each with its detail as JSON. It names its
-- submission with no foreign key, as the evidence index does, and a
-- retention job deletes a flag once the spot-check window that opened at
-- decided_at_ms has closed.
CREATE TABLE submission_flags (
    submission_id TEXT NOT NULL,
    code TEXT NOT NULL,
    detail TEXT NOT NULL,
    raised_at_ms BIGINT NOT NULL,
    decided_at_ms BIGINT,
    PRIMARY KEY (submission_id, code)
);

CREATE INDEX submission_flags_decided ON submission_flags (decided_at_ms);
