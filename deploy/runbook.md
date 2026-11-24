# Deploying pairtimed

This is the procedure for the agent that runs pairtime's server on a host (DESIGN 10.5; docs/PLAN.md, WP-OP2 and WP-OP4). Follow the steps in order. Each one says what it should print, and the last section says what to do when it doesn't.

The host builds nothing. It runs three containers from images pinned in `compose.yml`:

- `pairtimed`, which is pairtimed under Litestream. Litestream restores the database from the bucket when the disk has none, then copies every change to the bucket within about 20 seconds.
- `cloudflared`, which carries `https://<host>` to pairtimed with no open port on the host.
- `witness`, which pushes the signed day roots pairtimed writes to the public repository `some-du6e/pairtime-witness` with a deploy key.

## Two things to know first

**User-Agent.** The pilot's zone, karimeltaib.com, has Cloudflare's Bot Fight Mode on. From a data-center address, a request with curl's or Go's default User-Agent gets a 403 managed challenge from Cloudflare, and the server never sees it. That 403 carries the header `cf-mitigated: challenge`. It means the edge refused the request, not that pairtimed failed. Every request this runbook, `probe.yml`, `pairtimed doctor` and pairtimed itself make to a host behind Cloudflare sends a descriptive User-Agent, such as `pairtime-probe/<version> (+https://github.com/some-du6e/pairtime)`. Do the same in any request you add, for example:

```sh
UA="pairtime-deploy/1 (+https://github.com/some-du6e/pairtime)"
curl -fsS -A "$UA" https://pairtime.karimeltaib.com/healthz
```

**The image pin.** `server-image.yml` builds the server image from each commit on main that changes what goes into it, which is pairtimed's code and the files it embeds, never the rest of `deploy/`, and pushes it to `ghcr.io/some-du6e/pairtimed` as `sha-<12 hex digits of the commit>`. Each run's summary gives the line that pins it, tag and digest:

```yaml
    image: ghcr.io/some-du6e/pairtimed:sha-0123456789ab@sha256:<digest>
```

`compose.yml` carries one such line, and a bump is a PR that replaces it with a newer run's and changes nothing that goes into the image. Its restore drill runs the pinned image itself, after checking that the image was built from a commit on main, and must pass before the PR merges. To find the newest:

```sh
gh run list --repo some-du6e/pairtime --workflow server-image.yml --branch main --status success --limit 1
gh run view <run id> --repo some-du6e/pairtime   # its notice is the image: line
```

Until the first push is pinned, the line reads `ghcr.io/some-du6e/pairtimed:UNRELEASED`, which can't be pulled. If you see that, ask the orchestrator for the PR that pins it, and deploy once it has merged. The orchestrator makes the package public once, after its first push, so the host pulls it without logging in.

Images built from #123 on carry their own `litestream.yml` at `/etc/litestream.yml`, so each runs the Litestream settings and `exec` line it was built and drilled with. Older images have none, so `compose.yml` mounts `litestream.frozen.yml` for them, a copy of `litestream.yml` that only a drill of the pinned image runs. So it changes only in a PR of its own: a PR that edits it and changes what goes into the image fails its restore drill. A change to `litestream.yml` itself reaches hosts only in the next image, which its PR's drill builds and runs. The first PR that pins a newer image also deletes the mount, the `./litestream.frozen.yml` line under the pairtimed service and its comment, and `litestream.frozen.yml` with it. Its restore drill fails while the mount is there, and ci.yml's `TestFrozenLitestream` while the file is.

## The pilot

| What | Value |
|---|---|
| Public host | `pairtime.karimeltaib.com`, behind a dashboard-managed Cloudflare tunnel whose one public hostname points at `http://pairtimed:8080` (cloudflared/README.md) |
| Main bucket | R2 `pairtime-data`: Litestream's replica under `litestream/`, evidence under `evidence/`, key backups under `backups/keys/` |
| Snapshot bucket | R2 `pairtime-snapshots`, with a 30-day lifecycle rule |
| Push monitors | Three Uptime Kuma push monitors on the LAN, reached over plain http and kept off Cloudflare, so a Cloudflare outage can't raise a false alert |
| Witness repository | `some-du6e/pairtime-witness`, public, with a write deploy key for this host |

Neither bucket may keep versions. R2 has no versioning, and `pairtimed doctor` fails on a bucket that reports any. `pairtime-data` has no lifecycle rule that deletes objects, since it holds the replica, the evidence and the key backups; R2's default rule, which only aborts multipart uploads that never finished, can stay. `pairtime-snapshots` has one that deletes everything under `snapshots/` after 30 days, the period the privacy notice gives the backups. `pairtimed doctor` checks both.

## What Karim provides

Ask for anything on this list you don't have before step 1 (DESIGN 10.5):

1. The host: Linux, Docker with Compose v2, local disk (never NFS or SMB), outbound HTTPS, always on. DESIGN 10.5 asks for 40 GB, which is for R11 scale. The pilot runs in CT 118 with 20 GB on purpose: its database is small, and a snapshot needs free space about the database's size while it is taken. Grow the disk once the database passes a third of it: doctor's `disk` line warns from then on, and the nightly snapshot fails, so its monitor alerts, once the free space is under the database's size. The bundle lives in `/srv/pairtime`, and every compose command runs from `/srv/pairtime/deploy`.
2. The tunnel's token.
3. The Hack Club Auth app's client id and secret, with redirect URI `https://pairtime.karimeltaib.com/auth/hca/callback`. The app must allow the five scopes pairtimed asks for: `openid email profile slack_id verification_status`. HCA answers a sign-in that asks for a scope the app lacks with its own `invalid_scope` page, so the sign-in never reaches pairtimed and its log shows nothing. Without the id and secret the server runs, but nobody can sign in.
4. The R2 endpoint, `https://<account id>.r2.cloudflarestorage.com`, and one access key with read and write on the two buckets and nothing else.
5. His age public key, `age1...`. The private key stays with him and never comes near the host.
6. The three push-monitor URLs (step 4).
7. The HCA subs of the people who run the instance, for `operators` (step 9). Alpha testers aren't operators.
8. A deploy key with write access to `some-du6e/pairtime-witness`. The pilot host has one staged already (step 5).

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
sudo install -d -m 0700 -o 65532 -g 65532 data data/tmp witness-roots
install -d -m 0755 config
```

65532 is the image's user. pairtimed refuses a data directory that group or others can enter, because Litestream writes copies of the database there, and init refuses one it can't write. Don't let compose create these directories on its own: it would make them root's, and pairtimed couldn't write to them. `data/tmp` takes SQLite's temporary files, on the disk instead of the container's 256 MiB `/tmp`. pairtimed makes it if it is missing, from the images built from #123 on; an older image doesn't, and SQLite then uses `/tmp`.

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
| `PAIRTIME_LITESTREAM_HEARTBEAT_URL` | Litestream, while the database has synced in the last 5 minutes | 5 minutes, with 10 minutes of grace: on an idle server Litestream syncs only after pairtimed's 5-minute heartbeat writes, so its pings can come a little late |
| `PAIRTIME_HEARTBEAT_URL` | pairtimed, every 5 minutes while `/readyz` passes | 5 minutes, with a few retries |
| `PAIRTIME_SNAPSHOT_HEARTBEAT_URL` | pairtimed, after each nightly snapshot is stored, at about 03:10 UTC | 26 hours |

A push URL looks like `http://<kuma host>:3001/api/push/<token>?status=up&msg=OK&ping=`. Plain http is fine on the LAN. Use three different monitors: pairtimed refuses to start when two variables share a URL. Nothing pings when its part is broken, so a monitor alerts when its pings stop.

### 5. The witness deploy key

If `config/witness_deploy_key` is already there, mode 0600, as on the pilot host, where it is staged with write access to `some-du6e/pairtime-witness`, use it and skip to step 6. A second key would be a second credential to the repository. Otherwise make one:

```sh
ssh-keygen -t ed25519 -N '' -C "pairtimed witness, pairtime.karimeltaib.com" -f config/witness_deploy_key
chmod 600 config/witness_deploy_key
gh repo deploy-key add config/witness_deploy_key.pub --repo some-du6e/pairtime-witness \
  --allow-write --title "pairtimed witness, pairtime.karimeltaib.com"
```

Without admin rights on the repository, send `config/witness_deploy_key.pub` to Karim to add as a deploy key with write access. The key must be a file before step 10. If it's missing when compose starts the witness container, Docker makes an empty directory at `config/witness_deploy_key` in its place, and the sidecar can't push (see "When something fails").

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

Leave out `-hca-client-id` if there is none yet; with it, `PAIRTIME_HCA_CLIENT_SECRET` and `PAIRTIME_EMAIL_KEY` must be in `config/secrets.env` too, or init refuses, as pairtimed would. init makes `server.ed25519`, `evidence.p256` and `pepper` in `data/keys`, each mode 0600, and prints each key's id and public key to the terminal. Copy those two lines into the deploy log, issue #103. It reads the buckets from the `PAIRTIME_S3_*` lines of step 3, which Litestream reads too, refuses to run without them, and checks that the file loads before it writes anything. If it fails, it has written nothing, so remove `config/pairtime.toml.new`, fix what it names and run it again.

On a host that was rebuilt or moved, or lost its disk, init refuses to make keys: `data/` holds a database, or `pairtime-data` already holds Litestream's replica, evidence or key backups, so a server ran here before, and its keys come from their backup ("Restoring the keys"). Once they are in `data/keys`, init keeps them and writes `pairtime.toml`. pairtimed itself refuses to serve a database written with other keys. `-new-keys` is only for starting the server over and leaving its database behind, and the database and its replica go first ("Starting over with new keys"). Otherwise Litestream restores the old database and pairtimed refuses it.

init never replaces a key. It refuses to write `pairtime.toml` when one is already in `config/`, since the new file would lose the operators of step 9 and every edit made since. To add something later, such as the HCA client id once Karim has it, edit the file by hand:

```toml
[hca]
client_id = "<client id>"
```

The client id needs `PAIRTIME_HCA_CLIENT_SECRET` in `config/secrets.env` beside it. Then make the running server read what changed:

- After editing only `config/pairtime.toml`: `docker compose restart pairtimed`. pairtimed reads the file from its mount when it starts, and `docker compose up -d` doesn't recreate the container when only a mounted file changed. doctor reads the file, not what is running.
- After editing `config/secrets.env`: `docker compose up -d --force-recreate pairtimed`. compose reads that file only when it creates the container, and `docker compose restart` keeps the environment the container was made with. On 2026-11-21 a restart after an edit there left pairtimed without the new value, and it crash-looped for about 80 s until the container was recreated.

Only to start the file over, run init with `-force`.

### 8. Back up the keys

Do this before anything else. Losing the keys is losing every receipt, every sealed blob and every project's identity, and the database backups don't hold them.

```sh
docker compose run --rm -T pairtimed pairtimed keys backup -upload \
  > "pairtime-keys-$(date -u +%Y%m%dT%H%M%SZ).tar.age"
```

The backup is a tar archive of the three keys and `PAIRTIME_EMAIL_KEY`, encrypted with age to Karim's public key and armored as text. `-upload` also stores it in `pairtime-data` under `backups/keys/`, with a name that gives the time and the ids of the keys and of the email key it holds, so doctor can tell whether the newest backup holds this server's keys; `pairtimed keys list` lists them. Send the file to Karim too. Only his offline identity can read it, so any channel will do. Back up again whenever a key or `PAIRTIME_EMAIL_KEY` changes: doctor's `keys_backup` line fails until you do, and also for a backup from before names said what they hold.

### 9. Operators, and what stays closed

Ask Karim for the HCA subs of the people who run this instance and put them in `config/pairtime.toml`:

```toml
operators = ["<hca sub>", "<hca sub>"]
```

Then `docker compose restart pairtimed`, which is enough for an edit to `pairtime.toml` alone (step 7). Operators can sign in while sign-ups are closed, to smoke-test the server. Alpha testers aren't operators: they stay in local-only mode until sign-ups open. Leave `signups_open = false`, `profile_default = "ask"` and `[controller]` as init wrote them. pairtimed refuses to start with `signups_open = true` until `[controller]` has a name and a contact. Karim alone decides when these change (DESIGN 8.3, 8.6).

### 10. Start

```sh
docker compose up -d
docker compose ps
```

Within two minutes `pairtimed` should show `healthy`. Docker's health check asks `/healthz`, which writes nothing; `/readyz`, which writes a row to prove the database takes writes, runs from pairtimed's 5-minute heartbeat and from doctor, so Litestream doesn't upload for every health check (litestream.yml, Cost). `docker compose logs pairtimed` shows Litestream starting, `pairtimed serve` applying the migrations, then `serving`. On an empty bucket Litestream logs `no backup found, starting fresh`. `docker compose logs cloudflared` should show four connections registered.

Once the HCA client id is in, an operator checks sign-in in a browser:

```text
https://pairtime.karimeltaib.com/auth/hca/login?return_to=/api/v1/users/me
```

After Hack Club Auth, the browser shows the account as JSON, with the operator's `hca_sub`. A sign-in without `return_to` lands on `/`, which says it's signed in.

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

- `witness`, while `PAIRTIME_WITNESS_REPO` is unset, which leaves the day roots off. With it set, the line is `ok` or `FAIL`. It names the newest day head and counts the witness rows, and it fails when a due day has no head an hour after the publisher's 00:10 UTC run, when the newest head's file isn't in `witness-roots/` as stored, when the server's clock reads a day already published, or when the repository's latest commit is older than the newest head.
- `hca`, while there is no client id.

doctor checks the configuration, the keys and their modes, the database and its migrations, the room the database has on its disk, that `litestream.yml` and `pairtime.toml` name the same database and bucket, `/readyz` with the replica's lag, that neither bucket keeps versions, the lifecycle rules of both (none that deletes in `pairtime-data`, and one in `pairtime-snapshots` that deletes snapshots within 30 days), that both refuse a second write-once put, that an upload of about 11 MiB in parts reaches the snapshot bucket whole, as each nightly snapshot past 64 MiB goes up, the newest snapshot, that the newest key backup holds these keys, HCA's discovery document, the witness repository through GitHub's public API, each push monitor, and `https://<host>/healthz` through the tunnel. It pings a monitor only when that monitor's owner would, and checks the HCA client id and secret with one client_credentials grant. HCA refuses that grant with `invalid_scope` once the id and secret check out, and with `invalid_client` when they don't, so `invalid_scope` in the `hca` line is ok. An image from before #123 prints the same answer as `FAIL`, `the token endpoint answered 400 invalid_scope`, which is ok too. A `warn` line means doctor couldn't tell, and doesn't fail the run. An R2 key scoped to the two buckets may get 403 for GetBucketVersioning and GetBucketLifecycleConfiguration. R2 has no versioning, and the lifecycle rules show in the dashboard under each bucket's settings, so check them there, note it in the deploy log, and leave the two checks out with `-skip versioning,lifecycle`. Paste the output into the deploy log, issue #103.

### 13. From outside

```sh
gh workflow run probe.yml --repo some-du6e/pairtime -f host=pairtime.karimeltaib.com
```

The probe checks TLS, `/healthz` and `/.well-known/pairtime-keys.json` from GitHub's network. The keys document lists the server key with the dates it is valid (docs/spec/keys-v1.md). An image from before WP-SV17 answers 404 there, and the probe's last step says so.

### 14. The restore drill against the real buckets

The weekly drill in `restore-drill.yml` restores a MinIO deploy, and the real replica too, with keys that can only read, once they are there. Make an R2 API token with Object Read only on `pairtime-data` and `pairtime-snapshots`. Then, in the repository's Settings, give the environment `restore-drill`, which admits only `main`, these secrets:

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

It restores the replica in a throwaway container with the image `compose.yml` pins, runs `PRAGMA integrity_check`, serves the copy, and checks that the newest snapshot and key backup exist. It writes nothing to either bucket. From then on it runs every Monday with the MinIO drill. Until both read-only secrets are set, the weekly run skips it with a notice saying which are missing, and a run by hand fails.

## Upgrades

Each upgrade is a PR that changes an image line in `compose.yml`: pairtimed's by hand from a newer `server-image.yml` run ("The image pin"), cloudflared's and the sidecar's from Renovate. Its restore drill must pass before it merges. Once it has:

```sh
cd /srv/pairtime/deploy
cp config/pairtime.toml "config/pairtime.toml.before-$(date -u +%Y%m%d)"
git -C /srv/pairtime pull --ff-only
sudo install -d -m 0700 -o 65532 -g 65532 data/tmp
docker compose pull
docker compose up -d
docker compose exec pairtimed pairtimed doctor
```

Keep the copy of `pairtime.toml`: a rollback can need it. Migrations only add in v1 and run when pairtimed starts. The `install -d` line makes `data/tmp` on a host set up before #123, whose step 2 didn't, and only sets the owner and mode again where it is there: images from before #123 don't make it, and without it SQLite's temporary files go to the 256 MiB `/tmp`.

**After each release.** The host's `/install.sh` and `/install.ps1` serve the release that `ScriptsRelease` pins in `internal/server/pages/install.go`, not the newest one. After each release, a PR moves that copy and sets `ScriptsRelease`, as `internal/server/pages/install/README.md` says. Then a bump pins an image built from that commit or a later one. Once the bump is up, check that the host serves the pinned release's scripts. Each pair of lines must print the same SHA-256: the first reads the pinned manifest's `install_scripts` from the commit the image was built from (it needs `jq`), and the second hashes what the host serves.

```sh
cd /srv/pairtime/deploy
commit=$(sed -n 's/.*pairtimed:sha-\([0-9a-f]\{12\}\).*/\1/p' compose.yml)
for f in install.sh install.ps1; do
  git -C /srv/pairtime show "$commit:internal/server/pages/install/manifest.json" | jq -r .payload | base64 -d | jq -r --arg f "$f" '.install_scripts[$f]'
  curl -fsS -A "$UA" "https://pairtime.karimeltaib.com/$f" | sha256sum | cut -d' ' -f1
done
```

A PR that drops a release key can turn the pin off (`ScriptsRelease = ""`), and then both paths answer 404 until a later PR pins a release the new key signed (docs/spec/update-manifest.md, Replacing a key).

## Rolling back

A rollback is the previous image. Find the commit before the bump and put its `deploy/` back, so `compose.yml` and every file it mounts are the ones that image ran with:

```sh
git -C /srv/pairtime log --oneline -5 -- deploy/compose.yml
git -C /srv/pairtime checkout --no-overlay <commit before the bump> -- deploy/
docker compose up -d
```

An image from before #123 has no `litestream.yml` of its own. A commit from before #123 mounts its own `litestream.yml` for it, and a commit from #123 up to the next pin mounts `litestream.frozen.yml`. A newer image carries its own, and its commit mounts none. `--no-overlay` also removes the files in `deploy/` that the commit lacks. git ignores `config/`, `data/` and `witness-roots/`, so the checkout leaves them alone.

The previous release starts on the newer `pairtime.toml`: a release that adds a key raises `config_version`, and an older release ignores the keys it doesn't know in such a file, with a warning in its log. The exception is a release that added a value to an existing key. The previous release refuses that value and doesn't start, so put the copy back and start again:

```sh
cp config/pairtime.toml.before-<date> config/pairtime.toml
docker compose up -d
```

The older release also runs on a database a newer one migrated, since v1 migrations only add, and its log names them. Then open a PR that reverts the bump, and once it merges, `git -C /srv/pairtime checkout --no-overlay HEAD -- deploy/ && git -C /srv/pairtime pull --ff-only`.

## Restoring the keys

On a new host, or after losing `data/`, the keys come from their backup, never from `pairtimed init`, which would make new ones. Karim decrypts the backup on his own machine, where his age identity lives:

```sh
age -d -i <identity file> pairtime-keys-<time>.tar.age | tar -x
```

and sends you the `pairtime-keys/` directory over SSH. On the host:

```sh
sudo install -d -m 0700 -o 65532 -g 65532 data data/keys data/tmp
sudo install -m 0600 -o 65532 -g 65532 pairtime-keys/server.ed25519 \
  pairtime-keys/evidence.p256 pairtime-keys/pepper data/keys/
```

`install -d` makes `data/` itself with the right owner and mode when the host has none yet; otherwise it would make it root's. It makes `data/tmp` too, which an image from before #123 doesn't (step 2). Put `pairtime-keys/PAIRTIME_EMAIL_KEY`'s one line back into `config/secrets.env` as `PAIRTIME_EMAIL_KEY=<value>`, then `shred -u` every file under `pairtime-keys/` and remove the directory, here and on Karim's side. `docker compose run --rm -T pairtimed pairtimed keys show` should print the same key ids as the deploy log, issue #103, and `pairtimed keys list` the backups that hold them. With the keys back, step 7's init is safe: it keeps them, and writes `pairtime.toml` if `config/` has none.

## Starting over with new keys

Only when the keys are lost for good, with no backup Karim can decrypt, or when Karim decides the server starts over. Every account, device, receipt and project stays with the old keys, so starting over leaves them all behind. pairtimed refuses to serve a database written with other keys, and Litestream restores the old database from the bucket whenever `data/` has none, so the database, its replica and the evidence sealed to the old key go first:

```sh
docker compose down
sudo mkdir /srv/pairtime-old
sudo sh -c 'mv data/pairtime.db data/pairtime.db-wal data/pairtime.db-shm data/.pairtime.db-litestream data/keys /srv/pairtime-old/ 2> /dev/null'
sudo ls -la data   # tmp/, nothing else
```

Then, in the R2 dashboard, delete every object under `litestream/` and `evidence/` in `pairtime-data`. Keep `backups/keys/`. Make the new keys with init. `config/pairtime.toml` stays, with its operators and every edit: `-force` lets init run beside it, and `> /dev/null` drops the copy init prints.

```sh
docker compose run --rm -T pairtimed pairtimed init -base-url https://pairtime.karimeltaib.com \
  -new-keys -force -config-out - > /dev/null
```

init prints the new key ids for the deploy log, and warns if a database or replica is still there. Back the new keys up at once (step 8), start (step 10), and note in the deploy log, issue #103, that the server started over and why. Once nobody needs `/srv/pairtime-old`, remove it with `shred`.

## Restoring from a disk backup

A `data/` copied from a disk or VPS snapshot holds an old `pairtime.db`. Started as it is, Litestream would take that old database for the newest state and upload it over the replica. Before the first start, move the database aside and keep only the keys, so Litestream restores the replica instead:

```sh
sudo mkdir /srv/pairtime-old
sudo sh -c 'mv data/pairtime.db data/pairtime.db-wal data/pairtime.db-shm data/.pairtime.db-litestream /srv/pairtime-old/ 2> /dev/null'
sudo ls -la data   # keys/ and tmp/, nothing else
docker compose up -d
```

## Moving to a VPS

Before the native program pays out, the server moves to a Hetzner CCX13, or an AX42 while CCX plans are unavailable (DESIGN 10.5). Only one server may run against the replica at a time: two Litestreams writing one replica path would corrupt it.

1. On the new host, do steps 1 to 6. Copy `config/` from the old host with `scp -p`, which keeps the modes, instead of filling it in again.
2. Restore the keys onto the new host ("Restoring the keys").
3. On the old host, run doctor and note the newest day head and the witness row count that its `witness` line prints, and the newest file in the witness repository. Uploads keep arriving until the old host stops, so the count is a floor for step 5, while the newest day head and file don't change before the next 00:10 UTC. Then stop the server, so Litestream makes its last sync: `docker compose stop pairtimed`.
4. On the new host: `docker compose up -d`. `data/` holds no database, so Litestream restores it from the replica. Wait until `docker compose ps` shows `healthy`.
5. Run doctor on the new host, and check that its `witness` line names exactly the newest day head of step 3, and that the witness repository's newest file is the same. The witness row count may be higher than step 3's, by the uploads that arrived after you noted it, but never lower: a lower count means the restore lost rows. The new host writes the day files again into its empty `witness-roots/`; the sidecar finds them all in the repository and pushes nothing, which is right.
6. The tunnel follows the token, so the new host's cloudflared now serves `<host>`. Run `probe.yml` against it, then `docker compose down` on the old host and remove its `data/` with `shred`.

## When something fails

| What you see | What it means, and what to do |
|---|---|
| A 403 from a request to `<host>`, with `cf-mitigated: challenge` | Cloudflare's Bot Fight Mode refused the request. The server is fine. Send a descriptive User-Agent ("Two things to know first"). A monitor that can't set one, such as Litestream's heartbeat, belongs off Cloudflare, as the pilot's are. |
| `pull access denied` or `manifest unknown` for pairtimed | The pin says `UNRELEASED`, or the GHCR package is private (steps 1 and 6). |
| pairtimed logs `group or others can enter` and exits | `sudo chown 65532:65532 data && sudo chmod 700 data`, then `docker compose up -d`. |
| `config: ... unknown keys` | `pairtime.toml` has a key this release doesn't know. Fix the spelling, or after a rollback put the copy back ("Rolling back"). |
| `signups_open is true, but controller.name and controller.contact aren't both set` | Sign-ups can't open before the controller is named. Set it back to `false` unless Karim has given the controller's name and contact. |
| pairtimed exits with `the keys aren't the ones this database was written with` | `data/keys` holds other keys than the database was written with, as after an init on a rebuilt host. Put the server's keys back from their backup ("Restoring the keys"). `-new-keys` doesn't help here: new keys serve only a server that starts over without this database ("Starting over with new keys"). |
| pairtimed crash-loops right after an edit to `config/secrets.env` | The container still has the old environment: `docker compose restart` doesn't read the file again. `docker compose up -d --force-recreate pairtimed` (step 7). |
| init refuses with `a server ran here before` | This is a rebuilt or moved host. Restore the keys first ("Restoring the keys"), then run init again. |
| doctor's `keys_backup` fails | The newest key backup is from before backups named their keys, or holds other keys or another email key than this server has. Back up again (step 8). |
| doctor's `disk` warns, or the snapshot log says `no room for the snapshot's copy` | The database is past a third of the disk, or larger than its free space. Grow the disk (What Karim provides, item 1). |
| `/readyz` fails on `replication` | Litestream isn't uploading. `docker compose logs pairtimed` shows its errors, usually a wrong key or endpoint in `config/secrets.env`; fix it, then `docker compose up -d --force-recreate pairtimed`. Doctor's `litestream.yml` line says whether Litestream and pairtimed name the same bucket. |
| Sign-in shows Hack Club Auth's own `invalid_scope` page | The HCA app doesn't allow a scope pairtimed asks for. Ask Karim to give the app all five (What Karim provides, item 3). pairtimed's log shows nothing, since the sign-in never comes back to it. |
| `/readyz` fails on `object_storage.*` | The bucket or its key is wrong. Its error names the S3 code, such as `NoSuchBucket` or `SignatureDoesNotMatch`. |
| `config/witness_deploy_key` turned into a directory | compose started before the key existed. `sudo rmdir config/witness_deploy_key`, do step 5, then `docker compose up -d witness`. |
| doctor's `witness` fails with "isn't pushing" | `docker compose logs witness`. `Permission denied (publickey)` means the deploy key isn't on the repository or lacks write access. If port 22 is blocked, set `PAIRTIME_WITNESS_REMOTE=ssh://git@ssh.github.com:443/some-du6e/pairtime-witness.git` for the witness service. Its host keys are already in `witness/known_hosts`. |
| A push monitor alerts | Its part stopped. Litestream: replication (above). pairtimed: `/readyz` (doctor says which check). Snapshot: `docker compose logs pairtimed`, look for `nightly snapshot`. |
| doctor's `bucket.*.versioning` fails | That bucket has, or had, versioning. Use a bucket that never had it (docs/errata/2026-11-20-evidence-versioning.md). |
| doctor's `bucket.pairtime-data.lifecycle` fails | A lifecycle rule deletes objects in the main bucket, which would delete the replica, evidence and key backups. Delete the rule it names, in the dashboard; one that only aborts unfinished uploads can stay. |
| doctor's `bucket.pairtime-snapshots.lifecycle` fails | No enabled rule deletes the snapshots within 30 days, so they would outlive what the privacy notice promises. Add a rule for the prefix `snapshots/`, or the whole bucket, that deletes objects after 30 days, with no tag or size filter. |

## Reference

| Path on the host | Owner and mode | In the container |
|---|---|---|
| `data/` | 65532, 0700 | `/data` in pairtimed: the database, its WAL, Litestream's `.pairtime.db-litestream/`, `keys/`, and `tmp/` for SQLite's temporary files |
| `data/keys/*` | 65532, 0600 | written once by init, read by pairtimed |
| `witness-roots/` | 65532, 0700 | `/witness`, written by pairtimed (WP-SV17) and read by the witness sidecar |
| `config/` | you, 0755 | `/etc/pairtime`, read-only, in pairtimed |
| `config/pairtime.toml` | you, 0644 | `/etc/pairtime/pairtime.toml` |
| `config/secrets.env`, `config/tunnel.env` | you, 0600 | read by compose on the host and passed in as environment variables |
| `config/witness_deploy_key` | you, 0600 | `/run/witness/deploy_key` in the sidecar, which reads it with its one capability |
| `litestream.yml` | in the image from #123 on, which runs the copy it was built with | `/etc/litestream.yml` |
| `litestream.frozen.yml` | from git, mounted, while `compose.yml` pins an image from before #123 | `/etc/litestream.yml` |
| `witness/` | from git | `/etc/witness/` |

pairtimed listens on 8080, which only cloudflared reaches, and on 127.0.0.1:9091 inside its container for `/healthz`, `/readyz` and `/metrics`. compose publishes no port on the host. Never route the tunnel to 9091.

The day roots are off by default: compose.yml leaves `PAIRTIME_WITNESS_REPO` empty and puts the witness sidecar behind the `witness` profile. To turn them on, add `PAIRTIME_WITNESS_REPO=some-du6e/pairtime-witness` and `COMPOSE_PROFILES=witness` to `.env` and run `docker compose up -d`. pairtimed then publishes every day since the first receipt, and the sidecar pushes them within 5 minutes, permanently, so do it only once the operator has agreed to publish the daily counts.

`install.sh` and `install.ps1` at `https://<host>/` go live with the first image pinned after #240, which serves the release `ScriptsRelease` pins (Upgrades); until then the scripts are release assets. While the zone's bot protection is on (Two things to know first), the host's install lines can be challenged from data-center and VPN addresses, since curl and PowerShell send their own User-Agent, so give those users the release-asset lines. The first deploy that serves these paths runs `curl -fsSL https://<host>/install.sh` once from a data-center address, with curl's own User-Agent, and writes down here what Cloudflare answered.
