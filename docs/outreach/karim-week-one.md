# Karim's week one: 2026-11-20 to 11-27

Status: draft for Karim, not sent

Everything here needs you, or you with HQ. No agent can do it for you, and nothing in this folder has been sent. Deadlines come from DESIGN 9.6 and 9.7, or from PLAN.md's "Work that needs a human" table where the design gives none. Times are rough guesses. Every HQ-side step takes longer than code, so the long-lead items go first (DESIGN 9.6).

## Do these first

1. Create the two repo labels and approve the repo rules. WP-CI1 needs them on 11-20.
2. Pick the hostname. The HCA client, the tunnel, the review URLs and every justification line need it.
3. Apply for Azure Artifact Signing under Hack Club. Identity validation alone can take 30 business days.
4. Send the Quality and Integrity brief and the native-program proposal.
5. Start the controller conversation with HQ. Sign-ups and calibration uploads wait on it.

## Send

| Item | Why | Who | Time | Deadline |
|---|---|---|---|---|
| `qi-brief.md` | The counting rule decides whether pairtime hours count at all. The level-B answer decides whether most Windows hours can beat the one-third rule (DESIGN 11 risks 2 and 3) | You, to Quality and Integrity | 30 min to fill the TODOs | Send by 11-27 (DESIGN 9.6). Agreed or escalated by 12-11 (DESIGN 9.7) |
| `native-program-proposal.md` | Routes A and D carry 500 of the 1,200 planned weighted projects (DESIGN 9.1, 11 risk 4) | You, to an HQ sponsor, copying Q&I | 45 min, mostly finding the sponsor | Send by 11-27. Funded by 12-07, or the fallback starts (DESIGN 9.4) |
| `telescreen-and-ari.md` | Robert can't see pairtime-only users, and one Ari change reaches four programs (DESIGN 5.10, 11 risk 8) | You, to each team | 20 min | Send by 11-27 (DESIGN 9.6). Their answers can wait for February (DESIGN 9.7) |
| `alpha-testers-call.md` | Testers should be in daily use when the shadow week starts on 12-01. Labeled sessions start 12-07. The 1.0 gate needs reports from five pre-2017 Windows laptops (DESIGN 6.7, 9.7) | You | 30 min to post, then replies all week. The parental-consent and controller TODOs need HQ first | Post by 11-24 if you can, 11-27 at the latest (DESIGN 9.6). Testers installed by 12-01 (PLAN) |

## Accounts

| Item | Why | Who | Time | Deadline |
|---|---|---|---|---|
| Azure Artifact Signing under Hack Club's organization | Unsigned Windows builds start at zero SmartScreen reputation, Smart App Control on Windows 11 can block them, and the typing module ships only in signed builds (DESIGN 7.2, 11 risk 7) | You with HQ. The plan puts the account under Hack Club's organization, which HQ has to agree to (DESIGN 9.6, 13.2 item 10) | An hour to apply. Identity validation takes 1 to 30 business days, and billing of about $9.99 a month starts when the account is created (CP 4.3) | Apply by 11-27 (DESIGN 9.6). Signing lands in M4, 12-14 to 12-23 (DESIGN 9.7). SignPath Foundation is the free fallback (DESIGN 10.6) |
| Apple Developer membership | Developer ID signing and notarization on macOS, and the Declared Age Range test (DESIGN 8.2) | You, or HQ if Hack Club holds it (DESIGN 13.2 item 10). [TODO: check whether Apple requires an adult account holder] | [TODO: Apple's enrollment time]. $99 a year | Apply by 11-27 (DESIGN 9.6). The age-signal test needs it before 12-31 (PLAN) |
| HCA OAuth client | Sign-in for the server and for devices. The redirect URI is `https://<host>/auth/hca/callback`, and a community app gets every scope pairtime needs (DESIGN 10.5; SP 3.1) | You, on HCA's Developer Apps page | 15 min, once the hostname is picked | Week one (DESIGN 9.6). Needed before sign-ups, target 12-08 (DESIGN 9.7) |
| Privacy-hub audience | So Hack Club's privacy hub can delete or export a pairtime account the way it does a Hackatime one (DESIGN 8.9) | You ask HQ to register pairtime's own `aud` (DESIGN 13.2 item 9) | One message. HQ's side is unknown | Ask by 11-27. Needed before sign-ups, target 12-08 |

## The controller

| Item | Why | Who | Time | Deadline |
|---|---|---|---|---|
| Name the controller: Hack Club HQ with you operating the server, or you | Until a privacy notice at `https://<host>/privacy` names the controller, the server takes no sign-ups and stores no minor's data. The alpha stays local-only, and calibration sessions can't be uploaded (DESIGN 8.6, 11 risk 10) | You and HQ, with counsel (DESIGN 13.1 item 1, 13.2 item 6) | One conversation to start, then the notice text | Start by 11-27. Named before sign-ups open, target 12-08, and before the first calibration session is sent (DESIGN 6.7, 9.7) |

## Repo and deploy

| Item | Why | Who | Time | Deadline |
|---|---|---|---|---|
| Labels `oracle-change` and `erratum`, plus approval of the repo rules in DESIGN 13.1 item 9 | CI's `oracle-guard` step fails a PR that changes an existing oracle file along with the code it checks, unless you add `oracle-change`. A PR that finds an erratum adds `erratum` and stops for your call (PLAN ground rules 5 and 6) | You | 5 min, commands below | 11-20. WP-CI1 waits on it (PLAN) |
| Release signing key | The binary refuses an update manifest without a valid Ed25519 signature. Until the key exists, tags are signed with a throwaway key and marked unsigned (PLAN WP-RL1) | You | 15 min once WP-RL1 merges, following Making the key in `docs/spec/update-manifest.md`: limit the `release` environment to `v*` tags, run `tools/release keygen` into that environment's `RELEASE_SIGNING_KEY` secret (an environment secret, not a repo secret, which every branch's workflows could read), commit the public half | Before the first tag, about 11-30 (PLAN) |
| The hostname `<host>` | It goes into the HCA redirect URI, the install script URL, every review URL and every justification line (DESIGN 13.1 item 4) | You | 5 min to decide | Now. Three other items wait on it |
| Cloudflare tunnel | Gives the home server HTTPS with no open ports, at no cost. Point the public hostname at `http://pairtimed:8080` and keep the token for the deploy (DESIGN 10.1, 10.5) | You | 20 min | Before the deploy, about 12-09 (PLAN) |
| The `pairtime-witness` repo and its token | The server commits a signed root there every day, so anyone can check it never re-dated a batch (DESIGN 7.4). The token gets contents write on that repo only. Make a read-only token for forge checks at the same time (DESIGN 10.5) | You. [TODO: owner, such as some-du6e] | 10 min | Before the deploy, about 12-09 (PLAN) |
| Two buckets, the offline `age` key pair, two Healthchecks.io checks, and the host | Replication and the evidence store, encrypted nightly snapshots, key backups, and an alert when the server stops (DESIGN 10.3 to 10.5) | You | About an hour | Before the deploy, about 12-09 (PLAN) |

```sh
gh label create oracle-change --repo some-du6e/pairtime --description "Karim approved a change to an oracle file"
gh label create erratum --repo some-du6e/pairtime --description "A spec example doesn't reproduce; needs Karim's call"
```

## Decisions from DESIGN 13.1

The build follows the default until you decide, so none of these blocks code.

| # | Decision | Default until then | Decide by |
|---|---|---|---|
| 1 | The controller | Sign-ups closed, alpha local-only | See above |
| 2 | Where your AGENTS.md entry is installed | Every detected harness, beside its hooks | Before the entry ships, about 12-14 (DESIGN 9.7) |
| 3 | What the entry passes | The user's message with personal information removed, at most 400 characters | Same as 2 |
| 4 | The hostname and the box | Your home server for the pilot, a VPS before the first payout | Hostname now, VPS date in January (DESIGN 10.5; PLAN) |
| 5 | A private sentinel for detectors beyond SM 8.7 | Off. Only a stub ships | No deadline. Nothing depends on it |
| 6 | Whether your team runs Hard Mode, its name and dates, and who recruits outside reviewers | As in the proposal | 11-27, with the proposal |
| 7 | Whether to compete with grit only, or stay open to working together | Compete, with no margin for a slip (DESIGN 9.1, 9.7) | The design sets no date. Before route-B outreach, 11-30 to 12-11, is sensible |
| 8 | Go after 1.26 leaves support | Stay on 1.26 so macOS 12 Macs keep working | Before February 2027 |
| 9 | Repo rules: the CI layout beyond `ci.yml`, the nightly `-race` exception, errata approval | As PLAN.md writes them | 11-20, with the labels |
| 10 | Remote and WSL setups | WSL and the server-side WakaTime endpoint in v1.5, SSH left open | Not this week |
| 11 | Sign-up without HCA | HCA only | Before sign-ups, target 12-08 |
| 12 | Accounts only you can create | The tables above | This week |

## HQ's decisions from DESIGN 13.2, and what carries each ask

| # | Ask | Carried by |
|---|---|---|
| 1 to 4, 7 | The counting rule and a weekly count, two trackers and one number, significant evidence, the spot-check window, interviews with minors | `qi-brief.md`, questions 1 to 4 and 7 |
| 5 | A sponsor, the budget and the real grant per weighted project, Q&I observing audits, a higher spot-check rate | `native-program-proposal.md`, and brief question 5 |
| 6 | Counsel on the controller, the public-profile assessment (DESIGN 8.3), the "degrading" question (DESIGN 7.9), fitting detectors on minors' outcomes, HCA birthdates against the OS age signal, and India | [TODO: a separate message; who at HQ handles counsel?] |
| 8 | HCA: official app status or the reviewer allowlist, how under-13 identities are marked, an eligibility end date | Send with the OAuth client registration |
| 9, 10 | The privacy-hub `aud`, and who holds the signing accounts | The accounts table above |
| 11 | Robert's `kind: "pairtime"` and Ari's `pairtime` source | `telescreen-and-ari.md` |
| 12, 13 | Penalties and how long the fraud record is kept, and staffing the published audit rate | Penalties and audit staffing can wait until December, but need answers before decisions open on 2027-01-04. The fraud-record period goes in the privacy notice, so ask for it with the controller (DESIGN 4.4, 8.6) |
| 14 | Stardance's review data under a data-sharing agreement, for calibration | Ask this week if you can. The fit runs 12-28 to 2027-01-03 (DESIGN 6.7) |
| 15 | Confirm that no program may require the typing module | A rule in the proposal. Ask Q&I to confirm it |
| 16 | An "also install pairtime" step in hackatime-setup | Later |

Not on this list because someone else can do it: the Claude Code fixture captures on Windows and on macOS or Linux, due about 11-23 (PLAN WP-AD1).
