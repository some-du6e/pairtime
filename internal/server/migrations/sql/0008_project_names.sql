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
-- device_removed_at_ms is NULL while the device is linked: removing the
-- device sets it, linking it again clears it, and the retention job
-- (server.project_names) deletes a row 90 days after it, as the removed
-- device's own row goes 90 days after its removal (DESIGN 4.4's device_id
-- row, server.devices). The device's rollup_projects rows stay with the
-- account (D-project-map-lifetime).
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

-- A name the owner hid on the dashboard, after "Hide this name" deleted
-- its project_names row and with it the label: the device and project key
-- it named, and when, so the device's next names answer lists the key as
-- hidden and the device turns the name off too, telling a hide from a name
-- the server no longer holds. The device's delete of the key removes the
-- row, and the retention job (server.project_name_hides) deletes it 30 days
-- after the hide if no delete came, as from a device that stopped asking.
-- Account deletion deletes it too. A removed device's name gets no row: it
-- can't ask until it is linked again.
CREATE TABLE project_name_hides (
    device_id TEXT NOT NULL,
    project_key TEXT NOT NULL,
    user_id TEXT NOT NULL REFERENCES users (id),
    hidden_at_ms BIGINT NOT NULL,
    PRIMARY KEY (device_id, project_key)
);

-- Account deletion deletes an account's hides.
CREATE INDEX project_name_hides_user ON project_name_hides (user_id);

-- The retention job finds the hides 30 days old.
CREATE INDEX project_name_hides_hidden ON project_name_hides (hidden_at_ms);
