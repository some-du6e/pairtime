# Project names wire format, version 1

Status: the contract between `pairtimed`'s project-name endpoint and the client's uploader (WP-DB1; D-owner-names, docs/errata/2026-11-22-owner-project-names.md). It builds on docs/spec/ingest-v1.md: the same token, device key, request counter, clock and rate. Changing it needs both sides.

The server never learns a project's name unless its owner chooses to show it. This endpoint carries that choice, project by project: a label the owner saw and approved, for one of the device's project keys, which the owner's own dashboard then shows in place of the project's short code (DESIGN 4.3, 4.4). Nothing else about a project travels here: no path, remote URL, branch or folder name the owner didn't approve.

## 1. `POST /api/v1/projects/names`

### 1.1 Headers

Every header of ingest-v1 2.1, with the same forms: `Authorization`, `Idempotency-Key`, `Content-Type: application/json`, `Content-Encoding: gzip`, `X-Pairtime-Device`, `X-Pairtime-Req`, `X-Pairtime-Time`, `X-Pairtime-Body-SHA256`, `X-Pairtime-Signature` and `User-Agent`.

`X-Pairtime-Req` is the device's one request counter, the one its ingest requests use: a names request takes the next value, and the server moves the same stored counter (ingest-v1 2.5 step 13). So the uploader keeps one counter on disk, and a names request replayed later is refused as `counter_stale`.

### 1.2 The signed message

```text
M_names = "pairtime-project-names-v1\n" || device_id || "\n" || req || "\n" || time || "\n" || hex(SHA-256(raw body))
```

It is ingest-v1 2.2's `M` with its own context string, so a names signature never passes for an upload, nor an upload's for a names request.

### 1.3 The body

One gzip member holding one JSON object with up to two members, each optional. An absent member, a `null` one and an empty one mean the same:

```json
{
  "set":    [{"project": "<key>", "label": "<label>"}, ...],
  "delete": ["<key>", ...]
}
```

| Member | Meaning |
|---|---|
| `set` | Names to show: each a project key of this device (ingest-v1 1's key) and its label. A key already named gets the new label |
| `delete` | Project keys whose names the owner turned off. A key with no name is no error |

A label is 1 to 64 Unicode code points of valid UTF-8, already in the form D-display-name's cleaning gives: no control character (Unicode Cc) and no formatting character (Cf), no space at either end, and no two spaces in a row. The client cleans a label before the owner sees it, so the label sent is the one the owner approved, and the server refuses a label that cleaning would change rather than store one the owner didn't see.

An object with neither member, `{}`, is a read: it changes nothing and its answer lists the device's names (1.6).

### 1.4 Limits

| What | Limit |
|---|---|
| Raw body | 16,384 bytes |
| Inflated body | 65,536 bytes |
| Items in `set` and `delete` together | 200 |
| Names the server holds for one device | 1,000 |

### 1.5 Checks, in order

Steps 1 to 12 of ingest-v1 2.5, with `M_names` in place of `M` in step 10 and the limits of 1.4 in steps 8 and 11, then:

1. The body's members: a member that isn't `set` or `delete`, of another JSON type, or there twice is `body_shape`; more than 200 items is `too_many_names`.
2. Each item: a `set` item that isn't an object with exactly `project` and `label` as strings is `body_shape`; a `project` or a `delete` entry that isn't a key is `project_invalid`; a key twice in the request, in either member, is `project_repeated`; a label that isn't one as 1.3 says is `label_invalid`.
3. In the writer's transaction: step 13 of ingest-v1 2.5, the account check and the counter, except that the server stores no response for a names request and answers no names request from the stored one. Then a `set` that would leave the device with more than 1,000 names is `too_many_names`.

Each refusal is ingest-v1 2.6's JSON body, with these new codes:

| Status | `error` | When | What the client does |
|---|---|---|---|
| 413 | `too_many_names` | More than 200 items, or more than 1,000 names for the device | Split the request; turn some names off |
| 422 | `project_invalid` | A project that isn't a key | Fix the client |
| 422 | `project_repeated` | A key twice in one request | Fix the client |
| 422 | `label_invalid` | A label that is empty, longer than 64 code points, not valid UTF-8, or not already clean | Fix the client, which cleans every label before the owner sees it |

### 1.6 Processing and the response

In one writer transaction, the server deletes each `delete` key's name, then stores each `set` item under the token's account, the device and the key, with the time it stored it, and moves the counter. A request is idempotent by its content: sending the same body again, signed again with a new counter, stores the same names. So the server keeps no reply for it, and the device's most recent ingest reply stays as it was (ingest-v1 2.10).

The response is 200:

```json
{"names": [{"project": "<key>", "label": "<label>"}, ...]}
```

`names` is every name the server holds for the device after the request, in key order, `[]` for none. The client reads it to learn what the owner's dashboard shows, which includes a name the owner hid there (2).

## 2. Where a name shows, and how it goes

A name shows only to its owner: on their own signed-in `/dashboard`, in place of the project's short code, with the code in a `title` attribute, and in `GET /api/v1/users/me/dashboard`, as the project's `name` beside its `code`. Never on a public page, in a program's read, in an export for anyone else, or on /demo.

It goes when:

- the device sends its key in `delete`, when the owner turns the name off on the computer;
- the owner presses "Hide this name" on the dashboard, which deletes the name of every device key of that project for the account at once. The website can hide a name but never set or change one;
- the device is removed and 90 days pass: the retention job deletes the names of a device removed 90 days ago, as D-project-map-lifetime deletes its other project rows. A device linked again before then keeps them (`server.project_names`, docs/retention.json);
- the account is deleted.

The client sends a name when the owner turns it on or changes its label, and a delete when the owner turns it off, and nothing on its own after that. When an answer's `names` lacks a name it sent, the owner hid it on the dashboard, and the client turns the name off in `config.toml`, so it stays hidden until the owner turns it on again on the computer.

## 3. Storage

For reviewers; the client never sees it. Migration `0008_project_names.sql` creates `project_names`:

| Column | What it holds |
|---|---|
| `device_id`, `project_key` | The key, the primary key |
| `user_id` | The token's account |
| `label` | The label, as 1.3 says |
| `set_at_ms` | When the server stored it |
| `device_removed_at_ms` | When the device was removed, `NULL` while it is linked; the retention job deletes a row 90 days after it |
