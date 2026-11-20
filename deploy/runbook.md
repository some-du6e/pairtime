# Deploying pairtimed

This is the procedure for the agent that runs pairtime's server on a host (DESIGN 10.5; docs/PLAN.md, WP-OP2 and WP-OP4). Follow the steps in order. Each one says what it should print, and the last section says what to do when it doesn't.

The host builds nothing. It runs three containers from images pinned in `compose.yml`:

- `pairtimed`, which is pairtimed under Litestream. Litestream restores the database from the bucket when the disk has none, then copies every change to the bucket within about 10 seconds.
- `cloudflared`, which carries `https://<host>` to pairtimed with no open port on the host.
- `witness`, which pushes the signed day roots pairtimed writes to the public repository `some-du6e/pairtime-witness` with a deploy key.

## Two things to know first

**User-Agent.** The pilot's zone, karimeltaib.com, has Cloudflare's Bot Fight Mode on. From a data-center address, a request with curl's or Go's default User-Agent gets a 403 managed challenge from Cloudflare, and the server never sees it. That 403 carries the header `cf-mitigated: challenge`. It means the edge refused the request, not that pairtimed failed. Every request this runbook, `probe.yml`, `pairtimed doctor` and pairtimed itself make to a host behind Cloudflare sends a descriptive User-Agent, such as `pairtime-probe/<version> (+https://github.com/some-du6e/pairtime)`. Do the same in any request you add, for example:

```sh
UA="pairtime-deploy/1 (+https://github.com/some-du6e/pairtime)"
curl -fsS -A "$UA" https://pairtime.karimeltaib.com/healthz
```

**The image pin.** `compose.yml` pins pairtimed's image by tag and digest. Until the first release that line reads `ghcr.io/some-du6e/pairtimed:UNRELEASED`, which can't be pulled. If you see that, stop and ask the orchestrator for a release: Karim pushes a `v*` tag and approves the release run, its notes give the `image:` line, and a PR pins it in `compose.yml`, which runs the restore drill on the new image. Deploy once that PR has merged.

## The pilot

| What | Value |
|---|---|
| Public host | `pairtime.karimeltaib.com`, behind a dashboard-managed Cloudflare tunnel whose one public hostname points at `http://pairtimed:8080` (cloudflared/README.md) |
| Main bucket | R2 `pairtime-data`: Litestream's replica under `litestream/`, evidence under `evidence/`, key backups under `backups/keys/` |
| Snapshot bucket | R2 `pairtime-snapshots`, with a 30-day lifecycle rule |
| Push monitors | Three Uptime Kuma push monitors on the LAN, reached over plain http and kept off Cloudflare, so a Cloudflare outage can't raise a false alert |
| Witness repository | `some-du6e/pairtime-witness`, public, with a write deploy key for this host |

Neither bucket may keep versions. R2 has no versioning, and `pairtimed doctor` fails on a bucket that reports any.

## What Karim provides

Ask for anything on this list you don't have before step 1 (DESIGN 10.5):

1. The host: Linux, Docker with Compose v2, at least 40 GB of local disk (never NFS or SMB), outbound HTTPS, always on.
2. The tunnel's token.
3. The Hack Club Auth app's client id and secret, with redirect URI `https://pairtime.karimeltaib.com/auth/hca/callback`. Without them the server runs, but nobody can sign in.
4. The R2 endpoint, `https://<account id>.r2.cloudflarestorage.com`, and one access key with read and write on the two buckets and nothing else.
5. His age public key, `age1...`. The private key stays with him and never comes near the host.
6. The three push-monitor URLs (step 4).
7. The HCA subs of the people who run the instance, for `operators` (step 9). Alpha testers aren't operators.
8. Admin rights on `some-du6e/pairtime-witness`, or Karim adding the deploy key himself (step 5).

## First deploy

Run everything as a user that can use `sudo` and `docker`, from the bundle's directory.

### 1. Get the bundle

```sh
sudo install -d -o "$(id -u)" -g "$(id -g)" /srv/pairtime
git clone https://github.com/some-du6e/pairtime /srv/pairtime
cd /srv/pairtime/deploy
grep -n 'image:' compose.yml
```

The `pairtimed` line must name a released tag and a digest. If it says `UNRELEASED`, see "The image pin" above.

### 2. Make the directories

```sh
sudo install -d -m 0700 -o 65532 -g 65532 data witness-roots
install -d -m 0755 config
```

65532 is the image's user. pairtimed refuses a data directory that group or others can enter, because Litestream writes copies of the database there. Don't let compose create these directories on its own: it would make them root's, and pairtimed couldn't write to them.

### 3. The secrets

```sh
install -m 0600 secrets.env.example config/secrets.env
install -m 0600 cloudflared/tunnel.env.example config/tunnel.env
openssl rand -hex 32   # the value for PAIRTIME_EMAIL_KEY
```

Fill in `config/secrets.env`. Its comments say what each line is. For the pilot:

```sh
PAIRTIME_S3_ENDPOINT=https://<account id>.r2.cloudflarestorage.com
PAIRTIME_S3_REGION=auto
PAIRTIME_S3_BUCKET=pairtime-data
PAIRTIME_S3_SNAPSHOT_BUCKET=pairtime-snapshots
```

Write values unquoted, or in single quotes when one holds `$`, `\` or ` #`. pairtimed refuses a line compose would read differently. Fill in `TUNNEL_TOKEN` in `config/tunnel.env`. Both files stay mode 0600, and neither goes in git (`deploy/.gitignore` already ignores `config/`).

### 4. The push monitors

Make three push monitors in Uptime Kuma and put their URLs in `config/secrets.env`:

| Variable | Who pings it | Set the monitor to expect a ping every |
|---|---|---|
| `PAIRTIME_LITESTREAM_HEARTBEAT_URL` | Litestream, while the database has synced in the last 5 minutes | 5 minutes, with a few retries |
| `PAIRTIME_HEARTBEAT_URL` | pairtimed, every 5 minutes while `/readyz` passes | 5 minutes, with a few retries |
| `PAIRTIME_SNAPSHOT_HEARTBEAT_URL` | pairtimed, after each nightly snapshot is stored, at about 03:10 UTC | 26 hours |

A push URL looks like `http://<kuma host>:3001/api/push/<token>?status=up&msg=OK&ping=`. Plain http is fine on the LAN. Use three different monitors: pairtimed refuses to start when two variables share a URL. Nothing pings when its part is broken, so a monitor alerts when its pings stop.

### 5. The witness deploy key

```sh
ssh-keygen -t ed25519 -N '' -C "pairtimed witness, pairtime.karimeltaib.com" -f config/witness_deploy_key
chmod 600 config/witness_deploy_key
gh repo deploy-key add config/witness_deploy_key.pub --repo some-du6e/pairtime-witness \
  --allow-write --title "pairtimed witness, pairtime.karimeltaib.com"
```

Without admin rights on the repository, send `config/witness_deploy_key.pub` to Karim to add as a deploy key with write access. The key must exist before step 10: if it's missing when compose starts the witness container, Docker makes a directory in its place (see "When something fails").

### 6. Pull the images

```sh
docker compose pull
```

If GHCR answers `denied` for pairtimed, the package is still private. Ask Karim to make `ghcr.io/some-du6e/pairtimed` public once, or log the host in with `docker login ghcr.io` and a token that can read packages.

### 7. pairtimed init

The container mounts `config/` read-only, so init writes `pairtime.toml` to standard output, and the shell writes the file:

```sh
docker compose run --rm -T pairtimed pairtimed init \
  -base-url https://pairtime.karimeltaib.com -hca-client-id '<client id>' \
  -config-out - > config/pairtime.toml.new && mv config/pairtime.toml.new config/pairtime.toml
```

Leave out `-hca-client-id` if there is none yet. init makes `server.ed25519`, `evidence.p256` and `pepper` in `data/keys`, each mode 0600, and prints each key's id and public key to the terminal. Copy those two lines into the deploy issue. It reads the buckets from the `PAIRTIME_S3_*` lines of step 3, which Litestream reads too, and it checks that the file it wrote loads. If it fails, it has written nothing, so remove `config/pairtime.toml.new`, fix what it names and run it again. init never replaces a key or an existing `pairtime.toml`. When all three keys are there it keeps them.

### 8. Back up the keys

Do this before anything else. Losing the keys is losing every receipt, every sealed blob and every project's identity, and the database backups don't hold them.

```sh
docker compose run --rm -T pairtimed pairtimed keys backup -upload \
  > "pairtime-keys-$(date -u +%Y%m%dT%H%M%SZ).tar.age"
```

The backup is a tar archive of the three keys and `PAIRTIME_EMAIL_KEY`, encrypted with age to Karim's public key and armored as text. `-upload` also stores it in `pairtime-data` under `backups/keys/`. Send the file to Karim too. Only his offline identity can read it, so any channel will do. Back up again whenever a key or `PAIRTIME_EMAIL_KEY` changes.

### 9. Operators, and what stays closed

Ask Karim for the HCA subs of the people who run this instance and put them in `config/pairtime.toml`:

```toml
operators = ["<hca sub>", "<hca sub>"]
```

Operators can sign in while sign-ups are closed, to smoke-test the server. Alpha testers aren't operators: they stay in local-only mode until sign-ups open. Leave `signups_open = false`, `profile_default = "ask"` and `[controller]` as init wrote them. pairtimed refuses to start with `signups_open = true` until `[controller]` has a name and a contact. Karim alone decides when these change (DESIGN 8.3, 8.6).

### 10. Start

```sh
docker compose up -d
docker compose ps
```

Within two minutes `pairtimed` should show `healthy`. `docker compose logs pairtimed` shows Litestream starting, `pairtimed serve` applying the migrations, then `serving`. On an empty bucket Litestream logs `no backup found, starting fresh`. `docker compose logs cloudflared` should show four connections registered.

### 11. The first snapshot

The nightly snapshot runs at 03:10 UTC. Take the first one now, so that the backups and their monitor are proven today:

```sh
docker compose exec pairtimed pairtimed snapshot take
```

It prints `stored snapshots/pairtime-<time>.db.age in pairtime-snapshots`, and the snapshot monitor turns up.

### 12. Doctor

```sh
docker compose exec pairtimed pairtimed doctor
```

Every line should say `ok`, except these, which may say `skip`:

- `witness`, until WP-SV17's publisher writes day roots to `witness-roots/`. Before then the sidecar runs and finds nothing to push.
- `hca`, while there is no client id.

doctor checks the configuration, the keys and their modes, the database and its migrations, that `litestream.yml` and `pairtime.toml` name the same database and bucket, `/readyz` with the replica's lag, that neither bucket keeps versions and both refuse a second write-once put, the newest snapshot and key backup, HCA's discovery document, the witness repository through GitHub's public API, each push monitor, and `https://<host>/healthz` through the tunnel. It pings a monitor only when that monitor's owner would. Paste its output into the deploy issue.

### 13. From outside

```sh
gh workflow run probe.yml --repo some-du6e/pairtime -f host=pairtime.karimeltaib.com
```

The probe checks TLS, `/healthz` and `/.well-known/pairtime-keys.json` from GitHub's network. The keys document arrives with WP-SV17, so until then the probe's last step fails with a message saying so, and the others pass.

### 14. The restore drill against the real buckets

The weekly drill in `restore-drill.yml` restores a MinIO deploy. Once this host is up, prove the real replica restores too, with keys that can only read. Make an R2 API token with Object Read only on `pairtime-data` and `pairtime-snapshots`. Then, in the repository's Settings, create the environment `restore-drill` with these secrets:

| Secret | Value |
|---|---|
| `PAIRTIME_S3_ENDPOINT` | the endpoint of step 3 |
| `PAIRTIME_S3_REGION` | `auto` |
| `PAIRTIME_S3_BUCKET` | `pairtime-data` |
| `PAIRTIME_S3_SNAPSHOT_BUCKET` | `pairtime-snapshots` |
| `PAIRTIME_S3_READONLY_ACCESS_KEY_ID` | the read-only token's access key id |
| `PAIRTIME_S3_READONLY_SECRET_ACCESS_KEY` | its secret |

```sh
gh workflow run restore-drill.yml --repo some-du6e/pairtime --ref main -f target=production
```

It restores the replica in a throwaway container, runs `PRAGMA integrity_check`, serves the copy, and checks that the newest snapshot and key backup exist. It writes nothing to either bucket.

## Upgrades

Each upgrade is a PR that changes an image line in `compose.yml`, from Renovate or by hand after a release, and its restore drill must pass before it merges. Once it has:

```sh
cd /srv/pairtime/deploy
cp config/pairtime.toml "config/pairtime.toml.before-$(date -u +%Y%m%d)"
git -C /srv/pairtime pull --ff-only
docker compose pull
docker compose up -d
docker compose exec pairtimed pairtimed doctor
```

Keep the copy of `pairtime.toml`: a rollback can need it. Migrations only add in v1 and run when pairtimed starts.

## Rolling back

A rollback is the previous image. Find the commit before the bump and put its `compose.yml` back:

```sh
git -C /srv/pairtime log --oneline -5 -- deploy/compose.yml
git -C /srv/pairtime checkout <commit before the bump> -- deploy/compose.yml
docker compose up -d
```

The previous release starts on the newer `pairtime.toml`: a release that adds a key raises `config_version`, and an older release ignores the keys it doesn't know in such a file, with a warning in its log. The exception is a release that added a value to an existing key. The previous release refuses that value and doesn't start, so put the copy back and start again:

```sh
cp config/pairtime.toml.before-<date> config/pairtime.toml
docker compose up -d
```

The older release also runs on a database a newer one migrated, since v1 migrations only add, and its log names them. Then open a PR that reverts the bump, and once it merges, `git -C /srv/pairtime checkout deploy/compose.yml && git -C /srv/pairtime pull --ff-only`.

## Restoring the keys

On a new host, or after losing `data/`, the keys come from their backup, never from `pairtimed init`, which would make new ones. Karim decrypts the backup on his own machine, where his age identity lives:

```sh
age -d -i <identity file> pairtime-keys-<time>.tar.age | tar -x
```

and sends you the `pairtime-keys/` directory over SSH. On the host:

```sh
sudo install -d -m 0700 -o 65532 -g 65532 data/keys
sudo install -m 0600 -o 65532 -g 65532 pairtime-keys/server.ed25519 \
  pairtime-keys/evidence.p256 pairtime-keys/pepper data/keys/
```

Put `pairtime-keys/PAIRTIME_EMAIL_KEY`'s one line back into `config/secrets.env` as `PAIRTIME_EMAIL_KEY=<value>`, then `shred -u` every file under `pairtime-keys/` and remove the directory, here and on Karim's side. `docker compose run --rm -T pairtimed pairtimed keys show` should print the same key ids as the deploy issue.

## Moving to a VPS

Before the native program pays out, the server moves to a Hetzner CCX13, or an AX42 while CCX plans are unavailable (DESIGN 10.5). Only one server may run against the replica at a time: two Litestreams writing one replica path would corrupt it.

1. On the new host, do steps 1 to 6. Copy `config/` from the old host with `scp -p`, which keeps the modes, instead of filling it in again.
2. Restore the keys onto the new host ("Restoring the keys").
3. On the old host, note the newest file in the witness repository and, once WP-SV17 reports it, the witness row count. Then stop the server, so Litestream makes its last sync: `docker compose stop pairtimed`.
4. On the new host: `docker compose up -d`. `data/` holds no database, so Litestream restores it from the replica. Wait until `docker compose ps` shows `healthy`.
5. Run doctor on the new host, and check that the witness repository's newest file and the row count match step 3.
6. The tunnel follows the token, so the new host's cloudflared now serves `<host>`. Run `probe.yml` against it, then `docker compose down` on the old host and remove its `data/` with `shred`.

## When something fails

| What you see | What it means, and what to do |
|---|---|
| A 403 from a request to `<host>`, with `cf-mitigated: challenge` | Cloudflare's Bot Fight Mode refused the request. The server is fine. Send a descriptive User-Agent ("Two things to know first"). A monitor that can't set one, such as Litestream's heartbeat, belongs off Cloudflare, as the pilot's are. |
| `pull access denied` or `manifest unknown` for pairtimed | The pin says `UNRELEASED`, or the GHCR package is private (steps 1 and 6). |
| pairtimed logs `group or others can enter` and exits | `sudo chown 65532:65532 data && sudo chmod 700 data`, then `docker compose up -d`. |
| `config: ... unknown keys` | `pairtime.toml` has a key this release doesn't know. Fix the spelling, or after a rollback put the copy back ("Rolling back"). |
| `signups_open is true, but controller.name and controller.contact aren't both set` | Sign-ups can't open before the controller is named. Set it back to `false` unless Karim has given the controller's name and contact. |
| `/readyz` fails on `replication` | Litestream isn't uploading. `docker compose logs pairtimed` shows its errors, usually a wrong key or endpoint in `config/secrets.env`. Doctor's `litestream.yml` line says whether Litestream and pairtimed name the same bucket. |
| `/readyz` fails on `object_storage.*` | The bucket or its key is wrong. Its error names the S3 code, such as `NoSuchBucket` or `SignatureDoesNotMatch`. |
| `config/witness_deploy_key` turned into a directory | compose started before the key existed. `sudo rmdir config/witness_deploy_key`, do step 5, then `docker compose up -d witness`. |
| doctor's `witness` fails with "isn't pushing" | `docker compose logs witness`. `Permission denied (publickey)` means the deploy key isn't on the repository or lacks write access. If port 22 is blocked, set `PAIRTIME_WITNESS_REMOTE=ssh://git@ssh.github.com:443/some-du6e/pairtime-witness.git` for the witness service. Its host keys are already in `witness/known_hosts`. |
| A push monitor alerts | Its part stopped. Litestream: replication (above). pairtimed: `/readyz` (doctor says which check). Snapshot: `docker compose logs pairtimed`, look for `nightly snapshot`. |
| doctor's `bucket.*.versioning` fails | That bucket has, or had, versioning. Use a bucket that never had it (docs/errata/2026-11-20-evidence-versioning.md). |

## Reference

| Path on the host | Owner and mode | In the container |
|---|---|---|
| `data/` | 65532, 0700 | `/data` in pairtimed: the database, its WAL, Litestream's `.pairtime.db-litestream/`, `keys/` |
| `data/keys/*` | 65532, 0600 | written once by init, read by pairtimed |
| `witness-roots/` | 65532, 0700 | `/witness`, written by pairtimed (WP-SV17) and read by the witness sidecar |
| `config/` | you, 0755 | `/etc/pairtime`, read-only, in pairtimed |
| `config/pairtime.toml` | you, 0644 | `/etc/pairtime/pairtime.toml` |
| `config/secrets.env`, `config/tunnel.env` | you, 0600 | read by compose on the host and passed in as environment variables |
| `config/witness_deploy_key` | you, 0600 | `/run/witness/deploy_key` in the sidecar, which reads it with its one capability |
| `litestream.yml`, `witness/` | from git | `/etc/litestream.yml`, `/etc/witness/` |

pairtimed listens on 8080, which only cloudflared reaches, and on 127.0.0.1:9091 inside its container for `/healthz`, `/readyz` and `/metrics`. compose publishes no port on the host. Never route the tunnel to 9091.

Not deployed yet: `/.well-known/pairtime-keys.json` and the day roots (WP-SV17), and `install.sh` and `install.ps1` at `https://<host>/` (they wait for WP-RL2's scripts).
