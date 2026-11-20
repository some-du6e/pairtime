// SPDX-License-Identifier: AGPL-3.0-or-later

package main

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"regexp"
	"slices"
	"strings"
	"time"

	"github.com/some-du6e/pairtime/internal/server/backup"
	"github.com/some-du6e/pairtime/internal/server/config"
	"github.com/some-du6e/pairtime/internal/server/migrations"
	"github.com/some-du6e/pairtime/internal/server/objstore"
	"github.com/some-du6e/pairtime/internal/server/ops"
	"github.com/some-du6e/pairtime/internal/server/store"
)

const doctorSummary = "Check a deployed server from inside its container: config, keys, buckets, replication, snapshots, HCA, the witness repo, push monitors and the tunnel"

func init() {
	register(&command{name: "doctor", summary: doctorSummary, run: runDoctor})
}

// The witness repository's variables (deploy/secrets.env.example).
const (
	envWitnessRepo  = "PAIRTIME_WITNESS_REPO"
	envWitnessToken = "PAIRTIME_WITNESS_TOKEN"
)

// snapshotMaxAge is the oldest the newest snapshot may be: a day and two
// hours, so one missed night fails doctor.
const snapshotMaxAge = 26 * time.Hour

// doctorProbePrefix is where doctor's write-once test puts its probe
// objects, and deletes them.
const doctorProbePrefix = "pairtime-doctor/"

// Results of a check.
const (
	statusOK   = "ok"
	statusFail = "FAIL"
	statusSkip = "skip"
)

type result struct {
	status, detail string
}

func ok(format string, a ...any) result   { return result{statusOK, fmt.Sprintf(format, a...)} }
func bad(format string, a ...any) result  { return result{statusFail, fmt.Sprintf(format, a...)} }
func skip(format string, a ...any) result { return result{statusSkip, fmt.Sprintf(format, a...)} }

// doctor holds what the checks share.
type doctor struct {
	e              *env
	cfg            *config.Config
	objs           *buckets
	client         *http.Client
	agent          string
	litestreamPath string
	github         string
	// readiness is /readyz's answer, or nil when serve didn't answer.
	readiness *ops.Readiness
}

func runDoctor(ctx context.Context, e *env, args []string) int {
	fs := newFlagSet("doctor", doctorSummary, e)
	load := configFlags(fs, e)
	skipList := fs.String("skip", "", "checks to leave out, comma-separated, such as hca,witness")
	litestreamConfig := fs.String("litestream-config", "/etc/litestream.yml", "the litestream.yml compose mounts, to compare with pairtime.toml")
	timeout := fs.Duration("timeout", 15*time.Second, "how long each check may take")
	if code, done := parse(fs, args); done {
		return code
	}
	cfg, err := load()
	if err != nil {
		fmt.Fprintf(e.stdout, "%-4s  %-28s  %v\n", statusFail, "config", err)
		fmt.Fprintln(e.stdout, "doctor: the configuration doesn't load, so nothing else was checked")
		return 1
	}
	d := &doctor{e: e, cfg: cfg, client: doctorClient(), agent: userAgent("pairtimed-doctor"), litestreamPath: *litestreamConfig, github: "https://github.com"}
	if e.githubURL != "" {
		d.github = e.githubURL
	}
	skipped := make(map[string]bool)
	for _, s := range strings.Split(*skipList, ",") {
		if s = strings.TrimSpace(s); s != "" {
			skipped[s] = true
		}
	}

	type check struct {
		name string
		run  func(context.Context) result
	}
	checks := []check{
		{"config", d.checkConfig},
		{"data_dir", d.checkDataDir},
		{"keys", d.checkKeys},
		{"database", d.checkDatabase},
		{"litestream.yml", d.checkLitestream},
		{"readyz", d.checkReadyz},
	}
	objs, oerr := openBuckets(cfg)
	d.objs = objs
	if oerr != nil || objs == nil {
		checks = append(checks, check{"object_storage", func(context.Context) result {
			if oerr != nil {
				return bad("%v", oerr)
			}
			return bad("[object_storage] in %s is off; a deployment needs both buckets (DESIGN 10.5 item 4)", cfg.Path)
		}})
	} else {
		for _, b := range []*objstore.Bucket{objs.main, objs.snapshots} {
			checks = append(checks,
				check{"bucket." + b.Name() + ".versioning", func(ctx context.Context) result { return d.checkVersioning(ctx, b) }},
				check{"bucket." + b.Name() + ".write_once", func(ctx context.Context) result { return d.checkWriteOnce(ctx, b) }})
		}
		checks = append(checks, check{"snapshot", d.checkSnapshot}, check{"keys_backup", d.checkKeyBackup})
	}
	checks = append(checks,
		check{"hca", d.checkHCA},
		check{"witness", d.checkWitness},
		check{"push.pairtimed", d.checkPushPairtimed},
		check{"push.litestream", d.checkPushLitestream},
		check{"tunnel", d.checkTunnel},
	)

	var failed, skips, oks int
	for _, c := range checks {
		var r result
		if skipped[c.name] || skipped[strings.SplitN(c.name, ".", 2)[0]] {
			r = skip("left out with -skip")
		} else {
			cctx, cancel := context.WithTimeout(ctx, *timeout)
			r = c.run(cctx)
			cancel()
		}
		switch r.status {
		case statusOK:
			oks++
		case statusFail:
			failed++
		default:
			skips++
		}
		fmt.Fprintf(e.stdout, "%-4s  %-28s  %s\n", r.status, c.name, r.detail)
	}
	fmt.Fprintf(e.stdout, "doctor: %d ok, %d failed, %d skipped\n", oks, failed, skips)
	if failed > 0 {
		return 1
	}
	return 0
}

// doctorClient is doctor's HTTP client. It follows no redirect: each URL
// it fetches must answer where it is configured.
func doctorClient() *http.Client {
	return &http.Client{
		Timeout:       15 * time.Second,
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
	}
}

// get fetches u with doctor's User-Agent and returns the status and up to
// 64 KiB of the body.
func (d *doctor) get(ctx context.Context, u string, edit func(*http.Request)) (int, http.Header, []byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return 0, nil, nil, err
	}
	req.Header.Set("User-Agent", d.agent)
	if edit != nil {
		edit(req)
	}
	resp, err := d.client.Do(req)
	if err != nil {
		var uerr *url.Error
		if errors.As(err, &uerr) {
			err = uerr.Err
		}
		return 0, nil, nil, err
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, 64<<10))
	return resp.StatusCode, resp.Header, body, err
}

// challenged says whether an answer is Cloudflare's bot challenge rather
// than the server's own. Every answer through the tunnel says Server:
// cloudflare, so only cf-mitigated tells them apart.
func challenged(status int, h http.Header) bool {
	return status == http.StatusForbidden && h.Get("Cf-Mitigated") == "challenge"
}

func (d *doctor) checkConfig(context.Context) result {
	detail := fmt.Sprintf("%s, config_version %d, base_url %s, signups_open %v, profile_default %q", d.cfg.Path, d.cfg.ConfigVersion, d.cfg.BaseURL, d.cfg.SignupsOpen, d.cfg.ProfileDefault)
	if len(d.cfg.Warnings) > 0 {
		// A file for a newer release, as after a rollback (DESIGN 10.5
		// step 6): it loads, so it isn't a failure.
		detail += "; " + strings.Join(d.cfg.Warnings, "; ")
	}
	return ok("%s", detail)
}

func (d *doctor) checkDataDir(context.Context) result {
	if err := checkDataDir(d.cfg.DataDir); err != nil {
		return bad("%v", err)
	}
	return ok("%s has mode 0700", d.cfg.DataDir)
}

func (d *doctor) checkKeys(context.Context) result {
	keys, err := backup.Load(backup.KeysDir(d.cfg.DataDir))
	if err != nil {
		return bad("%v", err)
	}
	pub := keys.Public()
	return ok("server key %s, evidence key %s, pepper; each mode 0600", pub.ServerKeyID(), pub.EvidenceKeyID())
}

func (d *doctor) checkDatabase(ctx context.Context) result {
	o := storeOptions(d.cfg, d.e)
	o.NoCreate = true
	o.Logger = nil
	db, err := store.Open(ctx, o)
	if errors.Is(err, store.ErrNoDatabase) {
		return bad("%v; serve creates it, or Litestream restores it from the replica", err)
	}
	if err != nil {
		return bad("%v", err)
	}
	defer db.Close()
	ms, err := migrations.Embedded()
	if err != nil {
		return bad("%v", err)
	}
	states, err := migrations.Status(ctx, db, ms)
	if err != nil {
		return bad("%v", err)
	}
	var pending, newer []string
	for _, s := range states {
		switch {
		case !s.Known:
			newer = append(newer, s.ID)
		case s.AppliedAt.IsZero():
			pending = append(pending, s.ID)
		}
	}
	if len(pending) > 0 {
		return bad("migrations not applied: %s; serve applies them at start", strings.Join(pending, ", "))
	}
	detail := fmt.Sprintf("%s %s, %d migrations applied", db.Engine, db.Version, len(states)-len(newer))
	if len(newer) > 0 {
		detail += fmt.Sprintf(", and %d from a newer release (a rollback)", len(newer))
	}
	return ok("%s", detail)
}

// The lines of litestream.yml that checkLitestream reads.
var (
	litestreamDBPath  = regexp.MustCompile(`(?m)^\s*-\s*path:\s*"?([^"\s]+)"?\s*$`)
	litestreamReplica = regexp.MustCompile(`(?m)^\s+path:\s*"?([^"\s]+)"?\s*$`)
	litestreamExec    = regexp.MustCompile(`(?m)^exec:.*-litestream-replica[ =]"?([^"\s]+)"?`)
)

// checkLitestream compares litestream.yml and the variables it reads with
// pairtime.toml: the same database, the same bucket and endpoint, and the
// replica path that serve measures.
func (d *doctor) checkLitestream(context.Context) result {
	data, err := os.ReadFile(d.litestreamPath)
	if errors.Is(err, os.ErrNotExist) {
		return skip("%s isn't there, so this isn't compose's container; pass -litestream-config", d.litestreamPath)
	}
	if err != nil {
		return bad("%v", err)
	}
	var problems []string
	if m := litestreamDBPath.FindSubmatch(data); m == nil || string(m[1]) != d.cfg.Database.Path {
		problems = append(problems, fmt.Sprintf("it doesn't replicate database.path %s", d.cfg.Database.Path))
	}
	replica, exec := litestreamReplica.FindSubmatch(data), litestreamExec.FindSubmatch(data)
	switch {
	case exec == nil:
		problems = append(problems, "its exec doesn't pass -litestream-replica to pairtimed serve, so /readyz can't check the replica")
	case replica == nil || string(replica[1]) != string(exec[1]):
		problems = append(problems, "its replica path isn't the one exec passes to -litestream-replica")
	}
	o := d.cfg.ObjectStorage
	for _, v := range []struct{ name, toml, want string }{
		{envS3Endpoint, "object_storage.endpoint", o.Endpoint},
		{envS3Bucket, "object_storage.bucket", o.Bucket},
		{envS3Region, "object_storage.region", o.Region},
	} {
		got := d.cfg.Secrets.Get(v.name)
		if v.name == envS3Endpoint {
			if u, err := objstore.ParseEndpoint(got); err == nil {
				got = u.String()
			}
		}
		if got != v.want {
			problems = append(problems, fmt.Sprintf("%s, which litestream.yml reads, isn't %s in pairtime.toml", v.name, v.toml))
		}
	}
	if len(problems) > 0 {
		return bad("%s", strings.Join(problems, "; "))
	}
	return ok("replicates %s to %s/%s", d.cfg.Database.Path, o.Bucket, string(replica[1]))
}

func (d *doctor) checkReadyz(ctx context.Context) result {
	u := "http://" + dialAddr(d.cfg.OpsListen) + "/readyz"
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return bad("%v", err)
	}
	client := &http.Client{Transport: &http.Transport{Proxy: nil, DisableKeepAlives: true}}
	resp, err := client.Do(req)
	if err != nil {
		return bad("%s doesn't answer, so serve isn't running here: run doctor in the server's container, docker compose exec pairtimed pairtimed doctor (%v)", u, err)
	}
	defer resp.Body.Close()
	var res ops.Readiness
	if err := json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(&res); err != nil {
		return bad("%s answered %s, not a readiness report: %v", u, resp.Status, err)
	}
	d.readiness = &res
	var parts []string
	for _, c := range res.Checks {
		if c.OK {
			parts = append(parts, c.Name+" ok")
		} else {
			parts = append(parts, c.Name+": "+c.Error)
		}
	}
	if !slices.ContainsFunc(res.Checks, func(c ops.CheckResult) bool { return c.Name == "replication" }) {
		parts = append(parts, "no replication check, so serve runs without -litestream-replica")
		res.Ready = false
	}
	if !res.Ready {
		return bad("%s", strings.Join(parts, "; "))
	}
	return ok("%s", strings.Join(parts, ", "))
}

// readyCheck reports whether /readyz's check name passed.
func (d *doctor) readyCheck(name string) bool {
	if d.readiness == nil {
		return false
	}
	for _, c := range d.readiness.Checks {
		if c.Name == name {
			return c.OK
		}
	}
	return false
}

func (d *doctor) checkVersioning(ctx context.Context, b *objstore.Bucket) result {
	state, err := b.Versioning(ctx)
	switch {
	case errors.Is(err, objstore.ErrNotImplemented):
		return ok("the store doesn't implement GetBucketVersioning, as R2 doesn't, so the bucket keeps no versions")
	case err != nil:
		return bad("%v", err)
	case state == "":
		return ok("versioning was never turned on")
	default:
		return bad("versioning is %s. Neither bucket may keep versions: a purged or expired blob would survive as an old version (docs/errata/2026-11-20-evidence-versioning.md). Use a bucket that never had versioning", state)
	}
}

func (d *doctor) checkWriteOnce(ctx context.Context, b *objstore.Bucket) result {
	var r [8]byte
	rand.Read(r[:])
	key := doctorProbePrefix + hex.EncodeToString(r[:])
	defer b.Delete(context.WithoutCancel(ctx), key)
	if _, err := b.Put(ctx, key, strings.NewReader("first"), objstore.PutOptions{IfNoneMatch: true}); err != nil {
		return bad("a write-once put of %s failed: %v", key, err)
	}
	_, err := b.Put(ctx, key, strings.NewReader("second"), objstore.PutOptions{IfNoneMatch: true})
	switch {
	case errors.Is(err, objstore.ErrExists):
		return ok("refused a second write-once put to %s, and deleted the probe", key)
	case err == nil:
		return bad("the store took a second If-None-Match: * put to %s, so evidence and snapshots can be overwritten. Use a store with conditional writes", key)
	default:
		return bad("the second write-once put to %s failed oddly: %v", key, err)
	}
}

func (d *doctor) checkSnapshot(ctx context.Context) result {
	latest, err := objstore.NewSnapshots(d.objs.snapshots).Latest(ctx)
	if errors.Is(err, objstore.ErrNotFound) {
		return bad("no snapshot in %s yet; take the first now with pairtimed snapshot take (deploy/runbook.md)", d.objs.snapshots.Name())
	}
	if err != nil {
		return bad("%v", err)
	}
	age := time.Since(latest.LastModified)
	if age > snapshotMaxAge {
		return bad("the newest snapshot, %s, is %s old: the nightly job hasn't stored one since (look for \"nightly snapshot\" in docker compose logs pairtimed)", latest.Key, age.Round(time.Minute))
	}
	return ok("%s, %s old, %d bytes", latest.Key, age.Round(time.Minute), latest.Size)
}

func (d *doctor) checkKeyBackup(ctx context.Context) result {
	var newest objstore.ObjectInfo
	for o, err := range d.objs.main.List(ctx, backup.KeyBackupPrefix) {
		if err != nil {
			return bad("%v", err)
		}
		if o.Key > newest.Key {
			newest = o
		}
	}
	if newest.Key == "" {
		return bad("no key backup in %s/%s: run pairtimed keys backup -upload (deploy/runbook.md)", d.objs.main.Name(), backup.KeyBackupPrefix)
	}
	return ok("%s", newest.Key)
}

func (d *doctor) checkHCA(ctx context.Context) result {
	if d.cfg.HCA.ClientID == "" {
		return skip("hca.client_id isn't set yet, so nobody can sign in")
	}
	if d.cfg.Secrets.Get(config.HCAClientSecret) == "" || d.cfg.Secrets.Get(config.EmailKey) == "" {
		return bad("hca.client_id is set, so %s and %s must be too", config.HCAClientSecret, config.EmailKey)
	}
	u := strings.TrimSuffix(d.cfg.HCA.Issuer, "/") + "/.well-known/openid-configuration"
	status, h, body, err := d.get(ctx, u, nil)
	if err != nil {
		return bad("fetching %s: %v", u, err)
	}
	if status != http.StatusOK {
		hint := ""
		if challenged(status, h) {
			hint = "; Cloudflare challenged the request"
		}
		return bad("%s answered %d%s", u, status, hint)
	}
	var doc struct {
		Issuer                string `json:"issuer"`
		AuthorizationEndpoint string `json:"authorization_endpoint"`
		TokenEndpoint         string `json:"token_endpoint"`
		JWKSURI               string `json:"jwks_uri"`
	}
	if err := json.Unmarshal(body, &doc); err != nil {
		return bad("%s isn't a discovery document: %v", u, err)
	}
	if doc.Issuer != d.cfg.HCA.Issuer || doc.AuthorizationEndpoint == "" || doc.TokenEndpoint == "" || doc.JWKSURI == "" {
		return bad("%s names issuer %q and lacks an endpoint, or doesn't name hca.issuer %q", u, doc.Issuer, d.cfg.HCA.Issuer)
	}
	return ok("discovery at %s names issuer %s; redirect URI %s/auth/hca/callback", u, doc.Issuer, d.cfg.BaseURL)
}

// witnessRepo matches owner/name.
var witnessRepo = regexp.MustCompile(`^[A-Za-z0-9-]{1,39}/[A-Za-z0-9._-]{1,100}$`)

// checkWitness asks GitHub what `git push --dry-run` asks first: whether
// the token may push to the witness repository. The receive-pack
// advertisement answers 200 only for a token with write access, and
// changes nothing.
func (d *doctor) checkWitness(ctx context.Context) result {
	repo, token := d.cfg.Secrets.Get(envWitnessRepo), d.cfg.Secrets.Get(envWitnessToken)
	switch {
	case repo == "" && token == "":
		return skip("%s and %s aren't set yet; the daily witness roots go there (WP-SV17, DESIGN 10.5 item 6)", envWitnessRepo, envWitnessToken)
	case !witnessRepo.MatchString(repo):
		return bad("%s must be owner/name", envWitnessRepo)
	case token == "":
		return bad("%s is set, so set %s, a fine-grained token with contents write on it", envWitnessRepo, envWitnessToken)
	}
	u := d.github + "/" + repo + ".git/info/refs?service=git-receive-pack"
	status, _, _, err := d.get(ctx, u, func(r *http.Request) { r.SetBasicAuth("x-access-token", token) })
	switch {
	case err != nil:
		return bad("asking GitHub about %s: %v", repo, err)
	case status == http.StatusOK:
		return ok("%s takes pushes from the token (a dry run: nothing was pushed)", repo)
	default:
		return bad("GitHub answered %d for a push to %s: the repository is missing, or the token lacks contents write on it", status, repo)
	}
}

// checkPush pings a push monitor, but only when its owner would: a ping
// marks the monitor up, so doctor never sends one for a part that isn't
// working.
func (d *doctor) checkPush(ctx context.Context, name, owner string, healthy bool, why string) result {
	raw := d.cfg.Secrets.Get(name)
	if raw == "" {
		return bad("%s isn't set: %s's push monitor (DESIGN 10.4)", name, owner)
	}
	if err := checkPushURL(name, raw); err != nil {
		return bad("%v", err)
	}
	other := envLitestreamHeartbeatURL
	if name == envLitestreamHeartbeatURL {
		other = envHeartbeatURL
	}
	if raw == d.cfg.Secrets.Get(other) {
		return bad("%s and %s are the same URL; each needs a monitor of its own", name, other)
	}
	if !healthy {
		return skip("%s, so doctor sends no ping, as %s wouldn't", why, owner)
	}
	if err := ping(ctx, pushClient(), raw, d.agent); err != nil {
		return bad("%v", err)
	}
	return ok("pinged %s's push monitor, which answered 2xx", owner)
}

func (d *doctor) checkPushPairtimed(ctx context.Context) result {
	healthy := d.readiness != nil && d.readiness.Ready
	return d.checkPush(ctx, envHeartbeatURL, "pairtimed", healthy, "/readyz didn't pass")
}

func (d *doctor) checkPushLitestream(ctx context.Context) result {
	return d.checkPush(ctx, envLitestreamHeartbeatURL, "Litestream", d.readyCheck("replication"), "the replication check didn't pass")
}

// checkTunnel fetches /healthz at base_url, through Cloudflare and the
// tunnel and back to this container.
func (d *doctor) checkTunnel(ctx context.Context) result {
	u := d.cfg.BaseURL + "/healthz"
	status, h, body, err := d.get(ctx, u, nil)
	switch {
	case err != nil:
		return bad("fetching %s: %v", u, err)
	case challenged(status, h):
		return bad("%s answered %d from Cloudflare, not pairtimed: Bot Fight Mode challenged the request (deploy/runbook.md, User-Agent)", u, status)
	case status != http.StatusOK || strings.TrimSpace(string(body)) != "ok":
		return bad("%s answered %d %q, not pairtimed's 200 ok: check the tunnel's public hostname (deploy/cloudflared/README.md)", u, status, strings.TrimSpace(string(body)))
	}
	return ok("%s answered 200 ok", u)
}
