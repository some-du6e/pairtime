# Excerpt of hackclub/hackatime config/routes.rb
# at commit 43bde10b02dbd2371fb4f802e724a1a875ac0dfc (git blob 0a6026c1fa0a6bdc1794f50fbeafaa9f1af536c6).
# Copyright (c) 2026 Hack Club. MIT License; the full notice is in
# testdata/upstream/hackatime/LICENSE. Each block is copied verbatim from
# the lines its marker names. Lines between blocks are left out.

# ---- config/routes.rb lines 245-247 ----
  namespace :api do
    # This is our own API– don't worry about compatibility.
    namespace :v1 do

# ---- config/routes.rb lines 255-257 ----
      get "users/:username/stats", to: "stats#user_stats"
      get "users/:username/heartbeats/spans", to: "stats#user_spans"
      get "users/:username/trust_factor", to: "stats#trust_factor"

# ---- config/routes.rb lines 269-284 ----
      namespace :my do
        get "heartbeats/most_recent", to: "heartbeats#most_recent", export: true
        get "heartbeats", to: "heartbeats#index"
      end

      # oauth authenticated namespace
      namespace :authenticated do
        resources :me, only: [ :index ]
        get "hours", to: "hours#index"
        get "streak", to: "streak#show"
        get "projects", to: "projects#index"
        # get "projects/:name", to: "projects#show", constraints: { name: /.+/ }
        get "heartbeats/latest", to: "heartbeats#latest"
        get "api_keys", to: "api_keys#index"
      end
    end

# ---- config/routes.rb lines 286-291 ----
    # Admin-only API namespace
    namespace :admin do
      namespace :v1 do
        get "check", to: "admin#check"
        get "user/info", to: "admin#user_info"
        get "user/info_batch", to: "admin#user_info_batch"

# ---- config/routes.rb lines 300-300 ----
        post "user/get_user_by_email", to: "admin#get_user_by_email"

# ---- config/routes.rb lines 349-359 ----
    # Everything in this namespace conforms to wakatime.com's API.
    namespace :hackatime do
      namespace :v1 do
        get "/", to: redirect("/", status: 302) # some clients seem to link this as the user's dashboard instead of /api/v1/hackatime
        get "/users/:id", to: "hackatime#show_user"
        get "/users/:id/summaries", to: "hackatime#summaries"
        get "/users/:id/statusbar/today", to: "hackatime#status_bar_today"
        post "/users/:id/heartbeats", to: "hackatime#push_heartbeats"
        get "/users/current/stats/last_7_days", to: "hackatime#stats_last_7_days"
      end
    end
