# Notes to the Telescreen team and Ari's maintainers

Status: draft for Karim, not sent

Two separate messages. Example values in the JSON are made up.

## 1. To the Telescreen team: a `kind: "pairtime"` for Robert

Hi [TODO: name],

I lead pairtime, an AGPL-3.0 AI time tracker we're building for the AI time tracking bounty. So far it's a design and a code skeleton, and no program uses it yet. Programs that adopt it will still need Robert's fraud verdict, so I'd like to ask about one addition.

For v1 nothing in Robert has to change. A program that adopts pairtime keeps sending `kind: "hackatime"` with the same Hackatime names, because pairtime runs beside Hackatime and the teen's editor keeps reporting to both. Robert's `creditedSeconds` stays your figure for fraud scoring (DESIGN 5.10).

The gap is a teen who uses pairtime without Hackatime. Robert can't see them. We'd like to propose a new kind:

```http
POST /api/v1/ysws/projects

{
  "kind": "pairtime",
  "pairtimeSubmissionId": "sub_01HZX3K9",
  "name": "remind-bot",
  "codeUrl": "https://github.com/maya/remind-bot",
  "submitter": { "slackId": "U0123ABCD" },
  "externalId": "hardmode-42"
}
```

Only the `pairtime` kind and `pairtimeSubmissionId` are new. With the id, Robert fetches the signed bundle from `https://[TODO: host]/api/v1/submissions/<id>/bundle`, and the workbench links to the review page at `https://[TODO: host]/review/submissions/<id>` (DESIGN 7.7, 7.8).

As designed, the bundle is a tar file. Its manifest is signed with the server's Ed25519 key, published at `/.well-known/pairtime-keys.json`. It carries the witnessed batches with their receipts, the minute buckets next to the server's re-score, commits with three clocks, the consistency checks and the reviewer's cuts. It holds no prompt text, and `pairtime verify` checks all of it offline. Our webhooks will be signed the same way Robert's are, with `X-Pairtime-Signature`.

Questions:

1. Would you take this shape, or would you rather it looked different?
2. How should Robert authenticate to fetch bundles? We'd issue a read-only credential scoped to bundles unless you prefer another way.
3. For a pairtime project, should `creditedSeconds` come from the signed manifest, or stay your own figure?
4. Should `hackatimeProjects` still be allowed with this kind, for teens who use both trackers?
5. Could we read Robert's API docs? They're behind Hack Club Auth, and we'd rather fit what's there than guess.

Our planned winter program requires a Hackatime link, so this isn't blocking. If you're open to it, we'd aim for February (DESIGN 9.4, 9.7). Separately, that program will need a Robert event key. Who should we ask?

[TODO: sign-off]

## 2. To Ari's maintainers: a `pairtime` time source

Hi [TODO: name],

I lead pairtime, an AGPL-3.0 AI time tracker we're building. So far it's a design and a code skeleton, and no program uses it yet. Ari settles hours row by row and only lets a reviewer cut, which is the shape pairtime's rows are designed for.

With no change to Ari, a program could send pairtime time as one `journals[]` entry per work session (DESIGN 5.10):

```json
{
  "journals": [
    {
      "at": "2026-12-20T18:00:00Z",
      "seconds": 21240,
      "text": "Tracked by pairtime: 21,240 credited seconds in a work session from 2026-12-20T11:30:00Z to 2026-12-20T18:00:00Z.",
      "markdown": "Tracked by pairtime: **5 h 54 m** credited in this work session. Evidence: https://[TODO: host]/review/submissions/sub_01HZX3K9/rows/r_1"
    }
  ]
}
```

It goes in `journals[]`, not `hackatime_projects[]`, so the same time isn't paid twice, and not `program_seconds`, which Ari treats as evidence-free.

What we'd like to propose is a `pairtime` time source in `src/lib/review/settlement.ts`, fed by a `pairtime_submissions` field in ari-webhooks' `internal/ingest/payload.go` (DESIGN 5.10; EV 8.2). The rows would be:

- One row per work session inside each devlog window, with its captured seconds and a link to its evidence. Reviewers cut them the way they cut Hackatime rows (DESIGN 6.2 D13).
- Loop time as separate rows at 0. A reviewer may raise one up to its captured seconds and never past, so `clampSeconds` still holds (DESIGN 6.2 D12).
- No one-third `ai coding` discount on top, since pairtime's figure already leaves out agent-only time. That depends on Quality and Integrity agreeing, and we're asking them (DESIGN 9.2).

Questions:

1. Is this a change you'd consider?
2. Ari's repo has no license in its metadata. On what terms would you take a contribution? We'd write it fresh and copy nothing.
3. Would you rather Ari pull the rows from pairtime's API, or receive them in the ingest payload?

We'd send a written mapping with tests by [TODO: date; DESIGN 9.7 puts it in M5, by 2027-01-17]. Whether it goes in is your call.

[TODO: sign-off]
