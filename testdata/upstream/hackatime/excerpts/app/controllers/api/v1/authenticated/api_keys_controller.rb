# Excerpt of hackclub/hackatime app/controllers/api/v1/authenticated/api_keys_controller.rb
# at commit 43bde10b02dbd2371fb4f802e724a1a875ac0dfc (git blob 72455719676d3ac7ce0de8b2d56be20d772c5d34).
# Copyright (c) 2026 Hack Club. MIT License; the full notice is in
# testdata/upstream/hackatime/LICENSE. Each block is copied verbatim from
# the lines its marker names. Lines between blocks are left out.

# ---- app/controllers/api/v1/authenticated/api_keys_controller.rb lines 5-13 ----
        def index
          render json: { token: api_key.token }
        end

        private

        def api_key
          @api_key ||= current_user.api_keys.first || current_user.api_keys.create!
        end
