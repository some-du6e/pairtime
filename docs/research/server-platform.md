# Server platform: accounts, git, profiles, live dashboard, stack, hosting

Status: draft in progress

Track: the central server (R5, R10, R11). Researched 2026-11-19. Every claim that isn't common knowledge carries a source; anything not confirmed is marked "(unverified)".

## Contents

1. Recommended design (summary)
2. Load model for R11
3. Accounts and sign-in
4. Git integration
5. Public profiles and project pages
6. Real-time dashboard
7. Stack and database
8. Ingestion API
9. Self-hosting and cost
10. APIs for programs and reviewers
11. Design implications
12. Open questions
13. Sources

## 1. Recommended design (summary)

(pending)

## 2. Load model for R11

(pending)

## 3. Accounts and sign-in

### 3.1 Hack Club Auth (HCA, formerly Identity Vault)

HCA is Hack Club's identity provider at `https://auth.hackclub.com`. Source: [hackclub/auth](https://github.com/hackclub/auth) (Ruby on Rails, Doorkeeper plus doorkeeper-openid_connect; no license in repo metadata), read at `9133160bebcf` (2026-11-19). Its local database is still called `identity_vault_development` (`README.md`), which confirms that HCA is the renamed Identity Vault.

Discovery document, fetched live 2026-11-19 from `https://auth.hackclub.com/.well-known/openid-configuration`:

| Key | Value |
|---|---|
| `issuer` | `https://auth.hackclub.com` |
| `authorization_endpoint` | `/oauth/authorize` |
| `token_endpoint` | `/oauth/token` (`client_secret_basic` or `client_secret_post`) |
| `userinfo_endpoint` | `/oauth/userinfo` |
| `jwks_uri` | `/oauth/discovery/keys` (ID tokens signed `RS256`) |
| `revocation_endpoint`, `introspection_endpoint` | `/oauth/revoke`, `/oauth/introspect` |
| `grant_types_supported` | `authorization_code`, `client_credentials`, `refresh_token` |
| `claims_supported` | `iss sub aud exp iat email email_verified phone_number phone_number_verified name given_name family_name nickname updated_at birthdate address slack_id verification_status ysws_eligible groups` |

Scopes (`app/models/oauth_scope.rb`; `app/views/docs/api.md.erb`):

| Scope | Gives | Who may request it |
|---|---|---|
| `openid` | `sub`, the public identity id, shaped `ident!abc123` | Community apps |
| `email` | `email`, `email_verified` | Community apps |
| `name`, `profile` | `name`, `given_name`, `family_name`, `nickname`, `updated_at` | Community apps |
| `slack_id` | `slack_id` (Hack Club Slack user id) | Community apps |
| `verification_status` | `verification_status` (`needs_submission`, `pending`, `verified`, `ineligible`) and `ysws_eligible` | Community apps |
| `phone`, `birthdate`, `address`, `legal_name`, `basic_info`, `groups` | Sensitive identity fields; `groups` exposes directory entitlements | HQ official apps only (`HQ_OFFICIAL_SCOPES`) |

`COMMUNITY_ALLOWED = %w[openid profile email name slack_id verification_status]`. A pairtime deployment registers as a community app on the Developer Apps page unless HQ marks it official, and those six scopes are enough for everything pairtime needs: a stable id, the email programs match on, the Slack id that review tools key on, and eligibility.

Other facts that shape the design:

- **Token lifetime.** `access_token_expires_in 6.months`, `use_refresh_token`, `reuse_access_token` (`config/initializers/doorkeeper.rb`). Access tokens are prefixed `idntk.`.
- **No PKCE.** `oauth_access_grants` has no `code_challenge` or `code_challenge_method` columns (`db/schema.rb`), which Doorkeeper needs for PKCE, and discovery advertises no `code_challenge_methods_supported`. Apps default to `confidential: true`. So a desktop client can't safely talk to HCA directly. The pairtime server must be the HCA client and keep the secret, and the desktop client signs in to pairtime with its own flow (3.5).
- **REST API.** `GET /api/v1/me` returns the identity for the granted scopes. `GET /api/external/check?email=|slack_id=|idv_id=` needs no auth and returns `needs_submission`, `pending`, `verified_eligible` ("Verified and eligible for YSWS (under 18)"), `verified_but_over_18`, `rejected` or `not_found` (`app/views/docs/api.md.erb`). Program keys (`prgmk.`, HQ official only) can read any identity that authorized the app; the doc says "If you think you need a `prgmk`, think harder about it."
- **Per-app gating.** The `resource_owner_authenticator` runs `Directory::RequirementCheck.passes?(identity:, app:)` and refuses sign-in to apps whose directory requirements the person doesn't meet. A pilot program could restrict a pairtime instance to a group this way (unverified beyond the code path).
- **Re-auth.** `prompt=login` or `max_age=<seconds>` forces a fresh login with TOTP, backup code or email (`app/views/docs/oidc-guide.md.erb`). Useful before a user submits hours or changes privacy settings.
- **No avatar.** The OIDC guide says HCA has no `picture` claim and points to cachet (`https://cachet.dunkirk.sh/users/{slack_id}/r`) for a Slack avatar by Slack id.

**Hackatime is moving to HCA-only sign-in now.** `app/models/hca_transition.rb` in hackclub/hackatime at `43bde10b02db`: "The three-week move to Hack Club sign-in only", `STARTED_AT = Time.utc(2026, 11, 13, 12, 21)`. Week one lets legacy sign-ins skip the link prompt; from `STARTED_AT + 2.weeks` (2026-11-27) "every unlinked session is asked, however it signed in". `users.hca_id` got a unique partial index on 2026-11-13 (`db/migrate/20261113104759_make_users_hca_id_index_unique.rb`), and Slack sign-in now refuses to create accounts: "No Hackatime account uses that Slack account. Sign in with Hack Club to create one." (`app/controllers/sessions_controller.rb`). Hackatime asks HCA for `email slack_id verification_status` (`app/models/concerns/oauth_authentication.rb`, `hca_authorize_url`).

### 3.2 Slack, GitHub and email

| Method | Endpoints and scopes | Gives | Role in pairtime |
|---|---|---|---|
| Sign in with Slack (OIDC) | `https://slack.com/openid/connect/authorize`, token `https://slack.com/api/openid.connect.token`, userinfo `https://slack.com/api/openid.connect.userInfo`; scopes `openid`, `email`, `profile` only, which can't be mixed with ordinary Slack scopes in one flow ([Slack docs](https://docs.slack.dev/authentication/sign-in-with-slack/), fetched 2026-11-19, no page date) | `https://slack.com/user_id`, `https://slack.com/team_id`, email. The sample ID token lives 300 s; no PKCE | Not needed: HCA already returns `slack_id`. Check `team_id` if it is ever used, since any workspace can sign in otherwise |
| Slack OAuth v2 (what Hackatime uses) | `https://slack.com/oauth/v2/authorize` with `user_scope=users.profile:read,users.profile:write,users:read,users:read.email` (`oauth_authentication.rb`) | A user token that can set the person's Slack status | Optional later feature ("pairing with Claude, 1h 20m today"). Not a sign-in method |
| GitHub | OAuth app at `https://github.com/login/oauth/authorize`; Hackatime asks only `user:email` | `github_uid`, username | Linked identity for repo ownership and commit matching (section 4), not the primary account. Hackatime requires a linked `github_uid` for leaderboards (sibling doc 1.7) |
| Email magic link | Single-use link, as Hackatime offers | Email only | Fallback for self-hosters and people outside Hack Club. Accounts created this way can't submit to YSWS programs until they link HCA |

### 3.3 What programs match on

Every review and payout system already keys on a small set of identifiers (sibling doc [hackatime-and-hack-club.md](hackatime-and-hack-club.md) 3.3 to 3.6):

| System | Identifier it uses |
|---|---|
| Unified YSWS DB `approved_projects` | `Email`, `GitHub Username`, plus name and address |
| Stardance submission rows | `user_slack_id`, `Email`, `hackatime_uid`, `hackatime_keys` |
| Robert (Telescreen) | `submitter: {slackId?, hackatimeId?}`; Alchemize sends "Never the owner's email, name, address, birthdate or Unified ID" |
| Ari ingest | `maker.{email, name, slack_id, hackatime_id}` |
| Telescreen deep links | Slack id or Hackatime numeric id |
| HCA itself | `ident!...` id, email, Slack id (`/api/external/check`) |

So a pairtime account stores: `hca_sub` (unique, the primary key for identity), `slack_id`, primary email (encrypted at rest, never public), `github_uid` and username when linked, and optionally the user's `hackatime_uid` when they link Hackatime, so reviewers can cross-check both trackers. Programs get `hca_sub` and `slack_id` by default. Email goes only to a program the user has authorized, because the Unified DB row needs it.

### 3.4 Account model

- One `user` row per person, created only through HCA on the Hack Club instance. A self-hosted instance (Karim's domain) can also allow email or GitHub sign-up, with a config flag.
- `identities(user_id, provider, subject, linked_at)` with a unique `(provider, subject)`: providers `hca`, `slack`, `github`, `gitlab`, `codeberg`, `email`. This mirrors Hackatime's conflict handling (`HCAIdentityConflictError`: an email or Slack already on another account refuses the link).
- `username` (public handle for `/@username`), `display_name`, `timezone`, `country_code` optional.
- Trust fields copied from Hackatime's model (`trust_level` blue/green/yellow/red with yellow masked as blue, and an audit log), described in the sibling doc 1.9. They belong to the reviewer side (section 10), not the public profile.

### 3.5 Signing in the desktop client

Because HCA has no PKCE and Slack's OIDC needs a client secret, the desktop client authenticates to pairtime, not to HCA. Two standard options:

| Flow | How it works | Fit |
|---|---|---|
| OAuth 2.0 Device Authorization Grant ([RFC 8628](https://www.rfc-editor.org/rfc/rfc8628)) | Client `POST /oauth/device/code`, shows `user_code` and `verification_uri`; the user opens the page in any browser already signed in with HCA and approves; the client polls `/oauth/token` with `grant_type=urn:ietf:params:oauth:grant-type:device_code` until it gets tokens | Works on any OS without a local web server or custom URL scheme, survives school firewalls, and the same flow serves a headless Linux box. GitHub CLI uses it |
| Loopback redirect with PKCE ([RFC 8252](https://www.rfc-editor.org/rfc/rfc8252) section 7.3) | Client listens on `127.0.0.1:<random port>`, opens the browser to pairtime's authorize URL with a PKCE challenge, receives the code on the loopback | One click fewer, but a local listener can trip antivirus prompts on Windows (unverified) |

Recommendation: device grant as the default, loopback as an optimisation. The token the client receives is a per-device credential (section 8.3), revocable from the web UI, never the user's HCA token.

## 4. Git integration

R5 asks for "git integration". For pairtime that means three jobs: give each project a stable identity, tie credited time to the commits it produced, and let a reviewer check those commits against a forge where one exists. None of it needs code to leave the machine.

### 4.1 Ways to connect to git

| Option | What it can see | Privacy cost | Verdict |
|---|---|---|---|
| **Client reads the local repo** (refs, reflog, `git log`, `git diff --shortstat`) | Every commit the moment it's made, on any host or none: SHA, parents, author and committer time, branch, diffstat, and amends and rebases through the reflog | None; only numbers and hashes leave the machine | **Primary source.** Works for GitLab, Codeberg, self-hosted and never-pushed repos alike |
| **Local git hooks** | Same as above but event-driven (`post-commit`, `post-rewrite`, `pre-push`) | None | Optional accelerator. Only safe through Git 2.54's config-based hooks (below); `core.hooksPath` would replace every repo's own hooks |
| **GitHub App** | Repos the user installs it on. Push webhooks with up to 2,048 commits per event; commit API with `Contents: read` | `Contents: read` also exposes file contents of private repos to the server | Use for **verification** and push webhooks on repos the user chooses, plus identity linking |
| **GitHub OAuth app** | Public data with no scopes; private repos only with `repo`, which is full read/write | `repo` is far too broad | Identity linking only, if at all. Hackatime uses it with `user:email` |
| **GitLab OAuth** | `read_user` / `openid` for identity; private commits need `read_api`, which reads "all groups and projects" | `read_api` is broad | Identity plus public-project verification through the unauthenticated API |
| **Codeberg (Forgejo 17.0.0)** | OIDC sign-in works and supports PKCE, but "OAuth2 scopes are not yet implemented" and tokens "can be used to execute any actions on behalf of the user" ([Forgejo docs v17.0](https://forgejo.org/docs/latest/user/oauth2-provider/)) | Any stored token is full account access | Sign in to learn the username, then discard the token. Verify public repos unauthenticated |

Facts behind the table:

- **GitHub Apps vs OAuth apps** ([GitHub docs](https://docs.github.com/en/apps/oauth-apps/building-oauth-apps/differences-between-github-apps-and-oauth-apps), fetched 2026-11-19): "GitHub Apps use fine-grained permissions"; "OAuth apps need to request the `repo` scope to get access to issues, pull requests, or anything owned by the repository"; installing an app "grants the app access to a user or organization account's chosen repositories"; GitHub Apps "have built-in, centralized webhooks", while OAuth apps "must configure webhooks individually for each repository" and those webhooks "are not automatically disabled" when the token is deleted. "In general, GitHub Apps are preferred over OAuth apps."
- **Token lifetimes.** GitHub App user tokens: "The user access token expires after eight hours, and the refresh token expires after six months" (`expires_in` 28800, `refresh_token_expires_in` 15897600), and refresh tokens are single-use ([GitHub docs](https://docs.github.com/en/apps/creating-github-apps/authenticating-with-a-github-app/refreshing-user-access-tokens)). Installation tokens expire after 1 hour. GitLab access tokens expire after 7,200 s and refresh ([GitLab docs](https://docs.gitlab.com/integration/oauth_provider/)).
- **Rate limits** ([GitHub REST rate limits](https://docs.github.com/en/rest/using-the-rest-api/rate-limits-for-the-rest-api)): unauthenticated 60 requests/hour per IP; user tokens 5,000/hour (shared with the user's other apps); installation tokens 5,000/hour, plus 50 per repo and 50 per user above 20 each, capped at 12,500; secondary limits of 100 concurrent requests and 900 points/minute for REST. Hackatime limits its own GitHub mapping to 50 requests per user per day (sibling doc 1.7).
- **Push payloads.** GitHub: "The array includes a maximum of 2048 commits"; a GitHub App needs "at least read-level access for the 'Contents' repository permission" to receive `push`; payloads "are capped at 25 MB" ([GitHub webhook docs](https://docs.github.com/en/webhooks/webhook-events-and-payloads#push)). The per-commit fields (`id`, `tree_id`, `distinct`, `message`, `timestamp`, `author`, `committer`, `added`, `removed`, `modified`) are from the published payload schema and were not re-read on this fetch (unverified). GitLab sends "the newest 20 commits only" plus `total_commits_count`, and sends no push hook at all when one push touches more than 3 branches by default (`push_event_hooks_limit`) ([GitLab docs](https://docs.gitlab.com/user/project/integrations/webhook_events/)). Both list `added`/`modified`/`removed` file paths, so a webhook receiver learns file names whether it wants them or not.
- **Config-based hooks.** Git 2.54 (GitHub blog dated 2026-04-20) added `hook.<name>.event` and `hook.<name>.command` in any config scope; "you can now *run multiple* hooks for the same event"; "The traditional hook script in `$GIT_DIR/hooks` still works, and runs last"; `hook.<name>.enabled = false` disables one per repo ([Highlights from Git 2.54](https://github.blog/open-source/git/highlights-from-git-2-54/)). A global `[hook "pairtime-commit"] event = post-commit` is therefore safe on 2.54+. On older Git, which old Windows laptops are likely to have (unverified), the client should poll refs instead of installing hooks.

What Hackatime does: projects are free-text names from heartbeats, `AttemptProjectRepoMappingJob` maps a name to a public GitHub repo, `PullRepoCommitsJob` imports commits, private repos are never mapped, and the GitHub app asks only for Metadata (read) (sibling doc 1.7). The admin timeline draws commit markers next to spans (sibling doc 1.9). WakaTime goes further: `GET /api/v1/users/current/projects/:project/commits` returns per-commit `total_seconds` ("time coded in editor for this commit") with author and committer fields, for repos connected through its GitHub, GitLab or Bitbucket integration ([WakaTime API docs](https://wakatime.com/developers/), fetched 2026-11-19). The docs don't say how time is assigned to a commit.

### 4.2 Project identity

A project needs an id that survives renames, re-clones, forks and moving from local-only to GitHub. Free-text names (Hackatime's model) break all of these and are why Stardance makes users pick `hackatime_keys` by hand.

Proposed key, computed by the client:

1. **Root commit set.** `git rev-list --max-parents=0 HEAD`, sorted. A clone on another machine, a push to any forge, and a rename all keep it. The server stores `HMAC(server_pepper, root_sha)` as `repo_fingerprint` so a root SHA of a private repo isn't readable from the database, while two machines of the same user still converge on one project.
2. **Normalized remotes.** `git remote get-url` for each remote, normalized to `host/owner/name` (strip `.git`, credentials and protocol). Sent in clear only when the user marks the project public or the remote is a public repo the server can confirm.
3. **No git.** Folder path hashed with a per-user salt, plus a user-chosen display name.

Forks share root commits with their upstream, so the fingerprint is scoped per user, never global. Two different users with the same fingerprint (a fork, a template, a classmate's repo) is itself a reviewer signal for `HOURS_REUSE`-style checks (sibling doc 3.6).

### 4.3 Linking credited time to commits without uploading code

The client already knows, for each repo, the credited intervals (from the scoring layer) and the commits (from refs and reflog). Per commit it sends a `commit` record:

| Field | Example | Why |
|---|---|---|
| `sha`, `parents[]` | 40-hex | A commitment to the full tree and history. The server can check it against a forge later. Git hashes with collision-detecting SHA-1 (or SHA-256 in new-format repos), so passing off a different tree under the same SHA isn't a practical attack |
| `author_time`, `committer_time` | epoch s with offset | Both are user-settable, so never trusted alone |
| `observed_at` | client monotonic-anchored time (section 8.4) | When the client first saw the commit |
| `branch` | `main` | Display; hashed unless the project is public |
| `files_changed`, `insertions`, `deletions` | `7, 212, 31` | From `git diff --shortstat` against the first parent |
| `path_hashes[]` (optional) | HMAC per path with the user's key | Lets the server match commits to files touched in sessions without learning names |
| `credited_seconds_since_parent` | `7380` | Credited human time on this repo between the parent commit's `observed_at` and this one, split by `activity_kind` |
| `ai_attribution` (optional) | `{ai_lines, human_lines, tool}` | From git-ai notes or Agent Trace where present (prior-art doc 1.3, 1.4) |

The server adds `server_first_seen_at` the first time it receives the SHA. That number is the anti-backdating defence: the handbook calls "A commit history that was manufactured after the fact" outright fraud (sibling doc 3.7), and author dates can be set to anything. A commit whose `server_first_seen_at` is weeks after its `author_time` is flagged `backfilled`, the same witnessed/backfilled split grit uses for turns (prior-art doc 1.14).

When the repo is public (or the user installed the GitHub App on it), the server fetches `GET /repos/{owner}/{repo}/commits/{sha}` (GitHub), `GET /projects/:id/repository/commits/:sha` (GitLab) or `GET /api/v1/repos/{owner}/{repo}/git/commits/{sha}` (Forgejo) once, confirms the SHA and diffstat, and stores the commit's subject line and URL. The commit is then `forge_verified`. For never-pushed repos the commit stays `local_only`: still witnessed, still useful to a reviewer, and verifiable later if the user ever pushes it, because the SHA can't change.

What a project page then shows per commit, with no code uploaded:

```text
a1b2c3d  "Fix wall clipping in BSP traversal"        github.com/rowan/cs2go
         2h 03m credited since previous commit  (1h 10m reviewing diffs, 31m debugging, 22m hand edits)
         +212 −31 in 7 files · witnessed 14:02 · verified on GitHub
```

For a private or local repo the same line shows the short SHA, the stats and "local commit, witnessed", with no subject line.

### 4.4 Rules for crediting time to commits

- Time belongs to a commit only if it was credited on the same `repo_fingerprint` after the parent's `observed_at` and before this commit's. Time after the last commit shows as "uncommitted" (Ari shows the same thing as "Time after the last commit", sibling doc 3.6).
- Rebases and amends rewrite SHAs. The client follows `post-rewrite` (hook or reflog `rebase`/`amend` entries) and sends `rewrites: [{old_sha, new_sha}]`, so credited time moves to the new SHA instead of disappearing or doubling.
- Squash merges on the forge produce a SHA the client never saw. The server links it through the PR's commit list when the GitHub App is installed, or leaves the local commits as the evidence.
- Merge commits get the time credited on the branch since the previous merge base. Shown, not double-counted, because time is credited once in the global timeline and only labelled with commits afterwards (sibling doc Design implication 7).

## 5. Public profiles and project pages

### 5.1 What the reference products show

**Hackatime** (hackclub/hackatime at `43bde10b02db`):

| Page | Route | Content | Controls |
|---|---|---|---|
| Profile | `GET /@:username` | `ProfileStatsService::PROFILE_DASHBOARD_KEYS`: `total_time`, `total_heartbeats`, `project_durations`, `language_stats`, `editor_stats`, `operating_system_stats`, `category_stats`, `weekly_project_stats`, top and singular project/language/editor/OS/category, plus an `activity_graph`; social links (GitHub, Twitter, Bluesky, LinkedIn, Discord, website); an OG image (`app/controllers/profiles_controller.rb`, `app/services/profile_stats_service.rb`) | `allow_public_stats_lookup` (default `true`) hides the stats from everyone but the owner; `leaderboard_opt_out` hides the user from leaderboards. Those two checkboxes are the whole privacy page (`app/controllers/settings/privacy_controller.rb`, `privacy_params`) |
| Project | `GET /@:username/project/:project_name` | Total time, file count, language stats, **file stats and branch stats**, repo URL, "since" date, OG image ("@user has spent 3h coding X. Tracked with Hackatime.") | 404 unless `project_repo_mappings.public_shared_at` is set: per-project sharing is opt-in |

So Hackatime's profile is public by default and lists every project name, while a shared project page shows file names and branches. A folder called `college-essay` or `discord-token-grabber` shows up on the public profile unless the user turns off all stats.

**WakaTime** ([API docs](https://wakatime.com/developers/), fetched 2026-11-19) has finer switches. The user object carries `logged_time_public`, `languages_used_public`, `editors_used_public`, `categories_used_public`, `os_used_public`, `is_email_public`, `is_photo_public` and `is_hireable`; stats responses echo them as `is_coding_activity_visible`, `is_language_usage_visible`, `is_editor_usage_visible`, `is_category_usage_visible`, `is_os_usage_visible`. Projects have a `privacy` setting, organizations a `default_personal_privacy` of "visible or hidden", and embeddables "can be retracted to prevent future access to your code stats". Machine names in stats can include the IP address "when user's preference is to show IPs in machine names".

### 5.2 Proposed visibility model for pairtime

R5 asks for "public-by-default user profiles", R6 for something "most privacy conscious users are comfortable installing". Both hold if what's public by default is aggregate and anything that names a thing is public only when it's already public elsewhere.

| Item | Default | User control |
|---|---|---|
| Profile page (`/@username`): credited hours today, this week, all time; streak; activity heatmap; top languages; harnesses used (Claude Code, Cursor, Codex...) | Public | Whole profile private (one switch, like Hackatime's) |
| Breakdown of credited time by `activity_kind` (prompting, reviewing, testing, hand edits, debugging) | Public as a single bar; detail on click | Hide |
| Project linked to a **public** repo | Public, with name, repo link, hours and commit list (4.3) | Hide project |
| Project with a private repo, or no repo | Counted in totals as "3 private projects, 12h"; no name | Make public with a chosen display name |
| Commit subject lines | Shown only for forge-verified commits in public repos (already public there) | Hide |
| File names, paths, branch names of private repos, machine names, IPs, prompts, model outputs | **Never public**; file paths and prompts never reach the server at all for logged hours | None needed |
| Leaderboards | Opt-in, unlike Hackatime's opt-out. Ranked on consistency over a rolling window rather than raw hours (prior-art doc Design implication 15) | Join or leave |
| Email, HCA id, Slack id | Never public; given to programs the user authorized (3.3) | Per-program revoke |
| Self-improvement views (re-prompt loops, dark-flow time, agent-only time) | **Private** to the user | Share a snapshot |

Two more rules: a profile never shows hours the server marked as excluded (Hackatime's `heartbeat_exclusions` idea), and a suspected (`yellow`) user's profile looks exactly like everyone else's, as Hackatime does with `mask_trust_level` (sibling doc 1.9).

### 5.3 Pages a reviewer needs (not public)

The public profile is for motivation (R3). Reviewers need a different page, behind HCA sign-in with a reviewer role, for one submission at a time (section 10.3): the submission's window and credited seconds, the per-row breakdown a reviewer can cut, the commit list with witness times and forge verification, flags, the trust history, and links to uploaded evidence. Keep it on a separate route prefix (`/review/...`) so caching and access rules can't leak it onto public pages.

### 5.4 Motivation features that cost almost nothing on the server

Embeddable badges and OG images (Hackatime has both: `GET /api/v1/badge/:user_id/*project`, `profile_og_image`), a status-bar endpoint for editors (`statusbar/today`), weekly summary emails, and a "currently pairing" list (Hackatime's `GET /api/v1/currently_hacking`, users with activity in the last 5 minutes) are all reads of rollups. They help R3 at negligible R11 cost.

## 6. Real-time dashboard

R5: "a stats dashboard that updates in real-time as time is logged". The prior-art doc (section 5.3) already settled the transport question in favour of SSE over HTTP/2 with 15 s keepalives. This section adds how Hackatime does it, what fan-out costs at R11 scale, and the concrete stream design.

### 6.1 How Hackatime does it today

There is no push channel. `config/cable.yml` is the only Action Cable file in the tree (the Rails default); there are no channels, no `EventSource` and no WebSocket code (hackclub/hackatime tree at `43bde10b02db`). `ARCHITECTURE.md` section 4: `DashboardStats` serves all-time views from `dashboard_rollups` and queries heartbeats for other ranges; "Short Rails caches (currently 1/5/15 minutes depending on fragment)"; and `DashboardRollupRefreshService` "rebuilds totals, dimensions, weekly projects, project details, filter options, activity graph and today's stats from the user's non-archived heartbeats" and "atomically replaces all of one user's rows in a transaction", scheduled after heartbeat commits. Those rebuilds "take about 17 worker-hours a day" (PR #1745, sibling doc 1.10). A dashboard refreshes when the page is reloaded and the cache fragment has expired.

The lesson: recomputing aggregates from raw events on every write is what makes Hackatime's dashboard expensive, not the delivery. If credited seconds are additive per `(user, project, day, activity_kind)`, the ingest path can apply a delta with one upsert and push the same delta to open dashboards, and nothing is ever rebuilt on the hot path.

### 6.2 Fan-out cost at R11 scale

Load figures from the prior-art doc (5.1): 152 concurrent active users on average, about 610 at a daily peak (4x) and 1,220 at an event peak (8x), all assumptions until Hackatime's hourly histogram is available.

| Quantity | Assumption | Result |
|---|---|---|
| Client syncs | 1,220 trackers, one batch per 60 s, plus an immediate sync on prompt and stop hooks | ~20-40 updates/s |
| Open dashboard and profile tabs | Up to 5,000 at an event peak (4x the active trackers, generous) | 5,000 SSE streams |
| Messages from user updates | Each update goes to the user's own topic (1-3 tabs) and their public profile topic (0-few viewers) | ~100 msgs/s |
| Messages from global topics | Leaderboard and "currently pairing" coalesced to one message per 15 s, sent to every open tab | 5,000 / 15 = 333 msgs/s |
| Egress | ~300 bytes per message | ~130 KB/s, about 0.34 TB/month at a sustained peak (it won't be sustained) |
| Memory, Go | Goroutine stack starts at `stackMin = 2048` bytes (`src/runtime/stack.go`); `net/http` adds a 4 KiB `bufio.Reader` per connection and a `bufferBeforeChunkingSize = 2048` writer per response (`src/net/http/server.go`, golang/go master on 2026-11-19). Call it 10-20 KB per idle SSE stream with TLS terminated at the proxy | 5,000 streams ≈ 50-100 MB |
| Memory, BEAM | Phoenix held "2 million clients" on a "40core/128gb box" with "1s broadcasts" ([Phoenix blog, 2015-11-03](https://www.phoenixframework.org/blog/the-road-to-2-million-websocket-connections)), an upper bound of ~64 KB per connection | 5,000 sockets ≈ 0.3 GB at most |

Polling for comparison: 5,000 tabs polling every 10 s is 500 authenticated requests/s, each needing a cache or rollup read, with up to 10 s of staleness. SSE costs one long-lived stream per tab and sends nothing when nothing changes. At this scale either fits on one small box; SSE wins on freshness, not on capacity.

### 6.3 Where fan-out happens

- **One process: in memory.** A topic map (`user:<id>`, `profile:<username>`, `project:<id>`, `global:leaderboard`, `global:pairing`) with a bounded per-subscriber channel. Slow subscribers are dropped and reconnect, never allowed to block ingest.
- **Several processes: a broker.** Postgres `LISTEN/NOTIFY` works, with limits: payload "must be shorter than 8000 bytes", the queue is "8GB in a standard installation", and notifications are delivered only at commit ([PostgreSQL 18 docs](https://www.postgresql.org/docs/current/sql-notify.html)). Recall.ai found that a transaction that issued `NOTIFY` takes `AccessExclusiveLock on object 0 of class 1262 of database 0` during commit, "effectively serializing all commits", which stalled their database with "tens of thousands of simultaneous writers" ([Recall.ai, 2025-07-01, revised 2026-05-08](https://www.recall.ai/blog/postgres-listen-notify-does-not-scale)). The post's later update says an upstream Postgres commit "has eliminated the bottleneck"; which release carries it is unverified. At pairtime's ~20-40 writes/s it's harmless if each ingest transaction sends at most one `NOTIFY` carrying only ids. A small NATS or Redis instance is the alternative if writes grow.
- **Coalescing.** Global topics are recomputed at most every 15 s from rollups and broadcast once. Per-user topics are sent at most once per 2 s per user, merging deltas.

### 6.4 Stream design

```text
GET /api/v1/stream?topics=user:me,profile:@rowan,global:pairing
Accept: text/event-stream
Last-Event-ID: user:me:18342          (sent by the browser on reconnect)

retry: 5000
: keepalive                            (every 15 s)

id: user:me:18343
event: credited
data: {"project":"p_9f2c","day":"2026-11-19","delta_s":60,"today_s":5400,"week_s":31200,"kinds":{"reviewing":1800,"prompting":1200,"hand_edit":900,"testing":1500}}

id: user:me:18344
event: commit
data: {"project":"p_9f2c","sha7":"a1b2c3d","credited_since_parent_s":7380,"verified":"github"}

event: pairing
data: {"count":214,"top":[{"u":"rowan","harness":"claude-code","since":"2026-11-19T19:02:00Z"}]}
```

Rules:

- One `EventSource` per tab carrying every topic it needs, over HTTP/2, because browsers cap HTTP/1.1 SSE at 6 connections per domain across tabs (MDN, cited in prior-art doc 5.3). A `SharedWorker` or `BroadcastChannel` can share one stream across tabs later.
- Each topic keeps a ring buffer of the last 5 minutes of events. `Last-Event-ID` inside the window replays the gap; outside it the server sends `event: resync` and the page refetches its snapshot (`GET /api/v1/users/me/dashboard`).
- Public topics (`profile:`, `project:`, `global:`) are anonymous and carry only what the public page shows (5.2). Private topics need the session cookie.
- Fallback: if no event or keepalive arrives for 45 s (a school proxy buffering the stream, for example), the page switches to polling the snapshot endpoint every 30 s with `If-None-Match`.
- Behind Cloudflare: SSE through `cloudflared` tunnels was buffered for years; the maintainers closed the issue on 2025-05-14 with "Server Sent Events have been fixed and this should now work, even if the right header isn't set" ([cloudflare/cloudflared#199](https://github.com/cloudflare/cloudflared/issues/199)). Send `Content-Type: text/event-stream`, `Cache-Control: no-cache` and keepalives anyway, and keep each stream under Cloudflare's 125 s proxy read timeout between bytes (prior-art doc 5.3).
- The editor status bar keeps using a plain cached `GET .../statusbar/today`, as WakaTime clients expect.

## 7. Stack and database

### 7.1 What the server has to do

At R11 scale the workload is small: 20-80 ingest requests/s at an event peak, about 1 GB/month of derived rows if the client computes credit (prior-art doc 5.2, schema A), up to 5,000 SSE streams (6.2), and a review UI used by a few dozen reviewers. Any of the five stacks can serve that from one small box. Capacity doesn't pick the stack. These do:

1. **One scoring implementation.** The client computes credited time locally (privacy, R6) and the server must be able to recompute it from the same inputs at submission time to check it (R9, R10). If server and client share a language, they share the scoring package and its test vectors, and "the server got a different number" can only mean tampering or a version mismatch. The client doc ([client-platform.md](client-platform.md) 1.4) narrows the client to Go or Rust and favours cgo-free Go for cross-compilation.
2. **A self-host an agent can run.** Karim's fleet agent will deploy it on a home server (BOUNTY.md). One binary plus one data directory is the easiest thing to deploy, back up and restore.
3. **Contributors on old laptops.** Teen contributors may be on the same 10-year-old Windows laptops as users (R8). No Docker, no database server and fast rebuilds help them more than any framework feature.
4. **AI agents as contributors.** Most code will be written by agents steered by people, so a small, regular codebase with types and quick compile-test loops matters.

### 7.2 Five stacks compared

Versions current on 2026-11-19 (GitHub releases, go.dev, nodejs.org): Go 1.27.4, Rust 1.100.0 (axum 0.8.9), Bun 1.4.4 and Node 26.13.1 LTS (Hono 4.13.19, SvelteKit adapter-node 6.0.5), Elixir 1.20.5 with Phoenix 1.8.16 and LiveView 1.2.13, Rails 8.1.4.1.

| Criterion | Go | TypeScript (Bun or Node) | Elixir + Phoenix | Rails (Hackatime's stack) | Rust |
|---|---|---|---|---|---|
| Developers using it, Stack Overflow 2026, all respondents (n = 14,099) ([survey](https://survey.stackoverflow.co/2026/technology/data/languages)) | 16.5% | 43.8% (JavaScript 62.0%) | 2.6% | 5.8% | 18.0% |
| hackclub org repos pushed in the last 12 months, by primary language (GitHub search, 2026-11-19) | 21 | 170 (plus 122 JavaScript, 67 Svelte) | 0 | 52 | 18 |
| Hack Club production apps in it | ari-webhooks | Lapse, Ari web, High Seas, Shipwrecked | none found | Hackatime (78 contributors), Stardance, Flavortown, Summer of Making, HCA, HCB | Lookout, hackatime-setup |
| Agent issue-resolution on real repos, best resolved rate, Multi-SWE-bench (2025 models; Go 428 tasks, TS 224, JS 356, Rust 239) ([arXiv 2504.02605](https://arxiv.org/html/2504.02605v1)) | 7.48% | 11.61% (JS 5.06%) | not covered | not covered | 15.90% |
| Shares scoring code with the client | Yes, if the client is Go | No | No | No | Yes, if the client is Rust |
| Self-host artifact | One static binary (`CGO_ENABLED=0`), UI embedded with `embed`, pure-Go SQLite | `bun build --compile` single file; `bun.exe` itself is 115.3 MB (client doc 1.4) | `mix release` directory with the runtime; single file needs a wrapper such as Burrito (unverified) | Docker image (Hackatime: `Dockerfile` for Kamal or Coolify, plus a worker image) | One static binary |
| Idle RSS, one process (Linux, client doc 1.2) | 3.6 MB | Node 41.9 MB | not measured (unverified) | not measured; a booted Rails app is far above the runtimes listed (unverified) | 3.0-4.7 MB |
| Real-time fit | SSE in `net/http`, ~10-20 KB per stream (6.2) | SSE straightforward | Best: Phoenix PubSub and LiveView push diffs over WebSockets out of the box | Action Cable is WebSockets; SSE through `ActionController::Live` holds a Puma thread per stream, and Hackatime runs `RAILS_MAX_THREADS` 3 (sibling doc 1.1) | SSE in axum |
| Rebuild time for contributors | Seconds | None for the server; type-check only | Moderate | None | Minutes for clean builds of an async web stack (unverified) |
| What can be reused | grit's server (`gritd`, Go + SQLite, AGPL-3.0, prior-art doc 1.14) and Wakapi's WakaTime-compatible API (Go, MIT) | Ari's review UI patterns (no license) | nothing in Hack Club | Hackatime's ingest, UA parser and program API (MIT) | Lookout's capture code (AGPL-3.0) |

Notes on the evidence:

- The agent benchmark is weak evidence. Multi-SWE-bench used early-2025 models, ranks "Python and Java first", puts Go and Rust "generally outperforming TS and JS but falling behind Java", and says "TS and JS consistently yield the lowest resolved rates". The other often-cited result, that Elixir had "the highest completion rate across models" among 20 languages in Tencent's AutoCodeBench ([Dashbit, 2026-02-05](https://dashbit.co/blog/why-elixir-best-language-for-ai)), measures single-pass problem solving, which the post itself says ("Elixir shines on the first pass, without a coding agent behind it"). Neither measures maintaining a mid-size web app. Treat them as a tiebreaker at most.
- Rails is the strongest choice for reuse and for Hack Club's own maintainers, and the weakest for the single-binary self-host and for sharing code with a native client.
- Elixir has the best real-time story and the smallest contributor pool: no hackclub repo in it in the last year.
- TypeScript has the largest pool and the Svelte front-end habits Hack Club already has, but it can't share code with a Go or Rust client, and a resident Bun or Node process is 10x the native memory floor.

### 7.3 Recommendation: Go API, Svelte UI, SQLite

- **Go** for ingest, scoring, APIs, SSE and background jobs, in one binary. It shares `pkg/score` with the client, cross-compiles to every target in seconds, needs no runtime on the host, and keeps a contributor's edit-build-test loop in seconds on an old laptop. grit's `gritd` (Go 1.26, pure Go, SQLite, SSE) is proof that this shape works for this exact product (prior-art doc 1.14).
- **Svelte with TypeScript** for the web UI, built by Vite to static files and embedded in the Go binary. Hack Club contributors already write Svelte (67 recent hackclub repos; Hackatime's UI is Svelte through Inertia; Ari is SvelteKit). The API is described in OpenAPI and the TS types are generated from it, so the language boundary is typed.
- If the client ends up in Rust, the same argument picks a Rust server (axum). The rule is: match the client so scoring exists once.

### 7.4 Database

| Engine | Version, license (2026-11-19) | Fit |
|---|---|---|
| **SQLite** in WAL mode + Litestream | SQLite public domain; Litestream v0.5.18 (2026-11-03), Apache-2.0 | Default. One file; readers never block the writer; continuous backup to S3-compatible storage |
| PostgreSQL | 18 (docs show 18.7), PostgreSQL License; pg_partman v5.5.1 (2026-11-17), PostgreSQL License | Supported alternative when Hack Club wants it inside its existing Postgres operations, or when more than one app server is needed |
| TimescaleDB | 2.32.0 (2026-11-12); compression and continuous aggregates are under the Timescale License | No: TSL terms don't fit an AGPL entry (prior-art doc 5.4), and compression isn't needed at ~1 GB/month |
| ClickHouse | v26.9.32.8-stable (2026-11-18), Apache-2.0 | Only if pairtime stores raw WakaTime heartbeats at Hackatime volume. All 426M Hackatime heartbeats fit in 5.5 GiB there (PR #1745, sibling doc 1.10) |
| DuckDB | v2.0.0 (2026-10-21), MIT | Offline analysis for Fraud Squad against a restored Litestream replica, so heavy queries never touch production |

Why SQLite is enough, with numbers:

- **Write rate.** SQLite "will only allow one writer at any instant in time" ([sqlite.org/whentouse](https://www.sqlite.org/whentouse.html)). The FAQ's old figure of "a few dozen transactions per second" came from waiting on a 7200 RPM platter; its 2024-11-19 update says "SQLite will do far more than 50K inserts/second now" and that batching operations in one transaction is what matters ([FAQ 19](https://www.sqlite.org/faq.html)). In WAL mode with `synchronous=NORMAL`, "the checkpoint is the only operation to issue an I/O barrier or sync operation", at the cost that the last transactions "might rollback following a power failure" ([sqlite.org/wal](https://www.sqlite.org/wal.html)). Pairtime funnels all writes through one writer goroutine that commits every 50 ms or 500 rows, whichever comes first. That's at most 20 commits/s at any request rate. The client keeps its upload queue until the server acknowledges it (8.2), so a lost last-second commit after a power cut is re-sent and deduplicated.
- **Read rate.** SQLite's own guide: "any site that gets fewer than 100K hits/day should work fine", "a conservative estimate", and it "has been demonstrated to work with 10 times that amount". Dashboards read rollup rows, and SSE replaces most polling (6.2).
- **Size.** About 12 GB/year of derived rows (prior-art doc 5.2) and 0.5-2 GB of evidence metadata, with evidence blobs themselves in object storage (section 9).
- **Required version.** sqlite.org/wal section 11 documents a "WAL-reset bug" that can corrupt a database when several connections write or checkpoint at the same instant, "present in versions 3.7.0 through 3.51.2", fixed in 3.51.3 with backports in 3.44.6 and 3.50.7. Pin a driver that bundles a fixed SQLite and assert `sqlite_version()` at startup. Go drivers: `modernc.org/sqlite` v1.62.0 (pure Go), `github.com/ncruces/go-sqlite3` v0.37.0 (Wasm through wazero, no cgo; its `database/sql` driver now uses `BEGIN IMMEDIATE` by default), `github.com/mattn/go-sqlite3` v1.14.53 (cgo, which the cross-compile rule excludes). Which SQLite each bundles wasn't checked here (unverified).
- **Limits to plan for.** WAL "does not work over a network filesystem", so the data directory must be local disk, not NFS or a Docker volume on a network share. Only one app process can write, so scaling out means moving to Postgres.

Keeping the Postgres path open: write plain SQL that both engines accept (no SQLite-only `INSERT OR REPLACE`, use `ON CONFLICT ... DO UPDATE`), store times as integer epoch milliseconds, ids as UUIDv7 text (Postgres 18 has `uuidv7()`, "temporally sortable", per the [18.0 release notes](https://www.postgresql.org/docs/release/18.0/), released 2025-09-25), and run the test suite against both engines in CI. On Postgres, partition `client_events` and `credited_intervals` by month with declarative partitioning and pg_partman, which also makes retention a `DROP` of old partitions. On SQLite, the same retention for raw events is a per-month attached database file that gets deleted.

### 7.5 Core tables

| Table | Key columns | Notes |
|---|---|---|
| `users`, `identities` | 3.4 | `identities(provider, subject)` unique |
| `devices` | `id`, `user_id`, `name`, `token_hash`, `created_at`, `last_seen_at`, `revoked_at`, `client_version`, `os` | One per installed client (8.3) |
| `projects` | `id`, `user_id`, `repo_fingerprint`, `display_name`, `visibility`, `archived_at` | 4.2 |
| `project_remotes` | `project_id`, `host`, `owner`, `name`, `is_public`, `forge_repo_id` | Normalized remotes |
| `commits` | `project_id`, `sha`, `parents`, `author_time`, `committer_time`, `observed_at`, `server_first_seen_at`, stats, `credited_seconds_since_parent`, `forge_verified_at`, `subject` (public repos only) | 4.3 |
| `client_events` | `device_id`, `event_id` (UUIDv7), `seq`, `kind`, `client_time`, `server_received_at`, `payload` (JSON, no content) | Raw uploads; retention 90 days, then only what credited intervals reference |
| `credited_intervals` | `user_id`, `project_id`, `start`, `end`, `seconds`, `activity_kind`, `rule`, `scoring_version`, `trust_tier`, `flags` | The unit of credit; every second traceable to a rule |
| `rollups_daily` | `(user_id, project_id, day, activity_kind)` → `seconds` | Upserted with `seconds = seconds + delta`; what dashboards read |
| `witness_log` | `device_id`, `seq`, `chain_hash`, `server_received_at` | Hash-chain commitments (8.5) |
| `submissions`, `submission_rows`, `evidence` | 10.2 | Frozen windows with evidence hashes and object-storage keys |
| `programs`, `program_grants`, `webhook_endpoints`, `webhook_deliveries` | 10.1 | Program clients and what each user has shared with them |
| `trust_levels`, `trust_audit`, `exclusions` | Hackatime's model (sibling doc 1.9) | Reviewer-only |
| `audit_log` | actor, action, target, at, reason | Every reviewer and admin action |

## 8. Ingestion API

The client is offline-first: it scores locally, keeps a durable upload queue, and syncs every 60 s while active plus right after prompt and stop hooks (6.2). The server's job at ingest is to accept each event exactly once, put it on a trustworthy time axis, witness it, and apply rollup deltas. It never needs content.

### 8.1 Endpoints

| Method and path | Auth | Purpose |
|---|---|---|
| `POST /oauth/device/code`, `POST /oauth/token` | none, then device code | Device sign-in (3.5). Returns a device token and registers the device's public key |
| `POST /api/v1/ingest/batches` | device token + signature | Upload a batch of events (8.2) |
| `GET /api/v1/ingest/state` | device token | `{last_seq, chain_head, server_time_ms, scoring_version_min}` so a client can resume after a reinstall or a crash |
| `GET /api/v1/time` | none | Server clock for offset estimation (8.4) |
| `POST /api/v1/submissions` | device token or session | Freeze a window and upload its evidence (10.2) |
| `GET /api/v1/users/current/statusbar/today` | device token | Status-bar total, WakaTime-shaped |

### 8.2 Batches, exactly once

Request:

```http
POST /api/v1/ingest/batches
Authorization: Bearer ptd_7Q2f...(device token)
Idempotency-Key: "0193f6a2-6c1e-7b9a-9a51-2f6c0c1d4e88"
Content-Encoding: gzip
Content-Type: application/json
Signature-Input: sig1=("@method" "@path" "content-digest" "idempotency-key");created=1795046571;keyid="dev_4k2";alg="ed25519"
Signature: sig1=:base64...:
Content-Digest: sha-256=:base64...:
```

```json
{
  "device_id": "dev_4k2",
  "client": {"version": "0.4.0", "os": "windows-10-19045", "scoring_version": "2026.11.2"},
  "clock": {"wall_ms": 1795046571667, "mono_ms": 81234567, "boot_id": "b7e1..."},
  "seq_from": 18301,
  "seq_to": 18342,
  "chain": {"prev": "9c1f...", "head": "e04a..."},
  "events": [
    {"seq": 18301, "id": "0193f6a1-...", "kind": "turn", "t": 1795046500000, "mono": 81163000,
     "project": "pf_8d2c", "harness": "claude-code",
     "data": {"prompt_at": 1795046500000, "stop_at": 1795046548000, "prompt_chars": 312,
              "prompt_hmac": "5be1...", "tool_calls": 7, "files_touched": 3}},
    {"seq": 18302, "kind": "presence", "t": 1795046520000, "data": {"minute_bits": "AAH/"}},
    {"seq": 18303, "kind": "credited", "t": 1795046560000, "project": "pf_8d2c",
     "data": {"start": 1795046500000, "end": 1795046560000, "activity_kind": "reviewing", "rule": "review_tail/3"}},
    {"seq": 18304, "kind": "commit", "t": 1795046565000, "project": "pf_8d2c", "data": {"sha": "a1b2c3d...", "...": "4.3"}}
  ]
}
```

Response `200`:

```json
{"accepted_through_seq": 18342, "duplicates": [18301], "rejected": [{"seq": 18320, "error": "time_in_future"}],
 "server_time_ms": 1795046572001, "chain_head": "e04a...", "witnessed_at_ms": 1795046572001}
```

Rules:

- **Per-device sequence numbers are the dedupe key.** Unique index on `(device_id, seq)`; anything at or below the stored sequence is reported as a duplicate and ignored. Per-event UUIDv7 ids ([RFC 9562](https://www.rfc-editor.org/rfc/rfc9562)) survive a device re-registration. Hackatime gets the same property from an MD5 `fields_hash` with a partial unique index and `insert_all ... unique_by` (sibling doc 1.3); explicit sequence numbers also expose gaps, which a hash can't.
- **Batch-level `Idempotency-Key`** follows [draft-ietf-httpapi-idempotency-key-header-08](https://datatracker.ietf.org/doc/html/draft-ietf-httpapi-idempotency-key-header-08) (2026-10-22, work in progress): an RFC 9651 String, "RECOMMENDED that a UUID [RFC9562] or a similar random identifier be used"; a reused key with a different payload gets 422, a retry while the first is still processing gets 409, and the resource "SHOULD define such expiration policy and publish it". Pairtime keeps keys 7 days and returns the stored response on a retry.
- **Partial acceptance.** One bad event never rejects the batch (Hackatime's `heartbeats.bulk` returns one status per item for the same reason). Validation errors name the sequence number and a machine-readable code.
- **Limits.** At most 500 events and 256 KB compressed per batch, with decompressed size capped at 4 MB to stop gzip bombs. A client returning from weeks offline sends many batches in order; the rate limit (8.6) has a separate backlog allowance for that.
- **Order of work in one transaction:** verify the signature and chain link, insert events, append to `witness_log`, upsert `rollups_daily` deltas from `credited` events, update `devices.last_seen_at`, then publish deltas to the SSE hub (6.3) after commit.
- **A privacy firewall on the server.** Every event kind has a strict schema with an allowlist of fields. No field may carry free text: strings are ids, enums, hashes or HMACs of bounded length. A buggy or modified client that tries to upload a prompt, a file path or a code snippet gets a 422, so the privacy promise (R6) doesn't rest on the client alone.

### 8.3 Tokens

| Token | Format | Scope | Storage | Lifetime |
|---|---|---|---|---|
| Device token | `ptd_` + 30 or more random base62 characters + a CRC32 checksum in 6 base62 characters. This copies GitHub's format: prefixes like `ghp_` plus "A 32 bit checksum in the last 6 digits of each token", which "virtually eliminates false positives for secret scanning offline" ([GitHub blog, 2021-04-05, updated 2023-05-10](https://github.blog/engineering/platform-security/behind-githubs-new-authentication-token-formats/)) | `ingest`, `read:self` | SHA-256 hash only | Until revoked; listed per device in settings, with last-seen time and IP |
| Device key | Ed25519 key pair made on the device at sign-in; public key registered with the token | Signs every batch with HTTP Message Signatures ([RFC 9421](https://www.rfc-editor.org/rfc/rfc9421)) over the method, path, `Idempotency-Key` and a `Content-Digest` ([RFC 9530](https://www.rfc-editor.org/rfc/rfc9530)) of the body | Public key | Rotated with the token |
| Web session | HCA OIDC login, then an HttpOnly, `SameSite=Lax` cookie | Browser only | Server-side session row | 30 days; re-auth with `prompt=login` before submitting or changing privacy (3.1) |
| WakaTime-compatible key | UUID, as wakatime-cli expects | `ingest:heartbeats` only | Hash | Until rotated (8.7) |
| Program token | OAuth 2.0 client credentials or authorization code | `hours:read`, `submissions:read`, `submissions:write`, `profile:read` (10.1) | Hash | 1 h access, refresh tokens |

The device key doesn't stop the device's owner from forging events. It stops a stolen bearer token from being replayed elsewhere, it binds the witness chain to one install, and it gives Fraud Squad a stable device identity for alt detection (Hackatime does the same job with `machine` names, IPs and JA4, sibling doc 1.9). Unlike Hackatime, no OAuth app ever receives the user's ingest credential (Hackatime's `GET /api/v1/authenticated/api_keys` hands one to any approved app, sibling doc 1.8).

### 8.4 Time and clock skew

Old school laptops have wrong clocks, sleep for days, and can be set back on purpose. Rules:

- Every event carries the client's wall time `t` and a monotonic reading `mono`; every batch carries `boot_id` (Linux `/proc/sys/kernel/random/boot_id`, an install-generated id per boot elsewhere) and the wall and monotonic clocks at send time.
- The server computes the device offset per batch as `server_received_ms - clock.wall_ms` (accurate to within the request's round trip, which is fine at minute resolution). Within one `boot_id`, event times are re-anchored from `mono` differences, so a clock changed mid-session can't move earlier events. If wall and monotonic deltas disagree by more than 2 s within one boot, the batch is flagged `clock_jump`.
- Corrected times more than 5 minutes in the future are rejected. Corrected times older than 30 days are rejected (the oldest window a program would plausibly re-pull; adjustable). Hackatime accepts epoch seconds between 1,000,000,000 and 2,000,000,000 and rescales milliseconds, microseconds, nanoseconds and Windows FILETIME (sibling doc 1.3). Pairtime's protocol fixes milliseconds, so it doesn't need guessing.
- **Witness lag** = `server_received_at - corrected event time`. Under 15 minutes is `witnessed`; more is `backfilled` (grit's threshold, prior-art doc 1.14). Backfilled time still counts on the user's own dashboard. Programs see the split and decide; it's the honest form of Zach's "lookback mode" (BOUNTY.md).
- Day boundaries for streaks and rollups use the user's profile timezone, as Hackatime does with `users.timezone`; each event also keeps its device UTC offset for display.

### 8.5 Witness chain

Each event extends a per-device hash chain, `head = SHA-256(prev || seq || kind || t || canonical_data)`, and each batch reports `prev` and `head`. The server stores `(device_id, seq_to, head, server_received_at)` in `witness_log`. At submission the client uploads evidence for the submitted window only (redacted turn records, for instance), the server recomputes the chain over it, and each turn is proven to have existed by its witness time. This is the grit design (prior-art doc 1.14) and the sibling doc's Design implication 12. It costs one 32-byte hash per batch on the server, about 6.6M rows/month at a 60 s sync (prior-art doc 5.3), roughly 0.5 GB/month in SQLite, prunable to one row per hour after 90 days.

### 8.6 Rate limits

| Limit | Value | Why |
|---|---|---|
| Batches per device | 6 per minute sustained, burst 30 | A 60 s sync plus prompt and stop hooks fits under 6/min |
| Backlog flush | up to 120 batches/minute while `seq_from` is older than 15 minutes | Lets a laptop that was offline for a week catch up in minutes |
| Devices per user | 10 active | More is unusual and goes to review |
| Unauthenticated auth endpoints | 30 per minute per IP | Device-code polling and sign-in |
| WakaTime-compatible heartbeats | 360 per minute per key | Hackatime's (disabled) rack-attack value for heartbeat POSTs, sibling doc 1.1 |

Responses carry the fields from [draft-ietf-httpapi-ratelimit-headers-11](https://datatracker.ietf.org/doc/html/draft-ietf-httpapi-ratelimit-headers-11) (2026-05-23), for example `RateLimit-Policy: "device";q=6;w=60` and `RateLimit: "device";r=2;t=40`, and a `429` carries `Retry-After`. The draft says the client's "Retry-After field MUST take precedence" and recommends jitter because throttled clients otherwise return at the same moment. The client backs off exponentially with full jitter and never drops queued events because of a 429.

### 8.7 Abuse handling

- **Account creation costs an identity.** On the Hack Club instance, accounts come only from HCA (3.1), and submitting to a program requires `verification_status` of `verified`. A Sybil farm needs verified Hack Club identities, which is the same bar every YSWS program already relies on.
- **Ingest never tips off a cheater.** Suspicious devices keep getting `200`s; their intervals are flagged and their trust tier lowered, and reviewers see why (sibling doc Design implication 15). Detection thresholds live server-side, outside the public client.
- **Signals kept per batch** for Fraud Squad: IP (from `CF-Connecting-IP` behind Cloudflare), Cloudflare `CF-JA4` TLS fingerprint where available, client version and build hash, device key id, witness lag, clock flags. Raw IPs are kept 30 days, then truncated to /24 (IPv4) or /48 (IPv6).
- **Cloudflare in front** for DDoS and bot management on the free plan, with the origin locked to Cloudflare's IP ranges, as Hackatime does (sibling doc 1.1). A home server reaches Cloudflare through a tunnel (section 9).
- **Version floor.** `GET /api/v1/ingest/state` returns `scoring_version_min`. Clients below it still upload, but their credited events are recomputed server-side or marked stale, so a known-buggy client can't inflate hours.

### 8.8 Should the server accept WakaTime or Hackatime heartbeats?

The sibling doc's Design implication 1 proposes accepting WakaTime heartbeats on the server as a second `[api_urls]` destination. The trade-off, from the server's side:

| | Server-side WakaTime endpoint | Local endpoint in the pairtime client |
|---|---|---|
| How | `[api_urls] .* = https://pairtime.example/api/v1\|<key>` | `[api_urls] .* = http://127.0.0.1:<port>/api/v1\|<local key>`; wakatime-cli accepts plain `http://localhost` URLs (Wakapi's README uses `api_url = http://localhost:3000/api`) |
| What reaches the server | Every heartbeat field: `entity` (absolute file path), `branch`, `project`, `dependencies`, `lines`, `machine` (sibling doc 1.3) | Only what pairtime's own events carry: counts, hashes, credited intervals |
| Volume | About 150 rows per tracked hour at Hackatime (sibling doc 1.10); 755 GB for 426M rows in Hackatime's Postgres | None extra |
| Works without the pairtime client | Yes: Codespaces, Replit, school lab machines where nothing can be installed | No |

Recommendation: point wakatime-cli at the local client, which folds editor activity into local scoring as the hand-edit signal (R7) and drops paths. Offer the server endpoint only as an opt-in for environments where the client can't run, and on that path store `entity` as an HMAC, keep `language`, `editor`, `category`, `is_write`, `lines` deltas and `time`, and give those heartbeats a 90-day retention after they've been credited. One caveat for the installer: Wakapi's README warns "Do not add multiple `.*` entries because duplicate keys are invalid in an INI file and are ignored by `wakatime-cli`", so if the user already has a `.*` line (for Hackatime or Wakapi), pairtime has to add a differently spelled pattern that matches everything, and the installer should test that both destinations receive the heartbeat (unverified which spellings wakatime-cli treats as distinct).

## 9. Self-hosting and cost

(pending)

## 10. APIs for programs and reviewers

(pending)

## 11. Design implications

(pending)

## 12. Open questions

(pending)

## 13. Sources

(pending)
