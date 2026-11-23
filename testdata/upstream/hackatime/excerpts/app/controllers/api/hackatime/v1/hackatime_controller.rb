# Excerpt of hackclub/hackatime app/controllers/api/hackatime/v1/hackatime_controller.rb
# at commit 43bde10b02dbd2371fb4f802e724a1a875ac0dfc (git blob 08c640df347b1c2b02d0ccf9769ba0c461ba030d).
# Copyright (c) 2026 Hack Club. MIT License; the full notice is in
# testdata/upstream/hackatime/LICENSE. Each block is copied verbatim from
# the lines its marker names. Lines between blocks are left out.

# ---- app/controllers/api/hackatime/v1/hackatime_controller.rb lines 7-14 ----
  HEARTBEAT_KEYS = %i[
    ai_input_tokens ai_line_changes ai_model ai_output_tokens ai_prompt_length ai_session
    ai_subscription_plan branch category created_at cursorpos editor
    entity human_line_changes is_write language line_additions line_deletions lineno
    lines machine operating_system project project_root_count time type user_agent plugin
  ].freeze

  MAX_BULK_HEARTBEATS = 100

# ---- app/controllers/api/hackatime/v1/hackatime_controller.rb lines 58-108 ----
  def push_heartbeats
    if params["format"] == "bulk"
      # POST /api/hackatime/v1/users/:id/heartbeats.bulk
      heartbeat_array = heartbeat_bulk_params[:heartbeats] || []
      return render_bad_request("No data provided...") if heartbeat_array.empty?
      if heartbeat_array.size > MAX_BULK_HEARTBEATS
        return render_bad_request("Too many heartbeats in a single request (max #{MAX_BULK_HEARTBEATS})")
      end

      render json: { responses: handle_heartbeat(heartbeat_array) }, status: :created
    else
      # POST /api/hackatime/v1/users/:id/heartbeats
      heartbeat_array = Array.wrap(heartbeat_params)
      return render_bad_request("No data provided...") if heartbeat_array.empty? || heartbeat_params.blank?

      response_body, response_status = handle_heartbeat(heartbeat_array).first
      render json: response_body, status: response_status == 201 ? :accepted : response_status
    end
  end

  def status_bar_today
    Time.use_zone(@user.timezone) do
      total_seconds = @user.heartbeats.today.duration_seconds
      result = {
        data: {
          grand_total: {
            text: @user.format_extension_text(total_seconds),
            total_seconds: total_seconds
          }
        }
      }

      daily_goal = @user.goals.find_by(period: "day")
      if daily_goal && (goal_progress = ProgrammingGoalsProgressService.new(user: @user, goals: [ daily_goal ]).call.first)
        # Only append the goal text for users with simple_text style AND show_goals_in_statusbar enabled.
        if @user.simple_text? && @user.show_goals_in_statusbar
          goal_text = ApplicationController.helpers.short_time_simple(daily_goal.target_seconds)
          result[:data][:grand_total][:text] = "#{result[:data][:grand_total][:text]} / #{goal_text} goal"
        end

        result[:data][:goal] = {
          target_seconds: daily_goal.target_seconds,
          tracked_seconds: goal_progress[:tracked_seconds],
          completion_percent: goal_progress[:completion_percent],
          complete: goal_progress[:complete]
        }
      end

      render json: result
    end
  end

# ---- app/controllers/api/hackatime/v1/hackatime_controller.rb lines 214-299 ----
  def handle_heartbeat(heartbeat_array)
    result = HeartbeatIngest.call(
      user: @user,
      mode: :direct,
      heartbeats: heartbeat_array,
      request_context: {
        ip_address: request.headers["CF-Connecting-IP"] || request.remote_ip,
        machine: request.headers["X-Machine-Name"],
        user_agent: request.user_agent,
        ja4: request.headers["CF-JA4"]
      }
    )

    result.items.map do |item|
      next [ item.heartbeat.attributes, 201 ] if item.status == :accepted
      error = item.error

      unless error.is_a?(HeartbeatIngest::InvalidHeartbeatTime)
        report_error(
          error,
          message: "Error creating heartbeat: #{error.class}: #{error.message}",
          extra: { backtrace: error.backtrace&.first(20) }
        )
      end
      [ { error: error.message, type: error.class.name }, 422 ]
    end
  end

  def check_lockout = (render_forbidden("Account pending deletion") if @user&.pending_deletion?)

  def set_user
    @user = api_user_from_credentials(
      api_key_sources: %i[bearer basic query],
      allow_restricted: true
    )
    render_unauthorized unless @user
  end

  # allow either heartbeat or heartbeats
  def heartbeat_bulk_params
    if params[:_json].present?
      { heartbeats: permit_heartbeat_collection(params[:_json]) }
    elsif request.content_type&.include?("text/plain") && request.raw_post.present?
      { heartbeats: permit_heartbeat_collection(parse_plain_json) }
    else
      hackatime = params.require(:hackatime)
      { heartbeats: permit_heartbeat_collection(hackatime[:heartbeats]) }
    end
  end

  def heartbeat_params
    if params[:_json].present?
      permit_heartbeat(params[:_json].first) || {}
    elsif request.content_type&.include?("text/plain") && request.raw_post.present?
      parsed = parse_plain_json
      parsed = [ parsed ] unless parsed.is_a?(Array)
      permit_heartbeat(parsed.first) || {}
    elsif params[:hackatime].present?
      permit_heartbeat(params.require(:hackatime)) || {}
    else
      permit_heartbeat(params) || {}
    end
  end

  def parse_plain_json
    JSON.parse(request.raw_post)
  rescue JSON::ParserError
    nil
  end

  def permit_heartbeat_collection(value)
    return [] unless value.is_a?(Array)

    # Preserve array positions so a malformed item receives its own response
    # instead of discarding every valid heartbeat in the same CLI bulk upload.
    value.map { |heartbeat| permit_heartbeat(heartbeat) }
  end

  def permit_heartbeat(value)
    case value
    when ActionController::Parameters
      value.permit(*HEARTBEAT_KEYS, dependencies: [])
    when Hash
      ActionController::Parameters.new(value).permit(*HEARTBEAT_KEYS, dependencies: [])
    end
  end
