# Excerpt of hackclub/hackatime app/controllers/concerns/api/admin/v1/user_utilities.rb
# at commit 43bde10b02dbd2371fb4f802e724a1a875ac0dfc (git blob 29b7c654b68cb554314c002f8af90711b25a4bd6).
# Copyright (c) 2026 Hack Club. MIT License; the full notice is in
# testdata/upstream/hackatime/LICENSE. Each block is copied verbatim from
# the lines its marker names. Lines between blocks are left out.

# ---- app/controllers/concerns/api/admin/v1/user_utilities.rb lines 32-37 ----
        def get_user_by_email
          return render_error("bro dont have a email") if params[:email].blank?
          email_record = EmailAddress.find_by(email: params[:email])
          return render_error("email not found", status: :not_found) unless email_record
          render json: { user_id: email_record.user_id }
        end

# ---- app/controllers/concerns/api/admin/v1/user_utilities.rb lines 284-297 ----
        def user_info_batch
          return render_error("ids parameter required") if params[:ids].blank?

          user_ids = params[:ids].to_s.split(",").map(&:strip).map(&:to_i).uniq.take(2000)
          return render_error("no valid ids provided") if user_ids.empty?

          users = User.includes(:email_addresses).where(id: user_ids)
          render json: {
            users: users.as_json(
              only: %i[id username slack_uid slack_username github_username timezone country_code trust_level slack_avatar_url github_avatar_url],
              methods: %i[display_name avatar_url]
            )
          }
        end
