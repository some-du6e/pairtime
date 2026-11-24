-- SPDX-License-Identifier: AGPL-3.0-or-later
--
-- Submissions (WP-SB1; DESIGN 4.4, 7.5 steps 1 and 2, SP 10.2;
-- D-sb1-submissions), written by package submission. Times are
-- milliseconds and ids are UUIDv7s as TEXT. Every table but submission_flags
-- stays as long as the account (server.submissions): no retention job
-- deletes from submissions, so its child tables' foreign keys to it hold.
--
-- submissions is each submission: the account and the program it goes to,
-- with whether that program takes hours (takes_hours, 1 or 0), copied from
-- [[programs]] at the create, so a later change to the config moves no
-- submission; the program's external_id when a program made it (WP-PG6),
-- or the random request_id the teen's client made, with the SHA-256 of the
-- canonical request, so a retry is told from another request under the
-- same id; who made it (created_by: session or device) and from which
-- device; its mode, one window or devlog windows (D13); its state and when
-- it last reached each state a later step reads; and, from the freeze, the
-- scoring version and parameter hash it froze, the 32-byte r and its
-- SHA-256, and the eligibility note (DESIGN 8.2, 1 or 0). Before
-- challenged_at_ms is set, only DrawR reads r, inside the writer op that
-- stores the draw and sets it; every other read, through RevealedR, needs
-- challenged_at_ms. device_id names its device with no foreign key, since
-- a retention job deletes devices.
CREATE TABLE submissions (
    id TEXT PRIMARY KEY,
    user_id TEXT NOT NULL REFERENCES users (id),
    program TEXT NOT NULL,
    takes_hours BIGINT NOT NULL,
    external_id TEXT,
    request_id TEXT,
    request_sha256 TEXT NOT NULL,
    created_by TEXT NOT NULL,
    device_id TEXT,
    mode TEXT NOT NULL,
    state TEXT NOT NULL,
    scoring_version TEXT,
    params TEXT,
    r BYTEA,
    r_sha256 TEXT,
    eligibility_to_confirm BIGINT,
    created_at_ms BIGINT NOT NULL,
    updated_at_ms BIGINT NOT NULL,
    frozen_at_ms BIGINT,
    metadata_revealed_at_ms BIGINT,
    challenged_at_ms BIGINT,
    decided_at_ms BIGINT,
    withdrawn_at_ms BIGINT,
    revoked_at_ms BIGINT
);

-- A program's create is idempotent on (program, external_id) (WP-PG6), and
-- no teen-made submission has an external_id, so a teen can't take a
-- program's id first.
CREATE UNIQUE INDEX submissions_program_external_id ON submissions (program, external_id) WHERE external_id IS NOT NULL;

-- A teen's create is idempotent on the account and its client's request_id.
CREATE UNIQUE INDEX submissions_user_request_id ON submissions (user_id, request_id) WHERE request_id IS NOT NULL;

-- The account's own list and a program's list, by when each submission last
-- changed.
CREATE INDEX submissions_user_updated ON submissions (user_id, updated_at_ms, id);

CREATE INDEX submissions_program_updated ON submissions (program, updated_at_ms, id);

-- submission_projects is the projects P the teen picked, by their
-- account-level project ids, or the ids of the project keys a computer
-- named.
CREATE TABLE submission_projects (
    submission_id TEXT NOT NULL REFERENCES submissions (id),
    project_id TEXT NOT NULL,
    PRIMARY KEY (submission_id, project_id)
);

-- submission_keys is each (device id, project key) pair that P's projects
-- mapped to through rollup_projects at the freeze, with the project it
-- mapped to then, kept as it was: project registration (WP-SV10) may link
-- keys to another project id later and changes no frozen key. The overlap
-- refusal, D13's bound and the reveal set read it.
CREATE TABLE submission_keys (
    submission_id TEXT NOT NULL REFERENCES submissions (id),
    user_id TEXT NOT NULL,
    device_id TEXT NOT NULL,
    project_key TEXT NOT NULL,
    project_id TEXT NOT NULL,
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

-- submission_events is each change of a submission's state, in order.
CREATE TABLE submission_events (
    submission_id TEXT NOT NULL REFERENCES submissions (id),
    seq BIGINT NOT NULL,
    from_state TEXT NOT NULL,
    to_state TEXT NOT NULL,
    at_ms BIGINT NOT NULL,
    PRIMARY KEY (submission_id, seq)
);

-- submission_flags is the risk flags of a submission for reviewers, such as
-- the freeze's concurrent_devices and repo_overlap, each with its detail as
-- JSON. It names its submission with no foreign key, as the evidence index
-- does, and a retention job of evidence.revealed deletes a flag once the
-- spot-check window that opened at decided_at_ms has closed, from WP-SV18,
-- which writes decided_at_ms.
CREATE TABLE submission_flags (
    submission_id TEXT NOT NULL,
    code TEXT NOT NULL,
    detail TEXT NOT NULL,
    raised_at_ms BIGINT NOT NULL,
    decided_at_ms BIGINT,
    PRIMARY KEY (submission_id, code)
);

CREATE INDEX submission_flags_decided ON submission_flags (decided_at_ms);
