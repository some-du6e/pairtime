-- SPDX-License-Identifier: AGPL-3.0-or-later
--
-- The project names an owner turned on, one project at a time on their
-- computer, so their own dashboard shows a name in place of the project's
-- short code (D-owner-names; DESIGN 4.3, 4.4; docs/spec/project-names-v1.md).
-- The device sends each label in a request signed with its key, and a
-- delete when the owner turns the name off. Only the owner's signed-in
-- dashboard and its JSON read a label, never a public page or a program,
-- and only for a key its own device uploaded minutes of. A row names the
-- device and its project key, as rollup_projects does, and holds the label
-- the owner approved, at most 64 characters, and when the server stored it.
-- "Hide this name" on the dashboard sets label to NULL at once and
-- hidden_at_ms to when, and the row stays without its label so the
-- device's next answer says the name was hidden there, until the device's
-- delete says it turned the name off too. device_removed_at_ms is NULL
-- while the device is linked: removing the device sets it, linking it
-- again clears it, and the retention job (server.project_names) deletes a
-- row 90 days after it, as D-project-map-lifetime deletes the device's
-- other project rows.
CREATE TABLE project_names (
    device_id TEXT NOT NULL,
    project_key TEXT NOT NULL,
    user_id TEXT NOT NULL REFERENCES users (id),
    label TEXT,
    set_at_ms BIGINT NOT NULL,
    hidden_at_ms BIGINT,
    device_removed_at_ms BIGINT,
    PRIMARY KEY (device_id, project_key)
);

-- The dashboard reads an account's names, and the hide button and account
-- deletion change them.
CREATE INDEX project_names_user ON project_names (user_id);

-- The retention job finds the rows of devices removed long enough ago.
CREATE INDEX project_names_removed ON project_names (device_removed_at_ms);
