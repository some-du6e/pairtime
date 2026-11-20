# Excerpt of hackclub/hackatime app/controllers/api/v1/stats_controller.rb
# at commit 43bde10b02dbd2371fb4f802e724a1a875ac0dfc (git blob 5c62ea1bc30655ba07b7975e32a6a0d6ba09be8b).
# Copyright (c) 2026 Hack Club. MIT License; the full notice is in
# testdata/upstream/hackatime/LICENSE. Each block is copied verbatim from
# the lines its marker names. Lines between blocks are left out.

# ---- app/controllers/api/v1/stats_controller.rb lines 2-2 ----
  USER_LOOKUP_ACTIONS = [ :user_stats, :user_spans, :user_projects, :user_project, :user_projects_details ].freeze

# ---- app/controllers/api/v1/stats_controller.rb lines 5-6 ----
  before_action :set_user, only: USER_LOOKUP_ACTIONS
  before_action :ensure_public_stats_allowed!, only: USER_LOOKUP_ACTIONS

# ---- app/controllers/api/v1/stats_controller.rb lines 32-113 ----
  def user_stats
    # Used by the github stats page feature
    start_date = parse_datetime_param(:start_date, default: 10.years.ago)
    return if performed?
    end_date = parse_datetime_param(:end_date, default: Date.today.end_of_day)
    return if performed?

    # /api/v1/users/current/stats?filter_by_project=harbor,high-seas
    filter_by_projects = params[:filter_by_project].presence&.split(",")
    filter_by_categories = params[:filter_by_category].presence&.split(",")
    scope = filter_by_projects ? @user.heartbeats.where(project: filter_by_projects) : nil

    enabled_features = params[:features]&.split(",")&.map(&:to_sym) || %i[languages]

    service_params = {
      user: @user,
      specific_filters: enabled_features,
      allow_cache: false,
      limit: params[:limit].to_i,
      start_date: start_date,
      end_date: end_date
    }
    service_params[:scope] = scope if scope

    no_ai_coding = params[:no_ai_coding] == "true"

    if params[:test_param] == "true"
      service_params[:boundary_aware] = true # always and i mean always use boundary aware in test mode
      service_params[:valid_timestamps_only] = true
      excluded = [ "browsing", "meeting", "communicating" ]
      excluded << "ai coding" if no_ai_coding
      service_params[:exclude_categories] = excluded

      if params[:total_seconds] == "true"
        return render json: { total_seconds: WakatimeService.new(**service_params).generate_summary[:total_seconds] }
      end

      summary = WakatimeService.new(**service_params).generate_summary
    else
      if params[:total_seconds] == "true"
        query = Heartbeat.where(user_id: @user.id).where("time >= ? AND time < ?", start_date.to_f, end_date.to_f)
        query = query.where(project: filter_by_projects) if filter_by_projects
        query = query.where(category: filter_by_categories) if filter_by_categories

        total_seconds = if params[:boundary_aware] == "true"
          excluded = [ "browsing", "meeting", "communicating" ]
          excluded << "ai coding" if no_ai_coding
          Heartbeat.duration_seconds_boundary_aware(
            query,
            start_date.to_f,
            end_date.to_f,
            excluded_categories: excluded
          ) || 0
        else
          query = query.where.not(category: "ai coding") if no_ai_coding
          query.duration_seconds || 0
        end

        return render json: { total_seconds: total_seconds }
      end

      service_params[:exclude_categories] = [ "ai coding" ] if no_ai_coding
      summary = WakatimeService.new(**service_params).generate_summary
    end

    if params[:features]&.include?("projects") && params[:filter_by_project].present?
      heartbeats = @user.heartbeats.coding_only.with_valid_timestamps
                                   .where(time: start_date..end_date, project: filter_by_projects)
      summary[:unique_total_seconds] = unique_heartbeat_seconds(heartbeats)
    end

    trust_level = @user.public_trust_level

    summary[:streak] = @user.streak_days
    render json: {
      data: summary,
      trust_factor: {
        trust_level: trust_level,
        trust_value: User.trust_levels[trust_level]
      }
    }
  end

# ---- app/controllers/api/v1/stats_controller.rb lines 115-126 ----
  def user_spans
    start_date = parse_datetime_param(:start_date, default: 10.years.ago)
    return if performed?
    end_date = parse_datetime_param(:end_date, default: Date.today.end_of_day)
    return if performed?

    heartbeats = @user.heartbeats.where(time: start_date.to_f..end_date.to_f)
    heartbeats = heartbeats.where(project: params[:project]) if params[:project].present?
    heartbeats = heartbeats.where(project: params[:filter_by_project].split(",")) if params[:project].blank? && params[:filter_by_project].present?

    render json: { spans: heartbeats.to_span }
  end

# ---- app/controllers/api/v1/stats_controller.rb lines 128-138 ----
  def trust_factor
    id = params[:username] || params[:username_or_id] || params[:user_id]
    return render_not_found_json("User not found") if id.blank?

    query = User.where(slack_uid: id).or(User.where(username: id))
    query = query.or(User.where(id: id)) if id.match?(/^\d+$/)
    raw_level = query.pick(:trust_level)
    return render_not_found_json("User not found") unless raw_level
    level = User.mask_trust_level(raw_level)
    render json: { trust_level: level, trust_value: User.trust_levels[level] }
  end

# ---- app/controllers/api/v1/stats_controller.rb lines 168-184 ----
  def set_user
    identifier = params[:username] || params[:username_or_id] || params[:user_id]
    @api_caller_user = api_user_from_credentials(oauth_scopes: [ "read" ])

    if identifier == "my"
      @user = @api_caller_user
    else
      @user = User.lookup_by_identifier(identifier)
    end
  end

  def ensure_public_stats_allowed!
    return render_not_found_json("User not found") unless @user
    return if @user.allow_public_stats_lookup
    return if current_user == @user || @api_caller_user == @user
    render_forbidden("user has disabled public stats")
  end

# ---- app/controllers/api/v1/stats_controller.rb lines 205-215 ----
  def unique_heartbeat_seconds(heartbeats)
    timestamps = heartbeats.order(:time, :id).pluck(:time)
    return 0 if timestamps.empty?

    total_seconds = 0
    timestamps.each_cons(2) do |prev_time, curr_time|
      gap = curr_time - prev_time
      total_seconds += gap if gap > 0 && gap <= 2.minutes
    end
    total_seconds.to_i
  end

# ---- app/controllers/api/v1/stats_controller.rb lines 226-234 ----
  def parse_datetime_param(param_name, default:)
    raw_value = params[param_name]
    return default if raw_value.blank?
    parsed_time = Time.zone.parse(raw_value.to_s)
    raise ArgumentError if parsed_time.nil?
    parsed_time
  rescue ArgumentError, TypeError
    render_error("Invalid #{param_name}")
  end
