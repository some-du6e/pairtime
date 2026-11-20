# Excerpt of hackclub/hackatime app/models/concerns/heartbeatable.rb
# at commit 43bde10b02dbd2371fb4f802e724a1a875ac0dfc (git blob d033bd276ea351d4552c67167c8cd8a636dff90d).
# Copyright (c) 2026 Hack Club. MIT License; the full notice is in
# testdata/upstream/hackatime/LICENSE. Each block is copied verbatim from
# the lines its marker names. Lines between blocks are left out.

# ---- app/models/concerns/heartbeatable.rb lines 25-41 ----
  included do
    # Filter heartbeats to only include those with category equal to "coding"
    scope :coding_only, -> { where(category: "coding") }
    scope :excluding_browser_time, -> {
      where("editor IS NULL OR LOWER(editor) NOT IN (?)", BROWSER_EDITORS)
    }
    scope :leaderboard_eligible, -> {
      where(category: [ "coding", "ai coding" ])
        .excluding_browser_time
        .where("project IS DISTINCT FROM ?", "<<LAST_PROJECT>>")
        .with_valid_timestamps
    }

    # This is to prevent PG timestamp overflow errors if someones gives us a
    # heartbeat with a time that is enormously far in the future.
    scope :with_valid_timestamps, -> { where("time >= 0 AND time <= ?", 253402300799) }
  end

# ---- app/models/concerns/heartbeatable.rb lines 44-46 ----
    def heartbeat_timeout_duration(duration = nil)
      duration ? (@heartbeat_timeout_duration = duration) : (@heartbeat_timeout_duration || 2.minutes)
    end

# ---- app/models/concerns/heartbeatable.rb lines 86-134 ----
    def to_span(timeout_duration: nil)
      timeout_duration ||= heartbeat_timeout_duration.to_i

      heartbeats = with_valid_timestamps.order(time: :asc, id: :asc)
      return [] if heartbeats.empty?

      sql = <<~SQL
        SELECT
          time,
          LEAD(time) OVER (ORDER BY time, id) as next_time
        FROM (#{heartbeats.to_sql}) AS heartbeats
      SQL

      results = connection.select_all(sql)
      return [] if results.empty?

      spans = []
      current_span_start = results.first["time"]

      results.each do |row|
        current_time = row["time"]
        next_time = row["next_time"]

        if next_time.nil? || (next_time - current_time) > timeout_duration
          base_duration = (current_time - current_span_start).round

          if next_time
            gap_duration = [ next_time - current_time, timeout_duration ].min
            total_duration = base_duration + gap_duration
            end_time = current_time + gap_duration
          else
            total_duration = base_duration
            end_time = current_time
          end

          if total_duration > 0
            spans << {
              start_time: current_span_start,
              end_time: end_time,
              duration: total_duration
            }
          end

          current_span_start = next_time if next_time
        end
      end

      spans
    end

# ---- app/models/concerns/heartbeatable.rb lines 227-248 ----
    def attributed_durations_by(scope, field)
      scope = scope.with_valid_timestamps
      field_expr = connection.quote_column_name(field.to_s)
      attribution_heartbeats = scope.unscope(:group, :select, :order).select(:id, :time, field)
      heartbeat_gaps = unscoped.from(attribution_heartbeats, :attribution_heartbeats).select(<<~SQL.squish)
        attribution_heartbeats.#{field_expr} AS bucket,
        LAG(attribution_heartbeats.time) OVER (
          ORDER BY attribution_heartbeats.time, attribution_heartbeats.id
        ) AS duration_start,
        attribution_heartbeats.time AS duration_end
      SQL
      capped_durations = with_capped_duration(heartbeat_gaps)

      sql = <<~SQL.squish
        SELECT bucket, COALESCE(SUM(duration), 0)::integer AS duration
        FROM (#{capped_durations.to_sql}) attributed_durations
        WHERE bucket IS NOT NULL AND bucket <> ''
        GROUP BY bucket
      SQL

      connection.select_all(sql).each_with_object({}) { |row, hash| hash[row["bucket"]] = row["duration"].to_i }
    end

# ---- app/models/concerns/heartbeatable.rb lines 250-311 ----
    # With async: true, a grouped scope's query runs on the async query
    # executor and an ActiveRecord::Promise of the durations is returned.
    def duration_seconds(scope = all, async: false)
      scope = scope.with_valid_timestamps

      if scope.group_values.any?
        raise NotImplementedError, "Multiple group values are not supported" if scope.group_values.length > 1

        group_column = scope.group_values.first
        # Don't quote if it's a SQL function (contains parentheses)
        group_expr = group_column.to_s.include?("(") ? group_column : connection.quote_column_name(group_column)

        heartbeat_gaps = scope.select("#{group_expr} as grouped_time, LAG(time) OVER (PARTITION BY #{group_expr} ORDER BY time, #{quoted_table_name}.id) as duration_start, time as duration_end")
          .where.not(time: nil).unscope(:group)
        capped_durations = with_capped_duration(heartbeat_gaps)

        # Async queries need the executor's query session and are not allowed
        # inside a transaction, so elsewhere (console, transactions) the query
        # runs in the foreground and the promise is already complete.
        background = async && Rails.application.executor.active? && !connection.current_transaction.joinable?
        durations = connection.select_all(
          "SELECT grouped_time, COALESCE(SUM(duration), 0)::integer as duration FROM (#{capped_durations.to_sql}) AS durations GROUP BY grouped_time",
          "Heartbeat Durations", async: background
        )
        to_hash = ->(rows) { rows.each_with_object({}) { |row, hash| hash[row["grouped_time"]] = row["duration"].to_i } }
        return durations.then(&to_hash) if background

        async ? ActiveRecord::Promise::Complete.new(to_hash.call(durations)) : to_hash.call(durations)
      else
        # when not grouped, return a single value
        heartbeat_gaps = scope.select("LAG(time) OVER (ORDER BY time, #{quoted_table_name}.id) as duration_start, time as duration_end").where.not(time: nil)
        capped_durations = with_capped_duration(heartbeat_gaps)
        connection.select_value("SELECT COALESCE(SUM(duration), 0)::integer FROM (#{capped_durations.to_sql}) AS durations").to_i
      end
    end

    def duration_seconds_boundary_aware(scope, start_time, end_time, excluded_categories: [])
      scope = scope.with_valid_timestamps
      base_scope = scope.model.all.with_valid_timestamps
      base_scope = base_scope.where.not("LOWER(category) IN (?)", excluded_categories) if excluded_categories.present?

      where_values = scope.where_values_hash
      %w[user_id category project deleted_at].each do |key|
        base_scope = base_scope.where(key => where_values[key]) if where_values[key]
      end

      # get the heartbeat before the start_time
      boundary_heartbeat = base_scope.where("time < ?", start_time).order(time: :desc, id: :desc).limit(1).first

      # if it's not NULL, we'll use it
      combined_scope = boundary_heartbeat ?
        base_scope.where("time >= ? OR time = ?", start_time, boundary_heartbeat.time).where("time <= ?", end_time) :
        base_scope.where(time: start_time..end_time)

      # we calc w/ the boundary heartbeat, but we only sum within the orignal constraint
      heartbeat_gaps = combined_scope
        .select("LAG(time) OVER (ORDER BY time, #{quoted_table_name}.id) as duration_start, time as duration_end")
        .where.not(time: nil).order(time: :asc, id: :asc)
      capped_durations = with_capped_duration(heartbeat_gaps)

      connection.select_value("SELECT COALESCE(SUM(duration), 0)::integer FROM (#{capped_durations.to_sql}) AS durations WHERE duration_end >= #{connection.quote(start_time)}").to_i
    end

# ---- app/models/concerns/heartbeatable.rb lines 331-339 ----
    def with_capped_duration(heartbeat_intervals)
      timeout = heartbeat_timeout_duration.to_i

      unscoped.from(heartbeat_intervals, :heartbeat_intervals).select(<<~SQL.squish)
        heartbeat_intervals.*,
        CASE WHEN heartbeat_intervals.duration_start IS NULL OR heartbeat_intervals.duration_end IS NULL THEN 0
             ELSE LEAST(heartbeat_intervals.duration_end - heartbeat_intervals.duration_start, #{timeout}) END AS duration
      SQL
    end
