# Raw notes: Prior art, measurement science, anti-fraud, motivation, and server cost

These are unverified notes from a research agent that was cancelled on 2026-11-19, before it wrote its doc. Work on pairtime was paused. The orchestrator extracted them from the agent transcript so the work isn't lost. Fetched-page summaries were written by the fetch tool and have not been fact-checked. Treat them as leads, not findings.

## GitHub repos the agent read

- [cursor/agent-trace](https://github.com/cursor/agent-trace)
- [git-ai-project/git-ai](https://github.com/git-ai-project/git-ai)
- [hackclub/ari](https://github.com/hackclub/ari)
- [hackclub/ari-webhooks](https://github.com/hackclub/ari-webhooks)
- [hackclub/breadcrumb](https://github.com/hackclub/breadcrumb)
- [hackclub/hackatime](https://github.com/hackclub/hackatime)
- [hackclub/lapse](https://github.com/hackclub/lapse)
- [hackclub/lookout](https://github.com/hackclub/lookout)
- [hackclub/sunbeam-review](https://github.com/hackclub/sunbeam-review)
- [muety/wakapi](https://github.com/muety/wakapi)
- [ryoppippi/ccusage](https://github.com/ryoppippi/ccusage)
- [specstoryai/getspecstory](https://github.com/specstoryai/getspecstory)
- [vibe-log/vibe-log-cli](https://github.com/vibe-log/vibe-log-cli)
- [wakatime/claude-code-wakatime](https://github.com/wakatime/claude-code-wakatime)
- [wakatime/wakatime-cli](https://github.com/wakatime/wakatime-cli)

## Web searches

- "WakaTime AI coding tracking heartbeat ai_line_changes human_line_changes"
  - [src/wakatime.ts at master · wakatime/vscode-wakatime](https://github.com/wakatime/vscode-wakatime/blob/master/src/wakatime.ts)
  - [https://raw.githubusercontent.com/wakatime/wakatime-cli/HEAD/cmd/root.go](https://raw.githubusercontent.com/wakatime/wakatime-cli/HEAD/cmd/root.go)
  - [Features and Usage | wakatime/vscode-wakatime | DeepWiki](https://deepwiki.com/wakatime/vscode-wakatime/1.2-features-and-usage)
  - [Heartbeat Generation | wakatime/vscode-wakatime | DeepWiki](https://deepwiki.com/wakatime/vscode-wakatime/3.1-heartbeat-generation)
  - [FAQ - WakaTime](https://wakatime.com/faq)
- "git-ai git notes AI authorship standard github"
  - [specs/git%5Fai%5Fstandard%5Fv3.0.0.md](https://github.com/git-ai-project/git-ai/blob/main/specs/git%5Fai%5Fstandard%5Fv3.0.0.md)
  - [git-ai-project/git-ai](https://github.com/git-ai-project/git-ai/)
  - [AGENTS.md at main · git-ai-project/git-ai](https://github.com/git-ai-project/git-ai/blob/main/AGENTS.md)
  - [How Git AI Works | Git AI](https://usegitai.com/docs/get-started/how-git-ai-works)
  - [feat: sessions & trace IDs — authorship notes v2](https://github.com/git-ai-project/git-ai/pull/1153)
- ""Agent Trace" specification AI code attribution Cursor"
  - [Agent Trace](https://agent-trace.dev/)
  - [Agent Trace: Cursor Proposes an Open Specification for AI Code Attribution - InfoQ](https://www.infoq.com/news/2026/02/agent-trace-cursor/)
  - [README.md](https://github.com/cursor/agent-trace/blob/a9b8d05a/README.md)
  - [README.md at main · cursor/agent-trace](https://github.com/cursor/agent-trace/blob/main/README.md)
  - [Agent Trace Specification | cursor/agent-trace | DeepWiki](https://deepwiki.com/cursor/agent-trace/2-agent-trace-specification)
- "hackclub lapse timelapse github repository"
  - [hackclub/lapse](https://github.com/hackclub/lapse)
  - [AGENTS.md](https://github.com/hackclub/lapse/blob/main/AGENTS.md)
  - [docs/custom-clients.md](https://github.com/hackclub/lapse/blob/main/docs/custom-clients.md)
  - [API.md](https://github.com/hackclub/lapse/blob/bdd96c2d/API.md)
  - [Lapse](https://lapse.hackclub.com/)
- "WakaTime blog AI coding time tracking 2026 "AI Coding" category prompts tokens dashboard"
  - [Get ready for your yearly wrapped code stats - WakaTime](https://wakatime.com/blog/69-get-ready-for-your-yearly-wrapped-code-stats)
  - [WakaTime - AI coding analytics for developers](https://wakatime.com/)
  - [FAQ - WakaTime](https://wakatime.com/faq)
  - [wakatime/claude-code-wakatime](https://github.com/wakatime/claude-code-wakatime)
  - [https://wakatime.com/llms.txt](https://wakatime.com/llms.txt)
- "Claude Code Analytics Admin API usage_report/claude_code endpoint fields num_sessions lines_of_code tool_actions"
  - [Claude Code Analytics API](https://platform.claude.com/docs/en/manage-claude/claude-code-analytics-api)
  - [claude_code_reports | StackQL Anthropic Admin Provider](https://anthropic-admin-provider.stackql.io/services/usage/claude_code_reports/)
  - [docs/sources/anthropic/claude-code/claude-code.yaml at docs-v0.5.18 · Worklytics/psoxy](https://github.com/Worklytics/psoxy/blob/docs-v0.5.18/docs/sources/anthropic/claude-code/claude-code.yaml)
  - [claudeadmin_claude_code_usage_report | Data Sources | sauterdigital/claudeadmin | Terraform | Terraf](https://registry.terraform.io/providers/sauterdigital/claudeadmin/latest/docs/data-sources/claude_code_usage_report)
  - [Get Claude Code Usage Report](https://platform.claude.com/docs/en/api/beta/organization/usage_report/retrieve_claude_code)
- "Cursor AI Code Tracking API admin commits tabLinesAdded composerLinesAdded nonAiLinesAdded"
  - [https://cursor.com/docs/account/teams/ai-code-tracking-api](https://cursor.com/docs/account/teams/ai-code-tracking-api)
  - [Details on AI Code Tracking API](https://forum.cursor.com/t/details-on-ai-code-tracking-api/128253)
  - [[AI code tracking API] totalLinesAdded < nonAiLinesAdded](https://forum.cursor.com/t/ai-code-tracking-api-totallinesadded-nonailinesadded/158416)
  - [Get AI commit metrics with the AI Code Tracking API — METAGROK](https://metagrok.io/how-to/get-ai-commit-metrics-via-api)
  - [Admin API](https://cursor.com/docs/account/teams/admin-api)
- "GitHub Copilot usage metrics API 2026 user-level report endpoint loc_added_sum code_acceptance"
  - [REST API endpoints for Copilot usage metrics](https://docs.github.com/en/rest/copilot/copilot-usage-metrics?apiVersion=2026-03-10)
  - [https://docs.github.com/en/copilot/reference/copilot-usage-metrics/copilot-usage-metrics](https://docs.github.com/en/copilot/reference/copilot-usage-metrics/copilot-usage-metrics)
  - [https://docs.github.com/en/copilot/reference/copilot-usage-metrics/example-schema](https://docs.github.com/en/copilot/reference/copilot-usage-metrics/example-schema)
  - [Team-level Copilot usage metrics](https://docs.github.com/en/copilot/reference/copilot-usage-metrics/team-level-metrics)
  - [Lines of Code metrics](https://docs.github.com/en/copilot/reference/copilot-usage-metrics/lines-of-code-metrics)
- "DX AI Measurement Framework utilization impact cost metrics "AI-assisted engineering" 2026"
  - [AI measurement framework: Complete guide for engineering leaders](https://getdx.com/blog/ai-measurement-framework-guide/)
  - [How to measure AI performance in software engineering](https://getdx.com/blog/measure-ai-impact/)
  - [AI-assisted engineering: How AI is transforming software development](https://getdx.com/blog/ai-assisted-engineering-hub/)
  - [Measuring AI code assistants and agents](https://getdx.com/uploads/ai-measurement-framework.pdf)
  - [AI measurement in engineering | DX](https://getdx.com/ai-measurement/)
- "LinearB AI insights measure AI code impact PR labels "AI-generated" metrics 2026"
  - [AI Analytics: Adoption, Workflow, and Impact - HelpDocs & User Setup | LinearB](https://linearb.helpdocs.io/article/q8veszh4nr-ai-analytics-adoption-workflow-and-impact)
  - [Closing the AI gap: How to measure adoption and impact in engineering (without falling into the ROI ](https://linearb.io/blog/closing-the-ai-gap)
  - [AI Insights Dashboard | Understand AI Adoption & Developer Impact](https://linearb.io/resources/ai-insights-dashboard)
  - [AI FAQs - Governance, Detection & Trust - HelpDocs & User Setup | LinearB](https://linearb.helpdocs.io/article/ww1ciql9uw-linear-b-ai-insights-faq)
  - [AI Insights – LinearB Support](https://linearb.zendesk.com/hc/en-us/articles/45537044998299-AI-Insights)
- "Jellyfish AI Impact dashboard measures copilot cursor claude code adoption cycle time 2026"
  - [Measure AI Coding ROI and AI Impact Dashboard | Jellyfish](https://jellyfish.co/platform/jellyfish-ai-impact/)
  - [AI Adoption Dashboard for Engineering Leaders](https://jellyfish.co/library/ai-adoption-dashboard/)
  - [What Jellyfish AI Impact Delivers (That Native Dashboards Can’t) - Jellyfish](https://jellyfish.co/blog/what-jellyfish-ai-impact-delivers-that-native-dashboards-cant/)
  - [Measure AI Impact on Developer Productivity Metrics | Jellyfish](https://jellyfish.co/platform/jellyfish-ai-impact/impact-insights/)
  - [Cursor Reporting Dashboard | Jellyfish](https://jellyfish.co/platform/cursor-dashboard/)
- "Hack Club "weighted projects" unified YSWS database hours weight"
  - [nutshells](https://readme.hackclub.com/nutshells)
  - [AGENTS.md](https://github.com/hackclub/zach-mail-room/blob/main/AGENTS.md)
  - [The plan for hack club world dominance - Deven Jadhav](https://notes.deven.dev/posts/the-plan-for-hack-club-world-dominance)
  - [How many weighted projects will be sent to unified this October? | Manifold](https://manifold.markets/hippogriff/how-many-weighted-projects-will-be)
  - [YSWS true spend](https://ysws-true-spend.hackclub.com/)
- "news.hackclub.com "AI time tracking""
  - [Mission Law Brings AI Time Tracking to Its Corporate Law Practice with PointOne](https://pointone.com/news/mission-law-ai-time-tracking)
  - [PointOne raises $16m to target lawyers' timesheets with AI](https://www.nonbillable.co.uk/news/pointone-series-a-fundraise)
  - [Laurel Secures Strategic Investment from Workday Ventures to Transform AI Time Tracking in Professio](https://www.dhrmap.com/news/laurel-secures-strategic-investment-from-workday-ventures-to-transform-ai-time-tracking-in-professional-services)
  - [Unite. AI ranks AI time trackers for small business billing and payroll — AIXVZ](https://aixvz.cc/news/uniteai-ranks-ai-time-trackers-for-small-business-billing-and-payroll/)
  - [The Brief](https://joinajax.com/blog)
- "METR 2026 follow-up study AI developer productivity randomized trial uplift results"
  - [We are Changing our Developer Productivity Experiment Design - METR](https://metr.org/blog/2026-02-24-uplift-update/)
  - [METR/Measuring-Late-2025-AI-on-OSS-Devs](https://github.com/METR/Measuring-Late-2025-AI-on-OSS-Devs)
  - [METR's 2026 trial: developers took 4–18% less time with AI tools, but the signal is weak | Just a Pa](https://www.justaparrot.com/e/metr-2026-uplift-follow-up/)
  - [What METR’s Unfinished Tasks Tell Us About AI Productivity | Chen Yang](https://cafferyang.com/blog/metr-uplift-selection-bounds)
  - [METR 2026: AI Made Developers 19% Slower. Is That Still True?](https://ingenire.com/blog/metr-2026-developer-productivity-study)
- "Anthropic "How AI assistance impacts the formation of coding skills" randomized study quiz scores interaction patterns"
  - [How AI assistance impacts the formation of coding skills \\ Anthropic](https://www.anthropic.com/research/AI-assistance-coding-skills)
  - [Anthropic's study on AI and coding skills: what it found and what to do - Learning harness](https://piethanegraaf.github.io/ai-coding-skills-study/)
  - [How AI assistance impacts the formation of coding skills](https://blog.stephenturner.us/p/ai-assistance-coding-skills-anthropic)
  - [Anthropic Study: How AI Assistance Affected Coding Skills](https://deepnoodle.ai/atlas/timeline/2026-01-29-anthropic-coding-skills-study)
  - [How AI assistance impacts the formation of coding skills - Roger Wong](https://rogerwong.me/2026/02/how-ai-assistance-impacts-the-formation-of-coding-skills)
- "Anthropic Economic Index report 2026 Claude Code automation augmentation directive feedback loop"
  - [Anthropic Economic Index: AI's impact on software development \\ Anthropic](https://www.anthropic.com/research/impact-software-development)
  - [Anthropic Economic Index report: Cadences \\ Anthropic](https://www.anthropic.com/research/economic-index-june-2026-report)
  - [Anthropic Economic Index report: Learning curves \\ Anthropic](https://www.anthropic.com/research/economic-index-march-2026-report?app=claude-code)
  - [Economic Index report: Economic primitives \\ Anthropic](https://www.anthropic.com/research/anthropic-economic-index-january-2026-report)
  - [Appendix to “Anthropic Economic Index report: Learning curves”](https://www-cdn.anthropic.com/files/4zrzovbb/website/a3cdcd9e67c3c4c51440429dd016cacba514b35b.pdf)
- "Anthropic research 2026 Claude Code sessions report human prompts per session interruptions autonomy "Claude Code" companion report"
  - [How Claude Code is used in practice](https://www.anthropic.com/research/claude-code-expertise)
  - [Measuring AI agent autonomy in practice \\ Anthropic](https://www.anthropic.com/research/measuring-agent-autonomy?=)
  - [Measuring AI agent autonomy in practice: McCain (2026) | Anthony West](https://anthonywest.co.uk/explainers/folders/ai-safety/2026-08-01_mccain_agent-autonomy-in-practice_explainer)
  - [Anthropic analyzed 400,000 Claude Code sessions, and it turns out there's a right way to use it](https://www.xda-developers.com/anthropic-analyzed-400000-claude-code-sessions-turns-out-theres-a-right-way-to-use-it/)
  - [Anthropic reports on 'How much authority do humans who use AI agents give to AI?' - GIGAZINE](https://gigazine.net/gsc_news/en/20260219-anthropic-claude-ai-agent-report/)
- "Anthropic Education Report students Claude direct vs collaborative conversations Bloom's taxonomy 2025 2026"
  - [Anthropic Education Report: How University Students Use Claude \\ Anthropic](https://www.anthropic.com/research/anthropic-education-report-how-university-students-use-claude)
  - [How Do University Students Use AI Chatbots? Reviewing Anthropic’s Education Report | 안상진 (Sangzin Ah](https://ahn-lab.org/posts/53/)
  - [Students delegate higher-level thinking to AI, Anthropic study finds](https://the-decoder.com/students-delegate-higher-level-thinking-to-ai-anthropic-study-finds/)
  - [Empowering Educators: Insights from Anthropic’s Report on Claude’s Role in Higher Education](https://www.computingatschool.org.uk/forum-news-blogs/2025/april/empowering-educators-insights-from-anthropic-s-report-on-claude-s-role-in-higher-education/)
  - [Generative AI in education: Reshaping the future of learning](https://researchonline.lse.ac.uk/id/eprint/128458/1/management_2025_5_6_generative-ai-in-education-reshaping-the-future-of-learning.pdf)

## Pages fetched, with the fetch tool's summary

### https://agent-trace.dev/

Asked: Give me the full technical details of the Agent Trace spec: version number, date, who maintains it, license, the JSON schema fields (exact names), file location in repos, how it records conversations/ranges/contributors (human vs AI), and any mention of time or effort tracking. Quote exact field nam

> # Agent Trace: Technical Summary
> *(Summarized from the Agent Trace spec, licensed CC BY 4.0)*
>
> ## Basics
> - **Version:** `"0.1.0"`. Status is RFC, dated January 2026.
> - **Maintainer:** The page doesn't name one. It carries Cursor branding, and the repo and reference code are hosted at `"github.com/cursor/agent-trace"`, which accepts suggestions. Credited partners are Amp, Amplitude, Cline, Cloudflare, Cognition, git-ai, Jules, OpenCode, Tapes and Vercel.
> - **License:** CC BY 4.0.
> - **MIME type:** `"application/vnd.agent-trace.record+json"`.
>
> ## Schema
> It uses JSON Schema draft 2020-12, with `"$id"` set to `"https://agent-trace.dev/schemas/v1/trace-record.json"`.
>
> | Object | Required | Optional |
> |---|---|---|
> | Record (top level) | `"version"`, `"id"` (UUID), `"timestamp"` (date-time), `"files"` | `"vcs"`, `"tool"`, `"metadata"` (free-form) |
> | `"vcs"` | `"type"` (`"git"`, `"jj"`, `"hg"`, `"svn"`), `"revision"` | none |
> | `"tool"` | none | `"name"`, `"version"` |
> | file | `"path"` (relative to repo root), `"conversations"` | none |
> | conversation | `"ranges"` | `"url"` (URI), `"contributor"`, `"related"` |
> | `"related"` item | `"type"`, `"url"` | none |
> | range | `"start_line"`, `"end_line"` (integers ≥1) | `"content_hash"`, `"contributor"` |
> | contributor | `"type"` (`"human"`, `"ai"`, `"mixed"`, `"unknown"`) | `"model_id"` (≤250 chars) |
>
> ## Conversations, ranges and contributors
> - **Nesting:** The structure is record → `"files"` → `"conversations"` → `"ranges"`.
> - …

### https://www.infoq.com/news/2026/02/agent-trace-cursor/

Asked: Summarize: who proposed Agent Trace, when, which companies/tools support it, what it records, license, and criticisms. Quote exact numbers, dates and field names.

> **Agent Trace, as reported by InfoQ**
>
> - **Who/when:** Cursor released it at agent-trace.dev as a "draft open specification" and a "Request for Comments (RFC)." Robert Krzaczyński's InfoQ report is dated "Feb 04, 2026" ("2 min read"). The article doesn't give a separate release date, version number or adoption figures.
>
> - **Support:** The article names no company other than Cursor as a backer or adopter. Listed VCS support includes "Git, Jujutsu, and Mercurial." Cursor's "reference implementation" is "built around Cursor's own tooling," though its patterns are meant to be "reusable across other editors and agents."
>
> - **What it records:** It uses a JSON "trace record" designed to "connect code ranges to the conversations and contributors behind them."
>   - Attribution can be per line or per file and is grouped by conversation.
>   - Each contribution is "classified as human, AI, mixed, or unknown."
>   - It allows "optional model identifiers" for machine-written code.
>   - "Optional content hashes" keep attribution intact when code is relocated or refactored.
>   - Vendors can add extras through "namespaced keys."
>   - Storage location is left open: "files, git notes, database entries, or other mechanisms."
>
> - **Field names:** The article lists no actual JSON keys. The only named elements are the "trace record" itself and the four contribution categories.
>
> - **License:** The article doesn't mention one. It describes the spec as open and a "vendor-neutral format" but names no …

### https://wakatime.com/faq

Asked: Extract everything about: (1) how WakaTime calculates coding time from heartbeats (keystroke timeout, default minutes, how durations join), (2) heartbeat frequency (how often plugins send heartbeats, e.g., every 2 minutes), (3) AI coding tracking: how AI time is counted, "AI Coding" category, AI lin

> ## 1. How coding time is calculated
>
> - Heartbeats are "joined into durations based on your timeout preference" before appearing on the dashboard. The full algorithm is in a linked GitHub gist, not on this page.
> - The keystroke timeout is "the maximum time allowed between durations when calculating your total coding activity for a day." The page says to "Think of it like a granularity setting." It merges durations but can't add or remove real coding.
> - **Default: 15 minutes.** The page's example is 2 min of typing, then a 13 min pause, then 1 min of typing:
>   - 10 min timeout → 3 min
>   - 15 min timeout (default) → 16 min, because the gap is counted
>   - 30 min timeout → still 16 min, since the setting "only affects time between heartbeats not surrounding"
> - **In effect:** pauses within the timeout count as coding, longer pauses don't, and no time is added before or after a stretch of activity.
> - **AFK:** tracking stops once a typing pause exceeds the timeout. Reading or scrolling only registers if you move the cursor, change which file has focus, or click inside the file. This varies by editor.
> - **Precision:** "accurate to 1 microsecond (0.000001 seconds)," limited by your OS clock. On Windows it's "±16 milliseconds (0.016 seconds)."
> - **Contractors** can raise the timeout so billable time isn't lost.
> - **Which timeout applies where:**
>   - Public badges, profiles and leaderboards are fixed at 15 min.
>   - Profiles and leaderboards also use the "Last 7 Days from Yesterday" …

### https://code.claude.com/docs/en/monitoring-usage

Asked: List exactly: (1) every OpenTelemetry metric name with its unit and attributes (e.g., claude_code.session.count, claude_code.active_time.total, lines_of_code, etc.), (2) every event name (e.g., claude_code.user_prompt, claude_code.tool_result, claude_code.api_request, claude_code.tool_decision) with

> > **Note:** The provided page is cut off partway through the `tool_decision` event. The page names other events (`safety_event`, `subagent_completed`, `managed_settings_resolved`, `plugin_loaded`, `mcp_server_connection`), but their attribute lists aren't in the text you supplied.
>
> ## 1. Metrics
>
> "The Unit column shows the OpenTelemetry unit string attached to each metric; count metrics carry none." Every metric also carries all the standard attributes listed after this table.
>
> | Metric | Description (verbatim) | Unit | Additional attributes |
> |---|---|---|---|
> | `claude_code.session.count` | Count of sessions started | none | `start_type`: "One of `"fresh"`, `"resume"`, `"continue"`, or `"agents_view"`" |
> | `claude_code.lines_of_code.count` | Count of lines of code modified | none | `type` (`"added"`, `"removed"`), `model` |
> | `claude_code.pull_request.count` | Number of pull requests created | none | none |
> | `claude_code.commit.count` | Number of git commits created | none | none |
> | `claude_code.cost.usage` | Cost of the Claude Code session | USD | `model`, `query_source`, `speed`, `effort`, `agent.name`, `skill.name`, `plugin.name`, `marketplace.name`, `mcp_server.name`, `mcp_tool.name` |
> | `claude_code.token.usage` | Number of tokens used | tokens | `type` (`"input"`, `"output"`, `"cacheRead"`, `"cacheCreation"`) plus all the cost attributes |
> | `claude_code.code_edit_tool.decision` | Count of code editing tool permission decisions | none | `tool_name` (`"Edit"`, …

### https://platform.claude.com/docs/en/manage-claude/claude-code-analytics-api

Asked: Give the exact endpoint path, auth, granularity (daily?), and the full list of response fields (e.g., core_metrics.num_sessions, lines_of_code.added/removed, commits_by_claude_code, pull_requests_by_claude_code, tool_actions.edit_tool.accepted/rejected, model_breakdown, estimated_cost). Also data fr

> ---
> title: Claude Code Analytics API
> url: https://platform.claude.com/docs/en/manage-claude/claude-code-analytics-api
> description: Programmatically access your organization's Claude Code usage analytics and productivity metrics with the Claude Code Analytics Admin API.
> ---
>
> <Tip>
>   **The Admin API isn’t available to organizations on the claude.ai Team plan or to individual Claude Console accounts.** If your company also has a Claude Console organization, you can use the API with that organization; see [Create an Admin API key](https://platform.claude.com/docs/en/manage-claude/admin-api-keys). An individual Console account must first set up an organization on the [Organization](https://platform.claude.com/settings/organization) page (`platform.claude.com/settings/organization`) in the Claude Console.
> </Tip>
>
> The Claude Code Analytics Admin API provides programmatic access to daily aggregated usage metrics for Claude Code users, allowing organizations to analyze developer productivity and build custom dashboards. This API provides more detail than the basic [Analytics dashboard](https://platform.claude.com/claude-code) in the Claude Console, without the complexity of the OpenTelemetry integration.
>
> This API allows you to better monitor, analyze, and optimize your Claude Code adoption:
>
> * **Developer productivity analysis:** Track sessions, lines of code added/removed, commits, and pull requests created using Claude Code
> * **Tool usage metrics:** Monitor acceptance and …

### https://cursor.com/docs/account/teams/ai-code-tracking-api

Asked: Give exact endpoints, auth, plan requirement (Enterprise?), and the full field list for commits and changes (e.g., tabLinesAdded, composerLinesAdded, nonAiLinesAdded, totalLinesAdded, etc.). How does Cursor attribute lines to AI vs human (method)? Any limitations noted (e.g., only Cursor IDE, only g

> ## Endpoints (base URL in examples: `https://api.cursor.com`)
> - `GET /analytics/ai-code/commits`: commit-level attribution, JSON, paginated
> - `GET /analytics/ai-code/commits.csv`: the same data as a streamed CSV
> - `GET /analytics/ai-code/changes`: individual accepted AI edits keyed by `changeId`, JSON, paginated
> - `GET /analytics/ai-code/changes.csv`: the same data as a streamed CSV
> - `GET /analytics/ai-code/commits/:commitHash`: blame ranges plus linked conversation summaries. It accepts one hash or several separated by commas, with an optional `branch` query param. This endpoint is "in limited alpha and only available to select users."
>
> ## Auth and plan
> - **Auth:** HTTP Basic auth, identical to the Admin API. The API key goes in the username field and the password stays blank (`-u YOUR_API_KEY:`). Rate limits aren't listed on this page.
> - **Plan:** Yes, "Enterprise only," arranged through sales.
> - **Status:** Alpha, so "response shapes and fields may change"
>
> ## Query params and limits
> - `startDate`: ISO date, `now`, or a day offset like `7d`. Defaults to 7 days back.
> - `endDate`: ISO date, `now`, or `0d`. Defaults to now.
> - `page`: 1-based, starting at 1
> - `pageSize`: "Default: 100, Max: 1000"
> - `user`: email, `user_…` ID, or a number like `42`
> - The CSV sections document only `startDate`, `endDate` and `user`. The server streams CSV "in pages of 10,000 records."
>
> ## Commits fields
> **JSON** (wrapped in `items`, `totalCount`, `page`, `pageSize`; * = …

### https://docs.github.com/en/copilot/reference/copilot-usage-metrics/copilot-usage-metrics

Asked: List the exact metric field names available in the Copilot usage metrics (user-level and enterprise/org-level reports): e.g., daily_active_users, user_initiated_interaction_count, code_generation_activity_count, code_acceptance_activity_count, loc_suggested_to_add_sum, loc_added_sum, used_agent, use

> **Report names**
> - User-level: "\*-users-1-day", "\*-users-28-day"
> - Enterprise/org daily: "enterprise-1-day", "organization-1-day". 28-day: "enterprise-28-day", "organization-28-day"
> - Also "\*-user-teams-1-day" (team mapping) and "\*-repos-1-day" (repositories)
>
> **Identifiers:** "day", "enterprise_id", "organization_id" (org scope only). User-level 28-day rows also include "report_start_day" and "report_end_day".
>
> **User-level fields**
> - Identity and credits: "user_id", "user_login", "ai_credits_used" (for usage analysis, not invoices; no feature, model or surface split)
> - Activity: "user_initiated_interaction_count", "code_generation_activity_count", "code_acceptance_activity_count"
> - Lines: "loc_suggested_to_add_sum", "loc_suggested_to_delete_sum", "loc_added_sum", "loc_deleted_sum"
> - Flags: "used_agent", "used_chat", "used_cli", "used_copilot_app", "used_local_byok", "used_copilot_coding_agent", "used_copilot_cloud_agent" (same value as the coding-agent flag), "used_copilot_code_review_active", "used_copilot_code_review_passive", "used_vscode_agent"
> - Telemetry: "client_telemetry_present", "usage_metrics_requirements" ("met", "unmet", "unknown")
> - Adoption: "ai_adoption_phase" (defaults to "No Cohort")
> - Customizations: "distinct_skill_use_count", "distinct_custom_agent_use_count", "distinct_mcp_use_count", "distinct_slash_cmd_use_count", "distinct_plugin_use_count", "totals_by_skill", "totals_by_custom_agent", "totals_by_mcp", "totals_by_slash_cmd", …

### https://docs.github.com/en/rest/copilot/copilot-usage-metrics?apiVersion=2026-03-10

Asked: List all endpoint paths (GET ...) with a one-line description each, required permissions, and how reports are delivered (download links? NDJSON?). Note any retention period, and whether the legacy /orgs/{org}/copilot/metrics endpoint is deprecated or closed and when.

> ## Endpoints
>
> Every `*-1-day` path requires `?day=YYYY-MM-DD`.
>
> **Enterprise**
> - `GET /enterprises/{enterprise}/copilot/metrics/reports/enterprise-1-day`: enterprise-wide totals for a single date
> - `GET /enterprises/{enterprise}/copilot/metrics/reports/enterprise-28-day/latest`: enterprise-wide totals for the most recent 28-day window
> - `GET /enterprises/{enterprise}/copilot/metrics/reports/repos-1-day`: PR metrics per repo for one date, split by Coding Agent and Code Review. Repos with no activity are left out.
> - `GET /enterprises/{enterprise}/copilot/metrics/reports/user-teams-1-day`: user-to-team membership rows for one date, used to build team-level views from user reports
> - `GET /enterprises/{enterprise}/copilot/metrics/reports/users-1-day`: per-user engagement and adoption for one date
> - `GET /enterprises/{enterprise}/copilot/metrics/reports/users-28-day/latest`: per-user metrics over the latest 28 days
>
> **Organization**
> - `GET /orgs/{org}/copilot/metrics/reports/organization-1-day`: org-wide totals for one date
> - `GET /orgs/{org}/copilot/metrics/reports/organization-28-day/latest`: org-wide totals for the latest 28 days
> - `GET /orgs/{org}/copilot/metrics/reports/repos-1-day`: per-repo PR metrics within the org for one date
> - `GET /orgs/{org}/copilot/metrics/reports/user-teams-1-day`: org user-to-team rows for one date
> - `GET /orgs/{org}/copilot/metrics/reports/users-1-day`: per-user org metrics for one date
> - `GET …

### https://getdx.com/blog/ai-measurement-framework-guide/

Asked: Extract the DX AI Measurement Framework: its three dimensions (utilization, impact, cost) and the specific metrics listed under each (e.g., "time savings per developer", "% of PRs AI-assisted", "AI spend", "human-equivalent hours"). How do they collect data (self-reported surveys vs telemetry)? Any 

> # DX AI Measurement Framework
>
> **Source and date:** This is a DX blog post by Taylor Bruneaux (Analyst). It was last updated May 20, 2026, and no original publish date is shown. Most figures come from DX's Q1 2026 AI-assisted engineering report.
>
> ## Three dimensions
>
> DX links the dimensions to a maturity sequence: adoption first, then impact, then governance and spend efficiency. The framework is meant to supplement DX Core 4 ("speed, effectiveness, quality, and business impact"), not replace it: "AI metrics measure what's happening. Core 4 metrics confirm whether it's working."
>
> **Utilization**
> - "AI tool usage (DAUs/WAUs)"
> - "Percentage of PRs that are AI-assisted"
> - "Percentage of committed code that is AI-generated"
> - "Tasks assigned to agents"
> - Caveat: even top organizations reach only about 60% active use, and high uptake can still produce weak or harmful results.
>
> **Impact**
> - **Direct** (a quick read on a specific tool):
>   - "AI-driven time savings (developer hours/week)"
>   - "Developer satisfaction"
>   - "Human-equivalent hours (HEH) of work completed by agents"
> - **Indirect** (longer-term gains and less visible risks, found "through regression and longitudinal analysis of DX Core 4 measures"):
>   - "PR throughput"
>   - "Perceived rate of delivery"
>   - "Developer Experience Index (DXI)"
>   - "Code maintainability"
>   - "Change confidence"
>   - "Change fail percentage"
>
> **Cost**
> - "AI spend (both total and per developer)"
> - "Net time gain per developer" (hours saved minus …

### https://linearb.helpdocs.io/article/ww1ciql9uw-linear-b-ai-insights-faq

Asked: How does LinearB detect AI-generated or AI-assisted PRs (method: tool APIs, PR labels, commit trailers like Co-Authored-By, bot authors, heuristics)? What metrics does it compute? Quote the exact detection rules and any accuracy caveats.

> ## How detection works
>
> LinearB attributes AI work by "analyzing metadata signals in your connected Git repositories and supported AI-tool integrations." It lists four signals:
>
> 1. "Commit co-authors — Identifies AI agents listed in commit messages."
> 2. "AI comments — Detects comments generated by AI agents in pull requests."
> 3. "AI-authored PRs — Finds pull requests opened by known AI agents."
> 4. "AI-tool integrations — Uses supported provider APIs to collect adoption and usage data."
>
> How these map to the methods you asked about:
>
> - **Tool APIs:** Yes. The vendors named are "GitHub Copilot, Claude, and Codex."
> - **Commit trailers:** Yes. For Codex, the page suggests adding a rule to `~/.codex/AGENTS.md`: "Append exactly one Git trailer after a blank line: `Co-authored-by: Codex <codex@openai.com>`". LinearB "can use this commit metadata as a signal when identifying Codex-assisted activity."
> - **Bot authors:** Yes. PRs authored by a recognized AI agent count, and so do AI-written PR comments. A separate article on bot attribution is linked but wasn't provided.
> - **PR labels:** These aren't listed as an input. LinearB applies them itself: "LinearB labels PRs co-authored by AI to measure their effect on quality and throughput." You can also filter by label.
> - **Heuristics or code analysis:** None are mentioned. Detection runs "without indexing or storing your source code," and "LinearB does not transfer or store raw source-code content for AI detection." gitStream isn't …

### https://jellyfish.co/platform/jellyfish-ai-impact/

Asked: What does Jellyfish AI Impact measure (exact metric names), how does it collect data (which tool integrations: Copilot, Cursor, Claude Code, Gemini, Windsurf; git; Jira), and how does it attribute AI usage to PRs or work? Any numbers.

> **What it measures**
> - **Stated scope:** "adoption, usage, spend, and outcomes (speed, quality, productivity)"
> - **Named metrics:** "throughput, cycle time, code quality, and cost efficiency," plus "delivery speed" and "token usage"
> - **Modules:** "Adoption Insights," "Impact Insights," "AI Workflow Insights," "AI Token Spend Dashboard," "Multi-Tool Comparison," "Enablement Insights," "Auto Report Builder"
> - **Definitions:** The page gives no formulas or formal KPI definitions. It links a "7 AI KPIs" ebook but doesn't list the KPIs. Jellyfish positions its output as an alternative to "vendor-reported activity metrics."
>
> **How data is collected**
> According to the FAQ, you don't have to connect each assistant, because Jellyfish "derives AI signals directly from Git, planning systems, and workflow data." No labeling or manual configuration is needed.
>
> | Tool | Evidence on page |
> |---|---|
> | Copilot | GitHub Copilot logo and FAQ; FAQ also lists Copilot Agent |
> | Cursor | Logo and FAQ |
> | Claude Code | Named in FAQ; logo just reads "Claude" |
> | Gemini | "Google Gemini" logo; FAQ says "Gemini Code Assist" |
> | Windsurf | Logo and FAQ |
> | Git | Logo, plus GitHub, GitLab, Bitbucket, Azure Repos |
> | Jira | Logo, plus Linear, Azure Boards |
>
> - **Other logos:** Amazon Q, Sourcegraph, CodeRabbit, Greptile, SonarQube, Jenkins, CircleCI, PagerDuty, Opsgenie, Slack, Google Sheets, Google Calendar, Productboard, ProductPlan, Aha!, Azure, Azure DevOps.
> - **Agents:** The FAQ adds "Devin, …

### https://hack.club/ysws-rules

Asked: Extract the rules about: AI usage and self-declaration, how hours are counted/verified (Hackatime, timelapse, devlogs), what reviewers check, what counts as fraud, and weighted projects (how project weight is computed from hours, e.g., 10 hours = 1 weighted project). Quote exact text and numbers.

> REDIRECT DETECTED: The URL redirects to a location that was not fetched automatically.
>
>     Original URL: https://hack.club/ysws-rules
>     Redirect URL (from the server's Location header — server-supplied, not verified): https://hack-club-hq.gitbook.io/ysws-project-submission-guidelines/BLBRN8LIfoCZhFV6oMNR/
>     Status: 307 Temporary Redirect
>
>     To complete your request, I need to fetch content from the redirected URL. Please use WebFetch again with these parameters:
>     - url: "https://hack-club-hq.gitbook.io/ysws-project-submission-guidelines/BLBRN8LIfoCZhFV6oMNR/"
>     - prompt: "Extract the rules about: AI usage and self-declaration, how hours are counted/verified (Hackatime, timelapse, devlogs), what reviewers check, what counts as fraud, and weighted projects (how project weight is computed from hours, e.g., 10 hours = 1 weighted project). Quote exact text and numbers."

### https://hack-club-hq.gitbook.io/ysws-project-submission-guidelines/BLBRN8LIfoCZhFV6oMNR/

Asked: Extract the rules about: AI usage and self-declaration, how hours are counted/verified (Hackatime, timelapse, devlogs), what reviewers check, what counts as fraud, and weighted projects (how project weight is computed from hours, e.g., 10 hours = 1 weighted project). Quote exact text and numbers. Al

> REDIRECT DETECTED: The URL redirects to a location that was not fetched automatically.
>
>     Original URL: https://hack-club-hq.gitbook.io/ysws-project-submission-guidelines/BLBRN8LIfoCZhFV6oMNR
>     Redirect URL (from the server's Location header — server-supplied, not verified): https://hackclub.gitbook.io/ysws-project-submission-guidelines/BLBRN8LIfoCZhFV6oMNR/
>     Status: 307 Temporary Redirect
>
>     To complete your request, I need to fetch content from the redirected URL. Please use WebFetch again with these parameters:
>     - url: "https://hackclub.gitbook.io/ysws-project-submission-guidelines/BLBRN8LIfoCZhFV6oMNR/"
>     - prompt: "Extract the rules about: AI usage and self-declaration, how hours are counted/verified (Hackatime, timelapse, devlogs), what reviewers check, what counts as fraud, and weighted projects (how project weight is computed from hours, e.g., 10 hours = 1 weighted project). Quote exact text and numbers. Also list the page's subsections/links so I can fetch more."

### https://hackclub.gitbook.io/ysws-project-submission-guidelines/BLBRN8LIfoCZhFV6oMNR/

Asked: Extract the rules about: AI usage and self-declaration, how hours are counted/verified (Hackatime, timelapse, devlogs), what reviewers check, what counts as fraud, and weighted projects (how project weight is computed from hours, e.g., 10 hours = 1 weighted project). Quote exact text and numbers. Al

> **Short answer:** The text you provided is only the guidelines' intro page. It says **nothing** about AI use or self-declaration, Hackatime, timelapses, devlogs, what reviewers check, what counts as fraud, or project weighting. It also contains **no hour or weight numbers**, so I can't confirm the "10 hours = 1 weighted project" example from this source.
>
> **What the page does say**
> - **Required fields:** every YSWS submission "must include a complete set of required fields in its submission form."
> - **Scope:** "This document describes each field and the criteria it must satisfy for the project to be accepted." The per-field criteria aren't in this excerpt. That's probably where the reviewer checks are described.
> - **Exceptions:** cases outside the usual rules are covered on a separate "Project Exceptions" page.
> - **Fraud/integrity:** the only lead is an outside essay: "Read more about our Quality and Integrity processes here".
> - **Framing (@lfd, quoted from #zrl-land):** the rules collect existing practice for YSWS authors. They are "not a cage" and are "meant to evolve as Hack Club does." They'll be revised if a new program clashes with them.
> - "Last updated: March 2026"
>
> **Subsections:** The excerpt has no field-level headings. It contains only the title, the @lfd quote, the integrity link, and a GitBook "Agent Instructions" block.
>
> **Links to fetch next**
> 1. Index of all doc pages (best for finding pages on AI, hours, fraud and weighting): …

### https://news.hackclub.com/essays/quality-integrity-manager/

Asked: Summarize what this essay says about Quality and Integrity / Fraud Squad processes: how hours are reviewed, spot-check rates (e.g., percent of projects spot-checked), fines, deflation, weighted projects definition, and anything about AI. Quote exact numbers and the date.

> **Source:** "What a Quality and Integrity Manager Actually Does" by @lfd, published **April 17, 2026** (Essays). The author started the role about a month before publishing.
>
> **Weighted projects**
> - Defined as one entry in the shared database: "One ship, with hours attached, that has cleared the bar."
> - The author calls it "the closest thing Hack Club has to a common currency." They want an hour to carry identical value in every program, the way people accept cash without examining each bill. That would let brand-new and long-running programs be compared fairly.
>
> **How hours are reviewed**
> - Program reviewers check "whether they're real, hours logged match the project, and there's no fraud."
> - Approved work enters a single common database, with weighting tied to the builder's genuine time investment. Hack Club funds each program's author according to its weighted-project count.
> - Borderline calls used to depend on which reviewer you asked. That stopped working at the scale of "dozens of programs, hundreds of reviewers, and thousands of submissions."
> - The rules are now written at hackclub.com/ysws-rules, and every ship must satisfy them before entering the database.
> - Examples given:
>   - A polished game with "25 hours of work" seems legitimate.
>   - A half-built site with "40 hours logged" raises flags.
>   - The gray zone is work that's "decent but maybe not 20-hours-of-work decent."
> - The author has been auditing programs one at a time, contacting authors about problems and …

### https://www.fast.ai/posts/2026-01-28-dark-flow/

Asked: Summarize the post's definition of "dark flow" precisely: who wrote it, date, what behaviors characterize it (e.g., slot-machine-like re-prompting, losing track of time, not understanding code), any cited research (e.g., gambling "dark flow" from Dixon et al. on slot machines; METR results), and any

> **Source:** Rachel Thomas, "Breaking the Spell of Vibe Coding," fast.ai, January 28, 2026. The definition appears in the section "Not all Focus is Flow."
>
> **Definition (borrowed from others)**
> - Thomas doesn't define the term in her own words. She credits unnamed "Researchers on gambling addiction," who coined "dark flow" "to describe this insidious variation on true flow." She links a PMC article (PMC5846824).
> - She pairs it with Csikszentmihalyi's "junk flow," from a 2014 interview. He describes it as "becoming addicted to a superficial experience that may be flow at the beginning." Over time it becomes a compulsion pursued "instead of something that makes you grow."
> - She uses the two terms interchangeably ("“junk” (or “dark”) flow"). She calls it "a sinister under-current of the normally positive state of flow."
>
> **Genuine flow (the baseline)**
> Csikszentmihalyi introduced flow in the 1970s and described it in his 1990 bestseller. It requires abilities matched to the task, an activity with goals and rules, and feedback that "provides clear clues as to how well one is performing." Peak flow needs both skill and challenge to be high.
>
> **Distinguishing indicators** (vibe coding fails the same tests gambling does)
> 1. Feedback: it "does not provide clear clues of how well one is performing," and even produces "misleading losses disguised as wins."
> 2. Fit: "The match between challenge level and skill level is murky."
> 3. Control: "a false sense of control in which people think …

### https://metr.org/blog/2026-02-24-uplift-update/

Asked: Summarize precisely: what METR found in the late-2025 follow-up (sample size, speedup/slowdown estimates with confidence intervals), why they are changing the experiment design (selection effects, developers refusing to work without AI, unfinished tasks), and the new design. Quote key numbers and th

> **METR, "We are Changing our Developer Productivity Experiment Design" (February 24, 2026)**
>
> **Baseline:** METR's earlier paper (Feb–June 2025 data) estimated that AI increased task completion time by 19% (CI +2% to +39%). It was framed as roughly a 20% slowdown.
>
> **Late-2025 follow-up (launched August 2025)**
> - **Sample:** 57 veteran open-source developers with a median of a decade of experience. That's 10 returnees from the first study plus 47 newcomers from a broader mix of projects, including younger, smaller, greenfield-style ones. The study covered 143 repositories and more than 800 tasks. Developers chose tasks in advance, and each task was then randomly assigned to allow or bar AI. Pay was $50/hr, down from $150/hr.
> - **Raw estimates** (negative means faster with AI):
>   - Returning developers: **−18%** (CI −38% to +9%)
>   - New developers: **−4%** (CI −15% to +9%)
>   - Both intervals include zero.
> - **METR's interpretation:**
>   - Conversations with participants suggest developers get a bigger boost in early 2026 than METR estimated for early 2025.
>   - However, the data is "only very weak evidence for the size of this increase."
>   - The figures are likely "a lower-bound on the true productivity effects of AI on these developers," since those who opted out may gain far more.
>   - Overall, the data is "an unreliable signal of the current productivity effect of AI tools."
>
> **Why the design is changing**
> - **Developers refusing (main cause):** More people declined because …

### https://www.anthropic.com/research/AI-assistance-coding-skills

Asked: Extract exact details: date, authors, study design (participants, task, library e.g. Trio, groups), results (quiz score differences in %, speed differences, significance), and the interaction patterns identified (names of low-scoring vs high-scoring patterns, with descriptions and counts). Quote key

> **Date & authors**
> - Published Jan 29, 2026, by Anthropic's Societal Impacts team.
> - Led by Judy Hanwen Shen and Alex Tamkin, the two cited authors. The work was supported by the Anthropic Safety Fellows Program. Paper: "How AI Impacts Skill Formation" (arXiv 2601.20245).
>
> **Study design**
> - "a randomized controlled trial" with "52 (mostly junior) software engineers." All had used Python weekly for over a year, had some exposure to AI coding tools, and had never used Trio.
> - Library: Trio, which requires async-programming knowledge, "a skill often learned in a professional setting."
> - Sequence: warm-up (10 min) → build two Trio features (35 min) → quiz (25 min) → survey (5 min). The materials (problem statement, starter code, short concept primer) imitated a self-directed tutorial.
> - Groups: the treatment group had a sidebar AI assistant during the Trio task only. The control group never had AI. The assistant could see participants' code and "could at any time produce the correct code if asked."
> - Participants knew a quiz would follow but were urged to finish fast.
> - Quiz types: debugging, code reading, code writing, and conceptual. It emphasized debugging, reading, and conceptual items, which the authors saw as most relevant to supervising AI output.
> - Footnote: this setup isn't agentic like Claude Code. For agentic tools, the authors expect the effects "are likely to be more pronounced."
>
> **Results**
> - Core finding: "using AI assistance led to a statistically significant …

### https://www.anthropic.com/research/impact-software-development

Asked: Extract exact numbers: share of Claude Code conversations that are automation vs augmentation (79%?), comparison to Claude.ai (49%?), definitions of "feedback loop" vs "directive" automation patterns with percentages, startups vs enterprise share, languages, UI/UX tasks, date. Quote key sentences.

> # Anthropic Economic Index: AI's impact on software development
>
> ## Date and sample
> - **Published:** April 28, 2025. Conversations were sampled from April 6–13, 2025.
> - **Sample:** 500K coding conversations, divided 50/50 between Claude Code and Claude.ai Free/Pro. The Claude Code sessions came only through Anthropic's own API. Both platforms were weighted equally.
>
> ## Automation vs. augmentation (both of your figures are correct)
> - **Claude Code:** 79% automation, 21% augmentation. "79% of conversations on Claude Code were identified as 'automation'—where AI directly performs tasks"
> - **Claude.ai:** 49% automation. "only 49% of Claude.ai conversations were classified as automation." The article implies about 51% augmentation but never states it.
> - **Authors' conclusion:** "as more agentic AI products are built, we should expect more automation of tasks"
>
> ## Feedback Loop vs. Directive
> - **Feedback Loop:** "Claude completes tasks autonomously but with help of human validation." An example is the user passing error output to Claude to fix. The chart caption defines it as "Task completion guided by environmental feedback."
>   - Claude Code 35.8% vs. Claude.ai 21.3%, which the article calls "nearly twice as common."
> - **Directive:** "Claude completed a task with minimal user interaction." The caption says "Complete task delegation with minimal interaction."
>   - Claude Code 43.8% vs. Claude.ai 27.5%.
> - **Consistency check:** the two subtypes add up to 79.6% and 48.8%, roughly …

### https://www.anthropic.com/research/economic-index-march-2026-report?app=claude-code

Asked: Extract findings relevant to Claude Code and to learning/skill: what "learning curves" means here, how user experience/tenure affects success or automation vs augmentation, any Claude Code-specific statistics (autonomy, task length, directive vs collaborative share, human turns per task), and the da

> **Date & scope**
> - Published **March 24, 2026**. The sample covers Claude.ai and first-party (1P) API conversations from **Feb 5–12, 2026**. That window fell about three months after Opus 4.5 and overlapped the Opus 4.6 launch and Super Bowl ads that drew many new users. The main comparison is November 2025, with some trends going back to August 2025.
>
> **What "learning curves" means**
> - The question is whether people get more value from Claude as they gain experience, measured by tenure (time since signup). If good AI use depends on complementary skills "acquired through use and experimentation," early-adopter gains "may be self-reinforcing."
> - The finding: veterans "have developed habits and strategies that allow them to better harness Claude's capabilities." The authors call this consistent with learning-by-doing: "the more time one spends using AI, the more effective one becomes at harnessing it."
>
> **Tenure effects** (high tenure = account 6+ months old at sampling; reportedly robust to other cutoffs)
> - **Success:** high-tenure users have "a 10% higher success rate in their conversations." Regressions on Claude-judged binary success give:
>   - ~5 pp with no controls
>   - ~3 pp when comparing within the same O*NET task or request cluster
>   - ~4 pp with model, language, use-case and country controls
>   - The headline figure is in %, while the regressions are in percentage points.
> - **Augmentation vs. automation:** veterans iterate more and are "much less likely to delegate …

### https://www.anthropic.com/research/economic-index-june-2026-report

Asked: Extract findings relevant to Claude Code or coding: what "cadences" means (session timing, time of day, frequency, human turn cadence?), any statistics on how long agents run between human inputs, automation vs augmentation shares, and the date. Quote key numbers.

> **Date**
> - Published June 26, 2026. The artifact and autonomy analyses use chat and Cowork data from April 10 to June 10, 2026. Claude Code sessions from the same window are added when the two surfaces are compared.
> - The survey launched in April 2026. It is matched to usage from mid-May to early June for about 9,700 respondents.
>
> **What "cadences" means**
> - It refers to clock and calendar rhythms: weekday versus weekend, hour of day, and dated events like the US tax deadline. It does not cover session length or the spacing between prompts.
> - Continuous daily sampling replaced the "seven-day samples each previous Economic Index report drew on." The authors say "the cadences of daily life are etched into our usage logs."
> - In chat and Cowork, personal use rises from roughly 35% on weekdays to a bit below 50% on weekends. Claude Code and API traffic show the same swing, starting from smaller personal-use baselines.
> - On weekends, Claude Code's steepest drops are in "backend architecture, API debugging, and data storage." Its largest gains are in "AI agent design, quant trading, and gaming."
> - The hour-of-day chart covers only chat and Cowork, so there is no time-of-day profile for Claude Code.
> - Work done outside office hours leans toward better-paid jobs, even when computer and math tasks are excluded.
>
> **Time between human inputs**
> - The report gives no duration figures. It only says sessions "increasingly consist of long-running agentic tasks" and that models "can operate …

### https://www.anthropic.com/research/claude-code-expertise

Asked: Extract exact details: date, sample size (sessions), how expertise was measured, which behaviors distinguish expert vs novice Claude Code use (e.g., planning, interrupting, reviewing diffs, providing context, prompt length, turns per session, acceptance/rejection), success rates, and any metrics mea

> There's an issue with the selected model (claude-opus-5-5). It may not exist or you may not have access to it.

### https://www.anthropic.com/research/measuring-agent-autonomy?=

Asked: Extract exact details: date, data (Claude Code sessions?), how autonomy was measured (e.g., time between human interventions, auto-approve rates, interruptions), key statistics (e.g., median/99.9th percentile autonomous run length in minutes, share of sessions with auto-accept, how experienced users

> There's an issue with the selected model (claude-opus-5-5). It may not exist or you may not have access to it.

### https://www.anthropic.com/research/anthropic-education-report-how-university-students-use-claude

Asked: Extract: date, sample size, the four interaction patterns (Direct/Collaborative x Problem Solving/Output Creation) with percentages, Bloom's taxonomy distribution (Creating %, Analyzing %, etc.), and concerns about academic integrity / offloading. Quote key numbers.

> **Date**
> - Published "Apr 8, 2025"
>
> **Sample size**
> - The study involved "analyzing one million anonymized student conversations on Claude.ai." These came from Free and Pro accounts linked to university email domains (e.g., .edu, .ac.uk) over "an 18-day period."
> - Screening for school-related content "yielded 574,740 conversations."
> - The analysis used Anthropic's privacy-preserving Clio tool.
>
> **Four interaction patterns**
> - **Axes:**
>   - Direct means the user wants to "resolve their query as quickly as possible." Collaborative means the user "actively seeks to engage in dialogue with the model."
>   - Problem Solving means seeking "solutions or explanations to questions." Output Creation means seeking "longer outputs like presentations or essays."
> - **Shares:** Direct Problem Solving, Direct Output Creation, Collaborative Problem Solving and Collaborative Output Creation were "each between 23% and 29% of conversations."
> - **By mode:** "nearly half (~47%) of student-AI conversations were Direct." That implies about 53% Collaborative, which is my subtraction, not a stated figure.
> - **Missing data:** The text doesn't give exact figures for each pattern. They likely appear only in the chart.
> - **Subject example:** Education leaned toward Output Creation, "covering 74.4% of conversations." Teacher usage may have inflated this.
>
> **Bloom's taxonomy** (applied to Claude's outputs, not to students' thinking)
> - "Creating (39.8%) and Analyzing (30.2%) being the most common …

