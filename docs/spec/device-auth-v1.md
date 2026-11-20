# Device sign-in v1: the device grant, `ptd_` tokens and device keys

How the pairtime app on a teen's computer gets linked to their account on a pairtime server (WP-SV3; DESIGN 3.10; SP 3.5, 8.3). The app signs in with the OAuth 2.0 Device Authorization Grant ([RFC 8628](https://www.rfc-editor.org/rfc/rfc8628)), gets a `ptd_` device token, and registers an Ed25519 device key that signs its uploads (DESIGN 4.5). The app never holds a Hack Club Auth token: the teen signs in with HCA in their browser, on the server's own pages.

Code: `internal/server/device` (server), `internal/agent/account` and `cmd/pairtime/login.go` (client), `internal/devtoken` (the token format both use).

Every request the app sends carries `User-Agent: pairtime-cli/<version>`. Every endpoint is on the server's `base_url`, over https (http only on a loopback host, for tests).

## 1. The device id

The device id the server records is the app's own ledger chain id: the `device_id` every event, receipt (`M_b`), day leaf and revealed body carries (docs/spec/ledger-v1.md 5, 7, 8). The ledger writer makes it once per install, at random (`d_` and 26 base32 characters), so it is an opaque key and never a hash anyone could recompute (AGENTS.md). The app sends it when it asks for a code, and the server keeps it as the device's primary key. A local-only install keeps its chain and its id when it signs in, so its history stays its own (DESIGN 3.2).

The server accepts 16 to 128 characters of `A-Z a-z 0-9 _ -`. A device id belongs to the first account that links it, and is refused to any other while its row exists, which is until 90 days after it was removed (DESIGN 4.4).

## 2. `POST /oauth/device/code`

RFC 8628 3.1. A form (`application/x-www-form-urlencoded`), every parameter at most once:

| Parameter | | |
|---|---|---|
| `client_id` | required | `pairtime` |
| `device_id` | required | section 1 |
| `os` | required | `windows`, `macos`, `linux` or `other` |
| `client_build` | required | the app's version, 1 to 64 characters of `A-Z a-z 0-9 . _ + -`, such as `v0.1.0` or `dev` |
| `scope` | optional | space-separated, each `ingest` or `read:self`. Every device token gets both, whatever this names |

`200 OK`, JSON, `Cache-Control: no-store`:

```json
{
  "device_code": "<43 characters of base64url>",
  "user_code": "WDJB-MJHT",
  "verification_uri": "https://<host>/device",
  "verification_uri_complete": "https://<host>/device?user_code=WDJB-MJHT",
  "expires_in": 900,
  "interval": 5
}
```

The user code is 8 characters of RFC 8628 6.1's base-20 alphabet `BCDFGHJKLMNPQRSTVWXZ`, shown as `XXXX-XXXX`. The page accepts it in either case, with or without the dash and spaces. Both codes are kept only as hashes: SHA-256 for the device code, and an HMAC under a key that lives only in the server's memory for the user code, since 20^8 codes are few enough to try one by one against a stolen hash. A restart therefore ends every code in progress: the app's next poll gets `expired_token`, and `pairtime login` asks the teen to start again.

Errors are RFC 6749 5.2 JSON, `{"error": "<code>", "error_description": "<sentence>"}`, with status 400: `invalid_request` (a parameter missing, repeated or malformed), `invalid_client` (another `client_id`), `invalid_scope`. While sign-in isn't set up on the server, the answer is `503` with `temporarily_unavailable`, because nobody could approve the code.

## 3. `GET /device`: the teen approves

RFC 8628 3.3. The page asks a signed-in teen for the code, then shows what is asking (the OS family and pairtime build the app sent, and the code itself, to compare with the terminal, RFC 8628 3.3.1 and 5.4), and offers Approve and Deny. A signed-out visitor goes through `/auth/hca/login` and comes back. With sign-in off, the page says linking isn't open yet. Every POST it makes needs the server's own `Origin` and a token tied to the session (CSRF). After 10 wrong codes within 15 minutes, typed or posted with a decision, an account gets no more tries until that window ends, however often it signs in again.

## 4. `POST /oauth/token`

RFC 8628 3.4. A form: `grant_type=urn:ietf:params:oauth:grant-type:device_code`, `device_code`, `client_id=pairtime`.

`200 OK`, JSON, `Cache-Control: no-store`, `Pragma: no-cache`:

```json
{"access_token": "ptd_...", "token_type": "Bearer", "scope": "ingest read:self", "device_id": "d_..."}
```

The token doesn't expire; it works until the device is removed. Errors, each `400` JSON (RFC 8628 3.5):

| `error` | When | The app |
|---|---|---|
| `authorization_pending` | Nobody has approved or denied the code yet | waits `interval` seconds and asks again |
| `slow_down` | It asked again less than `interval` seconds after its last request (with 0.5 s of slack); the server raises this code's interval by 5 s | adds 5 s to its interval, waits, asks again |
| `access_denied` | The teen chose Deny, or the device id belongs to another account | stops |
| `expired_token` | 15 minutes passed, or the server restarted, before the app got its token | stops; `pairtime login` starts over |
| `invalid_grant` | No such device code, or its token was already handed out | stops |
| `unsupported_grant_type`, `invalid_request`, `invalid_client` | RFC 6749 5.2 | stops |

## 5. The `ptd_` token

`ptd_`, then 32 random base62 characters, then a 6-character checksum: 42 characters in all. Base62's digits are `0-9 A-Z a-z` in that order. The checksum is the CRC-32 (IEEE) of everything before it, `ptd_` included, written in base62 with the most significant digit first and padded with `0` to 6 characters. This copies GitHub's token format (SP 8.3): a scanner can tell a real token from a typo or a corrupted copy offline, and the server refuses a token whose checksum doesn't match before it looks anything up. A token of 30 to 64 random characters is accepted, so the length can grow. The server stores only the SHA-256 of the whole token, in hex.

Requests with a token send `Authorization: Bearer ptd_...`. A malformed, unknown or removed token gets `401` with `WWW-Authenticate: Bearer realm="pairtime", error="invalid_token"`, and a request without one gets `401` with `WWW-Authenticate: Bearer realm="pairtime"` and no error code (RFC 6750 3.1).

## 6. `POST /api/v1/devices/me/key`

The app makes an Ed25519 key pair right after it gets its token and registers the public key, once (DESIGN 4.5). JSON, at most 4 KiB:

```json
{"public_key": "<base64 of the 32-byte public key>", "signature": "<base64 of a 64-byte Ed25519 signature>"}
```

Base64 here is RFC 4648 section 4 with padding. The signature, by the private key, is over the bytes of

```text
"pairtime-device-key-v1\n" + device_id + "\n" + public_key
```

where `public_key` is the base64 string as sent. It shows the app holds the private key, for this device.

`201 Created` registers the key; `200 OK` answers a repeat of the same key, so a retry after a lost answer works; `409 Conflict` with `{"error": "key_already_registered"}` refuses a different one. The body of 200 and 201 is `{"device_id": "...", "public_key": "..."}`. A bad body or signature gets `400` with `invalid_request`.

A device that `pairtime login` links again (after `pairtime logout`, or after it was removed on the web) gets a new token, and its old key is forgotten, so the new login registers a new one.

## 7. `GET /api/v1/devices/me` and `DELETE /api/v1/devices/me`

`GET` returns the device as the server has it: `{"device_id", "os", "client_build", "public_key" (or null), "created_at", "last_seen_at"}`, times in RFC 3339 UTC. `DELETE` removes the device, the same as Remove on `/account/devices`, and answers `204`; its token stops working at once. `pairtime logout` sends it.

## 8. What the app keeps

| What | Where |
|---|---|
| The token and the device key's 32-byte Ed25519 seed | the device keystore, in the store that holds `K_dev` (internal/agent/keys; DESIGN 3.6). keystore.json lists them first, so uninstall's Destroy deletes them |
| `{v, server, device_id, public_key, linked_at}` | `account.json` in the data directory, readable by the user only |
| `mode = "online"` and `[server] url` | config.toml, the settings the teen can edit |

An upload goes out only while config.toml's mode is `online` and the keystore holds a token, and only to the server `account.json` names: a token is never sent to a server it didn't come from, even when `[server] url` changes later. `pairtime logout` sends section 7's `DELETE`, then deletes `account.json`, the token and the key whatever the server answered, and sets the mode back to `local`. The ledger and everything else stay. When the keystore can't be opened, as with a locked keyring, logout still deletes `account.json` and sets the mode to `local`, so nothing uploads, and says the token stays in the keystore and the device on the server until it runs again.

## 9. On the server

| Table | Kept |
|---|---|
| `devices`: the device id, the account, the token's hash, the public key, the OS family, the build, when it was linked, last seen and removed | until 90 days after the device is removed (docs/retention.json `server.devices`) |
| `device_codes`: the two code hashes, the device id, OS family and build the app sent, who approved or denied, and when | 1 day after the code expires (`server.device_codes`, deleted by package device) |

No other table has a foreign key to `devices`, because its retention job deletes rows (AGENTS.md, Retention). Other packages find a device through `device.Store.Authenticate(ctx, token)`, which checks the checksum first, then the hash, and refuses a removed device and an account Hack Club Auth has marked ineligible.
