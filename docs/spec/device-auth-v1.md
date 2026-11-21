# Device sign-in v1: the device grant, `ptd_` tokens and device keys

How the pairtime app on a teen's computer gets linked to their account on a pairtime server (WP-SV3; DESIGN 3.10; SP 3.5, 8.3). The app makes the Ed25519 device key that signs its uploads (DESIGN 4.5), signs in with the OAuth 2.0 Device Authorization Grant ([RFC 8628](https://www.rfc-editor.org/rfc/rfc8628)) naming that key, and gets a `ptd_` device token that works only with it. The app never holds a Hack Club Auth token. The teen signs in with HCA in their browser, on the server's own pages.

Code: `internal/server/device` (server), `internal/agent/account` and `cmd/pairtime/login.go` (client), `internal/devtoken` (the token format both use).

Every request the app sends carries `User-Agent: pairtime-cli/<version>`. Every endpoint is on the server's `base_url`, over https (http only on a loopback host, for tests).

## 1. The device id

The device id the server records is the app's own ledger chain id, the `device_id` every event, receipt (`M_b`), day leaf and revealed body carries (docs/spec/ledger-v1.md 5, 7, 8). The ledger writer makes it once per install, at random (`d_` and 26 base32 characters), so it is an opaque key and never a hash anyone could recompute (AGENTS.md). The app sends it when it asks for a code, and the server keeps it as the device's primary key. A local-only install keeps its chain and its id when it signs in, so its history stays its own (DESIGN 3.2).

The server accepts 16 to 128 characters of `A-Z a-z 0-9 _ -`.

A device id belongs to the account that linked it for as long as the server holds what the device uploaded (D-device-account-binding):

- Removing a device that ingest has accepted nothing from deletes its row at once. Its id is free again, and the next login links it to whichever account approves, as a new device. A wrong-account approval followed by `pairtime logout` before the first upload costs nothing.
- Removing any other device leaves its row as a tombstone that keeps the account, the removal time, the link generation and ingest's state for the device, and drops the token's hash, the key, the OS family, the build and the link and last-seen times. The tombstone keeps the id with that account until the retention job deletes the row (section 9). The same install can link the id to the same account again, and its chain goes on (section 6). Another account's approval links nothing, and the app gets `device_taken` (section 4). The install can be linked to that other account only as a new device, with a new device id and key (WP-AG8's new-device rule).

## 2. `POST /oauth/device/code`

RFC 8628 3.1. A form (`application/x-www-form-urlencoded`), every parameter at most once:

| Parameter | | |
|---|---|---|
| `client_id` | required | `pairtime` |
| `device_id` | required | section 1 |
| `os` | required | `windows`, `macos`, `linux` or `other` |
| `client_build` | required | the app's version, 1 to 64 characters of `A-Z a-z 0-9 . _ + -`, such as `v0.1.0` or `dev` |
| `public_key` | required | the device key: the 32-byte Ed25519 public key in base64 (RFC 4648 section 4, with padding) |
| `key_signature` | required | the key's 64-byte Ed25519 signature, in base64, of the bytes of `"pairtime-device-key-v1\n" + device_id + "\n" + public_key`, where `public_key` is the base64 string as sent. It shows the app holds the private key, for this device |
| `scope` | optional | space-separated, each `ingest` or `read:self`. Every device token gets both, whatever this names |

The app makes its key pair before it asks for a code, and keeps the private key in its keystore from then on (section 8). The code, its approval and the token it yields all name that key, and the token works only with it (D-key-at-grant). No endpoint takes a key from a token alone, so a token taken before or after a login can't add or swap one. A login that links a device again sends a key too, a new one or the same, and the new link's token works only with that key.

`200 OK`, JSON, `Cache-Control: no-store`:

```json
{
  "device_code": "<43 characters of base64url>",
  "user_code": "WDJB-MJHT",
  "verification_uri": "https://<host>/device",
  "verification_uri_complete": "https://<host>/device?user_code=WDJB-MJHT",
  "expires_in": 900,
  "interval": 5,
  "signups_open": true
}
```

`signups_open` is false while the server's sign-ups are closed (DESIGN 8.6), so only an account that exists already can approve the code. `pairtime login` says so before it waits, and an install can stay local on it (WP-UI3).

The user code is 8 characters of RFC 8628 6.1's base-20 alphabet `BCDFGHJKLMNPQRSTVWXZ`, shown as `XXXX-XXXX`. The page accepts it in either case, with or without the dash and spaces. Both codes are kept only as hashes. The device code's is SHA-256. The user code's is an HMAC under a key that lives only in the server's memory, since 20^8 codes are few enough to try one by one against a stolen hash. A restart therefore ends every code in progress. Each code records which run of pairtimed made it, and no other run uses it, so the app's next poll gets `expired_token` and `pairtime login` asks the teen to start again. A clock that steps back doesn't end codes, since nothing compares their times with the run's start.

Errors are RFC 6749 5.2 JSON, `{"error": "<code>", "error_description": "<sentence>"}`, with status 400: `invalid_request` (a parameter missing, repeated or malformed, a key that isn't 32 bytes, or a signature that doesn't verify), `invalid_client` (another `client_id`), `invalid_scope`. While sign-in isn't set up on the server, the answer is `503` with `temporarily_unavailable`, because nobody could approve the code. A failure on the server is `500` with `server_error`.

## 3. `GET /device`: the teen approves

RFC 8628 3.3. The page asks a signed-in teen for the code, then shows what is asking (the OS family and pairtime build the app sent, and the code itself, to compare with the terminal, RFC 8628 3.3.1 and 5.4), and offers Link it and Don't link. A signed-out visitor goes through `/auth/hca/login` and comes back. With sign-in off, the page says linking isn't open yet. Every POST it makes needs the server's own `Origin` and a token tied to the session (CSRF). After 10 wrong codes within 15 minutes, typed or posted with a decision, an account gets no more tries until that window ends, however often it signs in again.

The page has two other forms, by the state of the code's device id:

- The id belongs to the signed-in account and the device is linked now. The page says the computer has been linked to this account since a date and last connected on a date, and that linking it again signs that copy of pairtime out: its token stops working. That is the case of a code someone else asked for in this device's name (RFC 8628 5.4), so the page says to choose Don't link unless the teen just ran `pairtime login` on that computer.
- The id belongs to another account. The page says the computer can't be linked to this one, and offers one button, Stop pairtime login, which ends the code and never links anything. The app then gets `device_taken`. If the id no longer belongs to the other account by the time Stop arrives, as when that account removed the device after the page was shown, Stop ends the code as Don't link does, and the app gets `access_denied`. Leaving the page leaves the code pending, so the teen can sign in as the account the computer is linked to and type the code again.

The same answer twice from the same account, as from a double click, gets the first answer's page and costs no try.

## 4. `POST /oauth/token`

RFC 8628 3.4. A form: `grant_type=urn:ietf:params:oauth:grant-type:device_code`, `device_code`, `client_id=pairtime`.

`200 OK`, JSON, `Cache-Control: no-store`, `Pragma: no-cache`:

```json
{"access_token": "ptd_...", "token_type": "Bearer", "scope": "ingest read:self", "device_id": "d_...", "public_key": "<base64>", "account": "U0123ABCD"}
```

`public_key` is the key the code named, which the token works with. `pairtime login` checks it is the key it sent. `account` names the account that approved the code, as the server's pages show it: its Slack ID, or its Hack Club Auth ID without one. `pairtime login` prints it and says the same name is at the top of the server's pages while that account is signed in, so a teen whose code someone else approved finds out at once (RFC 8628 5.4). Logging out before the first upload then leaves nothing behind (section 1). The token doesn't expire. It works until the device is removed or linked again. Errors are RFC 6749 5.2 JSON, each with status 400 except `server_error`, which comes with 500 (RFC 8628 3.5):

| `error` | When | The app |
|---|---|---|
| `authorization_pending` | Nobody has approved or denied the code yet | waits `interval` seconds and asks again |
| `slow_down` | It asked again less than `interval` seconds after its last request (with 0.5 s of slack); the server raises this code's interval by 5 s | adds 5 s to its interval, waits, asks again |
| `access_denied` | The teen chose Don't link, or chose Stop after the device id stopped belonging to another account (section 3), or Hack Club Auth has marked the approving account ineligible since | stops |
| `device_taken` | An account other than the one the device id belongs to approved the code, or chose Stop (section 1) | stops; it can link to that account only as a new device |
| `expired_token` | 15 minutes passed without an answer, or a minute more after one, or the server restarted, before the app got its token | stops; `pairtime login` starts over |
| `invalid_grant` | No such device code, or its token was already handed out | stops |
| `unsupported_grant_type`, `invalid_request`, `invalid_client`, `server_error` | RFC 6749 5.2 | stops |

A code answered before it expired keeps its answer for a minute after. An approved one can still be redeemed, and a denied or stopped one still gets `access_denied` or `device_taken`, so an answer in its last seconds isn't lost to the wait before the app's next poll.

The app stops on any error code but `authorization_pending` and `slow_down`, as RFC 8628 3.5 says. A 5xx or 429 answer without an RFC 6749 error body, such as a proxy's 502 page, isn't the server's answer to the code. The app waits twice as long as before, up to a minute and never less than the interval, as after a network error, and asks again until the code's lifetime has passed. Any other answer without an error body ends the polling.

The app never follows a redirect from these endpoints: a redirect could carry the device code to another host, or the token over plain http. A 3xx is an error.

## 5. The `ptd_` token

`ptd_`, then 32 random base62 characters, then a 6-character checksum: 42 characters in all. Base62's digits are `0-9 A-Z a-z` in that order. The checksum is the CRC-32 (IEEE) of everything before it, `ptd_` included, written in base62 with the most significant digit first and padded with `0` to 6 characters. This copies GitHub's token format (SP 8.3). A scanner can tell a real token from a typo or a corrupted copy offline, and the server refuses a token whose checksum doesn't match before it looks anything up. A token of 30 to 64 random characters is accepted, so the length can grow. The server stores only the SHA-256 of the whole token, in hex.

Requests with a token send `Authorization: Bearer ptd_...`. A malformed, unknown or removed token gets `401` with `WWW-Authenticate: Bearer realm="pairtime", error="invalid_token"`, and a request without one gets `401` with `WWW-Authenticate: Bearer realm="pairtime"` and no error code (RFC 6750 3.1).

## 6. Linking a device again

A device that `pairtime login` links again, after `pairtime logout`, after a removal on the web, or while it is still linked, gets a new token, which works only with the key of the new code. Any token it had stops working. Its link generation goes up by one (section 9), and ingest's state for it stays, so its chain goes on. The first batch under the new link may skip the batches the device sealed in local-only mode meanwhile (D-local-gap).

## 7. `GET /api/v1/devices/me` and `DELETE /api/v1/devices/me`

`GET` returns the device as the server has it: `{"device_id", "os", "client_build", "public_key", "linked_at", "last_seen_at"}`, times in RFC 3339 UTC. `DELETE` removes the device, the same as Remove on `/account/devices`, and answers `204`. Its token stops working at once, and section 1 says what stays. `pairtime logout` sends it. A device whose account Hack Club Auth has since marked ineligible can still remove itself this way, though its token works for nothing else.

## 8. What the app keeps

| What | Where |
|---|---|
| The device key's 32-byte Ed25519 seed, from before the app asks for a code, and the token, once the server hands it out | the device keystore, in the store that holds `K_dev` (internal/agent/keys; DESIGN 3.6). keystore.json lists them first, so uninstall's Destroy deletes them |
| `{v, server, device_id, public_key, linked_at}` | `account.json` in the data directory, readable by the user only |
| `mode = "online"` and `[server] url` | config.toml, the settings the teen can edit |

A login that fails forgets the key it made. Once the server has handed out a token, the login also asks the server to remove the device.

An upload goes out only while config.toml's mode is `online` and the keystore holds a token, and only to the server `account.json` names. A token is never sent to a server it didn't come from, even when `[server] url` changes later. `pairtime login` uses `--server`, else `[server] url`, else https://pairtime.karimeltaib.com. A config.toml that doesn't parse, or a url that isn't a server's, stops it rather than falling back to the default.

`pairtime logout` sends section 7's `DELETE`, but only when the keystore's key is the one `account.json` names. Then it deletes `account.json`, the token and the key whatever the server answered, and sets the mode back to `local`. The ledger and everything else stay. When the keystore can't be read, as with a locked keyring, logout only sets the mode to `local`, so nothing uploads, and keeps `account.json`, so running it again once the keystore opens still tells the right server. When config.toml can't be switched either, logout says the mode is still online, so uploads start again once the keystore opens. Login and logout hold a lock in the data directory (`account/agent.lock`), so two of them can't run at once.

## 9. On the server

| Table | Kept |
|---|---|
| `devices`: the device id, the account, the token's hash, the public key, the OS family, the build, when it was linked and last seen, when it was removed, its link generation, and ingest's state for it | while the device is linked. A removed device keeps only its id, account, removal time, link generation and ingest's state, until 90 days after the removal, or while ingest keeps anything the device uploaded, if that's longer (docs/retention.json `server.devices`). A device removed before ingest took anything from it is deleted at once |
| `device_codes`: the two code hashes, the run that made the code, the device id, OS family, build and public key the app sent, when it asked and last polled, and who approved, denied or stopped it, and when | 1 day after the code expires (`server.device_codes`, deleted by package device) |

No other table has a foreign key to `devices`, because its retention job deletes rows (AGENTS.md, Retention). That job's `kept_by` tables, ingest's witness rows, buckets, commit records, labels and replies, keep a removed device's row while any of their rows names its device id (D-device-account-binding). Each waits, with `awaits`, for the work package that creates its table, and TestTables fails once the table exists until that package turns its guard on. Other packages find a device through `device.Store.Authenticate(ctx, token)`, which checks the checksum first, then the hash, and refuses a removed device and an account Hack Club Auth has marked ineligible. When an account turns ineligible, sign-in removes its devices in the transaction that revokes its sessions (`accounts.Options.Ineligible`, which pairtimed sets to `device.Store.RemoveAccount`).

### Ingest's state for a device

Ingest's state for a device lives on its `devices` row, as nullable columns, and goes with the row: the device's lifetime, then 90 days, or as long as the tombstone stays (AGENTS.md, Retention; D-device-row-chain-state). No ingest table keeps a copy, so a device silent for months comes back to the head ingest last stored, and a chain gap is still caught. Ingest reads and writes the state only through these, inside its own writer transaction, and never with SQL against `devices`:

```go
// State is ingest's state for one device, on its devices row.
type State struct {
	Generation     int64       // the device's link generation; only a link changes it
	Req            int64       // X-Pairtime-Req of the last request ingest accepted, 0 before the first
	HeadB          uint64      // b of the chain's head, the last batch root ingest stored; 0 while there is none
	HeadHash       ledger.Hash // H_b of the head
	HeadGeneration int64       // the link generation the head was stored under
	HeadReceivedAt time.Time   // when the head arrived
	ChainStart     uint64      // b of the first batch root ingest stored
	Gaps           []Gap       // batches sealed in local-only mode between two links, in order
}

// Gap is the batch numbers First to Last, both included.
type Gap struct{ First, Last uint64 }

func (s *Store) LoadState(ctx context.Context, tx writer.Tx, d Device) (State, error)
func (s *Store) SaveState(ctx context.Context, tx writer.Tx, d Device, st State) error
```

`d` is the `Device` that `Authenticate` returned for the request. Both methods return `ErrRevoked` when the device's row no longer holds that token, because the device was removed or linked again since, so a request can't land on a device that changed under it. A device ingest has taken nothing from has the zero state but for its generation. `SaveState` leaves the generation alone, and refuses a state that can't be right: a head below the chain's start or under a generation the device hasn't had, a head without its arrival time, or a gap outside the chain or out of order.

The link generation is 1 for the link that makes a device's row, and one more for each later link of the same id with a new token (section 6). Only a link resets what the chain expects next (D-local-gap). While `HeadGeneration` is below `Generation`, the device's first batch under the new link may have any `b` above `HeadB`, with its `prev` taken as given, as a device's first batch already may (ingest-v1 7 item 6). Ingest then records the batch numbers in between as a `Gap`, never asks for them, never takes their roots, and stores the new head with `HeadGeneration` set to `Generation`. Their minutes are never uploaded, so they are never credited. Without a new link, a skipped batch is still `chain_gap`. Ingest's state counts as something ingest holds from the device (section 1) once `SaveState` has stored it.
