-- SPDX-License-Identifier: AGPL-3.0-or-later
--
-- The project names an owner turned on, one project at a time on their
-- computer, so their own dashboard shows a name in place of the project's
-- short code (D-owner-names; DESIGN 4.3, 4.4; docs/spec/project-names-v1.md).
-- The device sends each label in a request signed with its key, and a
-- delete when the owner turns the name off; "Hide this name" on the
-- dashboard deletes the row too. Only the owner's signed-in dashboard and
-- its JSON read a row, never a public page or a program. A row names the
-- device and its project key, as rollup_projects does, and holds the label
-- the owner approved, at most 64 characters, and when the server stored it.
-- device_removed_at_ms is NULL while the device is linked: removing the
-- device sets it, linking it again clears it, and the retention job
-- (server.project_names) deletes a row 90 days after it, as
-- D-project-map-lifetime deletes the device's other project rows.
CREATE TABLE project_names (
    device_id TEXT NOT NULL,
    project_key TEXT NOT NULL,
    user_id TEXT NOT NULL REFERENCES users (id),
    label TEXT NOT NULL,
    set_at_ms BIGINT NOT NULL,
    device_removed_at_ms BIGINT,
    PRIMARY KEY (device_id, project_key)
);

-- The dashboard reads an account's names, and the hide button and account
-- deletion delete them.
CREATE INDEX project_names_user ON project_names (user_id);

-- The retention job finds the rows of devices removed long enough ago.
CREATE INDEX project_names_removed ON project_names (device_removed_at_ms);
