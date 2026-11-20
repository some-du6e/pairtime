# Excerpt of hackclub/hackatime lib/wakatime_service.rb
# at commit 43bde10b02dbd2371fb4f802e724a1a875ac0dfc (git blob 69d87c81dd6ef9a46c34c35fefb0ea1a2ad9eb75).
# Copyright (c) 2026 Hack Club. MIT License; the full notice is in
# testdata/upstream/hackatime/LICENSE. Each block is copied verbatim from
# the lines its marker names. Lines between blocks are left out.

# ---- lib/wakatime_service.rb lines 7-32 ----
  def initialize(user: nil, specific_filters: [], allow_cache: true, limit: 10, start_date: nil, end_date: nil, scope: nil, boundary_aware: false, valid_timestamps_only: false, exclude_categories: [])
    @scope = scope || Heartbeat.all
    @scope = @scope.with_valid_timestamps if valid_timestamps_only
    @scope = @scope.where.not("LOWER(category) IN (?)", exclude_categories) if exclude_categories.any?
    @exclude_categories = exclude_categories
    @user = user
    @boundary_aware = boundary_aware

    @start_date = convert_to_unix_timestamp(start_date)
    @end_date = convert_to_unix_timestamp(end_date)

    # Default to 1 year ago if no start_date provided or if no data exists
    @start_date = @start_date || @scope.minimum(:time) || 1.year.ago.to_i
    @end_date = @end_date || @scope.maximum(:time) || Time.current.to_i

    @scope = @scope.where("time >= ? AND time < ?", @start_date, @end_date)

    @limit = limit
    @limit = nil if @limit&.zero?

    @scope = @scope.where(user_id: @user.id) if @user.present?

    @specific_filters = specific_filters
    @allow_cache = allow_cache
    @raw_names = boundary_aware # test-mode parity: use raw key.presence names when boundary_aware
  end

# ---- lib/wakatime_service.rb lines 34-38 ----
  def generate_summary
    return cached_summary if @allow_cache

    build_summary
  end

# ---- lib/wakatime_service.rb lines 72-110 ----
  def build_summary
    summary = {}

    summary[:username] = @user.display_name if @user.present?
    summary[:user_id] = @user.id.to_s if @user.present?
    summary[:is_coding_activity_visible] = true if @user.present?
    summary[:is_other_usage_visible] = true if @user.present?
    summary[:status] = "ok"

    @start_time = @start_date
    @end_time = @end_date

    summary[:start] = Time.at(@start_time).strftime("%Y-%m-%dT%H:%M:%SZ")
    summary[:end] = Time.at(@end_time).strftime("%Y-%m-%dT%H:%M:%SZ")

    summary[:range] = "all_time"
    summary[:human_readable_range] = "All Time"

    # The grouped durations run on the async query executor while the total is computed.
    chunks = { languages: :language, projects: :project }.select { |filter, _| @specific_filters.include?(filter) }
      .to_h { |filter, group_by| [ filter, [ group_by, @scope.group(group_by).duration_seconds(async: true) ] ] }

    @total_seconds = if @boundary_aware
      Heartbeat.duration_seconds_boundary_aware(@scope, @start_date, @end_date, excluded_categories: @exclude_categories) || 0
    else
      @scope.duration_seconds || 0
    end
    summary[:total_seconds] = @total_seconds

    @total_days = (@end_time - @start_time) / 86400
    summary[:daily_average] = @total_days.zero? ? 0 : @total_seconds / @total_days

    summary[:human_readable_total] = ApplicationController.helpers.short_time_detailed(@total_seconds)
    summary[:human_readable_daily_average] = ApplicationController.helpers.short_time_detailed(summary[:daily_average])

    chunks.each { |filter, (group_by, durations)| summary[filter] = generate_summary_chunk(group_by, durations.value) }

    summary
  end

# ---- lib/wakatime_service.rb lines 119-137 ----
  def generate_summary_chunk(group_by, durations)
    result = []
    durations.each do |key, value|
      entry = {
        name: @raw_names ? (key.presence || "Other") : transform_display_name(group_by, key),
        total_seconds: value,
        text: ApplicationController.helpers.short_time_simple(value),
        hours: value / 3600,
        minutes: (value % 3600) / 60,
        percent: (100.0 * value / @total_seconds).round(2),
        digital: ApplicationController.helpers.digital_time(value)
      }
      entry[:color] = LanguageUtils.color(key) if group_by == :language
      result << entry
    end
    result = result.sort_by { |item| -item[:total_seconds] }
    result = result.first(@limit) if @limit.present?
    result
  end

# ---- lib/wakatime_service.rb lines 275-292 ----
  def convert_to_unix_timestamp(timestamp)
    # our lord and savior stack overflow for this bit of code
    return nil if timestamp.nil?

    case timestamp
    when String
      Time.parse(timestamp).to_i
    when Time, DateTime, Date
      timestamp.to_i
    when Numeric
      timestamp.to_i
    else
      nil
    end
  rescue ArgumentError => e
    report_error(e, message: "Error converting timestamp")
    nil
  end
