# Excerpt of hackclub/hackatime app/controllers/api/v1/my/heartbeats_controller.rb
# at commit 43bde10b02dbd2371fb4f802e724a1a875ac0dfc (git blob 179ac223e62649f36e3e0be4b58d6b215398f0ca).
# Copyright (c) 2026 Hack Club. MIT License; the full notice is in
# testdata/upstream/hackatime/LICENSE. Each block is copied verbatim from
# the lines its marker names. Lines between blocks are left out.

# ---- app/controllers/api/v1/my/heartbeats_controller.rb lines 26-38 ----
  def index
    start_time = params[:start_time].present? ? Time.parse(params[:start_time]) : Time.current.beginning_of_day
    end_time = params[:end_time].present? ? Time.parse(params[:end_time]) : Time.current.end_of_day

    heartbeats = current_user.heartbeats
      .where("time >= ? AND time <= ?", start_time.to_f, end_time.to_f)
      .order(time: :asc)

    render json: {
      start_time: start_time, end_time: end_time,
      total_seconds: heartbeats.duration_seconds, heartbeats: heartbeats
    }
  end

# ---- app/controllers/api/v1/my/heartbeats_controller.rb lines 42-50 ----
  def ensure_authenticated!
    @current_user = api_user_from_credentials(
      oauth_scopes: [ "read" ],
      api_key_sources: %i[bearer basic]
    )
    render_unauthorized unless @current_user
  end

  def current_user = @current_user
