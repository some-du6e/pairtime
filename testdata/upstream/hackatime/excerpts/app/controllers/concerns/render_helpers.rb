# Excerpt of hackclub/hackatime app/controllers/concerns/render_helpers.rb
# at commit 43bde10b02dbd2371fb4f802e724a1a875ac0dfc (git blob ec322217afdc86ccaf746836a3e7570ddabe0f90).
# Copyright (c) 2026 Hack Club. MIT License; the full notice is in
# testdata/upstream/hackatime/LICENSE. Each block is copied verbatim from
# the lines its marker names. Lines between blocks are left out.

# ---- app/controllers/concerns/render_helpers.rb lines 9-29 ----
  # Render a JSON error response. Returns nil so callers can early-return easily.
  def render_error(message, status: :unprocessable_entity)
    render json: { error: message }, status: status
    nil
  end

  def render_unauthorized(message = "Unauthorized")
    render_error(message, status: :unauthorized)
  end

  def render_forbidden(message = "lmao no perms")
    render_error(message, status: :forbidden)
  end

  def render_not_found_json(message = "Not found")
    render_error(message, status: :not_found)
  end

  def render_bad_request(message)
    render_error(message, status: :bad_request)
  end
