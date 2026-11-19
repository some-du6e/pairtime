# Excerpt of hackclub/hackatime app/controllers/api/v1/authenticated/application_controller.rb
# at commit 43bde10b02dbd2371fb4f802e724a1a875ac0dfc (git blob 46ba56e89f66f18fd1829598e836351f5b3dd43c).
# Copyright (c) 2026 Hack Club. MIT License; the full notice is in
# testdata/upstream/hackatime/LICENSE. Each block is copied verbatim from
# the lines its marker names. Lines between blocks are left out.

# ---- app/controllers/api/v1/authenticated/application_controller.rb lines 16-26 ----
        def current_user
          @current_user ||= User.find(doorkeeper_token.resource_owner_id) if doorkeeper_token
        end

        def ensure_api_access_allowed
          render json: { error: "Unauthorized" }, status: :unauthorized if current_user&.api_access_restricted?
        end

        def ensure_no_pending_deletion
          render json: { error: "Unauthorized" }, status: :unauthorized if current_user&.pending_deletion?
        end
