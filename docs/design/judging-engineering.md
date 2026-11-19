# Judging: engineering lens

Judge: the senior engineer who has to build pairtime with AI agents working in 2 to 4 hour sessions, with no local compiling (GitHub Actions only, minutes shared with another busy project), on a codebase teens will later contribute to.

Designs judged:

- [approach-adoption.md](approach-adoption.md) (adoption first)
- [approach-evidence.md](approach-evidence.md) (evidence first)
- [approach-teen.md](approach-teen.md) (teen first)

Shared inputs: [struggle-metric.md](struggle-metric.md), the research in [../research/](../research/), and [../BOUNTY.md](../BOUNTY.md).

Status: complete. Written 2026-11-19 from full reads of all three designs, checked against SM, CP, SP, HC and TP where the scores depend on them.

## Criteria

| Criterion | What scores high |
|---|---|
| Buildability and plan quality | Work packages fit a 2 to 4 hour agent session, come in a sensible order, and each has a check that runs in CI |
| Complexity against value | Every moving part pays for itself against the requirements |
| Old-laptop performance (R8) | Near-zero idle CPU and small memory on a 2016 Windows laptop, with the claim backed by numbers |
| Operating cost and simplicity (R11) | Cheap and boring to run on Karim's home server, and still under $500/month at Hack Club scale |
| Maintainability | Teens can find their way around and add things without breaking the core |
| Slip risk | The plan's critical path is short and its riskiest parts come first |

## Scores

Scores run from 1 to 10, higher is better. For slip risk, a higher score means less risk.

| Criterion | Adoption | Evidence | Teen |
|---|---|---|---|
| Buildability and plan quality | 7 | 5 | 7 |
| Complexity against value | 6 | 4 | 5 |
| Old-laptop performance (R8) | 6 | 7 | 7 |
| Operating cost and simplicity (R11) | 7 | 6 | 8 |
| Maintainability | 6 | 5 | 7 |
| Slip risk | 5 | 3 | 4 |
| **Overall** | **6.5** | **5** | **6** |

The three designs share about 90% of their architecture: a pure-Go client and server, a Svelte UI embedded in both binaries, SQLite in WAL mode with Litestream, `cloudflared` on Karim's domain, the SM rulebook in a shared `pkg/score`, and TP B6 commit-then-reveal. None of them breaks R11: all three price the same one-box deployment from SP 9.4 at $49 to $150 a month. What separates them is what each adds on top, how the work is cut into packages, and what sits on the critical path.

## Adoption first

### Buildability and plan quality: 7

What it gets right:

- 66 work packages with a written definition of done (section 10) and a check per package that runs in Actions. The milestones come in the right order for a product that has to be lived with before anyone pays for it: a local tracker (M0, M1), then the server (M2), then the program path (M3), then breadth (M4).
- The riskiest integration is tested first, against the real thing. WP 1.16 runs the real wakatime-cli v2.28.4 binary on three OS runners, including the case HC 1.5 doesn't document (the local agent is down), and blocks the release on failure. Neither other design tests the agent-down case: evidence WP2.8 and teen WP5.5 check only that a heartbeat reaches both destinations.
- The alpha can be downloaded from the first milestone. WP 1.18 adds a minimal `release.yml` with a SHA-256 manifest "so the alpha has something to download before WP 4.8", and local-only mode (section 10, milestone order) lets testers run the client before the server and an HCA client registration exist.
- WP 1.7 checks that 100 shuffled arrival orders inside the settling window give byte-identical buckets. That is exactly the property a scorer needs when the server must reproduce it.

What it gets wrong:

- Several packages are too big for one 2 to 4 hour session. WP 1.6 is normalization, MinHash, re-roll, same-error, autopilot, the half rate and its restore. WP 1.8 is the lock, spool, SQLite index, socket, HTTP server and scorer loop, tested on three OSes with a `kill -9` replay. WP 1.14 is hooks, a transcript reader and an OTLP receiver. WP 3.6 is five Hackatime-shaped endpoints, contract tests and a Ruby script. WP 4.8 is GoReleaser, a signed manifest, three package managers and self-update with the Windows rename swap. Teen cuts the same work into two to four packages each.
- The definition of done runs `go test ./...` on Linux, Windows and macOS for every package, pure-logic ones like `pkg/score` included. With CI shared with another busy project, the macOS queue sets the pace. Evidence's rule (Windows and macOS only for platform code) is the better default.
- WP 1.13 passes when "two console windows swap focus and both changes are seen" on `windows-latest`. Windows restricts which process may take the foreground, so a test that swaps real focus on a hosted runner is a flaky check an agent will lose a session to. Teen WP1.4 uses injected fakes and a golden classification table, and evidence WP2.3 uses Xvfb with `xdotool`. Both are deterministic. WP 1.11's "one focus event" on `macos-latest` has the same problem.
- The scorer's oracle is the design's own scenario harness (WP 0.5). Adoption was written while SM 6 to 8 were "still in progress" (its own source table), so it doesn't turn SM 6's worked examples into fixtures, and the agents writing the rules would also write the scenarios that check them.

### Complexity against value: 6

Most of the extra machinery is integration code that serves acceptance (A): the dual-send (4.5), the WakaTime bridge that turns transcript-derived heartbeats into weak prompt events (4.3, D2), the settling window (D1), handbook numbers computed exactly as Hackatime computes them (D4), the `hackatime_mismatch` cross-witness (6.1) and the Hackatime-shaped reads (4.7, WP 3.6). Each has a reason grounded in HC. The cost is coupling to two outside systems that change without notice: wakatime-cli's AI parsing and relabel rules (HC 2.2), and Hackatime's API, which is mid-migration (HCA-only sign-in, the ClickHouse PR; risk 13). D4 in particular reimplements Hackatime's boundary-aware duration path to compare with Hackatime's own number, which is a second scorer to keep in step with someone else's code.

It skips the heaviest items in the other two: no coach engine, no VS Code extension, no WSL mode, no consistency-check suite, and no keyboard listener before signing. It keeps two things that don't pay for themselves at R11 scale: the private detection module as a separate process (3.2), and the full test suite on Postgres from day one (section 9). All three designs inherit the second from SP 7.4.

### Old-laptop performance (R8): 6

The Go agent and the CP 7 targets are the same as in the other designs: a 15 MB binary, 25 MB RSS on Windows, 0.1% idle CPU, a 50 ms warm hook round trip. Adoption is the only design that keeps every keyboard listener out of the binary until it is signed (6.1 rank 5; WP 5.1 depends on 4.9), which is right for antivirus heuristics on an unsigned Go binary (HS 14). The hook latency guard comes in M1 (WP 1.9) and the RSS and CPU gate in M4 (WP 4.11), before launch.

Misses:

- Section 3.2 picks Go 1.27 for both binaries. CP 1.5 says Go 1.27 drops macOS 12 Monterey, which strands 2015 and 2016 Macs. Evidence and teen both keep darwin on Go 1.26 until February 2027.
- No EcoQoS on Windows and no low-priority lookback, although adoption runs a 30-day lookback during install (4.6, step 6) on what is often a spinning disk. Teen has both (2, R8; WP4.7).
- Presence is sampled every 15 s whether or not a project is open. Evidence's project-context gating (3.3, D10) is cheaper and records less.

### Operating cost and simplicity (R11): 7

One Go process with SQLite and Litestream behind `cloudflared`, and no heartbeats on the server because the dual-send goes to `127.0.0.1` (section 9). The restore drill (WP 2.10) and the load test (WP 4.12) run in Actions, not on the home server. The extra moving part is the private detection module: a second image, built from a private repository, talking over a local socket (3.2). On a home server that means a private registry or a local build, and one more thing to update and watch, for rules the design calls optional ("every requirement is met by the public build with the no-op module", 11.1 risk 15). Teen's D13, with thresholds as private configuration and the detector code public, does the same job inside one process.

### Maintainability: 6

The layout is clear, and the conformance kit (4.2, WP 4.7) gives contributors a way to test an adapter. Three things will cost teen contributors:

- The `Source` interface (4.2) gives every adapter its own `Run` and `Lookback` loops, so each adapter owns its I/O. Teen's `Parse` (teen 3.3) is a pure function over records that the agent's shared tailers and hook receiver deliver, which is easier to test and harder to get wrong.
- The WakaTime bridge's rules (4.3) encode wakatime-cli's relabel quirks. Changing them safely needs HC 2.2 first.
- The Hackatime API mirror is pinned to one Hackatime commit (WP 3.6) and has to follow Hackatime as it moves.

### Slip risk: 5

The timeline (8.5) puts M0 and M1, 26 packages, in the first ten days, and 59 packages by the week route A launches (12-14 to 12-20). That needs several agents in parallel from day two, all queueing on three-OS CI. The adoption steps are dated where they should be: the first Quality and Integrity conversation and the Windows signing application both happen in week one, and signing validation takes 1 to 30 business days (CP 4.3). The cut list (11.2) protects the program path. What makes this the least risky plan of the three is that nothing on its critical path to a program is new cryptography or a new protocol: commit-then-reveal is TP B6 as written, and the riskiest integration is tested first.

### Fatal flaws

None. The worst problem, oversized packages under a CI-heavy definition of done, is a planning fix.

### Best ideas to graft

- WP 1.16: the real-wakatime-cli e2e job, agent-down case included, as a release blocker (4.5; 11.1 risk 1).
- WP 1.18 and the local-only mode in section 10: a prerelease pipeline in M1, and a client that works before the server and the HCA registration exist.
- WP 1.7: the shuffled-arrival determinism test for minute buckets.
- 6.1 rank 5 and WP 5.1: no keyboard-listener code in any build until the binary is signed.
- 4.5: installer key validation with wakatime-cli's own regex, the `.+`, `^.*$`, `(?s).*` pattern choice, and byte-for-byte uninstall.

## Evidence first

### Buildability and plan quality: 5

What it gets right. It has the best test oracles of the three:

- WP0.3 turns SM 6's worked examples into fixtures with their exact totals (10,095 s, 265 s, 1,800 s, 810 s, 1,490 s, 0 s) before any scorer code exists, and WP1.4 to 1.6 must reproduce them minute by minute. The agent writing the scorer doesn't write its own oracle.
- WP1.1 checks the ledger against an independent, stdlib-only Python reference over 1,000 random batches.
- WP1.7's `score-regression` job fails when outputs change without a `pkg/score/VERSION` bump in the same PR.
- WP4.7 runs `pairtime verify` with networking disabled (`unshare -n`).
- The CI rule in section 10 runs Windows and macOS only for platform code.

What it gets wrong:

- Its packages are the worst sized of the three. WP2.2 is presence and power for Windows, macOS through purego, X11, Mutter and `ext-idle-notify-v1`, plus project-context gating: five backends and a gating rule in one session. WP2.3 is focus, process snapshots and hosted-session resolution on three OSes. WP4.4 is all eleven consistency checks, each with a red-team fixture and a clean one. WP4.8 is the handbook text, the Hackatime-shaped reads, and `PairtimeService` clients in Ruby and TypeScript whose test suites both run in CI. WP5.4 adds VSIX packaging and `@vscode/test-electron` to the release pipeline. "48 work packages, roughly 150 agent-hours" (section 10) undercounts. Cut to session size, this is 70 to 80 packages.
- Two packages are missing. Nothing builds `install.ps1` or `install.sh`, which the pitch relies on, and the first release pipeline is WP5.4 in M5 (Dec 17 to Jan 3), while 8.4 opens a closed alpha of about 20 teens on Dec 3. There is also no code-signing package, although the pitch promises "the signed binary" and section 9 prices signing.
- The order is wrong for R3 and R8. The local dashboard is WP5.1 in M5, after the review page. The footprint gate is WP5.5, the last package before M6, so an RSS or CPU regression introduced in M2 surfaces in late December.
- Section 10 tells an agent that finds a fixture disagreeing with SM's table to fix whichever is wrong "in the same PR, with a note in SM". That lets the agent edit the spec to pass its own test. Teen WP2.8 (record an erratum for a human to approve) is the safe version.

### Complexity against value: 4

This design carries the most machinery:

- a header and a nonce-protected body per event, in deterministic CBOR (6.2), and a signed receipt for every batch
- batches every 60 s instead of TP's 5 minutes (D2)
- per-token commitments on the agent's final message as well as the prompt (D3)
- keyed MinHash sketches, error HMACs and hunk triples committed in every body, with per-shingle HMACs revealed for challenged turns (6.2, CC8)
- a commit-then-reveal challenge seed (6.5) and a project-scoped reveal
- eleven consistency checks (6.4) and a private sentinel process (3.6)
- a verifier that compiles in every released scoring version (6.6)
- a third witness label with three new parameters (D6), held minutes (D7), reviewer restores (D8) and a labeling mode (WP4.10)

Some of it is cheap and worth having in any design: the receipts, integer-only scoring (11.1 risk 9), the Python vectors. Much of the rest gets built before anyone knows whether Fraud Squad will accept it, which is the design's own risk 1. Section 8.4 sends the evidence model to Quality and Integrity during the same two weeks that M0 to M2 are being built, so the answer arrives after the format is fixed.

Two of the extras cause engineering problems:

- Committing derived scorer inputs in each event body (6.2) couples `pkg/score` to `pkg/ledger`. A new content-derived detector needs a new committed field, so a scoring change becomes a ledger format change, and events recorded before it can't be re-scored under the new rule.
- The project-scoped reveal and exact re-scoring pull against each other. The header carries `seq`, `kind`, `project` and `session` but no time (6.2), and other projects' bodies stay hidden (6.5). SM 3.2 resolves a terminal with several tabs by "the session whose next human anchor in this window comes soonest", and SM 3.3 breaks ties and checks lookup brackets with other projects' anchors. Without those times the server can't reproduce attribution for two harness sessions in one terminal window, so CC6 fails and D7 holds honest minutes. WP4.2's test uses fixture 6.3, whose agents run in separate iTerm windows, so CI won't catch it. SM 6.3's own two-tab variant would.

### Old-laptop performance (R8): 7

Sampling presence only while a project context is live, and pausing after 10 quiet minutes (3.3, D10), is the cheapest idle profile of the three, and it records less. The hashing costs microseconds per event (6.2). darwin builds stay on Go 1.26 (3.9), and WP2.1 includes the named-pipe fallback that CP 6.3 suggests for Windows 10 builds where AF_UNIX misbehaves. Against that, footprint is measured last (WP5.5), and the vault stores every turn's final agent message (D3), which adds writes on a spinning disk. 3.3 also says sampling "pauses until the next input or hook event", but `GetLastInputInfo` is a poll, so "next input" has to mean a focus change or a hook, or the pause brings back a timer or a global input hook.

### Operating cost and simplicity (R11): 6

It is the same one-box deployment: about 11M ingest requests a month, each with one Ed25519 verification and one signed receipt (section 9), which a CCX13 handles, and a CI load test at 8x peak against a 2 vCPU, 8 GB container (WP3.8). For an operator it adds the private sentinel built from a private repository, the public `pairtime-witness` repository the server commits to every day (so the server holds a GitHub token with write access), and a rotation procedure for the receipt-signing key (11.1 risk 8).

### Maintainability: 5

The module boundaries are good (`pkg/ledger`, `pkg/check`, `pkg/score`, `pkg/redact`), and vectors plus a second implementation protect the hardest code. Against that:

- The verifier must compile in "every released scoring version" (6.6), so old rule code stays in the tree for as long as any bundle has to verify.
- The scorer-ledger coupling above makes rule changes expensive. A teen who wants to add a dark-flow detector has to change the committed event format.
- The stack spans Go, Svelte, Python, Ruby and TypeScript.

### Slip risk: 3

The critical path to the first program release (M4, by Dec 17) runs through the most novel code in any of the three: the ledger, the metadata and content reveal, the checks and the bundle. D7 makes exact re-scoring a hard requirement, so an attribution mismatch withholds hours instead of flagging them. Distribution, signing and the local dashboard all come after the program path, and the packages that carry the most risk are the ones that most need splitting.

### Fatal flaws

None that a rewrite of the plan couldn't fix. Two need design changes before building, though: the scorer-ledger coupling, and the re-scoring gap in the project-scoped reveal. I wouldn't start from this design.

### Best ideas to graft

- WP0.3: SM 6's worked examples as fixtures with exact totals, before any scorer code.
- WP1.7: the `score-regression` job with a required `VERSION` bump.
- WP1.1: an independent reference implementation for the chain and Merkle test vectors.
- 11.1 risk 9: integer-only `pkg/score`, with half-weight seconds kept as half-second units.
- Section 10: Windows and macOS runners only for platform code.
- 3.3 and D10: sample presence only while a project context is live.
- 6.2: a signed receipt for every batch, which makes witnessing two-sided for one signature per request.
- WP2.1: the named-pipe fallback, kept behind a test on Windows 10 1809 and 21H2 (CP 6.3).
- WP4.7: offline verification tested with networking disabled.

## Teen first

### Buildability and plan quality: 7

What it gets right:

- Section 10.1 has the best rules of the three for agent sessions: "A package that grows past that is split before it starts", fixtures instead of live services, and a plain statement of what CI can't measure, with real-laptop numbers coming from an opt-in `pairtime doctor --report` (WP7.9) that is tested against planted usernames and paths.
- Its packages are the best sized. Presence on Windows (WP1.2) is separate from macOS and Linux (1.3), focus is split per OS (1.4, 1.5), power stands alone (1.6), IPC (1.7) is apart from the agent skeleton (1.1) and the hook fast path (1.8), OTLP (5.4) is apart from the Claude Code adapter (1.9), and self-update (7.2) is apart from the release pipeline (7.1).
- WP2.8: when an SM example doesn't reproduce, "the PR records an erratum for a human to approve instead of tuning the rules".
- WP0.7 ships a release skeleton and both install scripts in M0, exercised on all three runners.
- WP0.6 adds a static check that every parameter `pkg/score` reads is in the public table.

What it gets wrong:

- It has the heaviest per-PR gates of the three (10.1): `go test -race` on three OS runners, golangci-lint, the cross-compile matrix, the license gate, the scorer corpus, and vitest plus Playwright for any `web/` change, with `macos-15-intel` also in the runner list. `-race` needs cgo and a C toolchain, so the build under test differs from the `CGO_ENABLED=0` build that ships, and race-enabled tests run several times slower. With CI shared with another busy project and about six agents pushing in parallel (10.2), the macOS queue decides the pace.
- Some acceptance criteria only check what the agent itself wrote: "Compiles; tests with fakes" (1.5), "Unit tests per anchor kind" (2.2), "Output validates against the schema; unit tests" (2.7). The SM 6 corpus arrives in WP2.8, at the end of M2, after the rules it checks. Evidence puts it first.
- R8 promises a warm hook round trip under 50 ms on a 2016 dual-core with a hard drive (section 2, R8), while WP1.8 gates at 80 ms on a 4-CPU runner with an SSD (10.1 gives the runner spec). The two numbers should be one, or the relation between them should be stated.

### Complexity against value: 5

This design has the largest product surface: a coach engine with recaps, twelve moment kinds, a logbook, habit trends, project stories, devlog drafts and prompt-kind rules (3.6; M4's 15 packages); notifications on three OSes; clubs and boards with shadowbans; build days, weekly goals and freezes; OG images and README badges; a VS Code extension with per-platform VSIX packages and editor change-shape events (D10, WP5.6); WSL mode (WP5.10); generic hook mode with TOML mappings (WP5.8); a Cline adapter; npm packages (WP7.1). That comes to 93 packages. The value is R3, which the bounty names and no tracker has solved, and the coach rules are pure functions over scorer output, which are cheap to test. The cut list (11.2) is honest and in the right order.

Where the other two are heavy, this one is the simplest: tier-2 thresholds are configuration read by the one public process instead of a private sidecar (D13), batch roots stay at TP's 5 minutes (6.1), and there is no Python reference and no Ruby client.

### Old-laptop performance (R8): 7

It pays the most attention to the old laptop: EcoQoS on Windows (section 2, R8); lookback at low CPU and I/O priority with a progress bar and a test that asserts the priority (WP4.7); the coach computed once at session end rather than resident (3.6); notifications as one short process per session end, never a polling loop (3.2); and Go 1.26 so macOS 12 keeps working (3.10).

Against that:

- The opt-in key counter (WP5.11) ships in M5, before Windows signing (WP7.3) in M7. A beta binary containing a raw-input keyboard listener would go out unsigned, which is what HS 14 and the design's own D3 warn about.
- The VS Code extension sends per-edit events to the agent (4.3, D10), and the design doesn't say how they are batched.
- The footprint gates are in M7 (WP7.6), last.

### Operating cost and simplicity (R11): 8

One process with everything in it (D13), plus the operational pieces an agent needs to run a home server: an uptime check, a scheduled restore drill, a nightly `VACUUM INTO` encrypted with `age` (3.8 Ops, WP7.8) and a runbook. The teen layer adds about 100 MB a month of devlogs and badges and no measurable server compute (9.2). OG images rendered daily are the only new server job, and they are on the cut list (11.2, item 7).

### Maintainability: 7

The adapter interface (3.3) is the best of the three. `Sources()` declares the hook events, log roots and routes, and `Parse` is a pure function pinned per harness version, so an adapter is a parser plus config edits and the agent owns all I/O. Coach rules make a natural first contribution for a teen, with a fixture test per moment kind (WP4.3). `-race` catches the agent's concurrency bugs. The cost is breadth: Go, Svelte, a TypeScript extension, PowerShell and sh, and a large surface for a small maintainer team.

### Slip risk: 4

93 packages, 42 of them (M0 to M3) scheduled for the first ten days (8.5). The program path is scheduled last: M6 (submissions, review, program APIs) runs "after M4" (10.2), although it needs only the scorer, the chain and the server, so the bounty's critical path waits on the coach. The dual-send that keeps a teen's Hackatime working is WP5.5, in M5, tested only on the happy path. Scoring 1.0.0 is fitted and frozen in one week (Dec 28 to Jan 3), and pairtime 1.0 and Hard Mode open the next day (8.5), so any slip in M4 moves every later date. The early alpha does buy something real: retention data arrives before the program path is built and before prizes distort it (10.4).

### Fatal flaws

None. Running M6 alongside M4 and trimming M4 and M5 are planning changes.

### Best ideas to graft

- 10.1: the package rules (split before starting, fixtures instead of live services, what CI can't measure), and M1's per-OS and per-concern splits.
- 3.3: the adapter interface with a declarative `Sources()` and a pure `Parse`.
- D13: tier-2 thresholds as private configuration inside the one public process, instead of a private sidecar.
- WP2.8: errata go to a human; never tune rules to pass a golden.
- WP0.6: the static check that every parameter `pkg/score` reads is in the table.
- Section 2 (R8) and WP4.7: EcoQoS on Windows, and low-priority lookback with a progress bar.
- WP7.9: `pairtime doctor --report` with a planted-PII test, as the source of real-laptop numbers.
- 3.8 Ops and WP7.8: the uptime check, restore drill and encrypted nightly snapshot as code.
- 10.4: an alpha with nothing worth cheating for, to measure retention first.
- 3.10: one Go 1.26 toolchain until February 2027, instead of a split.

## Recommendation

### Base: adoption

Any of the three could be built. I'd start from adoption, because nothing on its critical path to a program is new: commit-then-reveal is TP B6 as written, the credit rule is SM, and the integrations are tested against the real binaries and pinned sources they integrate with. It tests its biggest risk, breaking a teen's Hackatime, in M1 against the real wakatime-cli, including the case nobody has documented. It ships a downloadable alpha and a local-only mode before the server exists. Its extra machinery serves A, the acceptance criterion, and it is cuttable integration code, not a load-bearing data format.

Teen is a close second. Its planning discipline is the best of the three, and re-ordered it would make a good base, but its program path waits on the coach and its scope is 40% larger (93 packages against 66). Evidence's thesis lives in its ledger format, which is the hardest thing in any of the three designs to change after launch.

### Grafts

From teen:

1. 10.1's package rules, and adoption's oversized packages re-cut along teen's lines: adoption WP 1.6 into detectors and progress (teen WP2.5, 2.6); 1.8 into agent skeleton, spool and IPC (teen 1.1, 0.5, 1.7); 1.14 into the Claude Code adapter and the OTLP receiver (teen 1.9, 5.4); 4.8 into release and self-update (teen 7.1, 7.2); 3.6 into one package per endpoint group.
2. 3.3's adapter interface (`Sources()` plus a pure `Parse`) in place of adoption's `Source` with per-adapter `Run` and `Lookback` loops (adoption 4.2).
3. D13: tier-2 thresholds as private configuration, replacing adoption's private module process (adoption 3.2), which also removes adoption's AGPL open point (adoption 2, R1).
4. WP2.8: errata go to a human for approval.
5. WP0.6: the static check that every parameter `pkg/score` reads is in the table.
6. Section 2 (R8), WP4.7 and 3.10: EcoQoS on Windows, low-priority lookback with a progress bar, and Go 1.26 for both binaries until February 2027, replacing adoption's Go 1.27 (adoption 3.2).
7. WP7.9: `pairtime doctor --report` with its planted-PII test, as the R8 evidence adoption already relies on (adoption 10).
8. 3.8 Ops and WP7.8: the uptime check, restore drill and encrypted nightly snapshot as code.
9. A minimal coach on top of adoption's dashboard v0 (adoption WP 1.19): the welcome recap from lookback (teen WP4.7) and the session-end recap with one notification (teen WP4.2, 4.6). The rest of teen's M4 goes on the cut list.

From evidence:

10. WP0.3: SM 6's worked examples as fixtures with exact totals, as the first content of adoption's scenario harness (adoption WP 0.5), before any scorer package.
11. WP1.7: the `score-regression` job with a required version bump.
12. WP1.1: an independent reference implementation cross-checking adoption's chain package (adoption WP 0.3) on random batches, beyond the RFC 9162 vectors.
13. 11.1 risk 9: integer-only `pkg/score`.
14. Section 10's CI rule, Windows and macOS only for platform code, replacing adoption's three-OS definition of done.
15. 3.3 and D10: presence sampled only while a project context is live.
16. 6.2: a signed receipt for every batch.
17. WP4.7: offline verification tested with networking disabled.

### What the final design must fix

Whichever base is chosen:

1. **Packages sized to a session, each with an oracle the agent didn't write.** SM 6 fixtures for the scorer, a reference implementation for the chain, real binaries or pinned sources for integrations. "Unit tests" alone is not an acceptance criterion.
2. **Real harness fixtures.** All three designs test adapters against "golden fixtures", and none has a package that captures them. Fixtures an agent writes from documentation only test the agent's reading of the docs. Add one capture package per harness and version, run by a person or a machine with that harness installed, with a scrub step and a provenance note in each fixture directory, and mark synthesized fixtures as synthesized.
3. **A CI budget.** GitHub's free plan caps concurrent jobs per account (20, of which 5 can be macOS; this is general knowledge, not from the research), and this CI is shared with another busy project. Use path filters so Windows and macOS jobs run only when platform code changes, run `-race` on Linux only, run the Postgres suite nightly and on PRs that touch SQL instead of on every PR, add `concurrency` groups with `cancel-in-progress` so superseded pushes don't queue, and keep the cross-compile matrix in one job.
4. **Footprint gates from the first agent build.** Put the RSS, idle CPU, wakeup and binary-size job right after the agent skeleton, with every heavy dependency already imported (modernc SQLite, the embedded UI, the OTLP receiver, zstd), so every later package is measured. 15 MB is tight with all of them: decide early whether the OTLP receiver needs protobuf or JSON alone. Make the CI hook threshold and the R8 claim the same number, or state how they relate.
5. **The dual-send with the agent down.** Keep adoption's WP 1.16 and run it before any release that edits `~/.wakatime.cfg`. wakatime-cli requeues a heartbeat at most 3 times (HC 1.5), and what happens to Hackatime's copy when the local destination refuses connections isn't in the research (adoption 4.5).
6. **A scoring-version policy.** All three re-score "at the stated version", so the server and the verifier need the code of every version still referenced by an open submission or by a bundle inside its spot-check window. Make calibration refits parameter-only, so a new `scoring_version` is a new table rather than new code; version rule changes separately and keep them rare; and state how many rule versions the server carries. Don't compile every version into one verifier binary (evidence 6.6). Point `verify` at the release built from that version's tag.
7. **An exact list of what a submission reveals about other projects.** Exact re-scoring under SM 3.2 and 3.3 needs the times and anchor classes of other sessions that shared a window with the submitted project. Adoption reveals all event metadata in the window (3.6), which works. Evidence's header-only reveal breaks it (6.2, 6.5), and teen says both that the server re-scores from revealed metadata and that "other projects" are never in the submission evidence (3.9). Pick the field list, and test it with SM 6.3's two-tab variant.
8. **`go build` and `go test` without Node.** Every design embeds the Svelte build in both binaries. Check in a stub or use a build tag, so Go-only packages and Go-only CI jobs don't need the web toolchain.
9. **A simpler ingest signature.** RFC 9421 with `Content-Digest` (SP 8.2, adopted by all three) is a general HTTP signing scheme, and pairtime owns both ends of this protocol. Signing a fixed envelope (device id, sequence number, body hash, timestamp) with the device's Ed25519 key gives the same protection against a stolen token with much less code to get wrong.
10. **Distribution and identity on the calendar.** Apply for Windows signing and register the HCA OAuth client in week one. Ship no keyboard-listener code before the binary is signed (teen WP5.11 does). Have a prerelease pipeline and both install scripts before the first alpha (evidence has neither).
11. **No flaky platform checks.** Replace real focus switching on hosted Windows and macOS runners (adoption WP 1.11, 1.13) with injected fakes there, and use Xvfb with `xdotool` on Linux for the end-to-end case.
