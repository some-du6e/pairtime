-- Hackatime's duration SQL at 43bde10b02db, as standalone PostgreSQL functions.
--
-- The SQL fragments are from hackclub/hackatime app/models/concerns/heartbeatable.rb
-- at 43bde10b02dbd2371fb4f802e724a1a875ac0dfc, Copyright (c) 2026 Hack Club, under
-- the MIT License in LICENSE next to this file. excerpts/app/models/concerns/heartbeatable.rb
-- quotes them. ActiveRecord composes them into full queries at run time; WP-PG0 wrote
-- out by hand the queries it composes for the calls below, using that Ruby and
-- lib/wakatime_service.rb, and each step names the Ruby lines it comes from. The
-- assembly is pairtime's (AGPL-3.0-or-later).
--
-- WP-PG0 did not run this file. WP-PG11 runs it on Postgres against durations.json.
-- Where this file and the Ruby disagree, the Ruby is the oracle and this file has an
-- erratum.
--
-- Two textual changes from what ActiveRecord sends, neither of which changes a result:
--   1. An IN (...) list is = ANY (array parameter). Both are NULL for a NULL left side.
--   2. Ruby fetches the boundary heartbeat with its own query and inlines its time as a
--      literal (heartbeatable.rb lines 297-302). Here it is a subquery. With no boundary
--      row, "time = NULL" is never true, which leaves Ruby's other branch,
--      time BETWEEN start AND end.
-- Left out: the default scope's heartbeat_exclusions predicate (app/models/heartbeat.rb
-- line 12), Hackatime's moderation tool for hiding a user's heartbeats. Heartbeats a
-- client keeps for itself have no such exclusions.
--
-- Parameters, the same for every function:
--   p_user_id   the user
--   p_projects  the filter_by_project names, or NULL for no project filter
--   p_start     window start, epoch seconds
--   p_end       window end, epoch seconds. In test mode WakatimeService truncates both
--               bounds to whole seconds first (wakatime_service.rb lines 275-292)
--   p_excluded  categories to leave out, lowercase, compared with LOWER(category):
--               {browsing,meeting,communicating} in test mode, plus "ai coding" when the
--               call has no_ai_coding=true. NULL means no exclusion, as when Ruby skips
--               the condition for an empty list. With a non-empty list a NULL category
--               is left out too, because NOT (NULL) is not true.

-- Only the columns these functions read, with Hackatime's types (HC 1.2).
CREATE TABLE IF NOT EXISTS heartbeats (
  id         bigint PRIMARY KEY,
  user_id    bigint NOT NULL,
  time       double precision NOT NULL,
  project    character varying,
  category   character varying,
  deleted_at timestamp(6) without time zone
);

-- Heartbeat.duration_seconds_boundary_aware (heartbeatable.rb lines 286-311) as
-- WakatimeService#build_summary calls it in test mode (wakatime_service.rb lines 94-95).
-- This is the total Stardance reads with test_param=true&total_seconds=true.
CREATE OR REPLACE FUNCTION hackatime_seconds_boundary_aware(
  p_user_id bigint, p_projects text[], p_start double precision, p_end double precision, p_excluded text[]
) RETURNS integer LANGUAGE sql STABLE AS $$
  WITH base AS (
    -- base_scope, lines 287-294: the default scope's deleted_at IS NULL,
    -- with_valid_timestamps (line 40), the category exclusion, and the user_id and
    -- project conditions that where_values_hash copies from the caller's scope.
    SELECT id, time FROM heartbeats
    WHERE deleted_at IS NULL
      AND (time >= 0 AND time <= 253402300799)
      AND (p_excluded IS NULL OR NOT (LOWER(category) = ANY (p_excluded)))
      AND user_id = p_user_id
      AND (p_projects IS NULL OR project = ANY (p_projects))
  ),
  boundary AS (
    -- line 297: the last heartbeat before the window, by (time, id)
    SELECT time FROM base WHERE time < p_start ORDER BY time DESC, id DESC LIMIT 1
  ),
  gaps AS (
    -- lines 300-307
    SELECT LAG(time) OVER (ORDER BY time, id) AS duration_start, time AS duration_end
    FROM base
    WHERE (time >= p_start OR time = (SELECT time FROM boundary))
      AND time <= p_end
      AND time IS NOT NULL
  ),
  capped AS (
    -- with_capped_duration, lines 331-339, with heartbeat_timeout_duration's 2 minutes
    -- (lines 44-46)
    SELECT gaps.*,
      CASE WHEN duration_start IS NULL OR duration_end IS NULL THEN 0
           ELSE LEAST(duration_end - duration_start, 120) END AS duration
    FROM gaps
  )
  -- line 310: only gaps that end inside the window count
  SELECT COALESCE(SUM(duration), 0)::integer FROM capped WHERE duration_end >= p_start
$$;

-- The scope the other three functions read: WakatimeService's @scope
-- (wakatime_service.rb lines 8-27) under the default scope, with start <= time < end.

-- Heartbeat.duration_seconds without a group (heartbeatable.rb lines 252-253, 279-283).
CREATE OR REPLACE FUNCTION hackatime_seconds(
  p_user_id bigint, p_projects text[], p_start double precision, p_end double precision, p_excluded text[]
) RETURNS integer LANGUAGE sql STABLE AS $$
  WITH scope AS (
    SELECT id, time FROM heartbeats
    WHERE deleted_at IS NULL
      AND (p_projects IS NULL OR project = ANY (p_projects))
      AND (time >= 0 AND time <= 253402300799)
      AND (p_excluded IS NULL OR NOT (LOWER(category) = ANY (p_excluded)))
      AND (time >= p_start AND time < p_end)
      AND user_id = p_user_id
  ),
  gaps AS (
    -- line 280
    SELECT LAG(time) OVER (ORDER BY time, id) AS duration_start, time AS duration_end
    FROM scope WHERE time IS NOT NULL
  ),
  capped AS (
    SELECT gaps.*,
      CASE WHEN duration_start IS NULL OR duration_end IS NULL THEN 0
           ELSE LEAST(duration_end - duration_start, 120) END AS duration
    FROM gaps
  )
  -- line 282
  SELECT COALESCE(SUM(duration), 0)::integer FROM capped
$$;

-- Heartbeat.duration_seconds on a scope grouped by project (heartbeatable.rb lines
-- 255-277), as WakatimeService#build_summary runs it for data.projects (line 92).
-- LAG is partitioned by project, so each switch between projects can count up to
-- 120 s in both of them.
CREATE OR REPLACE FUNCTION hackatime_seconds_by_project(
  p_user_id bigint, p_projects text[], p_start double precision, p_end double precision, p_excluded text[]
) RETURNS TABLE (project_name character varying, seconds integer) LANGUAGE sql STABLE AS $$
  WITH scope AS (
    SELECT id, time, project FROM heartbeats
    WHERE deleted_at IS NULL
      AND (p_projects IS NULL OR project = ANY (p_projects))
      AND (time >= 0 AND time <= 253402300799)
      AND (p_excluded IS NULL OR NOT (LOWER(category) = ANY (p_excluded)))
      AND (time >= p_start AND time < p_end)
      AND user_id = p_user_id
  ),
  gaps AS (
    -- line 262
    SELECT project AS grouped_time,
      LAG(time) OVER (PARTITION BY project ORDER BY time, id) AS duration_start,
      time AS duration_end
    FROM scope WHERE time IS NOT NULL
  ),
  capped AS (
    SELECT gaps.*,
      CASE WHEN duration_start IS NULL OR duration_end IS NULL THEN 0
           ELSE LEAST(duration_end - duration_start, 120) END AS duration
    FROM gaps
  )
  -- line 271
  SELECT grouped_time, COALESCE(SUM(duration), 0)::integer FROM capped GROUP BY grouped_time
$$;

-- Heartbeat.attributed_durations_by(scope, :category) (heartbeatable.rb lines 227-248):
-- one order, each gap credited to the category of the heartbeat that ends it, so the
-- buckets add up to hackatime_seconds before rounding. Hackatime uses this path for
-- its category breakdowns. Each bucket is cast to integer on its own.
CREATE OR REPLACE FUNCTION hackatime_seconds_by_category(
  p_user_id bigint, p_projects text[], p_start double precision, p_end double precision, p_excluded text[]
) RETURNS TABLE (category_name character varying, seconds integer) LANGUAGE sql STABLE AS $$
  WITH scope AS (
    SELECT id, time, category FROM heartbeats
    WHERE deleted_at IS NULL
      AND (p_projects IS NULL OR project = ANY (p_projects))
      AND (time >= 0 AND time <= 253402300799)
      AND (p_excluded IS NULL OR NOT (LOWER(category) = ANY (p_excluded)))
      AND (time >= p_start AND time < p_end)
      AND user_id = p_user_id
  ),
  gaps AS (
    -- lines 231-237
    SELECT category AS bucket,
      LAG(time) OVER (ORDER BY time, id) AS duration_start,
      time AS duration_end
    FROM scope
  ),
  capped AS (
    SELECT gaps.*,
      CASE WHEN duration_start IS NULL OR duration_end IS NULL THEN 0
           ELSE LEAST(duration_end - duration_start, 120) END AS duration
    FROM gaps
  )
  -- lines 240-245
  SELECT bucket, COALESCE(SUM(duration), 0)::integer
  FROM capped
  WHERE bucket IS NOT NULL AND bucket <> ''
  GROUP BY bucket
$$;
