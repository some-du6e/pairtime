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
| `set` | Names to show: each a project key of this device (ingest-v1 1's key) and its label. A key already named, or hidden on the dashboard, gets the new label |
| `delete` | Project keys whose names the owner turned off, or whose hiding on the dashboard the device has seen (2). A key with no name is no error |

A label is valid UTF-8, not empty, and already in the form D-display-name's cleaning gives, which `pkg/projectname`'s `CleanLabel` does on both sides: each run of spaces, line breaks and control characters becomes one space, the ends are trimmed, formatting characters other than the zero-width joiner and non-joiner are dropped, and at most 64 code points stay. So a label has no control character, no formatting character but those two, no space at either end, no two whitespace characters in a row, and at most 64 code points (`ValidLabel`). The client cleans a label before the owner sees it, so the label sent is the one the owner approved, and the server refuses a label that cleaning would change rather than store one the owner didn't see.

An object with neither member, `{}`, is a read: it changes nothing and its answer lists the device's names (1.6).

### 1.4 Limits

| What | Limit |
|---|---|
| Raw body, and inflated body | ingest-v1 2.4's: 262,144 and 4,194,304 bytes |
| Items in `set` and `delete` together | 200 |
| Names the server holds for one device, hidden ones included | 1,000 |

### 1.5 Checks, in order

Steps 1 to 12 of ingest-v1 2.5, with `M_names` in place of `M` in step 10, then:

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

The endpoint never answers 404. A 404 comes from a server without it, as an older `pairtimed`, whose router has no such path, or from a proxy that doesn't route it, and it carries no refusal to act on (ingest-v1 5 item 5). The client tells doctor nothing, holds back no upload, and sends nothing for a day while what `config.toml` asks it to send stays as it was. Then, or at once when that changes, it sends again, a read first while it doesn't know what the server holds (section 2), so such a server hears from it once a day and at each change.

### 1.6 Processing and the response

In one writer transaction, the server deletes each `delete` key's name and hide, then stores each `set` item under the token's account, the device and the key, with the time it stored it, dropping the key's hide, and moves the counter. A request is idempotent by its content: sending the same body again, signed again with a new counter, stores the same names. So the server keeps no reply for it, and the device's most recent ingest reply stays as it was (ingest-v1 2.10).

The response is 200:

```json
{"names": [{"project": "<key>", "label": "<label>"}, ...], "hidden": ["<key>", ...], "codes": {"<key>": "<code>", ...}}
```

`names` is every name the server holds for the device after the request, in key order, `[]` for none. `hidden` is the keys of the device's names the owner hid on the dashboard, whose labels the server deleted, in key order, `[]` for none: the server keeps each key until the device sends it in `delete`, 30 days at most (2). The client reads both to learn what the owner's dashboard shows.

`codes` gives each project key the device uploaded minutes of the short code the owner's dashboard calls its project by, such as `"7c41e9"`, `{}` for none. Codes are worked out over every key the account's devices uploaded (`pkg/projectname.ProjectCodes`), and a code gets longer when another device's key starts the same way, so a device can't work one out from its own keys. It shows the codes this answer gives, and none for a key the server has no minutes of yet. A `{}` read every day keeps them current.

## 2. Where a name shows, and how it goes

A name shows only to its owner: on their own signed-in `/dashboard`, in place of the project's short code, with the code's name ("Project 7c41e9") in a `title` attribute, and in `GET /api/v1/users/me/dashboard`, as the project's `name` beside its `code`, with `"named": true`. It shows only on the project its device's key maps to, so a name a device sent for a key it never uploaded minutes of shows nowhere. A project with names on several of its device keys shows the first key's, in device and key order. Never on a public page, in a program's read, in an export for anyone else, or on /demo.

It goes when:

- the device sends its key in `delete`, when the owner turns the name off on the computer;
- the owner presses "Hide this name" on the dashboard, a form that posts the project's id to `POST /dashboard/projects/hide` from the dashboard itself (the zone form's same-origin rule). The server deletes the name on every device key of that project for the account at once, label and all. For a linked device it keeps a hide, the device and project key and the time, which its answers list in `hidden` until the device sends the key in `delete`, or 30 days after the hide, whichever comes first (`server.project_name_hides`, docs/retention.json). A removed device's name leaves no hide, since that device can't ask until it is linked again. The website can hide a name but never set or change one;
- the device is removed and 90 days pass: the retention job deletes the names of a device removed 90 days ago, as the server's rule for a removed device's own row has it (DESIGN 4.4's `device_id` row, `server.devices`). A device linked again before then keeps them (`server.project_names`, docs/retention.json);
- the account is deleted, names and hides alike.

`pairtime privacy delete` ("delete everything", DESIGN 8.8) takes the names of the projects this computer tracked out of `config.toml`, leaves the tables of keys it never had, deletes `project-names.json` and the sign-in, and sends nothing: a name the server shows for that device stays until the owner hides it on the dashboard, or until the device's purge 90 days after it is removed.

The computer never sends a name on its own (D-owner-names). It sends a set only when the owner turns a name on or changes its label, and a delete only when they turn it off, or once it turned off a name the server lists as hidden. It sends a name only for a key of its own projects, never one it didn't produce, such as a key in a `config.toml` copied from another computer. A read, `{}`, which carries no name, goes when the agent starts and once a day, so the computer learns its codes and the names the owner hid.

Whenever the computer doesn't know what the server holds, it reads first and works out what to send from the read's answer: at the agent's start, after a sign-in, after a request whose outcome it never learned (a timeout, a 5xx, a 429, an answer that doesn't read, or any answer but a 200 to a request the transport sent again itself after a connection dropped, `counter_stale` among them), before a request the server didn't take, with a 404 or a refusal, goes again, and when `project-names.json` is missing or names another sign-in. A 404's day of quiet ends at the first answer and at any such outcome. So no retry undoes a hide made in between.

Before a request goes, the client writes the names it sets, and the keys it deletes, into `project-names.json`, and the next answer settles them; a request it can't write them down for doesn't go. A name it sent that an answer lists neither in `names` nor in `hidden` is one the server doesn't have, however that came about: a lost answer whose hide was purged after its 30 days, or a removed device's name, which leaves no hide. Unless the owner turned it off since, the client turns it off in `config.toml` with a note and doesn't send it again, as for a name the server dropped. A request settles nothing when the transport never wrote it in full, or wrote it once and got a 404, a 429 or one of pairtimed's refusals, none of which stores anything. When the transport wrote it twice, as after a connection dropped before the answer, the first may have been stored whatever the answer says.

What the client does with an answer:

- A key in `hidden`: the owner hid that name on the dashboard. The client turns the name off in `config.toml` with a note, so it stays hidden until the owner turns it on again on the computer, then sends the key in `delete`. When `config.toml` has the name in a form pairtime can't change, such as an inline table, the client sends nothing of it, under any label, until `config.toml` has had it off; it goes again only when the owner turns it on after that. The same holds for every name the client turns off, for any reason. Once a set of a name the owner turned on again may have reached the server, which deletes the hide, the key in `hidden` is a new hide, and the name goes off again; so it does when the client turned the name off for any other reason, at a sign-in, for a file that didn't read, or as one the server dropped. An answer that shows a name `config.toml` has on ends its note.
- A name the server showed before, that `config.toml` still has on, that the answer lists in neither `names` nor `hidden`: the server no longer has it, as after the device's purge, a restore of its database, or a hide whose 30 days passed before the device asked again, and the client can't tell which. It turns the name off in `config.toml` with a note saying the server stopped showing it, and doesn't send it again until the owner turns it on.

A sign-in to a server, or as a device, other than the one `project-names.json` names, never shows that server the names the other showed: `pairtime login` turns off in `config.toml` every name that's on, and every name the old server showed, each with a note, and lists them with the `pairtime projects show-name` command that turns each on again there. The agent does the same when it first loads the file after such a sign-in. A `project-names.json` that exists but doesn't read counts as another sign-in's, since pairtime can't tell. While `config.toml` doesn't parse, login only writes its sign-in into a file that names none, and leaves one that names another sign-in, or doesn't read, for the agent; until then, `pairtime projects show-name` and `hide-name` and the local app's switch do what login does before the owner's change, so a name turned on then is this sign-in's and goes. The file names its sign-in as soon as the computer is linked: login writes it, and so do `pairtime logout`, the agent's first load, `pairtime projects show-name` and `hide-name`, and the local app's switch, into a file that names none. Only a computer whose `project-names.json` names no sign-in at all, as before its first link, sends the names turned on before it, as `pairtime projects show-name` told the owner it would.

The client sends no names request, a read included, until the server holds a batch the device sealed since its link. A names request stores the device's ingest state, its counter, as an upload does, and from then on the server holds something from the device (device-auth-v1 9), so `pairtime logout` after a sign-in the wrong account approved would no longer free the device's id (device-auth-v1 1). Before that batch the server has no minutes of the link's projects, so it would show no name anyway.

`uploads.jsonl` keeps a line for each names request the client sent, answered or not: when, to which server, how many names it set and deleted, the answer's status and code or why none came, and the SHA-256 of its body, never a name or a key. `pairtime privacy show` prints the next names request that carries a name byte for byte, names and all, before it goes; a read, `{}`, carries none and isn't shown.

## 3. Storage

For reviewers; the client never sees it. Migration `0008_project_names.sql` creates `project_names`:

| Column | What it holds |
|---|---|
| `device_id`, `project_key` | The key, the primary key |
| `user_id` | The token's account |
| `label` | The label, as 1.3 says |
| `set_at_ms` | When the server stored the label |
| `device_removed_at_ms` | When the device was removed, `NULL` while it is linked; the retention job deletes a row 90 days after it |

and `project_name_hides`, a hidden name without its label:

| Column | What it holds |
|---|---|
| `device_id`, `project_key` | The key, the primary key |
| `user_id` | The account the name was the device's under |
| `hidden_at_ms` | When the owner hid the name; the retention job deletes a hide 30 days after it |
