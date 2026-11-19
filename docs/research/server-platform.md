# Server platform: accounts, git, profiles, live dashboard, stack, hosting

Status: fact-checked 2026-11-19

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
11. Sources
12. Design implications
13. Open questions

## 1. Recommended design (summary)

One Go binary with an embedded Svelte UI, SQLite in WAL mode replicated by Litestream to object storage, behind a Cloudflare tunnel on Karim's domain. Hack Club Auth for accounts. The client computes credited time and uploads numbers, hashes and commit metadata, never content. The server witnesses, rolls up, streams, and turns submitted windows into signed, reviewable evidence for programs.

```text
 pairtime client (Go)                         browsers                     programs / reviewers
   | scores locally, queues events              | SSE /api/v1/stream          | OAuth + webhooks, /review (HCA)
   | POST /api/v1/ingest/batches (signed)       |                             |
   v                                            v                             v
 Cloudflare edge --- cloudflared tunnel ---> pairtime server (one Go process)
                                               - ingest: dedupe, witness chain, rollup deltas
                                               - SSE hub (in-process fan-out)
                                               - submissions, review UI, Hackatime-shaped reads
                                               - HCA OIDC client, GitHub App webhooks
                                               |
                                    SQLite (WAL, local disk) --Litestream--> R2 / S3 bucket
                                                                evidence blobs (submitted hours only)
```

| Decision | Choice | Main reason | Section |
|---|---|---|---|
| Identity | HCA OIDC (`openid email slack_id verification_status`), server as confidential client; desktop via device grant | Hackatime is in a three-week move to HCA-only sign-in that started 2026-11-13; programs and review tools key on Slack id, email and HCA id; HCA has no PKCE | 3 |
| Git | Client reads refs and reflog; root-commit fingerprint; commit records with credited time and server witness time; forge verification for public repos; GitHub App only where the user installs it | Works for GitHub, GitLab, Codeberg and never-pushed repos without uploading code | 4 |
| Profiles | Public aggregates by default; names only for public repos; paths and prompts never on the server; leaderboards opt-in | R5's "public-by-default" without Hackatime's project-name leak | 5 |
| Real time | SSE over HTTP/2, additive rollup deltas, in-process fan-out | ~5,000 streams cost ~100 MB; Hackatime's per-insert rebuilds cost 17 worker-hours a day | 6 |
| Language | Go (match the client) + Svelte/TS UI | One scoring implementation on client and server; single static binary; fast builds on old laptops | 7 |
| Database | SQLite WAL + Litestream; Postgres path kept open | ~1.5 GB/month of derived data and ≤20 commits/s fit easily; zero-setup for contributors and self-hosters | 7 |
| Ingest | Per-device sequence numbers, UUIDv7, Idempotency-Key, Ed25519-signed batches, schema that rejects free text | Exactly-once from an offline queue; privacy enforced server-side too | 8 |
| Time | Server-anchored, monotonic re-anchoring, `witnessed` vs `backfilled` at 15 minutes | Defeats backdating while allowing lookback | 8 |
| WakaTime | Heartbeats to the local client on localhost; server endpoint opt-in with hashed paths | Keeps file paths off the server; avoids Hackatime's heartbeat storage cost | 8 |
| Programs | Submission object, Robert-style signed webhooks, handbook justification text, Hackatime-compatible reads | Near-zero integration cost for programs that already have a `HackatimeService` | 10 |
| Audit | Signed self-verifying evidence bundles plus an open-source verifier; evidence-access log shown to the user | R10's external auditability without trusting pairtime's UI | 10 |
| Hosting | Compose file: one app container + `cloudflared`; ~$49/month at R11 scale, ~$150 with a standby | 10-30% of the $500 cap. Hetzner CCX plans showed "currently unavailable" on 2026-11-19, so a fallback is needed (fact-check) | 9 |

## 2. Load model for R11

R11 caps hosting at $500/month for 15,488 MAU logging 109,759 hours/month. The prior-art doc ([prior-art-and-measurement.md](prior-art-and-measurement.md) section 5) built the base model; this doc uses its numbers and adds the server-side ones.

| Quantity | Value | Source |
|---|---|---|
| Hours per MAU per month | 7.09 | prior-art 5.1 |
| Concurrent active users: average, daily peak (4x), event peak (8x) | 152, ~610, ~1,220 | prior-art 5.1 (peak factors assumed) |
| Ingest requests at a 60 s sync | 6.6M/month; 2.5/s average, 20/s at 8x | prior-art 5.3 |
| Extra hook-triggered syncs (prompt, stop), coalesced within 10 s | at most ~4.4M/month (20 turns per active hour x 2 hooks x 109,759 h), ~1.7/s average | this doc; turn rate from prior-art 5.2 |
| Writer commits after batching (8.2, 7.4) | at most 20/s whatever the request rate | this doc |
| Derived rows | ~1.0 GB/month | prior-art 5.2, schema A |
| Witness log | ~0.5 GB/month, prunable | 8.5 |
| Open SSE streams at an event peak | up to 5,000 (assumed) | 6.2 |
| Program API reads | well under 1/s: programs re-pull one window per devlog or ship | sibling doc 3.3 |
| Evidence for the acceptance target | 0.5-2 GB in total for 10,000 submitted hours | prior-art 5.2 |
| Hackatime, for reference | ~3 of 32 cores busy, 65 of 192 GB RAM, 1 of 4 TB disk, ~1 MB/s network; 426M heartbeat rows in 755 GB | BOUNTY.md R11; sibling doc 1.10 |

Everything above fits on one 2-vCPU, 8 GB machine with room to spare (section 9.4). What the server must not do is anything per event that costs real CPU or money: recomputing aggregates (6.1), calling an LLM, or storing content.

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
| `scopes_supported` | Only `openid profile phone birthdate`, so a generic OIDC library that trusts discovery won't learn that `email`, `slack_id` and `verification_status` exist. Request them explicitly (added in fact-check) |

Scopes (`app/models/oauth_scope.rb`; `app/views/docs/api.md.erb`):

| Scope | Gives | Who may request it |
|---|---|---|
| `openid` | `sub`, the public identity id, shaped `ident!abc123` | Community apps |
| `email` | `email`, `email_verified` | Community apps |
| `name`, `profile` | `name`, `given_name`, `family_name`, `nickname`, `updated_at` | Community apps |
| `slack_id` | `slack_id` (Hack Club Slack user id) | Community apps |
| `verification_status` | `verification_status` (`needs_submission`, `pending`, `verified`, `ineligible`) and `ysws_eligible` | Community apps |
| `phone`, `birthdate`, `address`, `basic_info`, `groups` | Sensitive identity fields; `groups` exposes directory entitlements | HQ official apps only (`HQ_OFFICIAL_SCOPES`) |
| `legal_name` | Legal first and last name | Super-admin apps only: the code puts it in `SUPER_ADMIN_SCOPES = HQ_OFFICIAL_SCOPES + %w[legal_name]`, although the docs table says "HQ only" (fact-check: corrected) |
| `set_slack_id` | Associate Slack ids with identities | No tier: "intentionally omitted from all tiers"; docs mark it deprecated |

`COMMUNITY_ALLOWED = %w[openid profile email name slack_id verification_status]`. A pairtime deployment registers as a community app on the Developer Apps page unless HQ marks it official, and those six scopes are enough for everything pairtime needs: a stable id, the email programs match on, the Slack id that review tools key on, and eligibility.

Other facts that shape the design:

- **Token lifetime.** `access_token_expires_in 6.months`, `use_refresh_token`, `reuse_access_token` (`config/initializers/doorkeeper.rb`). Access tokens are prefixed `idntk.`.
- **No PKCE.** `oauth_access_grants` has no `code_challenge` or `code_challenge_method` columns (`db/schema.rb`), which Doorkeeper needs for PKCE, and discovery advertises no `code_challenge_methods_supported`. Apps default to `confidential: true`. So a desktop client can't safely talk to HCA directly. The pairtime server must be the HCA client and keep the secret, and the desktop client signs in to pairtime with its own flow (3.5).
- **REST API.** `GET /api/v1/me` returns the identity for the granted scopes. `GET /api/external/check?email=|slack_id=|idv_id=` needs no auth and returns `needs_submission`, `pending`, `verified_eligible` ("Verified and eligible for YSWS (under 18)"), `verified_but_over_18`, `rejected` or `not_found` (`app/views/docs/api.md.erb`). Program keys (`prgmk.`, HQ official only) can read any identity that authorized the app; the doc says "If you think you need a `prgmk`, think harder about it."
- **Per-app gating.** The `resource_owner_authenticator` runs `Directory::RequirementCheck.passes?(identity:, app:)` and refuses sign-in to apps whose directory requirements the person doesn't meet. A pilot program could restrict a pairtime instance to a group this way (unverified beyond the code path).
- **Re-auth.** `prompt=login` or `max_age=<seconds>` forces a fresh login with TOTP, backup code or email (`app/views/docs/oidc-guide.md.erb`). Useful before a user submits hours or changes privacy settings.
- **No avatar.** The OIDC guide says HCA has no `picture` claim and points to cachet's `GET /users/{slack_id}/r` endpoint on `cachet.dunkirk.sh` for a Slack avatar by Slack id.

**Hackatime is moving to HCA-only sign-in now.** `app/models/hca_transition.rb` in hackclub/hackatime at `43bde10b02db`: "The three-week move to Hack Club sign-in only", `STARTED_AT = Time.utc(2026, 11, 13, 12, 21)`. Week one lets legacy sign-ins skip the link prompt; from `STARTED_AT + 2.weeks` (2026-11-27) "every unlinked session is asked, however it signed in". `users.hca_id` got a unique partial index on 2026-11-13 (`db/migrate/20261113104759_make_users_hca_id_index_unique.rb`), and Slack sign-in now refuses to create accounts: "No Hackatime account uses that Slack account. Sign in with Hack Club to create one." (`app/controllers/sessions_controller.rb`). Hackatime asks HCA for `email slack_id verification_status` (`app/models/concerns/oauth_authentication.rb`, `hca_authorize_url`).

### 3.2 Slack, GitHub and email

| Method | Endpoints and scopes | Gives | Role in pairtime |
|---|---|---|---|
| Sign in with Slack (OIDC) | `https://slack.com/openid/connect/authorize`, token `https://slack.com/api/openid.connect.token`, userinfo `https://slack.com/api/openid.connect.userInfo`; scopes `openid`, `email`, `profile` only, which can't be mixed with ordinary Slack scopes in one flow ("Each set of scopes must be requested in separate OAuth flows"; live `https://slack.com/.well-known/openid-configuration` agrees, with `grant_types_supported: authorization_code`, auth methods `client_secret_post`/`client_secret_basic` and no `code_challenge_methods_supported`, checked in fact-check; [Slack docs](https://docs.slack.dev/authentication/sign-in-with-slack/), fetched 2026-11-19, no page date) | `https://slack.com/user_id`, `https://slack.com/team_id`, email. The sample ID token lives 300 s; no PKCE | Not needed: HCA already returns `slack_id`. Check `team_id` if it is ever used, since any workspace can sign in otherwise |
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
- Hackatime also has timed leaderboard shadowbans: `users.leaderboard_shadowbanned`, `leaderboard_shadowban_reason`, `leaderboard_shadowban_expires_at` and `leaderboard_shadowbanned_by_id` (`db/schema.rb` at `43bde10b02db`), with their own admin controller. That is the same quiet handling 8.7 proposes for ingest, and pairtime's opt-in leaderboards need it too (added in fact-check).

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

- **GitHub Apps vs OAuth apps.** [GitHub's comparison page](https://docs.github.com/en/apps/oauth-apps/building-oauth-apps/differences-between-github-apps-and-oauth-apps) (fetched 2026-11-19) says "GitHub Apps use fine-grained permissions"; "OAuth apps need to request the `repo` scope to get access to issues, pull requests, or anything owned by the repository"; installing an app "grants the app access to a user or organization account's chosen repositories"; GitHub Apps "have built-in, centralized webhooks", while OAuth apps "must configure webhooks individually for each repository" and those webhooks "are not automatically disabled" when the token is deleted. "In general, GitHub Apps are preferred over OAuth apps."
- **Token lifetimes.** GitHub App user tokens: "The user access token expires after eight hours, and the refresh token expires after six months" (`expires_in` 28800, `refresh_token_expires_in` 15897600), and refresh tokens are single-use ([GitHub docs](https://docs.github.com/en/apps/creating-github-apps/authenticating-with-a-github-app/refreshing-user-access-tokens)). Installation tokens expire after 1 hour. GitLab access tokens expire after 7,200 s and refresh ([GitLab docs](https://docs.gitlab.com/integration/oauth_provider/)).
- **Rate limits.** [GitHub's REST rate-limit page](https://docs.github.com/en/rest/using-the-rest-api/rate-limits-for-the-rest-api) gives 60 requests/hour per IP unauthenticated; user tokens 5,000/hour (shared with the user's other apps); installation tokens 5,000/hour, plus 50 per repo and 50 per user above 20 each, capped at 12,500; secondary limits of 100 concurrent requests and 900 points/minute for REST. Hackatime limits its own GitHub mapping to 50 requests per user per day (sibling doc 1.7).
- **Push payloads.** GitHub: "The array includes a maximum of 2048 commits"; a GitHub App needs "at least read-level access for the 'Contents' repository permission" to receive `push`; payloads "are capped at 25 MB" ([GitHub webhook docs](https://docs.github.com/en/webhooks/webhook-events-and-payloads#push)). The same page adds two cut-offs: "Events will not be created if more than 5000 branches are pushed at once" and "Events will not be created for tags when more than three tags are pushed at once" (added in fact-check). The per-commit fields are `id`, `tree_id`, `distinct`, `message`, `timestamp`, `url`, `author`, `committer`, `added`, `removed`, `modified`, all required in octokit's schema (`payload-schemas/api.github.com/common/commit.schema.json` in octokit/webhooks at `aa80c24a3f55`, checked in fact-check). GitLab sends "the newest 20 commits only" plus `total_commits_count`, and sends no push hook at all when one push touches more than 3 branches by default (`push_event_hooks_limit`, "default: `3`", which "applies to both tags and branches"; "When exceeded, no webhooks are triggered at all for that push event") ([GitLab docs](https://docs.gitlab.com/user/project/integrations/webhook_events/), GitLab 19.6 era). Both list `added`/`modified`/`removed` file paths, so a webhook receiver learns file names whether it wants them or not.
- **Config-based hooks.** Git 2.54 (GitHub blog dated 2026-04-20) added `hook.<name>.event` and `hook.<name>.command` in any config scope; "you can now run *multiple* hooks for the same event"; "The traditional hook script in `$GIT_DIR/hooks` still works, and runs last"; `hook.<name>.enabled = false` disables one per repo ([Highlights from Git 2.54](https://github.blog/open-source/git/highlights-from-git-2-54/)). A global `[hook "pairtime-commit"] event = post-commit` is therefore safe on 2.54+. On older Git, which old Windows laptops are likely to have (unverified), the client should poll refs instead of installing hooks.

What Hackatime does: projects are free-text names from heartbeats, `AttemptProjectRepoMappingJob` maps a name to a public GitHub repo, `PullRepoCommitsJob` imports commits, private repos are never mapped, and the GitHub app asks only for Metadata (read) (sibling doc 1.7). The admin timeline draws commit markers next to spans (sibling doc 1.9). WakaTime goes further: `GET /api/v1/users/current/projects/:project/commits` (and `.../commits/:hash`; optional `author`, `branch`, `page`; needs the `read_heartbeats` scope) returns per-commit `total_seconds` ("time coded in editor for this commit") plus `human_readable_total`, `hash`, `truncated_hash`, `message`, `branch`, `ref`, `html_url`, and `author_*`/`committer_*` fields (fact-check: confirmed, scope and fields added), for repos connected through its GitHub, GitLab or Bitbucket integration ([WakaTime API docs](https://wakatime.com/developers/), fetched 2026-11-19). The docs don't say how time is assigned to a commit.

### 4.2 Project identity

A project needs an id that survives renames, re-clones, forks and moving from local-only to GitHub. Free-text names (Hackatime's model) break all of these and are why Stardance makes users pick `hackatime_keys` by hand.

Proposed key, computed by the client:

1. **Root commit set.** `git rev-list --max-parents=0 HEAD`, sorted. A clone on another machine, a push to any forge, and a rename all keep it. The server stores `HMAC(server_pepper, root_sha)` as `repo_fingerprint` so a root SHA of a private repo isn't readable from the database, while two machines of the same user still converge on one project.
2. **Normalized remotes.** `git remote get-url` for each remote, normalized to `host/owner/name` (strip `.git`, credentials and protocol). Sent in clear only when the user marks the project public or the remote is a public repo the server can confirm.
3. **No git.** Folder path hashed with a per-user salt, plus a user-chosen display name.

Forks share root commits with their upstream, so projects are per user: a fork never merges two people's projects. The pepper is server-wide, though, so the server can see when two users' projects share root commits (a fork, a template, a classmate's repo). That's a reviewer signal for `HOURS_REUSE`-style checks (sibling doc 3.6).

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

When the repo is public (or the user installed the GitHub App on it), the server fetches `GET /repos/{owner}/{repo}/commits/{sha}` (GitHub), `GET /projects/:id/repository/commits/:sha` (GitLab) or `GET /api/v1/repos/{owner}/{repo}/git/commits/{sha}` (Forgejo) once, confirms the SHA and diffstat, and stores the commit's subject line and URL. On Forgejo, pass `stat=true&files=false&verification=false`: its `Commit.stats` has `additions`, `deletions` and `total` but no file count, so `files_changed` is only checkable with `files=true` (Codeberg `swagger.v1.json`, 2026-11-19; added in fact-check). The commit is then `forge_verified`. For never-pushed repos the commit stays `local_only`: still witnessed, still useful to a reviewer, and verifiable later if the user ever pushes it, because the SHA can't change.

What a project page then shows per commit, with no code uploaded:

```text
a1b2c3d  "Fix wall clipping in BSP traversal"        github.com/rowan/cs2go
         2h 03m credited since previous commit  (1h 10m reviewing diffs, 31m debugging, 22m hand edits)
         +212 -31 in 7 files · witnessed 14:02 · verified on GitHub
```

For a private or local repo the same line shows the short SHA, the stats and "local commit, witnessed", with no subject line.

### 4.4 Rules for crediting time to commits

- Time belongs to a commit only if it was credited on the same `repo_fingerprint` after the parent's `observed_at` and before this commit's. Time after the last commit shows as "uncommitted" (Ari shows the same thing as "Time after the last commit", sibling doc 3.6).
- Rebases and amends rewrite SHAs. The client follows `post-rewrite` (hook or reflog `rebase`/`amend` entries) and sends `rewrites: [{old_sha, new_sha}]`, so credited time moves to the new SHA instead of disappearing or doubling.
- Squash merges on the forge produce a SHA the client never saw. The server links it through the PR's commit list when the GitHub App is installed, or leaves the local commits as the evidence.
- A merge commit shows the time credited on the merged branch since the merge base. That time isn't counted twice, because each second is credited once in the user's global timeline and only labelled with commits afterwards (sibling doc Design implication 7).

## 5. Public profiles and project pages

### 5.1 What the reference products show

**Hackatime** (hackclub/hackatime at `43bde10b02db`):

| Page | Route | Content | Controls |
|---|---|---|---|
| Profile | `GET /@:username` | `ProfileStatsService::PROFILE_DASHBOARD_KEYS`: `total_time`, `total_heartbeats`, `project_durations`, `language_stats`, `editor_stats`, `operating_system_stats`, `category_stats`, `weekly_project_stats`, top and singular project/language/editor/OS/category, plus an `activity_graph`; social links (GitHub, Twitter, Bluesky, LinkedIn, Discord, website); an OG image (`app/controllers/profiles_controller.rb`, `app/services/profile_stats_service.rb`) | `allow_public_stats_lookup` (default `true`) hides the stats from everyone but the owner; `leaderboard_opt_out` hides the user from leaderboards. Those two checkboxes are the whole privacy page (`app/controllers/settings/privacy_controller.rb`, `privacy_params`) |
| Project | `GET /@:username/project/:project_name` | Total time, file count, language stats, **file stats and branch stats**, repo URL, "since" date, OG image ("@user has spent 3h coding X. Tracked with Hackatime.") | 404 unless `project_repo_mappings.public_shared_at` is set: per-project sharing is opt-in |

So Hackatime's profile is public by default and lists every project that isn't archived (rollups are built "from the user's non-archived heartbeats", `ARCHITECTURE.md` section 4), while a shared project page shows file names and branches. WakaTime clients name a project after its repo or folder, so a folder called `college-essay` shows up on the public profile unless the user archives it or turns off all stats.

**WakaTime** ([API docs](https://wakatime.com/developers/), fetched 2026-11-19) has finer switches. The user object carries `logged_time_public`, `languages_used_public`, `editors_used_public`, `categories_used_public`, `os_used_public`, `is_email_public`, `is_photo_public` and `is_hireable`; stats responses echo them as `is_coding_activity_visible`, `is_language_usage_visible`, `is_editor_usage_visible`, `is_category_usage_visible`, `is_os_usage_visible`. Projects have a `privacy` setting, organizations a `default_personal_privacy` of "visible or hidden", and embeddables "can be retracted to prevent future access to your code stats". Machine names in stats can include the IP address: the `machines[].name` field in Stats, Status Bar and Summaries is described as "machine hostname and ip address" (fact-check: the doc's earlier quote, "when user's preference is to show IPs in machine names", wasn't found on the page, so it's replaced with this one).

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

There is no push channel. `config/cable.yml` is the only Action Cable file in the tree (the Rails default); there are no channels, no `EventSource` and no WebSocket code (hackclub/hackatime tree at `43bde10b02db`). `ARCHITECTURE.md` section 4: `DashboardStats` serves all-time views from `dashboard_rollups` and queries heartbeats for other ranges; "Short Rails caches (currently 1/5/15 minutes depending on fragment)"; and `DashboardRollupRefreshService` "rebuilds totals, dimensions, weekly projects, project details, filter options, activity graph and today's stats from the user's non-archived heartbeats" and "atomically replaces all of one user's rows in a transaction", scheduled after heartbeat commits. Those rebuilds "take about 17 worker-hours a day to refresh" ([PR #1745](https://github.com/hackclub/hackatime/pull/1745) body; the same PR measures about 3 hours on ClickHouse and 21 s for the largest user's rebuild on Postgres. The PR was still open on 2026-11-19). A dashboard refreshes when the page is reloaded and the cache fragment has expired.

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
| Memory, Go | Goroutine stack starts at `stackMin = 2048` bytes (`src/runtime/stack.go`); `net/http` adds a 4 KiB `bufio.Reader` and a 4 KiB `bufio.Writer` (`newBufioWriterSize(checkConnErrorWriter{c}, 4<<10)`, added in fact-check) per connection and a `bufferBeforeChunkingSize = 2048` writer per response (`src/net/http/server.go`, golang/go master `b496a3044e1d` on 2026-11-19). Call it 10-20 KB per idle SSE stream with TLS terminated at the proxy (estimate) | 5,000 streams ≈ 50-100 MB |
| Memory, BEAM | Phoenix held "2 million clients" on a "40core/128gb box" with "1s broadcasts" ([Phoenix blog, 2015-11-03](https://www.phoenixframework.org/blog/the-road-to-2-million-websocket-connections)), an upper bound of ~64 KB per connection | 5,000 sockets ≈ 0.3 GB at most |

Polling for comparison: 5,000 tabs polling every 10 s is 500 authenticated requests/s, each needing a cache or rollup read, with up to 10 s of staleness. SSE costs one long-lived stream per tab and sends nothing when nothing changes. At this scale either fits on one small box; SSE wins on freshness, not on capacity.

### 6.3 Where fan-out happens

- **One process: in memory.** A topic map (`user:<id>`, `profile:<username>`, `project:<id>`, `global:leaderboard`, `global:pairing`) with a bounded per-subscriber channel. Slow subscribers are dropped and reconnect, never allowed to block ingest.
- **Several processes: a broker.** Postgres `LISTEN/NOTIFY` works, with limits: payload "must be shorter than 8000 bytes", the queue is "8GB in a standard installation", and notifications are delivered only at commit ([PostgreSQL 18 docs](https://www.postgresql.org/docs/current/sql-notify.html)). Recall.ai found that a transaction that issued `NOTIFY` takes `AccessExclusiveLock on object 0 of class 1262 of database 0` during commit, "effectively serializing all commits", which stalled their database with "tens of thousands of simultaneous writers" ([Recall.ai, 2025-07-01, revised 2026-05-08](https://www.recall.ai/blog/postgres-listen-notify-does-not-scale)). The post's later update says an upstream Postgres commit "has eliminated the bottleneck". That overstates it (fact-check, corrected). The commit is [`282b1cde`](https://github.com/postgres/postgres/commit/282b1cde9dedf456ecf02eb27caf086023a7bb71) (2026-01-15, Joel Jacobson), "Optimize LISTEN/NOTIFY via shared channel map and direct advancement". It stops waking listeners that aren't interested in a channel, which shortens the time spent under the lock, and its message says "There is no improvement for the simplest case where every listener reads every message". The commit-time lock itself is still in `src/backend/commands/async.c` at `REL_19_0` ("Serialize writers by acquiring a special lock that we hold till after commit", `LockSharedObject(DatabaseRelationId, InvalidOid, 0, AccessExclusiveLock)`). The change is in PostgreSQL 19 only, not in any 18.x: the `REL_19_0` tag is dated 2026-11-16, but the draft release notes still read "Release date: 2026-??-??, AS OF 2026-10-09" ("Improve NOTIFY to only wake up backends that are listening to specified notifications"), and postgresql.org/support/versioning still listed 18.7 as the newest release on 2026-11-19. At pairtime's ~20-40 writes/s, with one listener per app process, it's harmless if each ingest transaction sends at most one `NOTIFY` carrying only ids. A small NATS or Redis instance is the alternative if writes grow.
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
- Behind Cloudflare: SSE through `cloudflared` tunnels was buffered for years; the maintainers closed the issue on 2025-05-14 with "Server Sent Events have been fixed and this should now work, even if the right header isn't set" ([cloudflare/cloudflared#199](https://github.com/cloudflare/cloudflared/issues/199)). Send `Content-Type: text/event-stream`, `Cache-Control: no-cache` and keepalives anyway, and keep each stream under Cloudflare's 125 s proxy read timeout between bytes, which returns a 524 and can be changed only on Enterprise zones ([Cloudflare connection limits](https://developers.cloudflare.com/fundamentals/reference/connection-limits/), `dateModified` 2026-11-13, checked in fact-check). The 15 s keepalive leaves plenty of margin.
- The editor status bar keeps using a plain cached `GET .../statusbar/today`, as WakaTime clients expect.

## 7. Stack and database

### 7.1 What the server has to do

At R11 scale the workload is small: 20-80 ingest requests/s at an event peak, about 1 GB/month of derived rows if the client computes credit (prior-art doc 5.2, schema A), up to 5,000 SSE streams (6.2), and a review UI used by a few dozen reviewers. Any of the five stacks can serve that from one small box. Capacity doesn't pick the stack. These do:

1. **One scoring implementation.** The client computes credited time locally (privacy, R6) and the server must be able to recompute it from the same inputs at submission time to check it (R9, R10). If server and client share a language, they share the scoring package and its test vectors, and "the server got a different number" can only mean tampering or a version mismatch. The client doc ([client-platform.md](client-platform.md) 1.4) narrows the client to Go or Rust and favours cgo-free Go for cross-compilation.
2. **A self-host an agent can run.** Karim's fleet agent will deploy it on a home server (BOUNTY.md). One binary plus one data directory is the easiest thing to deploy, back up and restore.
3. **Contributors on old laptops.** Teen contributors may be on the same 10-year-old Windows laptops as users (R8). No Docker, no database server and fast rebuilds help them more than any framework feature.
4. **AI agents as contributors.** Most code will be written by agents steered by people, so a small, regular codebase with types and quick compile-test loops matters.

### 7.2 Five stacks compared

Versions current on 2026-11-19 (GitHub releases, go.dev, nodejs.org): Go 1.27.4, Rust 1.100.0 (2026-11-12; axum 0.8.9, 2026-04-14), Bun 1.4.5 (released 2026-11-19; fact-check, was 1.4.4) and Node 26.13.1 LTS "Lithium" (2026-11-10) (Hono 4.13.19, SvelteKit adapter-node 6.0.5), Elixir 1.20.5 with Phoenix 1.8.16 and LiveView 1.2.13, Rails 8.1.4.1.

| Criterion | Go | TypeScript (Bun or Node) | Elixir + Phoenix | Rails (Hackatime's stack) | Rust |
|---|---|---|---|---|---|
| Developers using the language, Stack Overflow 2026, all respondents to the optional languages question (n = 14,099) ([survey](https://survey.stackoverflow.co/2026/technology/data/languages)) | 16.5% | 43.8% (JavaScript 62.0%) | 2.6% (Elixir; Phoenix not on this page) | 5.8% (Ruby; Rails not on this page) | 18.0% |
| hackclub org repos pushed in the last 12 months, by primary language (GitHub search, 2026-11-19) | 21 | 170 (plus 122 JavaScript, 67 Svelte) | 0 | 52 | 18 |
| Hack Club production apps in it | ari-webhooks | Lapse, Ari web, High Seas, Shipwrecked | none found | Hackatime (78 contributors), Stardance, Flavortown, Summer of Making, HCA, HCB | Lookout, hackatime-setup |
| Agent issue-resolution on real repos, best resolved rate, Multi-SWE-bench (2025 models; Go 428 tasks, TS 224, JS 356, Rust 239) ([arXiv 2504.02605](https://arxiv.org/html/2504.02605v1)) | 7.48% | 11.61% (JS 5.06%) | not covered | not covered | 15.90% |
| Shares scoring code with the client | Yes, if the client is Go | No | No | No | Yes, if the client is Rust |
| Self-host artifact | One static binary (`CGO_ENABLED=0`), UI embedded with `embed`, pure-Go SQLite | `bun build --compile` single file; `bun.exe` itself is 115.3 MB (client doc 1.4) | `mix release` directory with the runtime; single file needs a wrapper such as Burrito (unverified) | Docker image (Hackatime: `Dockerfile` for Kamal or Coolify, plus a worker image) | One static binary |
| Idle RSS, one process (Linux, client doc 1.2) | 3.6 MB | Node 41.9 MB | not measured (unverified) | not measured; a booted Rails app is far above the runtimes listed (unverified) | 3.0-4.7 MB |
| Real-time fit | SSE in `net/http`, ~10-20 KB per stream (6.2) | SSE straightforward | Best: Phoenix PubSub and LiveView push diffs over WebSockets out of the box | Action Cable is WebSockets; SSE through `ActionController::Live` holds a Puma thread per open stream (not re-checked on Rails 8.1, unverified), and Hackatime runs `RAILS_MAX_THREADS` 3 (sibling doc 1.1) | SSE in axum |
| Rebuild time for contributors | Seconds | None for the server; type-check only | Moderate | None | Minutes for clean builds of an async web stack (unverified) |
| What can be reused | grit's server (`gritd`, Go + SQLite, AGPL-3.0, prior-art doc 1.14) and Wakapi's WakaTime-compatible API (Go, MIT) | Ari's review UI patterns (no license) | nothing in Hack Club | Hackatime's ingest, UA parser and program API (MIT) | Lookout's capture code (AGPL-3.0) |

Notes on the evidence:

- The agent benchmark is weak evidence. Multi-SWE-bench used early-2025 models (best results from Claude 3.7 Sonnet and Claude 3.5 Sonnet with the MopenHands and MSWE-agent scaffolds), says "Java ranks second after Python, though with a noticeable gap", that "Go and Rust exhibit inconsistent performance across models, generally outperforming TS and JS but falling behind Java", and that "TS and JS consistently yield the lowest resolved rates". Its own Table 4 contradicts that last line for best scores: TypeScript's best (11.61%) beats Go's (7.48%) (fact-check: quote corrected). The other often-cited result, that Elixir had "the highest completion rate across models" among 20 languages in Tencent's AutoCodeBench ([Dashbit, 2026-02-05](https://dashbit.co/blog/why-elixir-best-language-for-ai)), measures single-pass problem solving, which the post itself says ("Elixir shines on the first pass, without a coding agent behind it"). Neither measures maintaining a mid-size web app. Treat them as a tiebreaker at most.
- Rails is the strongest choice for reuse and for Hack Club's own maintainers, and the weakest for the single-binary self-host and for sharing code with a native client.
- Elixir has the best real-time story and the smallest contributor pool. No hackclub repo used it in the last year.
- TypeScript has the largest pool and the Svelte front-end habits Hack Club already has, but it can't share code with a Go or Rust client, and a resident Bun or Node process is 10x the native memory floor.

### 7.3 Recommendation: Go API, Svelte UI, SQLite

- **Go** for ingest, scoring, APIs, SSE and background jobs, in one binary. It shares `pkg/score` with the client, cross-compiles to every target in seconds, needs no runtime on the host, and keeps a contributor's edit-build-test loop in seconds on an old laptop. grit's `gritd` (Go 1.26, pure Go, SQLite, SSE) already uses this shape for the same product, though it hasn't been load-tested yet (prior-art doc 1.14).
- **Svelte with TypeScript** for the web UI, built by Vite to static files and embedded in the Go binary. Hack Club contributors already write Svelte (67 recent hackclub repos; Hackatime's UI is Svelte through Inertia; Ari is SvelteKit). The API is described in OpenAPI and the TS types are generated from it, so the language boundary is typed.
- If the client ends up in Rust, the same argument picks a Rust server (axum). The rule is: match the client so scoring exists once.

### 7.4 Database

| Engine | Version, license (2026-11-19) | Fit |
|---|---|---|
| **SQLite** in WAL mode + Litestream | SQLite public domain; Litestream v0.5.18 (2026-11-03), Apache-2.0 | Default. One file; readers never block the writer; continuous backup to S3-compatible storage |
| PostgreSQL | 18 (docs show 18.7), PostgreSQL License; pg_partman v5.5.1 (2026-11-17), PostgreSQL License | Supported alternative when Hack Club wants it inside its existing Postgres operations, or when more than one app server is needed |
| TimescaleDB | 2.32.0 (2026-11-12); compression and continuous aggregates are under the Timescale License (`LICENSE` at 2.32.0: everything in the `tsl` folder is under the Timescale License unless a file says otherwise, and `tsl/src/` holds `compression` and `continuous_aggs`; checked in fact-check) | No: TSL terms don't fit an AGPL entry (prior-art doc 5.4), and compression isn't needed at ~1 GB/month |
| ClickHouse | v26.9.32.8-stable (2026-11-18), Apache-2.0 | Only if pairtime stores raw WakaTime heartbeats at Hackatime volume. All 426M Hackatime heartbeats fit in 5.5 GiB there, against 755 GB in Postgres (592 GB of it in 32 indexes). These are the author's measurements in [hackclub/hackatime#1745](https://github.com/hackclub/hackatime/pull/1745), "Move heartbeats to ClickHouse", which was still open and unmerged on 2026-11-19, so production Hackatime still runs heartbeats on Postgres (fact-check) |
| DuckDB | v2.0.0 (2026-10-21), MIT | Offline analysis for Fraud Squad against a restored Litestream replica, so heavy queries never touch production |

Why SQLite is enough, with numbers:

- **Write rate.** SQLite "will only allow one writer at any instant in time" ([sqlite.org/whentouse](https://www.sqlite.org/whentouse.html)). The FAQ's old figure of "a few dozen transactions per second" came from waiting on a 7200 RPM platter; its 2024-11-19 update says "SQLite will do far more than 50K inserts/second now" and that batching operations in one transaction is what matters ([FAQ 19](https://www.sqlite.org/faq.html)). In WAL mode with `synchronous=NORMAL`, "the checkpoint is the only operation to issue an I/O barrier or sync operation", at the cost that the last transactions "might rollback following a power failure" ([sqlite.org/wal](https://www.sqlite.org/wal.html)). Pairtime funnels all writes through one writer goroutine that commits every 50 ms or 500 rows, whichever comes first. That's at most 20 commits/s at any request rate. The client keeps its upload queue until the server acknowledges it (8.2), so a lost last-second commit after a power cut is re-sent and deduplicated.
- **Read rate.** SQLite's own guide: "any site that gets fewer than 100K hits/day should work fine", "a conservative estimate", and it "has been demonstrated to work with 10 times that amount". Dashboards read rollup rows, and SSE replaces most polling (6.2).
- **Size.** About 12 GB/year of derived rows (prior-art doc 5.2) and 0.5-2 GB of evidence metadata, with evidence blobs themselves in object storage (section 9).
- **Required version.** sqlite.org/wal section 11 documents a "WAL-reset bug" that can corrupt a database when several connections write or checkpoint at the same instant, "present in versions 3.7.0 through 3.51.2", fixed in 3.51.3 with backports in 3.44.6 and 3.50.7. Pin a driver that bundles a fixed SQLite and assert `sqlite_version()` at startup. Go drivers, with the SQLite each bundles (checked in fact-check): `modernc.org/sqlite` v1.62.0 (2026-11-07, pure Go transpiled from C, SQLite 3.54.1; a v1.62.1 entry is in the changelog but wasn't tagged on 2026-11-19), `github.com/ncruces/go-sqlite3` v0.37.0 (2026-11-07, SQLite 3.54.1 via `go-sqlite3-wasm/v6 v6.5.35401`, read from the module's version suffix rather than a stated version, so lightly unverified; no cgo. It no longer runs on wazero: the README says it "wraps a Wasm build of SQLite, and uses wasm2go to translate it to Go", and "Go and x/sys are the only required dependencies". Its `database/sql` driver now uses `BEGIN IMMEDIATE` by default, per the v0.37.0 release notes), `github.com/mattn/go-sqlite3` v1.14.53 (2026-11-05, cgo, SQLite 3.54.1 in `sqlite3-binding.h`, which the cross-compile rule excludes). All three are past the WAL-reset fix. So is the Litestream sidecar: the `litestream` v0.5.18 binary opens databases through `modernc.org/sqlite` v1.49.1 (`db.go`, `cmd/litestream/main.go`, `go.mod`), which ships SQLite 3.53.0. That matters because Litestream is exactly the "two or more database connections ... in separate threads or processes" that checkpoint, the bug's trigger. Check Litestream's bundled SQLite on every Litestream upgrade, not only the app's.
- **Limits to plan for.** WAL "does not work over a network filesystem", so the data directory must be local disk, not NFS or a Docker volume on a network share. Only one app process can write, so scaling out means moving to Postgres.

Keeping the Postgres path open: write plain SQL that both engines accept (no SQLite-only `INSERT OR REPLACE`, use `ON CONFLICT ... DO UPDATE`), store times as integer epoch milliseconds, ids as UUIDv7 text (Postgres 18 has `uuidv7()`, "temporally sortable", per the [18.0 release notes](https://www.postgresql.org/docs/release/18.0/), released 2025-09-25), and run the test suite against both engines in CI. On Postgres, partition `client_events` and `credited_intervals` by month with declarative partitioning and pg_partman, which also makes retention a `DROP` of old partitions. On SQLite, the same retention for raw events is a per-month attached database file that gets deleted. Two caveats (added in fact-check): in WAL mode "Transactions that involve changes against multiple ATTACHed databases are atomic for each individual database, but are not atomic across all databases as a set" ([sqlite.org/wal](https://www.sqlite.org/wal.html)), so an ingest transaction that writes both the main file and a month file can half-apply after a crash. Keep each transaction to one file, or make the month-file write idempotent and replayable. Each attached file is also a separate Litestream database entry. modernc's changelog also records a 3.53.3 data-corruption bug in multi-database (ATTACH) commits, fixed upstream in 3.53.4 (modernc v1.56.0 and v1.58.0 entries). That is one more reason to pin a current SQLite.

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
| `POST /api/v1/submissions`, `POST /api/v1/submissions/:id/evidence` | session, program token or device token | Freeze a window for a program, then upload its evidence (10.2) |
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
- **One transaction per batch.** It verifies the signature and chain link, inserts events, appends to `witness_log`, upserts `rollups_daily` deltas from `credited` events and updates `devices.last_seen_at`. Deltas go to the SSE hub (6.3) only after commit.
- **A privacy firewall on the server.** Every event kind has a strict schema with an allowlist of fields. No field may carry free text: strings are ids, enums, hashes or HMACs of bounded length. A buggy or modified client that tries to upload a prompt, a file path or a code snippet gets a 422, so the privacy promise (R6) doesn't rest on the client alone.

### 8.3 Tokens

| Token | Format | Scope | Storage | Lifetime |
|---|---|---|---|---|
| Device token | `ptd_` + 30 or more random base62 characters + a CRC32 checksum in 6 base62 characters. This copies GitHub's format: prefixes like `ghp_` plus "A 32 bit checksum in the last 6 digits of each token", which "virtually eliminates false positives for secret scanning offline" ([GitHub blog, 2021-04-05, updated 2023-05-10](https://github.blog/engineering/platform-security/behind-githubs-new-authentication-token-formats/)) | `ingest`, `read:self` | SHA-256 hash only | Until revoked; listed per device in settings, with last-seen time and IP |
| Device key | Ed25519 key pair made on the device at sign-in; public key registered with the token | Signs every batch with HTTP Message Signatures ([RFC 9421](https://www.rfc-editor.org/rfc/rfc9421)) over the method, path, `Idempotency-Key` and a `Content-Digest` ([RFC 9530](https://www.rfc-editor.org/rfc/rfc9530)) of the body | Public key | Rotated with the token |
| Web session | HCA OIDC login, then an HttpOnly, `SameSite=Lax` cookie | Browser only | Server-side session row | 30 days; re-auth with `prompt=login` before submitting or changing privacy (3.1) |
| WakaTime-compatible key | Lowercase UUIDv4, optionally `waka_`-prefixed: wakatime-cli rejects anything not matching `^(waka_)?[a-f0-9]{8}-[a-f0-9]{4}-4[a-f0-9]{3}-[89ab][a-f0-9]{3}-[a-f0-9]{12}$` (`pkg/params/params.go` at v2.28.4; fact-check), so it can't carry a `ptd_` prefix or checksum | `ingest:heartbeats` only | Hash | Until rotated (8.7) |
| Program token | OAuth 2.0 client credentials or authorization code | `hours:read`, `submissions:read`, `submissions:write`, `profile:read` (10.1) | Hash | 1 h access, refresh tokens |

The device key doesn't stop the device's owner from forging events. It stops a stolen bearer token from being replayed elsewhere, it binds the witness chain to one install, and it gives Fraud Squad a stable device identity for alt detection (Hackatime does the same job with `machine` names, IPs and JA4, sibling doc 1.9). Unlike Hackatime, no OAuth app ever receives the user's ingest credential (Hackatime's `GET /api/v1/authenticated/api_keys` hands one to any approved app, sibling doc 1.8).

### 8.4 Time and clock skew

Old school laptops have wrong clocks, sleep for days, and can be set back on purpose. Rules:

- Every event carries the client's wall time `t` and a monotonic reading `mono`; every batch carries `boot_id` (Linux `/proc/sys/kernel/random/boot_id`, an install-generated id per boot elsewhere) and the wall and monotonic clocks at send time.
- The server computes the device offset per batch as `server_received_ms - clock.wall_ms` (accurate to within the request's round trip, which is fine at minute resolution). Within one `boot_id`, event times are re-anchored from `mono` differences, so a clock changed mid-session can't move earlier events. If wall and monotonic deltas disagree by more than 2 s within one boot, the batch is flagged `clock_jump`.
- Corrected times more than 5 minutes in the future are rejected. Corrected times older than 30 days are rejected: a laptop offline for more than a month is rare, and events that old can only ever be `backfilled` (next rule). The limit is a config value. Hackatime accepts epoch seconds between 1,000,000,000 and 2,000,000,000 and rescales milliseconds, microseconds, nanoseconds and Windows FILETIME (sibling doc 1.3). Pairtime's protocol fixes milliseconds, so it doesn't need guessing.
- **Witness lag** = `server_received_at - corrected event time`. Under 15 minutes is `witnessed`; more is `backfilled` (grit's threshold, prior-art doc 1.14). Backfilled time still counts on the user's own dashboard. Programs see the split and decide; it's the honest form of Zach's "lookback mode" (BOUNTY.md).
- Day boundaries for streaks and rollups use the user's profile timezone, as Hackatime does with `users.timezone`; each event also keeps its device UTC offset for display.

### 8.5 Witness chain

Each event extends a per-device hash chain, `head = SHA-256(prev || seq || kind || t || canonical_data)`, and each batch reports `prev` and `head`. The server stores `(device_id, seq_to, head, server_received_at)` in `witness_log`. At submission the client uploads evidence for the submitted window only (redacted turn records, for instance), the server recomputes the chain over it, and each turn is proven to have existed by its witness time. This is the grit design (prior-art doc 1.14) and the sibling doc's Design implication 12. The server keeps one row per device per minute: the last head of a minute commits to every event before it, so extra hook-triggered batches inside a minute replace that minute's row instead of adding one. That's at most 6.6M rows/month (one per active user-minute, prior-art doc 5.1), roughly 0.5 GB/month in SQLite, prunable to one row per hour after 90 days.

### 8.6 Rate limits

| Limit | Value | Why |
|---|---|---|
| Batches per device | 6 per minute sustained, burst 30 | A 60 s sync plus prompt and stop hooks fits under 6/min |
| Backlog flush | up to 120 batches/minute while `seq_from` is older than 15 minutes | Lets a laptop that was offline for a week catch up in minutes |
| Devices per user | 10 active | More is unusual and goes to review |
| Unauthenticated auth endpoints | 30 per minute per IP | Device-code polling and sign-in |
| WakaTime-compatible heartbeats | 360 per minute per key | Hackatime's (disabled) rack-attack value for heartbeat POSTs, sibling doc 1.1 |

Responses carry the fields from [draft-ietf-httpapi-ratelimit-headers-11](https://datatracker.ietf.org/doc/html/draft-ietf-httpapi-ratelimit-headers-11) (2026-05-23; it expires 2026-11-24, so check for a -12 before freezing the header format), for example `RateLimit-Policy: "device";q=6;w=60` and `RateLimit: "device";r=2;t=40`, and a `429` carries `Retry-After`. The draft says the client's "Retry-After field MUST take precedence" and recommends jitter because throttled clients otherwise return at the same moment. The client backs off exponentially with full jitter and never drops queued events because of a 429.

### 8.7 Abuse handling

- **Account creation costs an identity.** On the Hack Club instance, accounts come only from HCA (3.1), and submitting to a program requires `verification_status` of `verified`. A Sybil farm needs verified Hack Club identities, which is the same bar every YSWS program already relies on.
- **Ingest never tips off a cheater.** Suspicious devices keep getting `200`s; their intervals are flagged and their trust tier lowered, and reviewers see why (sibling doc Design implication 15). Detection thresholds live server-side, outside the public client.
- **Signals kept per batch.** IP (from `CF-Connecting-IP` behind Cloudflare), Cloudflare's `CF-JA4` TLS fingerprint where the plan provides it, client version and build hash, device key id, witness lag and clock flags. These are for Fraud Squad. Raw IPs are kept 30 days, then truncated to /24 (IPv4) or /48 (IPv6).
- **Cloudflare in front** for DDoS protection, with the origin accepting only Cloudflare traffic, as Hackatime does (sibling doc 1.1). A home server reaches Cloudflare through a tunnel (section 9).
- **Version floor.** `GET /api/v1/ingest/state` returns `scoring_version_min`. Clients below it still upload, but their credited events are recomputed server-side or marked stale, so a known-buggy client can't inflate hours.

### 8.8 Should the server accept WakaTime or Hackatime heartbeats?

The sibling doc's Design implication 1 proposes accepting WakaTime heartbeats on the server as a second `[api_urls]` destination. The trade-off, from the server's side:

| | Server-side WakaTime endpoint | Local endpoint in the pairtime client |
|---|---|---|
| How | `[api_urls] .* = https://pairtime.example/api/v1\|<key>` | `[api_urls] .* = http://127.0.0.1:<port>/api/v1\|<local key>`; wakatime-cli accepts plain `http://localhost` URLs (Wakapi's README uses `api_url = http://localhost:3000/api`) |
| What reaches the server | Every heartbeat field: `entity` (absolute file path), `branch`, `project`, `dependencies`, `lines`, `machine` (sibling doc 1.3) | Only what pairtime's own events carry: counts, hashes, credited intervals |
| Volume | About 150 rows per tracked hour at Hackatime (sibling doc 1.10); 755 GB for 426M rows in Hackatime's Postgres | None extra |
| Works without the pairtime client | Yes: Codespaces, Replit, school lab machines where nothing can be installed | No |

Recommendation: point wakatime-cli at the local client, which folds editor activity into local scoring as the hand-edit signal (R7) and drops paths. Offer the server endpoint only as an opt-in for environments where the client can't run, and on that path store `entity` as an HMAC, keep `language`, `editor`, `category`, `is_write`, `lines` deltas and `time`, and give those heartbeats a 90-day retention after they've been credited. One caveat for the installer: Wakapi's README warns "Do not add multiple `.*` entries because duplicate keys are invalid in an INI file and are ignored by `wakatime-cli`", so if the user already has a `.*` line (for Hackatime or Wakapi), pairtime has to add a differently spelled pattern that matches everything, and the installer should test that both destinations receive the heartbeat. How wakatime-cli v2.28.4 (2026-11-05) behaves, from source (checked in fact-check): `[api_urls]` is read as a map (`vipertools.GetStringMapString(v, "api_urls")`), every key is compiled as a case-insensitive regex (`(?i)` is prepended), and each heartbeat goes to the default `api_url` with `api_key` and "Also send to ALL matching api_urls patterns", deduplicated by `url|key` (`pkg/params/params.go` lines 405-460, `pkg/apikey/apikey.go` `WithReplacing`). So any distinct key that matches everything (`.+`, `^.*$`) adds pairtime as one more destination without touching the user's Hackatime setup. The key after `|` must match `^(waka_)?[a-f0-9]{8}-[a-f0-9]{4}-4[a-f0-9]{3}-[89ab][a-f0-9]{3}-[a-f0-9]{12}$`, a lowercase UUIDv4. Anything else returns an auth error for the whole run instead of skipping that entry, so a malformed pairtime line would also break the user's Hackatime tracking. Because the default `api_url` always receives a copy, a user with no WakaTime or Hackatime account should have the installer set `api_url` to the local client, not add an `[api_urls]` line.

## 9. Self-hosting and cost

### 9.1 Deploy shape

One container plus a tunnel, one local data directory, one object-storage bucket.

```yaml
# compose.yml (sketch; image names and versions are placeholders)
services:
  pairtime:
    image: ghcr.io/some-du6e/pairtime-server:0.1.0   # distroless static base + pairtime and litestream binaries
    restart: unless-stopped
    command: ["litestream", "replicate", "-config", "/etc/pairtime/litestream.yml"]  # config's exec: runs "pairtime serve"
    volumes:
      - ./data:/data                                # SQLite DB + WAL; must be local disk
      - ./config:/etc/pairtime:ro                   # pairtime.toml, litestream.yml
    environment:
      PAIRTIME_BASE_URL: https://pairtime.example.org
      PAIRTIME_SECRETS_FILE: /etc/pairtime/secrets.env   # HCA client secret, GitHub App key, R2 keys
    healthcheck:
      test: ["CMD", "/pairtime", "healthcheck"]
  cloudflared:
    image: cloudflare/cloudflared:2026.11.0           # pin a dated tag, not latest (placeholder version)
    restart: unless-stopped
    command: ["tunnel", "--no-autoupdate", "run"]
    env_file: ./config/tunnel.env                   # TUNNEL_TOKEN
```

Why each piece:

- **No inbound ports.** `cloudflared` "creates outbound-only connections to Cloudflare's global network" and "does not use an inbound listener on your origin", so a home server behind NAT or a dynamic IP gets a public hostname on Karim's domain without port forwarding ([Cloudflare Tunnel docs](https://developers.cloudflare.com/cloudflare-one/networks/connectors/cloudflare-tunnel/), modified 2026-11-18). The tunnel costs nothing: Cloudflare's Tunnel docs say "Available on all plans" ([developers.cloudflare.com/tunnel](https://developers.cloudflare.com/tunnel/), modified 2026-11-18), and the announcement says "any organization can use the secure, outbound-only connection feature of the product at no cost" ([Cloudflare blog, 2021-04-15](https://blog.cloudflare.com/tunnel-for-everyone/)). Each tunnel keeps "four long-lived connections to two Cloudflare data centers" (fact-check: confirmed, "(unverified)" removed). SSE works through it (6.4).
- **Upload size.** Cloudflare's request body limit is "100 MB" on Free and Pro, "200 MB" on Business and up to 5 GB self-serve on Enterprise, with a 413 above it ([Cloudflare limits](https://developers.cloudflare.com/workers/platform/limits/), modified 2026-11-18). That page states the limit for requests to a Worker, keyed to the zone's plan; that the same cap applies to tunnel-proxied requests is likely but not stated there (unverified). Ingest batches are capped at 256 KB (8.2), and evidence goes straight to object storage through presigned URLs, so neither passes through the limit or the home connection's upstream.
- **Litestream in front of the app.** The config's `exec` key runs the app as a subprocess ("Subcommand to execute") so one container does both, and `restore-if-db-not-exists` (v0.5.6+) restores the database from the replica on startup when the local file is missing, which turns "rebuild the server" into "start the container on a new box" ([Litestream config reference](https://litestream.io/reference/config/), v0.5.18 current).
- **Local disk only.** SQLite's WAL "does not work over a network filesystem" (7.4), so `./data` can't sit on NFS or SMB.
- **Image size.** A static Go binary on a distroless or scratch base is tens of MB (wakatime-cli, a comparable Go binary, is 22.9 MB for Windows; client doc 1.1); compare Hackatime's Ruby image plus a separate worker image (sibling doc 1.1).

### 9.2 Backups

| Layer | Mechanism | RPO / RTO |
|---|---|---|
| Continuous | Litestream to S3-compatible storage. `sync-interval` "Defaults to `1s`"; `snapshot.interval` and `snapshot.retention` default `24h`; compaction levels `30s`, `5m`, `1h`. Set retention to 72 h or more | About 1 s of writes / minutes (restore on start) |
| Nightly | `VACUUM INTO` a snapshot file, encrypt it with `age` to an offline key, upload to a second bucket with 30-day lifecycle. Needed because Litestream v0.5 refuses its old age-encryption option at startup (`cmd/litestream/main.go` at v0.5.18: "age encryption is not currently supported, if you need encryption please revert back to Litestream v0.3.x"), so continuous replicas are only as private as the bucket. S3 SSE-C (`sse-customer-key`, v0.5.6+) is the remaining at-rest option, and whether R2 honours it through Litestream is unverified. v0.5 also allows one replica per database ("multiple replicas on a single database are no longer supported"), which is why the second copy is a `VACUUM INTO` job and not a second replica (fact-check) | 24 h / tens of minutes |
| Evidence blobs | Written once to object storage under content hashes; bucket versioning on | n/a |
| Restore drill | A scheduled GitHub Actions workflow restores the latest replica into a throwaway container, runs `PRAGMA integrity_check` and the API smoke tests, and fails loudly | Proves the backups work, without using the home server's CPU |

Where to put the bucket: Cloudflare R2 costs "$0.015 / GB-month" for standard storage, "$4.50 / million" Class A and "$0.36 / million" Class B requests, egress "Free", with a free tier of "10 GB-month", 1 million Class A and 10 million Class B requests a month ([R2 pricing](https://developers.cloudflare.com/r2/pricing/), modified 2026-10-30). Litestream's uploads are Class A operations (`PutObject`, multipart parts and every `List*` call count as Class A; `GetObject`/`HeadObject` are Class B; `DeleteObject` is free, so retention cleanup costs nothing). If something is written every second, a 1 s sync interval means up to 2.6 million uploads a month, about $7 above the free million; a 10 s interval means about 260,000, inside the free tier (estimate, before compaction uploads). Hetzner Object Storage is €6.49/month including 1 TB stored and 1 TB egress (prior-art doc 5.5), and "S3 API calls such as PUT, GET, or DELETE requests" are free there, so a 1 s Litestream `sync-interval` costs nothing extra on Hetzner but about $7/month on R2 ([hetzner.com/storage/object-storage](https://www.hetzner.com/storage/object-storage/), fetched 2026-11-19; regions FSN1, HEL1, NBG1; added in fact-check).

### 9.3 Running it on a home server

An agent can operate this if the repo ships the runbook as code:

- Litestream's own heartbeat pings (v0.5.6+, a GET to a URL such as Healthchecks.io while every database is healthy, interval at least `1m`, default `5m`) give a backup dead-man's switch for free, and its metrics endpoint (`addr: ":9090"`, off by default) exposes replication lag ([Litestream config reference](https://litestream.io/reference/config/); added in fact-check).
- `pairtime healthcheck`, `GET /healthz` (process up) and `GET /readyz` (database writable, replica lag under 60 s, object storage reachable), plus Prometheus metrics at `/metrics` on a private port: ingest rate, batch latency, SSE streams, Litestream lag, queue depth.
- Pinned image tags updated by pull requests (Renovate or Dependabot), never `latest`, so every update is a reviewed diff.
- An external uptime check, for example a scheduled GitHub Actions job against `/healthz` (the shortest schedule interval Actions allows is 5 minutes).
- What a home server risks: power cuts (with `synchronous=NORMAL` the last transactions "might rollback following a power failure", 7.4, and clients re-send them from their queues), ISP outages (clients buffer; program API calls fail, which matters near a program deadline), and residential upload speed (avoided by serving evidence from object storage).

That is fine for a pilot. Once a program relies on pairtime for payouts, the same compose file should move to a VPS, which is the next table.

### 9.4 Cost

| Setup | Monthly cost | Notes |
|---|---|---|
| Karim's home server, pilot | ~$1-3 electricity for a ~10 W mini PC (estimate) + R2 inside the free tier | Domain already owned; Cloudflare free plan |
| R11 scale, minimal | $48.94: Hetzner CCX13 (2 dedicated vCPU, 8 GB, 80 GB NVMe; €36.99/month in HEL1, €43.49 NBG1, €43.99 in the US, excl. VAT) plus Object Storage and snapshots, €44.62 at ECB 1.0968 USD/EUR (prior-art doc 5.5) | 10% of the $500 cap. On 2026-11-19 every CCX plan on [hetzner.com/cloud/general-purpose](https://www.hetzner.com/cloud/general-purpose/) showed "This product is currently unavailable. Please check back later." Have a fallback (an AX dedicated box, a shared-vCPU plan, or another provider) before a program depends on it (fact-check) |
| R11 scale, with a warm standby | $149.68: CCX23 primary + CCX13 standby restoring from the replica + Object Storage (prior-art doc 5.5) | 30% of the cap; failover is "start the container on the standby" |
| Evidence at R11 scale | 10,000 submitted hours (the acceptance target) x 50-200 KB of redacted transcript = 0.5-2 GB: inside R2's free 10 GB. If programs ever want screen evidence at Lookout's ~66 MB per recorded hour (sibling doc Design implication 19), 10,000 hours is 660 GB, about $9.90/month on R2 with free egress | Evidence for submitted hours only, as R10 suggests |

For comparison, Hackatime's bill is $213/month and Zach puts its effective cost at $55/month (BOUNTY.md). The design here lands in the same range because it stores derived rows (about 1 GB/month) instead of heartbeats (about 1.8 KB per row in Hackatime's Postgres, sibling doc 1.10). The things that could break the budget are per-event server-side LLM calls and storing evidence for logged rather than submitted hours (prior-art doc 5.5); both are excluded by design.

### 9.5 What breaks first, and the next step

| Symptom | Threshold (estimate) | Step |
|---|---|---|
| Single SQLite writer saturated | Writer transaction time above ~50% of wall time, far above the 20 commits/s the batching produces | Move to Postgres (7.4), same schema, then more app processes behind the tunnel |
| SSE memory | Tens of thousands of open streams | Second process with a broker (6.3) |
| Raw heartbeat storage (only if the server-side WakaTime endpoint is popular) | Tens of GB per month | 90-day retention, or that one table in ClickHouse |
| Reviewer analytics slowing production | Long queries during review sprints | Run them in DuckDB against a restored replica |

## 10. APIs for programs and reviewers

The sibling doc (sections 3.4 to 3.7 and Design implications 8 to 11 and 16) establishes what programs already call and what reviewers and spot-checkers need. This section turns that into server endpoints.

### 10.1 Program clients

- A program registers an OAuth client on pairtime (`client_id`, `client_secret`, redirect URIs, webhook URL). Users connect their pairtime account to the program with the authorization-code flow and see exactly which scopes they grant.
- Scopes: `profile:read` (pairtime id, `hca_sub`, `slack_id`, username), `email:read` (only because the Unified DB row needs `Email`, 3.3), `hours:read` (credited seconds per project and window), `submissions:write` (create submissions for the user), `submissions:read`.
- Server-to-server reads use the client-credentials grant and see only users who connected to that program, the same rule as HCA's program keys (3.1).
- Lookup for programs whose users signed up elsewhere: `GET /api/v1/program/users?hca_sub=ident!...` or `?slack_id=U...`, answering only for users who connected to that program. Hackatime's admin email lookup (`POST /api/admin/v1/user/get_user_by_email`, sibling doc 1.8) is the pattern to avoid: it answers for anyone.

### 10.2 Submissions

A submission freezes a window of credited time for one program project, with its evidence. It is the object Hackatime lacks (sibling doc 1.8).

| Method and path | Who | Does |
|---|---|---|
| `POST /api/v1/submissions` | Program (`submissions:write`) or the user | Body: `external_id`, `program_project` (name, `code_url`, `demo_url`), `projects[]` (pairtime project ids the user picked), `window: {start, end}`. Idempotent on `(program, external_id)`, as Robert is (sibling doc 3.5). Returns `state: awaiting_evidence` |
| `POST /api/v1/submissions/:id/evidence` | The user's client | Uploads the evidence manifest for that window (8.5) and gets presigned URLs for blobs. The server checks every item against the witness chain, then moves the submission to `submitted` |
| `GET /api/v1/submissions/:id` | Program, reviewer, user | Totals, rows, commits, flags, review state, links (below) |
| `POST /api/v1/submissions/:id/review` | Reviewer | Per-row cuts `[{row_id, approved_seconds, reason}]` and a note; rows can only be cut, never raised above captured seconds (Ari's `clampSeconds` rule, sibling doc 3.6) |
| `POST /api/v1/submissions/:id/outcome` | Program | `approved` or `rejected` with a reason; final |
| `GET /api/v1/submissions/:id/justification?format=handbook` | Program | Text for the handbook fields (10.6) |

`GET /api/v1/submissions/:id`, abridged:

```json
{
  "id": "sub_01JD...", "external_id": "stardance-ship-4821", "state": "under_review",
  "user": {"pairtime_id": "u_93", "hca_sub": "ident!k2p9", "slack_id": "U0123ABC", "username": "rowan"},
  "window": {"start": "2026-11-01T00:00:00Z", "end": "2026-11-18T23:59:59Z"},
  "scoring_version": "2026.11.2",
  "totals": {"logged_s": 51120, "credited_s": 34560, "witnessed_s": 33180, "backfilled_s": 1380,
             "agent_only_s": 12900, "excluded_loops_s": 2280, "idle_s": 1380},
  "kinds": {"prompting": 6120, "reading_output": 7440, "reviewing": 8280, "testing": 5160, "debugging": 4320, "hand_edit": 3240},
  "rows": [
    {"row_id": "r_1", "start": "2026-11-03T18:02:00Z", "end": "2026-11-03T20:11:00Z", "project": "p_9f2c",
     "captured_s": 6840, "kinds": {"reviewing": 2400, "testing": 1800, "prompting": 1500, "hand_edit": 1140},
     "turns": 23, "commits": ["a1b2c3d"], "flags": [], "evidence": "/review/submissions/sub_01JD.../rows/r_1"}
  ],
  "commits": [{"sha": "a1b2c3d", "credited_since_parent_s": 7380, "server_first_seen_at": "2026-11-03T20:11:40Z",
               "forge_verified": "github", "url": "https://github.com/rowan/cs2go/commit/a1b2c3d"}],
  "flags": [{"code": "one_shot_session", "rows": ["r_7"]}],
  "evidence_manifest_sha256": "3f7a...", "manifest_signature": "ed25519:...",
  "review_url": "https://pairtime.example.org/review/submissions/sub_01JD..."
}
```

`credited_s` is the number pairtime stands behind, and the breakdown says what was left out. A program puts the reviewer-approved figure into `Optional - Override Hours Spent`, as Stardance does today (sibling doc 3.3).

Rules:

- **Deterministic.** A submission's numbers never change after `submitted`. If the scoring version changes, open submissions are recomputed and decided ones keep their numbers, the way ari-webhooks handles `CurrentEvidenceVersion` (sibling doc 3.6).
- **Webhooks** for `submission.submitted`, `submission.reviewed` and `submission.decided`, signed `X-Pairtime-Signature: sha256=<hex HMAC-SHA256 of "<X-Pairtime-Timestamp>.<raw body>">` with a 300 s tolerance: the same scheme as Robert's `X-Robert-Signature`, so program authors reuse their verification code (sibling doc 3.5). Retries with exponential backoff for 24 h; a `GET /api/v1/submissions?updated_since=` list covers missed deliveries, since Pixl found webhooks alone weren't enough with Robert.
- **Overlap.** The same project and window can't go to two programs; the server rejects the second submission. An overlap with another user's submission on the same `repo_fingerprint` is flagged. Flavortown already deducts hours another program paid for (sibling doc 3.3).

### 10.3 What a reviewer opens

`/review/submissions/:id`, behind HCA sign-in with a reviewer role:

| Panel | Content | Source |
|---|---|---|
| Summary | Credited, logged, witnessed and backfilled seconds; agent-only and excluded time; trust tier; scoring version | 10.2 |
| Timeline | Rows on a time axis with activity kinds, presence minutes, prompt and stop markers, test runs, commit markers with witness times | `credited_intervals`, `client_events`, `commits` |
| Row detail | For each row, the evidence the user uploaded for it: redacted turn records (prompt length and an optional redacted text if the user chose to include it, agent run time, what the person did between turns), diffstats, test outcomes | Evidence blobs (presigned URLs, 10-minute expiry) |
| Commits | The commit table from 4.3 with forge verification; a link to the repo | 4.3 |
| Flags | One-shot session, re-roll loop, regular timing, no-input prompts, backfilled time, overlap, clock jump | Private rule set (sibling doc Design implication 15) |
| History | The user's previous decided submissions and approval ratios; trust audit log | `submissions`, `trust_audit` |
| Cross-checks | If the user linked Hackatime: a Telescreen link `/workbench/hackatime/overview?u=<hackatime uid>&p=<keys>` (sibling doc 3.5) | Linked identity |
| Cut controls | Per-row approved seconds with a required reason | `POST .../review` |

Access rules:

- Program reviewers see their own program's submissions. HQ roles (Quality and Integrity, Fraud Squad, spot-checkers) see all submitted evidence. Pairtime is a community HCA app, so it can't read HCA's `groups` claim (3.1); roles are kept in pairtime as an allowlist of HCA subs until HQ makes the app official (Open questions).
- Every evidence view is logged with reviewer, submission and time, and the user can see that log on their own submission page. A user who uploads evidence can check exactly who read it (R6).
- Logged-only data is never on the server, so no reviewer can browse anything the user didn't submit (R10's "different privacy guarantees for logged hours vs. submitted hours").

### 10.4 Evidence an outside party can verify

R10 asks that hours be "auditable by an external party". A submission's evidence bundle is self-verifying:

1. A manifest lists every evidence item with its SHA-256 and the witness-chain positions it covers.
2. The server signs the manifest hash, the totals and the scoring version with its Ed25519 key, published at `/.well-known/pairtime-keys.json` with key ids and validity dates.
3. `GET /api/v1/submissions/:id/bundle` returns the manifest, signature, witness heads with server timestamps and the blobs as one archive.
4. An open-source verifier (`pairtime verify bundle.tar`) recomputes the chain, checks the signature and re-runs the scoring version on the inputs, offline. Fraud Squad doesn't have to trust pairtime's server or its UI; it can check that the numbers follow from the evidence.

The handbook's spot-checks remove a project when "Required evidence ... cannot be located" (sibling doc 3.7), so bundles stay downloadable for as long as spot-checks can happen; the window is an open question in the sibling doc (Open question 4). Default retention: two years after the decision, then deletion with a notice to the user.

### 10.5 Hackatime-compatible reads

Programs already have a `HackatimeService` (sibling doc 3.4). Serving the same calls with pairtime's credited numbers lets them add pairtime by pointing a second client at a new base URL: `GET /api/v1/authenticated/me`, `GET /api/v1/users/:username/stats?features=projects&total_seconds=true&filter_by_project=<name>,<name>&start_date=&end_date=` (credited seconds; `filter_by_project` and `features` are single comma-separated strings, not `[]` arrays, per Hackatime's own example `?filter_by_project=harbor,high-seas`; `test_param`, `no_ai_coding`, `boundary_aware` and `filter_by_category` accepted and ignored), `GET /api/v1/users/:username/trust_factor`, `GET /api/v1/users/:username/heartbeats/spans` (credited intervals returned as spans), and `GET /api/admin/v1/user/info_batch?ids=1,2,3` (comma-separated, `take(2000)`, behind an admin key with `users:read`). In Hackatime the `:username` segment resolves, in order, a numeric id, a Slack id, an HCA id or a username, or `my` for the caller (`User.lookup_by_identifier`, `app/models/user.rb`); pairtime should accept the same identifiers (fact-check: corrected from `:id` and `filter_by_project[]`, `app/controllers/api/v1/stats_controller.rb` and `app/controllers/concerns/api/admin/v1/user_utilities.rb` at `43bde10b02db`). These answer only for users connected to the calling program (10.1), unlike Hackatime's public stats path. The JSON shapes match Hackatime's. The one documented difference is that the seconds are credited struggle time, not heartbeat time.

### 10.6 The handbook text

`GET .../justification?format=handbook` returns the fields Quality and Integrity's handbook defines (sibling doc 3.7), so a program's Airtable automation can paste them:

```text
Alternate Tracking Method: Tracked with pairtime 0.4.0 (scoring 2026.11.2). Pairtime credits only intervals with
evidence of the submitter's own work (prompting, reading agent output, reviewing diffs, running tests, debugging,
editing by hand) and excludes agent-only runtime, repeated re-prompting and idle time. Logged 14.2 h, credited
9.6 h; 96% of credited time was witnessed by the server within 15 minutes. Every credited interval links to its
evidence at the review URL below, and the signed evidence bundle can be verified offline.
Additional Justification: https://pairtime.example.org/review/submissions/sub_01JD...
Hackatime Project Name(s) and Date Range(s): cs2go 11/1/2026-11/18/2026   (only if the user linked Hackatime)
Submitter Hackatime ID: 48213                                               (only if linked)
```

The human-written fields (`Specific Technical Features`, `Deflation Justification`) stay with the reviewer.

## 11. Sources

Fetched or read on 2026-11-19 unless a date is given. Repos were read through `gh api` at the commit shown.

| # | Source | Used for |
|---|---|---|
| 1 | [hackclub/auth](https://github.com/hackclub/auth) at `9133160bebcf`: `app/models/oauth_scope.rb`, `config/initializers/doorkeeper.rb`, `db/schema.rb`, `app/views/docs/oidc-guide.md.erb`, `app/views/docs/api.md.erb`, `README.md` | HCA scopes, community vs HQ scopes, token lifetime, no PKCE, REST and check endpoints (3.1) |
| 2 | `https://auth.hackclub.com/.well-known/openid-configuration` (live) | HCA endpoints, grants, claims (3.1) |
| 3 | [hackclub/hackatime](https://github.com/hackclub/hackatime) at `43bde10b02db`: `app/models/hca_transition.rb`, `app/controllers/sessions_controller.rb`, `app/models/concerns/oauth_authentication.rb`, `db/migrate/20261113104759_make_users_hca_id_index_unique.rb`, `app/controllers/profiles_controller.rb`, `app/controllers/settings/privacy_controller.rb`, `app/javascript/pages/Users/Settings/Privacy.svelte`, `app/services/profile_stats_service.rb`, `ARCHITECTURE.md`, repo tree | HCA-only transition, sign-in scopes, profile and privacy model, no push channel (3.1, 3.2, 5.1, 6.1) |
| 4 | [Sign in with Slack](https://docs.slack.dev/authentication/sign-in-with-slack/) (no page date) | Slack OIDC endpoints, scopes, claims (3.2) |
| 5 | [RFC 8628](https://www.rfc-editor.org/rfc/rfc8628), [RFC 8252](https://www.rfc-editor.org/rfc/rfc8252) | Device grant, loopback redirect (3.5) |
| 6 | [GitHub: GitHub Apps vs OAuth apps](https://docs.github.com/en/apps/oauth-apps/building-oauth-apps/differences-between-github-apps-and-oauth-apps) | Permissions, repo selection, webhooks, recommendation (4.1) |
| 7 | [GitHub: refreshing user access tokens](https://docs.github.com/en/apps/creating-github-apps/authenticating-with-a-github-app/refreshing-user-access-tokens) | 8 h tokens, 6-month single-use refresh tokens (4.1) |
| 8 | [GitHub: REST API rate limits](https://docs.github.com/en/rest/using-the-rest-api/rate-limits-for-the-rest-api) | Primary and secondary limits (4.1) |
| 9 | [GitHub: webhook events, push](https://docs.github.com/en/webhooks/webhook-events-and-payloads#push) | 2,048 commits, Contents read, 25 MB cap (4.1) |
| 10 | [GitLab: webhook events](https://docs.gitlab.com/user/project/integrations/webhook_events/), [GitLab as OAuth provider](https://docs.gitlab.com/integration/oauth_provider/) | 20-commit cap, `push_event_hooks_limit`, scopes, 7,200 s tokens (4.1) |
| 11 | [Forgejo OAuth2 provider docs v17.0](https://forgejo.org/docs/latest/user/oauth2-provider/); Codeberg `/.well-known/openid-configuration` and `/api/v1/version` (live: `17.0.0-100-2254cb0+gitea-1.22.0`) | No OAuth scopes, PKCE support (4.1) |
| 12 | [Highlights from Git 2.54](https://github.blog/open-source/git/highlights-from-git-2-54/) (2026-04-20) | Config-based hooks (4.1) |
| 13 | [WakaTime API docs](https://wakatime.com/developers/) | Commit endpoints and fields; profile visibility fields (4.1, 5.1) |
| 14 | [PostgreSQL 18 docs, NOTIFY](https://www.postgresql.org/docs/current/sql-notify.html) (18.7) | Payload and queue limits (6.3) |
| 15 | [Recall.ai, "Postgres LISTEN/NOTIFY does not scale"](https://www.recall.ai/blog/postgres-listen-notify-does-not-scale) (2025-07-01, revised 2026-05-08) | Global lock at commit (6.3) |
| 16 | [cloudflare/cloudflared#199](https://github.com/cloudflare/cloudflared/issues/199) (closed 2025-05-14) | SSE buffering fixed (6.4) |
| 17 | [Phoenix, "The Road to 2 Million Websocket Connections"](https://www.phoenixframework.org/blog/the-road-to-2-million-websocket-connections) (2015-11-03) | BEAM connection density (6.2) |
| 18 | [golang/go](https://github.com/golang/go) master: `src/runtime/stack.go` (`stackMin = 2048`), `src/net/http/server.go` (`bufferBeforeChunkingSize = 2048`, `newBufioReader`) | Per-stream memory (6.2) |
| 19 | [Stack Overflow Developer Survey 2026, languages](https://survey.stackoverflow.co/2026/technology/data/languages) (n = 14,099) | Developer population (7.2) |
| 20 | GitHub search API, `org:hackclub language:<L> pushed:>2025-11-19`, and `gh api repos/hackclub/<repo>` for languages and licenses | Hack Club language mix (7.2) |
| 21 | [Multi-SWE-bench, arXiv 2504.02605v1](https://arxiv.org/html/2504.02605v1) (2025-04-03) | Agent resolved rates by language (7.2) |
| 22 | [Dashbit, "Why Elixir is the best language for AI"](https://dashbit.co/blog/why-elixir-best-language-for-ai) (2026-02-05) | AutoCodeBench claim and caveat (7.2) |
| 23 | [PostgreSQL 18.0 release notes](https://www.postgresql.org/docs/release/18.0/) (2025-09-25) | `uuidv7()`, partitioning changes (7.4) |
| 24 | [SQLite: Appropriate Uses](https://www.sqlite.org/whentouse.html), [FAQ](https://www.sqlite.org/faq.html) (item 19, updated 2024-11-19), [WAL](https://www.sqlite.org/wal.html) | Traffic guidance, one writer, `synchronous=NORMAL`, WAL-reset bug, no network filesystems (7.4) |
| 25 | GitHub and GitLab release metadata (2026-11-19): Litestream v0.5.18, pg_partman v5.5.1 (PostgreSQL License, `LICENSE.txt`), TimescaleDB 2.32.0, ClickHouse v26.9.32.8-stable, DuckDB v2.0.0, mattn/go-sqlite3 v1.14.53, ncruces/go-sqlite3 v0.37.0, modernc sqlite v1.62.0, Phoenix v1.8.16, LiveView v1.2.13, Elixir v1.20.5, Bun 1.4.4, Rails 8.1.4.1, Rust 1.100.0, axum 0.8.9, Hono 4.13.19, SvelteKit adapter-node 6.0.5; `go.dev/dl/?mode=json` (Go 1.27.4); `nodejs.org/dist/index.json` (Node 26.13.1 LTS) | Versions and licenses (7.2, 7.4) |
| 26 | [muety/wakapi](https://github.com/muety/wakapi) `README.md` (v2.18.2) | `api_url` over localhost, `[api_urls]` duplicate-key warning, SQLite default (8.8) |
| 27 | [draft-ietf-httpapi-idempotency-key-header-08](https://datatracker.ietf.org/doc/html/draft-ietf-httpapi-idempotency-key-header-08) (2026-10-22) | Idempotency-Key semantics (8.2) |
| 28 | [draft-ietf-httpapi-ratelimit-headers-11](https://datatracker.ietf.org/doc/html/draft-ietf-httpapi-ratelimit-headers-11) (2026-05-23) | RateLimit fields, Retry-After precedence (8.6) |
| 29 | [RFC 9562](https://www.rfc-editor.org/rfc/rfc9562), [RFC 9421](https://www.rfc-editor.org/rfc/rfc9421), [RFC 9530](https://www.rfc-editor.org/rfc/rfc9530) | UUIDv7, HTTP Message Signatures, Content-Digest (8.2, 8.3) |
| 30 | [GitHub blog, new token formats](https://github.blog/engineering/platform-security/behind-githubs-new-authentication-token-formats/) (2021-04-05, updated 2023-05-10) | Token prefix and checksum design (8.3) |
| 31 | [Cloudflare Tunnel docs](https://developers.cloudflare.com/cloudflare-one/networks/connectors/cloudflare-tunnel/) (modified 2026-11-18) | Outbound-only connections (9.1) |
| 32 | [Cloudflare limits](https://developers.cloudflare.com/workers/platform/limits/) | Request body size per plan (9.1) |
| 33 | [Litestream config reference](https://litestream.io/reference/config/) | Replica keys, defaults, `exec`, restore-on-start, v0.5 changes (9.1, 9.2) |
| 34 | [Cloudflare R2 pricing](https://developers.cloudflare.com/r2/pricing/) (modified 2026-10-30) | Storage and operation prices, free tier (9.2) |
| 35 | Sibling docs in this repo: [hackatime-and-hack-club.md](hackatime-and-hack-club.md), [prior-art-and-measurement.md](prior-art-and-measurement.md), [client-platform.md](client-platform.md); and [BOUNTY.md](../BOUNTY.md) | Hackatime internals, YSWS pipeline, load model, Hetzner prices, client language and memory figures |
| 36 | Added in fact-check: [octokit/webhooks](https://github.com/octokit/webhooks) `payload-schemas/api.github.com/common/commit.schema.json` at `aa80c24a3f55` | Push commit fields (4.1) |
| 37 | Added in fact-check: Codeberg `swagger.v1.json` (live, 2026-11-19) | Forgejo commit endpoint query flags and `stats` fields (4.3) |
| 38 | Added in fact-check: [postgres/postgres](https://github.com/postgres/postgres) commit `282b1cde9ded`, `src/backend/commands/async.c` at `REL_18_STABLE` and `REL_19_0`; [PostgreSQL 19 draft release notes](https://www.postgresql.org/docs/release/19.0/) (as of 2026-10-09); [postgresql.org/support/versioning](https://www.postgresql.org/support/versioning/) | What the NOTIFY fix changes and which release carries it (6.3) |
| 39 | Added in fact-check: [Cloudflare connection limits](https://developers.cloudflare.com/fundamentals/reference/connection-limits/) (2026-11-13); [developers.cloudflare.com/tunnel](https://developers.cloudflare.com/tunnel/) (2026-11-18); [Cloudflare blog, free tunnels](https://blog.cloudflare.com/tunnel-for-everyone/) (2021-04-15) | 125 s proxy read timeout; Tunnel on all plans at no cost (6.4, 9.1) |
| 40 | Added in fact-check: gitlab.com/cznic/sqlite `CHANGELOG.md` and proxy.golang.org `modernc.org/sqlite`; ncruces/go-sqlite3 `README.md`, `go.mod`, `driver/driver.go` and release notes at v0.37.0; mattn/go-sqlite3 `sqlite3-binding.h` at v1.14.53; benbjohnson/litestream `go.mod`, `db.go`, `cmd/litestream/main.go` at v0.5.18 | Bundled SQLite versions, wasm2go, Litestream's SQLite and its age and replica rules (7.4, 9.2) |
| 41 | Added in fact-check: [Hetzner Cloud General Purpose](https://www.hetzner.com/cloud/general-purpose/) and [Object Storage](https://www.hetzner.com/storage/object-storage/) (2026-11-19) | Per-region CCX prices, "currently unavailable", free S3 API calls (9.2, 9.4) |
| 42 | Added in fact-check: [wakatime/wakatime-cli](https://github.com/wakatime/wakatime-cli) v2.28.4 `pkg/params/params.go`, `pkg/apikey/apikey.go`; live `https://slack.com/.well-known/openid-configuration` | `[api_urls]` fan-out and key format (8.3, 8.8); Slack client auth methods (3.2) |
| 43 | Added in fact-check: hackclub/hackatime at `43bde10b02db`: `config/routes.rb`, `app/controllers/api/v1/stats_controller.rb`, `app/controllers/concerns/api/admin/v1/user_utilities.rb`, `app/models/user.rb`, `db/schema.rb`; [hackclub/hackatime#1745](https://github.com/hackclub/hackatime/pull/1745) (open on 2026-11-19) | Hackatime-compatible API details (10.5), shadowbans (3.4), rollup and storage figures (6.1, 7.4) |

## 12. Design implications

Each item names the requirements it serves.

1. **Hack Club Auth is the account.** On the Hack Club instance, create accounts only through HCA, which is where Hackatime's three-week transition (started 2026-11-13) ends up (3.1). Request `openid email slack_id verification_status` (all community-allowed) explicitly, because HCA's discovery document advertises only `openid profile phone birthdate` (fact-check), keep `hca_sub` as the identity key, and require `verified` before a user can submit to a program. The pairtime server is the confidential client, because HCA has no PKCE. (R5, A, R9)
2. **The desktop client signs in to pairtime with the device grant**, gets a per-device token plus an Ed25519 device key, and never holds an HCA, GitHub or Slack token. (R5, R8, R9)
3. **Forges are linked identities and verifiers, not data sources.** A GitHub App with Metadata and Contents read, installed only on repos the user picks, for push webhooks and commit verification; GitHub and GitLab public repos verified without installation; Codeberg tokens discarded after sign-in because Forgejo's OAuth tokens can do "any actions on behalf of the user" (4.1). (R5, R6)
4. **Git integration lives in the client.** Read refs and the reflog for commits, amends and rebases; use Git 2.54 config-based hooks only where available, and never set `core.hooksPath`. This covers GitLab, Codeberg and never-pushed repos with one code path. (R5, R4, R8)
5. **Project identity is the root commit set**, stored as an HMAC, with normalized remotes sent only for public repos. Stardance's hand-picked `hackatime_keys` and Hackatime's free-text project names both go away. (R4, R5, R6)
6. **Commits carry credited time and a witness time.** `credited_seconds_since_parent`, diffstat, `server_first_seen_at` and forge verification let a profile say "2h 03m of struggle went into this commit" without code, and flag commits that first reached the server long after their author date. (R5, R9, R10)
7. **Public by default means aggregates by default.** Profiles show credited hours, streaks, heatmap, languages and harnesses; project names only for public repos; file names, paths, branches of private repos, prompts and machine details never reach the public page, and file paths never reach the server. Leaderboards are opt-in; self-improvement views are private. (R5, R6, R3)
8. **Rollups are additive deltas.** Upsert `seconds = seconds + delta` per `(user, project, day, activity_kind)` at ingest and push the same delta to dashboards. Never rebuild a user's aggregates on the hot path, which is what costs Hackatime 17 worker-hours a day. (R5, R11)
9. **Live dashboards over SSE.** Use one multiplexed stream per tab over HTTP/2, topic ring buffers for `Last-Event-ID` resume, 15 s keepalives, polling fallback after 45 s of silence, in-process fan-out, and at most one `NOTIFY` per transaction if the server ever runs as several processes. PostgreSQL 19's NOTIFY change cuts wakeups but keeps the commit-time lock, so the one-`NOTIFY` rule still applies there (fact-check). (R5, R11)
10. **One scoring implementation.** Write the server in the client's language so `pkg/score` and its test vectors are shared: Go if the client is Go (the client doc's leaning), Rust if it's Rust. The web UI is Svelte with TypeScript, built to static files and embedded in the binary, with API types generated from OpenAPI. (R2, R9, R8)
11. **SQLite in WAL mode with Litestream** as the default database, a single writer that commits every 50 ms or 500 rows, a SQLite build at 3.51.3 or later (or the 3.44.6 or 3.50.7 backports) checked at startup in the app and on every Litestream upgrade, because the Litestream sidecar is the second connection the WAL-reset bug needs (v0.5.18 ships SQLite 3.53.0 through modernc v1.49.1; the Go drivers ship 3.54.1), one database file per transaction if raw events go to per-month attached files, portable SQL, and CI against both SQLite and Postgres so the Postgres path stays real. (R11, R5)
12. **Exactly-once ingest.** Use per-device sequence numbers, UUIDv7 event ids, a batch `Idempotency-Key`, per-event results, and a server-side schema that rejects any free-text field, so a broken client can't leak a prompt or a path. (R6, R9, R11)
13. **Server time is the reference.** Wall clock plus monotonic clock plus boot id per event; per-device offsets; reject events more than 5 minutes in the future or more than 30 days old; label time `witnessed` or `backfilled` at a 15-minute lag and let programs see both. (R4, R9, R10)
14. **Rate limits that never lose data.** Use per-device quotas with a backlog allowance, `RateLimit-Policy`/`RateLimit` headers per the IETF draft, `429` with `Retry-After`, and clients that back off with jitter but keep their queue. (R11, R9)
15. **Abuse handling stays quiet.** HCA verification gates submission; suspicious devices keep getting `200`s while their time is flagged; IP, JA4, client build and device key are kept for Fraud Squad, raw IPs only for 30 days. (R9, R6)
16. **WakaTime heartbeats go to the local client by default** (`http://127.0.0.1` in `[api_urls]`), where they become the hand-edit signal and paths are dropped. The installer adds a distinct match-all `[api_urls]` key (for example `.+`) with a lowercase UUIDv4 key. wakatime-cli v2.28.4 sends every heartbeat to the default `api_url` and to every matching pattern, but fails the whole run on a malformed key, so the installer validates the line before writing it. Users with no Hackatime or WakaTime account get `api_url` pointed at the client instead (fact-check). A server-side WakaTime endpoint is opt-in for places the client can't run, with `entity` stored as an HMAC and 90-day retention. This differs from the sibling doc's Design implication 1, for privacy and storage reasons (8.8). (R6, R7, R11)
17. **Submissions are first-class.** Make them idempotent on the program's `external_id`, with frozen numbers and a scoring version, rows a reviewer can only cut, webhooks signed like Robert's, an `updated_since` list for missed deliveries, and a handbook-shaped justification endpoint. (R9, R10, A)
18. **Evidence bundles verify themselves.** A signed manifest, witness heads and an open-source `pairtime verify` let Fraud Squad check a submission offline. Every reviewer view of evidence is logged and shown to the user. (R10, R6, R9)
19. **Speak Hackatime's program API** for connected users only, with the same JSON shapes and credited seconds, so a program adds pairtime by pointing a second `HackatimeService` at it. Match Hackatime's request details too: the `:username` segment accepts a numeric id, Slack id, HCA id or username, and `filter_by_project` is one comma-separated string (fact-check). (A, R5)
20. **Self-host as one container plus a tunnel.** Ship a distroless Go image wrapped by Litestream, `cloudflared` for the public hostname, replicas and nightly encrypted snapshots in R2, a restore drill in GitHub Actions, pinned image tags. Pilot on Karim's home server; move the same compose file to a VPS once a program depends on it. (R11, R5)
21. **Budget.** Expect about $49/month for one Hetzner CCX13 plus object storage at R11 scale, about $150/month with a warm standby, against the $500 cap. Hetzner listed every CCX plan as "currently unavailable" on 2026-11-19, so keep the compose file provider-neutral and price a fallback (an AX dedicated box at €59-79/month is still under 20% of the cap, prior-art doc 5.5) (fact-check). Keep per-event LLM calls and logged-hour content off the server for good. (R11)

## 13. Open questions

For Karim or Hack Club staff.

1. **Official HCA app status.** Pairtime as a community HCA app can't read `groups`, so it can't recognise HQ reviewers (Quality and Integrity, Fraud Squad, spot-checkers) from HCA. Will HQ mark pairtime official, or is a pairtime-maintained allowlist of reviewer HCA ids acceptable? (R10, A)
2. **Who runs production?** The pilot runs on Karim's domain. If programs adopt pairtime, does Hack Club host it (on Coolify, like Hackatime), and who then holds the HCA client secret, the GitHub App key and the backups? (R11, R5)
3. **Cloudflare as the edge.** Is putting Cloudflare (tunnel and proxy) in front acceptable under pairtime's privacy promise, given that it terminates TLS for all traffic? Hackatime already does this. (R6, R5)
4. **Sign-up outside Hack Club.** Should Karim's instance accept email or GitHub sign-ups from people without an HCA identity, so pairtime is useful beyond YSWS (R3), with submission still gated on HCA? (R3, R5)
5. **Leaderboards.** Opt-in, as proposed here, or opt-out like Hackatime? (R3, R6)
6. **Private repos on GitLab and Codeberg.** Is "local commit, witnessed" without forge verification acceptable evidence for reviewers, given that verifying private GitLab repos needs the broad `read_api` scope and Codeberg has no scoped tokens at all? (R5, R10)
7. **Evidence retention.** Two years after a decision is the default here. It depends on the spot-check window (sibling doc Open question 4). (R10, R6)
8. **Pilot integration.** Which program integrates first, and will it call pairtime's submission API or only the Hackatime-compatible reads? That decides which half of section 10 ships first. (A)
9. **Server-side WakaTime endpoint.** Are there enough users on Codespaces, Replit or locked-down school machines, where the client can't run, to build it at launch? (R7, R8)
10. **Privacy hub.** Does Hack Club's privacy hub (`yeet.hackclub.com`, sibling doc 3.4) expect every new app to implement its export and deletion endpoints, and how does an app register with it? (R6)
11. **Client language.** The server language follows the client's under Design implication 10. The client doc leans Go but hasn't decided. (R2, R9)
12. **Production host if Hetzner CCX stays unavailable** (added in fact-check). Every CCX plan showed "This product is currently unavailable" on 2026-11-19. Is there a box in Karim's fleet, or does Hack Club have a preferred provider or region for a pilot that programs depend on? (R11, A)
13. **HQ-only scopes** (added in fact-check). HCA's docs table marks `legal_name` "HQ only", but the code gates it to super-admin apps. If a program ever needs legal names on pairtime submissions, who at HQ grants that? (A, R6)

## Fact-check log

Adversarial fact-check, 2026-11-19. Each row is a claim (or a tight group of related claims) a designer would lean on, the primary source opened for it, and what changed in the doc. "Confirmed" means the source says what the doc says; "Fixed" means the doc was corrected in place; "(unverified)" means the source couldn't be opened or didn't settle it. Additions are marked "(added in fact-check)" or "(fact-check)" in the body.

Summary: 32 rows checked against primary sources. Most of the doc held up. Corrected in place: HCA's `legal_name` tier (row 2); Hackatime's API path segment and `filter_by_project` format (14); what Postgres's NOTIFY fix does and which release carries it (18); ncruces/go-sqlite3 no longer using wazero (21); a paraphrase presented as a Multi-SWE-bench quote (27); the Bun version (29); a WakaTime quote that wasn't on the page (32). Gaps the doc had left "(unverified)" are now filled: push commit fields (8), the SQLite each Go driver and Litestream bundles (21), Tunnel pricing (24), and how wakatime-cli fans out `[api_urls]` and validates keys (31). Most important additions for a designer: Hetzner listed every CCX plan as unavailable (26); the Litestream sidecar is a second SQLite connection for the WAL-reset bug, and v0.5 allows one replica per database (21, 22); ATTACHed month files aren't atomic in WAL mode (20); a malformed `[api_urls]` key breaks the user's Hackatime tracking too (31).

| # | Claim (section) | Primary source opened | Result |
|---|---|---|---|
| 1 | HCA discovery: issuer, endpoints, grants, auth methods, RS256, claims (3.1) | `curl https://auth.hackclub.com/.well-known/openid-configuration`, 2026-11-19 | Confirmed. Added the `scopes_supported` row (only `openid profile phone birthdate` advertised) |
| 2 | HCA scope tiers and `COMMUNITY_ALLOWED` (3.1) | `app/models/oauth_scope.rb` in hackclub/auth at `9133160bebcf` | `COMMUNITY_ALLOWED` and `HQ_OFFICIAL_SCOPES` confirmed. Fixed: `legal_name` is `SUPER_ADMIN_SCOPES` only, not HQ official; added `set_slack_id` |
| 3 | HCA token lifetime 6 months, refresh tokens, `idntk.` prefix, no PKCE, apps confidential by default, `prompt=login`/`max_age`, no `picture` (3.1) | `config/initializers/doorkeeper.rb`, `db/schema.rb` (`oauth_access_grants` has no `code_challenge*` columns; `oauth_applications.confidential default: true`), `app/views/docs/oidc-guide.md.erb`, `app/models/oauth_token.rb` at `9133160bebcf` | Confirmed |
| 4 | HCA `/api/v1/me` statuses, unauthenticated `/api/external/check` and its six results, `prgmk.` keys (3.1) | `app/views/docs/api.md.erb` at `9133160bebcf` | Confirmed, quotes match |
| 5 | Hackatime's three-week HCA-only move from 2026-11-13 12:21 UTC; Slack sign-in refuses new accounts; HCA scope string; Slack `user_scope`; GitHub `user:email` (3.1, 3.2) | `app/models/hca_transition.rb`, `app/controllers/sessions_controller.rb` line 106, `app/models/concerns/oauth_authentication.rb` lines 70-118, `db/migrate/20261113104759_*` in hackclub/hackatime at `43bde10b02db` (main on 2026-11-19) | Confirmed. The file also says "Slack and email sign-in keep working throughout" for existing accounts; Hackatime's HCA request omits `openid` |
| 6 | GitHub App user tokens 8 h (`expires_in` 28800), refresh 6 months (15897600), refresh tokens single-use; installation tokens 1 h (4.1) | [docs.github.com refreshing-user-access-tokens](https://docs.github.com/en/apps/creating-github-apps/authenticating-with-a-github-app/refreshing-user-access-tokens), fetched 2026-11-19 | Confirmed: "Once you use a refresh token, that refresh token and the old user access token will no longer work". Expiry is opt-out per app, and `offline_access` forces it. The 1 h installation-token lifetime isn't on that page and wasn't re-opened in this pass |
| 7 | GitHub REST limits: 60/h unauthenticated, 5,000/h user, installation 5,000 + 50/repo + 50/user above 20, cap 12,500; 100 concurrent, 900 points/min (4.1) | [docs.github.com rate-limits-for-the-rest-api](https://docs.github.com/en/rest/using-the-rest-api/rate-limits-for-the-rest-api), fetched 2026-11-19 | Confirmed. Not in the doc: 2,000 OAuth token requests/hour per app, 80 content-creating requests/min, 15,000/h on Enterprise Cloud |
| 8 | GitHub push webhook: 2,048 commits, Contents read, 25 MB cap; per-commit fields (4.1) | [docs.github.com webhook-events-and-payloads](https://docs.github.com/en/webhooks/webhook-events-and-payloads#push); octokit/webhooks `common/commit.schema.json` at `aa80c24a3f55` | Confirmed, so the "(unverified)" came off. Added the 5,000-branch and three-tag cut-offs |
| 9 | GitLab push hook: newest 20 commits, `total_commits_count`, `push_event_hooks_limit` default 3; tokens 7,200 s; `read_api` reads "all groups and projects" (4.1) | [GitLab webhook events](https://docs.gitlab.com/user/project/integrations/webhook_events/), [GitLab OAuth provider](https://docs.gitlab.com/integration/oauth_provider/), fetched 2026-11-19 (pages cite GitLab 19.6) | Confirmed. The limit covers tags too, and self-managed admins can change token lifetime since 19.1 |
| 10 | Git 2.54 config hooks: `hook.<name>.event`/`.command` in any config scope, several hooks per event, `$GIT_DIR/hooks` "runs last", `hook.<name>.enabled = false` (4.1) | [Highlights from Git 2.54](https://github.blog/open-source/git/highlights-from-git-2-54/) (Taylor Blau, 2026-04-20, updated 2026-04-27) | Confirmed. The post also says hooks in config "run in the order it encounters their configuration" and `git hook list` shows where each comes from |
| 11 | Forgejo/Codeberg: OAuth2 has no scopes, tokens "can be used to execute any actions on behalf of the user", PKCE supported; Codeberg on 17.0.0; commit endpoint (4.1, 4.3) | [Forgejo docs v17.0](https://forgejo.org/docs/latest/user/oauth2-provider/) (latest v17.0.2, LTS v15.0.12); `https://codeberg.org/api/v1/version` = `17.0.0-100-2254cb0+gitea-1.22.0`; Codeberg discovery (`code_challenge_methods_supported: plain, S256`); `swagger.v1.json` | Confirmed. Added the Forgejo query flags and stats fields to 4.3 |
| 12 | Hackatime privacy page is two checkboxes (`allow_public_stats_lookup` default true, `leaderboard_opt_out`); shared project pages need `project_repo_mappings.public_shared_at`; `PROFILE_DASHBOARD_KEYS` (5.1) | `app/controllers/settings/privacy_controller.rb` line 41, `db/schema.rb`, `app/services/profile_stats_service.rb`, `config/routes.rb` lines 366-370 at `43bde10b02db` | Confirmed (the key set also has `language_colors`). Added the timed leaderboard shadowban columns to 3.4 |
| 13 | Hackatime has no push channel: only `config/cable.yml`, no channels, no `EventSource` (6.1) | Repo tree at `43bde10b02db` (no `app/channels`); code search for `EventSource`, `WebSocket`, `ActionCable` and `text/event-stream` finds only `bun.lock` and `Gemfile.lock` | Confirmed |
| 14 | Hackatime's program API: `users/:id/stats` with `filter_by_project[]`, `trust_factor`, `heartbeats/spans`, `info_batch` up to 2,000 (10.5) | `config/routes.rb` lines 252-300, `app/controllers/api/v1/stats_controller.rb`, `concerns/api/admin/v1/user_utilities.rb` line 287, `app/models/user.rb` line 96 at `43bde10b02db` | Fixed: the segment is `:username` (it also accepts numeric, Slack or HCA ids); `filter_by_project` is a comma-separated string, not `[]`. 2,000 cap confirmed |
| 15 | Rollup rebuilds take about 17 worker-hours a day; 426M heartbeats in 755 GB, or 5.5 GiB on ClickHouse (6.1, 7.4) | [hackclub/hackatime#1745](https://github.com/hackclub/hackatime/pull/1745) body, read 2026-11-19 | Numbers confirmed. Added that the PR is open and unmerged (production still uses Postgres) and that 592 GB of the 755 GB is indexes |
| 16 | cloudflared buffered SSE until the maintainers closed #199 on 2025-05-14 with "Server Sent Events have been fixed..." (6.4) | `gh api repos/cloudflare/cloudflared/issues/199` and its comments | Confirmed: closed 2025-05-14T21:24:29Z by `jcsf` with exactly that comment |
| 17 | Cloudflare proxy read timeout 125 s (6.4) | [Cloudflare connection limits](https://developers.cloudflare.com/fundamentals/reference/connection-limits/) (`dateModified` 2026-11-13) | Confirmed (the old figure was 100 s). Added the cite, the 524 status and "Enterprise only" |
| 18 | Postgres NOTIFY: payload under 8,000 bytes, 8 GB queue, delivered at commit; Recall.ai's global commit lock, fixed upstream in an unknown release (6.3) | [PG docs NOTIFY](https://www.postgresql.org/docs/current/sql-notify.html) (18.7); [Recall.ai](https://www.recall.ai/blog/postgres-listen-notify-does-not-scale) (2025-07-01, revised 2026-05-08); postgres/postgres commit `282b1cde`, `compare` against `REL_18_STABLE`/`REL_19_0`, `async.c` at both refs; 19.0 draft release notes; postgresql.org/support/versioning | Limits and quotes confirmed. Fixed: the upstream commit cuts wakeups but keeps the commit lock, and it ships only in PostgreSQL 19 (tagged 2026-11-16, not yet listed as released) |
| 19 | Go memory: `stackMin = 2048`, `bufferBeforeChunkingSize = 2048`, 4 KiB reader per connection (6.2) | `src/runtime/stack.go` line 78, `src/net/http/server.go` lines 348, 983, 2151 at golang/go `b496a3044e1d` | Confirmed. Added the 4 KiB per-connection writer; the 10-20 KB per stream estimate still holds |
| 20 | SQLite: one writer; FAQ "far more than 50K inserts/second" (2024-11-19 update); "fewer than 100K hits/day"; WAL with `synchronous=NORMAL`; no network filesystems; WAL-reset bug in 3.7.0-3.51.2, fixed 3.51.3 with backports 3.44.6 and 3.50.7 (7.4) | sqlite.org `whentouse.html`, `faq.html`, `wal.html` section 11, fetched 2026-11-19 | Confirmed word for word. Added that the bug needs two connections writing or checkpointing at once, which a Litestream sidecar provides, and the ATTACH non-atomicity caveat for per-month files |
| 21 | Go SQLite drivers: modernc v1.62.0, ncruces v0.37.0 "Wasm through wazero" with `BEGIN IMMEDIATE` default, mattn v1.14.53; bundled SQLite "unverified" (7.4) | proxy.golang.org `modernc.org/sqlite/@latest` and `@v/v1.62.1.info`; gitlab.com/cznic/sqlite `CHANGELOG.md`; ncruces `README.md`, `go.mod`, `go.sum`, `driver/driver.go`, release notes at v0.37.0; mattn `sqlite3-binding.h` at v1.14.53 | Versions confirmed. Fixed: ncruces now uses wasm2go, not wazero. Filled the gap: all three bundle SQLite 3.54.1; Litestream v0.5.18 links modernc v1.49.1 (SQLite 3.53.0) |
| 22 | Litestream v0.5.18 (2026-11-03, Apache-2.0); `exec`; `restore-if-db-not-exists` since v0.5.6; `sync-interval` 1 s; snapshot interval and retention 24 h; levels 30s/5m/1h; age encryption rejected (9.1, 9.2) | `gh api repos/benbjohnson/litestream/releases/latest`; [litestream.io/reference/config](https://litestream.io/reference/config/); `cmd/litestream/main.go` lines 777 and 1342-1348 at v0.5.18 | Confirmed. Added: one replica per database in v0.5, SSE-C as the remaining encryption option, and heartbeat pings and metrics for the runbook |
| 23 | R2: $0.015/GB-month, Class A $4.50/M, Class B $0.36/M, free egress; free tier 10 GB-month, 1M A, 10M B (9.2) | [developers.cloudflare.com/r2/pricing](https://developers.cloudflare.com/r2/pricing/) (`dateModified` 2026-10-30) | Confirmed. Added which operations are Class A, B or free. The $7/month estimate for a 1 s sync interval holds (2.6M - 1M free = 1.6M x $4.50) |
| 24 | Tunnel is outbound-only with no inbound listener; free on Cloudflare's free plan "(unverified)"; body limit 100 MB Free/Pro, 200 MB Business (9.1) | [Cloudflare Tunnel docs](https://developers.cloudflare.com/cloudflare-one/networks/connectors/cloudflare-tunnel/) and [developers.cloudflare.com/tunnel](https://developers.cloudflare.com/tunnel/) (both 2026-11-18); [blog, 2021-04-15](https://blog.cloudflare.com/tunnel-for-everyone/); [Workers limits](https://developers.cloudflare.com/workers/platform/limits/) (2026-11-18) | Confirmed, and "free" is now sourced. Noted that the body-limit page talks about Workers, so the tunnel case stays unverified |
| 25 | Idempotency-Key draft -08 (2026-10-22): RFC 9651 String, UUID RECOMMENDED, 422 on reuse with a different payload, 409 while in flight, publish an expiry policy; RateLimit draft -11 (2026-05-23): `RateLimit-Policy`/`RateLimit` with `q`/`w`/`r`/`t`, "Retry-After field MUST take precedence", jitter (8.2, 8.6) | datatracker API `doc/document/<name>` (rev and time); `ietf.org/archive/id/...-08.txt` sections 2.1-2.3 and 2.7; `...-11.txt` lines 323-444, 734, 888 | Confirmed. Added that -11 expires 2026-11-24 |
| 26 | Hetzner R11 budget: CCX13 + Object Storage = $48.94; with standby $149.68; Object Storage €6.49 with 1 TB + 1 TB (9.2, 9.4) | [hetzner.com/cloud/general-purpose](https://www.hetzner.com/cloud/general-purpose/) and [storage/object-storage](https://www.hetzner.com/storage/object-storage/), fetched 2026-11-19; prior-art doc 5.5 for the conversion | Prices confirmed (CCX13 €36.99 HEL1, CCX23 €86.49 NBG1, both excl. VAT). Added that every CCX plan was shown "currently unavailable", that S3 API calls are free on Hetzner, and the per-region prices |
| 27 | Multi-SWE-bench: Go 428 tasks / 7.48%, TS 224 / 11.61%, JS 356 / 5.06%, Rust 239 / 15.90%; quoted rankings (7.2) | [arXiv 2504.02605v1](https://arxiv.org/html/2504.02605v1) Tables 1, 2 and 4 (2025-04-03) | Numbers confirmed. Fixed: "Python and Java first" was a paraphrase in quote marks; replaced it with the paper's sentence, and noted that TS's best score beats Go's |
| 28 | Stack Overflow 2026: Go 16.5%, TS 43.8%, JS 62.0%, Elixir 2.6%, Rails 5.8%, Rust 18.0%, n = 14,099 (7.2) | [survey.stackoverflow.co/2026 languages](https://survey.stackoverflow.co/2026/technology/data/languages), fetched 2026-11-19 | Numbers confirmed. Clarified that 5.8% is Ruby and 2.6% is Elixir (the page has no framework rows), and that n counts answers to one optional question |
| 29 | Versions and licenses: Go 1.27.4, Rust 1.100.0, Node 26.13.1 LTS, Bun 1.4.4, Rails 8.1.4.1, Elixir 1.20.5, Phoenix 1.8.16, LiveView 1.2.13, axum 0.8.9, Hono 4.13.19, TimescaleDB 2.32.0 (TSL for compression and continuous aggregates), ClickHouse v26.9.32.8 (Apache-2.0), DuckDB v2.0.0 (MIT), pg_partman v5.5.1 (7.2, 7.4) | `go.dev/dl/?mode=json`; `nodejs.org/dist/index.json`; `gh api repos/<repo>/releases/latest` and repo license metadata; timescaledb `LICENSE` and `tsl/src` tree at 2.32.0 | All confirmed except Bun: 1.4.5 shipped 2026-11-19, so the doc now says 1.4.5. SvelteKit adapter-node 6.0.5 not re-checked |
| 30 | Sign in with Slack: endpoints, `openid email profile` only, no mixing with other scopes, 300 s sample ID token, no PKCE (3.2, 3.5) | [docs.slack.dev sign-in-with-slack](https://docs.slack.dev/authentication/sign-in-with-slack/); live `slack.com/.well-known/openid-configuration` | Confirmed. Discovery advertises only secret-based client auth and no `code_challenge_methods_supported`, so "Slack's OIDC needs a client secret" (3.5) holds |
| 31 | Wakapi README: `api_url = http://localhost:3000/api`, no duplicate `.*` keys; Wakapi MIT; how wakatime-cli treats several `[api_urls]` patterns "(unverified)" (8.8, 7.2) | muety/wakapi `README.md` at 2.18.2 (lines 136-157), repo license (MIT); wakatime-cli v2.28.4 `pkg/params/params.go`, `pkg/apikey/apikey.go` | Confirmed, and the gap is filled: every matching pattern receives the heartbeat, deduplicated by url and key; keys must be UUIDv4 or the run fails. 8.3 and 8.8 updated |
| 32 | WakaTime: per-commit `total_seconds` at `GET /api/v1/users/current/projects/:project/commits`; user `*_public` fields; stats `is_*_visible` fields; IPs in machine names (4.1, 5.1) | [wakatime.com/developers](https://wakatime.com/developers/), both halves, fetched 2026-11-19 | Endpoint, `total_seconds` wording and all 13 visibility fields confirmed. Added the `read_heartbeats` scope and the commit fields. Replaced the machine-name quote with the wording found on the page. The embeddables quote wasn't re-found (unverified) |
