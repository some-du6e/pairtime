# Excerpt of hackclub/hackatime app/controllers/api/v1/authenticated/me_controller.rb
# at commit 43bde10b02dbd2371fb4f802e724a1a875ac0dfc (git blob 6809c76a7f08e3d8f6da0c4fd0a63c285a378eff).
# Copyright (c) 2026 Hack Club. MIT License; the full notice is in
# testdata/upstream/hackatime/LICENSE. Each block is copied verbatim from
# the lines its marker names. Lines between blocks are left out.

# ---- app/controllers/api/v1/authenticated/me_controller.rb lines 9-27 ----
        def index
          app = doorkeeper_token&.application
          exposed_level = if app&.verified? && app&.confidential?
            current_user.trust_level
          else
            current_user.public_trust_level
          end

          render json: {
            id: current_user.id,
            emails: current_user.email_addresses&.map(&:email)|| [],
            slack_id: current_user.slack_uid,
            github_username: current_user.github_username,
            trust_factor: {
              trust_level: exposed_level,
              trust_value: User.trust_levels[exposed_level]
            }
          }
        end
