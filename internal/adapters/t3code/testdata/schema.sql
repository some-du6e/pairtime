-- The tables and indexes of T3 Code's statev2.sqlite that the T3 Code
-- reader reads, in the shape T3's migrations leave them at T3 Code's commit
-- d8f3feff0c1749ca020941427b153005755baca6 (pingdotgg/t3code). The CREATE
-- statements are T3 Code's own, Copyright (c) 2026 T3 Tools Inc., under the
-- MIT license in LICENSE beside this file, which is T3 Code's at that
-- commit. They are those of migration 55 (OrchestrationV2), with the columns
-- and indexes later migrations add, copied by hand from
-- apps/server/src/persistence/Migrations:
-- 005_Projections.ts, 016, 019, 039, 040, 045, 047 and 076 (projects),
-- OrchestrationV2/Foundation.ts (provider_instance_id and driver),
-- OrchestrationV2/ShellIndexes.ts and RecoveryIndexes.ts, 079, 081
-- (search_text) and 084, and effect_sql_migrations as Effect's SQLite
-- migrator makes it. The tables the reader never reads are left out, and
-- so is the search table, orchestration_v2_message_search, with its
-- triggers. The tests fill the tables with invented rows.

CREATE TABLE IF NOT EXISTS effect_sql_migrations (
  migration_id integer PRIMARY KEY NOT NULL,
  created_at datetime NOT NULL DEFAULT current_timestamp,
  name VARCHAR(255) NOT NULL
);

CREATE TABLE IF NOT EXISTS projection_projects (
  project_id TEXT PRIMARY KEY,
  title TEXT NOT NULL,
  workspace_root TEXT NOT NULL,
  scripts_json TEXT NOT NULL,
  created_at TEXT NOT NULL,
  updated_at TEXT NOT NULL,
  deleted_at TEXT,
  default_model_selection_json TEXT,
  default_thread_env_mode TEXT,
  favicon_path TEXT,
  auto_pull INTEGER NOT NULL DEFAULT 0,
  project_icon_json TEXT,
  pinned_at TEXT,
  archived_at TEXT
);
CREATE INDEX IF NOT EXISTS idx_projection_projects_updated_at ON projection_projects(updated_at);
CREATE INDEX IF NOT EXISTS idx_projection_projects_workspace_root_deleted_at ON projection_projects(workspace_root, deleted_at);

CREATE TABLE orchestration_v2_projection_threads (
  thread_id TEXT PRIMARY KEY,
  project_id TEXT NOT NULL,
  title TEXT NOT NULL,
  default_provider TEXT NOT NULL,
  runtime_mode TEXT NOT NULL,
  interaction_mode TEXT NOT NULL,
  active_provider_thread_id TEXT,
  created_at TEXT NOT NULL,
  updated_at TEXT NOT NULL,
  archived_at TEXT,
  deleted_at TEXT,
  payload_json TEXT NOT NULL,
  provider_instance_id TEXT
);
CREATE INDEX orchestration_v2_projection_threads_project_updated_idx ON orchestration_v2_projection_threads(project_id, updated_at);
CREATE INDEX orchestration_v2_projection_threads_subagent_result_idx
ON orchestration_v2_projection_threads(
  thread_id, json_extract(payload_json, '$.lineage.parentThreadId')
)
WHERE CASE WHEN json_valid(payload_json) THEN
  json_extract(payload_json, '$.lineage.relationshipToParent') = 'subagent'
  AND json_extract(payload_json, '$.lineage.parentThreadId') IS NOT NULL
  AND json_extract(payload_json, '$.forkedFrom.type') = 'node'
  ELSE 0 END;

CREATE TABLE orchestration_v2_projection_runs (
  run_id TEXT PRIMARY KEY,
  thread_id TEXT NOT NULL,
  ordinal INTEGER NOT NULL,
  provider TEXT NOT NULL,
  provider_thread_id TEXT,
  status TEXT NOT NULL,
  requested_at TEXT NOT NULL,
  completed_at TEXT,
  payload_json TEXT NOT NULL,
  provider_instance_id TEXT
);
CREATE UNIQUE INDEX orchestration_v2_projection_runs_thread_ordinal_idx ON orchestration_v2_projection_runs(thread_id, ordinal);
CREATE INDEX orchestration_v2_projection_runs_provider_thread_idx ON orchestration_v2_projection_runs(provider_thread_id);
CREATE INDEX orchestration_v2_projection_runs_thread_status_idx ON orchestration_v2_projection_runs(thread_id, status);
CREATE INDEX orchestration_v2_projection_runs_recovery_idx
ON orchestration_v2_projection_runs(status, thread_id)
WHERE status IN ('queued', 'preparing', 'starting', 'running', 'waiting');
CREATE INDEX orchestration_v2_projection_runs_delegated_delivery_idx
ON orchestration_v2_projection_runs(thread_id)
WHERE CASE WHEN json_valid(payload_json)
  THEN json_type(payload_json, '$.delegatedCompletion.delivery') = 'object'
  ELSE 0 END;
CREATE INDEX IF NOT EXISTS orchestration_v2_projection_runs_last_executed_idx
ON orchestration_v2_projection_runs(
  thread_id, (completed_at IS NULL) DESC, completed_at DESC, ordinal DESC, run_id DESC
)
WHERE status <> 'queued'
  AND NOT (status = 'cancelled' AND json_extract(payload_json, '$.startedAt') IS NULL);

CREATE TABLE orchestration_v2_projection_run_attempts (
  attempt_id TEXT PRIMARY KEY,
  thread_id TEXT NOT NULL,
  run_id TEXT NOT NULL,
  attempt_ordinal INTEGER NOT NULL,
  root_node_id TEXT NOT NULL,
  provider TEXT NOT NULL,
  provider_thread_id TEXT NOT NULL,
  provider_turn_id TEXT,
  status TEXT NOT NULL,
  payload_json TEXT NOT NULL,
  provider_instance_id TEXT
);
CREATE UNIQUE INDEX orchestration_v2_projection_run_attempts_run_ordinal_idx ON orchestration_v2_projection_run_attempts(run_id, attempt_ordinal);
CREATE INDEX orchestration_v2_projection_run_attempts_thread_idx ON orchestration_v2_projection_run_attempts(thread_id, run_id);

CREATE TABLE orchestration_v2_projection_provider_threads (
  provider_thread_id TEXT PRIMARY KEY,
  thread_id TEXT,
  owner_node_id TEXT,
  provider TEXT NOT NULL,
  provider_session_id TEXT,
  status TEXT NOT NULL,
  first_run_ordinal INTEGER,
  last_run_ordinal INTEGER,
  updated_at TEXT NOT NULL,
  payload_json TEXT NOT NULL,
  driver TEXT,
  provider_instance_id TEXT
);
CREATE INDEX orchestration_v2_projection_provider_threads_thread_idx ON orchestration_v2_projection_provider_threads(thread_id);
CREATE INDEX orchestration_v2_projection_provider_threads_session_idx ON orchestration_v2_projection_provider_threads(provider_session_id);
CREATE INDEX orchestration_v2_projection_provider_threads_owner_idx ON orchestration_v2_projection_provider_threads(owner_node_id);
CREATE INDEX orchestration_v2_projection_provider_threads_instance_status_idx ON orchestration_v2_projection_provider_threads(provider_instance_id, status);

CREATE TABLE orchestration_v2_projection_messages (
  message_id TEXT PRIMARY KEY,
  thread_id TEXT NOT NULL,
  run_id TEXT,
  node_id TEXT,
  role TEXT NOT NULL,
  streaming INTEGER NOT NULL,
  created_at TEXT NOT NULL,
  updated_at TEXT NOT NULL,
  payload_json TEXT NOT NULL,
  search_text TEXT
);
CREATE INDEX orchestration_v2_projection_messages_thread_created_idx ON orchestration_v2_projection_messages(thread_id, created_at, message_id);
CREATE INDEX orchestration_v2_projection_messages_run_idx ON orchestration_v2_projection_messages(run_id);
CREATE INDEX orchestration_v2_projection_messages_node_idx ON orchestration_v2_projection_messages(node_id);
CREATE INDEX orchestration_v2_projection_messages_latest_user_idx
ON orchestration_v2_projection_messages(thread_id, updated_at DESC, message_id DESC)
WHERE role = 'user';
