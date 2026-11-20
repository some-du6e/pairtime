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
	"io/fs"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"time"

	"github.com/some-du6e/pairtime/internal/server/backup"
	"github.com/some-du6e/pairtime/internal/server/config"
	"github.com/some-du6e/pairtime/internal/server/migrations"
	"github.com/some-du6e/pairtime/internal/server/objstore"
	"github.com/some-du6e/pairtime/internal/server/ops"
	"github.com/some-du6e/pairtime/internal/server/retention"
	"github.com/some-du6e/pairtime/internal/server/store"
)

const doctorSummary = "Check a deployed server from inside its container: config, keys, buckets, replication, snapshots, HCA, the witness repo, push monitors and the tunnel"

func init() {
	register(&command{name: "doctor", summary: doctorSummary, run: runDoctor})
}

// envWitnessRepo names the public witness repository, as owner/name.
// compose.yml sets it for pairtimed and for the witness sidecar, which
// pushes with a deploy key; pairtimed holds no GitHub credential.
const envWitnessRepo = "PAIRTIME_WITNESS_REPO"

// witnessPushLag is how long a root pairtimed wrote may wait for the
// witness sidecar, which pushes every 5 minutes, before doctor fails.
const witnessPushLag = 15 * time.Minute

// witnessKey is the sidecar's deploy key, as the config directory holds
// it; only the sidecar mounts it, and pairtimed can't read it.
const witnessKey = "witness_deploy_key"

// snapshotMaxAge is the oldest the newest snapshot may be: a day and two
// hours, so one missed night fails doctor.
const snapshotMaxAge = 26 * time.Hour

// doctorProbePrefix is where doctor's write-once test puts its probe
// objects, and deletes them.
const doctorProbePrefix = "pairtime-doctor/"

// Results of a check. A warning says doctor couldn't tell; it doesn't fail
// the run.
const (
	statusOK   = "ok"
	statusWarn = "warn"
	statusFail = "FAIL"
	statusSkip = "skip"
)

// writeGap is the wait between the write-once check's two puts to one key:
// R2 takes one write a second to a key and answers a faster second with
// 429, which isn't the refusal the check looks for.
const writeGap = 1100 * time.Millisecond

type result struct {
	status, detail string
}

func ok(format string, a ...any) result   { return result{statusOK, fmt.Sprintf(format, a...)} }
func bad(format string, a ...any) result  { return result{statusFail, fmt.Sprintf(format, a...)} }
func skip(format string, a ...any) result { return result{statusSkip, fmt.Sprintf(format, a...)} }
func warn(format string, a ...any) result { return result{statusWarn, fmt.Sprintf(format, a...)} }

// doctor holds what the checks share.
type doctor struct {
	e              *env
	cfg            *config.Config
	objs           *buckets
	client         *http.Client
	agent          string
	litestreamPath string
	github         string
	// witnessDir is where pairtimed writes the day roots the sidecar
	// pushes.
	witnessDir string
	// readiness is /readyz's answer, or nil when serve didn't answer.
	readiness *ops.Readiness
	// snapshotFresh says the newest snapshot is under snapshotMaxAge.
	snapshotFresh bool
	// writeGap is the write-once check's wait; tests shorten it.
	writeGap time.Duration
}

func runDoctor(ctx context.Context, e *env, args []string) int {
	fs := newFlagSet("doctor", doctorSummary, e)
	load := configFlags(fs, e)
	skipList := fs.String("skip", "", "checks to leave out, comma-separated: a name, or its first or last part, such as hca,push,write_once")
	onlyList := fs.String("only", "", "the checks to run, comma-separated in the same way, leaving out the rest")
	litestreamConfig := fs.String("litestream-config", "/etc/litestream.yml", "the litestream.yml compose mounts, to compare with pairtime.toml")
	witnessDir := fs.String("witness-dir", "/witness", "where pairtimed writes the day roots the witness sidecar pushes")
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
	d := &doctor{e: e, cfg: cfg, client: doctorClient(), agent: userAgent("pairtimed-doctor"),
		litestreamPath: *litestreamConfig, github: "https://api.github.com", witnessDir: *witnessDir}
	if e.githubURL != "" {
		d.github = e.githubURL
	}
	d.writeGap = writeGap
	if e.doctorWriteGap > 0 {
		d.writeGap = e.doctorWriteGap
	}
	skipped, only := nameSet(*skipList), nameSet(*onlyList)

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
		for _, bucket := range []struct {
			b         *objstore.Bucket
			snapshots bool
		}{{objs.main, false}, {objs.snapshots, true}} {
			b := bucket.b
			checks = append(checks,
				check{"bucket." + b.Name() + ".versioning", func(ctx context.Context) result { return d.checkVersioning(ctx, b) }},
				check{"bucket." + b.Name() + ".lifecycle", func(ctx context.Context) result { return d.checkLifecycle(ctx, b, bucket.snapshots) }},
				check{"bucket." + b.Name() + ".write_once", func(ctx context.Context) result { return d.checkWriteOnce(ctx, b) }})
		}
		checks = append(checks, check{"snapshot", d.checkSnapshot}, check{"keys_backup", d.checkKeyBackup})
	}
	checks = append(checks,
		check{"hca", d.checkHCA},
		check{"witness", d.checkWitness},
		check{"push.pairtimed", d.checkPushPairtimed},
		check{"push.snapshot", d.checkPushSnapshot},
		check{"push.litestream", d.checkPushLitestream},
		check{"tunnel", d.checkTunnel},
	)

	var failed, skips, oks, warns int
	for _, c := range checks {
		var r result
		switch {
		case matches(skipped, c.name):
			r = skip("left out with -skip")
		case len(only) > 0 && !matches(only, c.name):
			r = skip("left out with -only")
		default:
			cctx, cancel := context.WithTimeout(ctx, *timeout)
			r = c.run(cctx)
			cancel()
		}
		switch r.status {
		case statusOK:
			oks++
		case statusFail:
			failed++
		case statusWarn:
			warns++
		default:
			skips++
		}
		fmt.Fprintf(e.stdout, "%-4s  %-28s  %s\n", r.status, c.name, r.detail)
	}
	fmt.Fprintf(e.stdout, "doctor: %d ok, %d warnings, %d failed, %d skipped\n", oks, warns, failed, skips)
	if failed > 0 {
		return 1
	}
	return 0
}

// nameSet splits a comma-separated list of check names.
func nameSet(list string) map[string]bool {
	set := make(map[string]bool)
	for _, s := range strings.Split(list, ",") {
		if s = strings.TrimSpace(s); s != "" {
			set[s] = true
		}
	}
	return set
}

// matches reports whether set names the check: its whole name, or the
// part before its first dot or after its last, so "push" names
// push.pairtimed and "write_once" names every bucket's.
func matches(set map[string]bool, name string) bool {
	first, _, _ := strings.Cut(name, ".")
	return set[name] || set[first] || set[name[strings.LastIndex(name, ".")+1:]]
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
	if err := litestreamQuotable(d.cfg.Secrets.Get); err != nil {
		problems = append(problems, err.Error())
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
	// d.readiness keeps /readyz's own answer: pairtimed's heartbeat pings
	// on it, so doctor's push.pairtimed check does too.
	d.readiness = &res
	var parts []string
	for _, c := range res.Checks {
		if c.OK {
			parts = append(parts, c.Name+" ok")
		} else {
			parts = append(parts, c.Name+": "+c.Error)
		}
	}
	replicated := d.hasReadyCheck("replication")
	switch {
	case !res.Ready:
		return bad("%s", strings.Join(parts, "; "))
	case !replicated && d.cfg.ObjectStorage.Enabled():
		// litestream.yml's exec passes the flag, so a deployment with a
		// bucket that lacks the check has a serve Litestream didn't start.
		return bad("%s; no replication check, so serve runs without -litestream-replica, which litestream.yml's exec passes (deploy/litestream.yml)", strings.Join(parts, ", "))
	case !replicated:
		return skip("%s; no replication check, since object storage is off, so nothing replicates and there is no replica to measure", strings.Join(parts, ", "))
	}
	return ok("%s", strings.Join(parts, ", "))
}

// hasReadyCheck reports whether /readyz's answer has the check name.
func (d *doctor) hasReadyCheck(name string) bool {
	return d.readiness != nil && slices.ContainsFunc(d.readiness.Checks, func(c ops.CheckResult) bool { return c.Name == name })
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

// checkVersioning reads GetBucketVersioning (D-evidence-versioning).
// Enabled or Suspended fails, no status passes, and so does a store that
// doesn't implement the call, as R2's S3 API page says it doesn't. Any
// other answer is a warning, since doctor can't decide it and
// Evidence.Delete (WP-SB2) fails on a bucket that keeps versions too.
func (d *doctor) checkVersioning(ctx context.Context, b *objstore.Bucket) result {
	state, err := b.Versioning(ctx)
	var e *objstore.Error
	switch {
	case errors.Is(err, objstore.ErrNotImplemented):
		return ok("the store doesn't implement GetBucketVersioning, as R2 doesn't, so the bucket keeps no versions")
	case errors.As(err, &e) && e.Status == http.StatusForbidden:
		// An object-scoped key, such as an R2 token for two buckets, may
		// not ask.
		return warn("can't tell: the key may not read the bucket's versioning state (%s). R2 has no versioning; elsewhere, check that it was never turned on, and then leave this out with -skip versioning", e.Code)
	case err != nil:
		return warn("can't tell whether the bucket keeps versions: %v. Check that versioning was never turned on, and then leave this out with -skip versioning", err)
	case state == "":
		return ok("versioning was never turned on")
	default:
		return bad("versioning is %s. Neither bucket may keep versions: a purged or expired blob would survive as an old version (docs/errata/2026-11-20-evidence-versioning.md). Use a bucket that never had versioning", state)
	}
}

// checkLifecycle reads a bucket's lifecycle rules (DESIGN 10.5 item 4).
// The main bucket holds Litestream's replica, the evidence and the key
// backups, so no rule may delete objects in it; one that only aborts
// unfinished uploads, which R2 gives every bucket, can stay. In the
// snapshot bucket an enabled rule has to delete every snapshot within the
// period the privacy notice gives the backups (docs/retention.json,
// server.db_snapshots), since pairtimed deletes none itself. A key that
// may not read the rules, or any other answer doctor can't decide, is a
// warning, as for versioning.
func (d *doctor) checkLifecycle(ctx context.Context, b *objstore.Bucket, snapshots bool) result {
	days, err := snapshotDays()
	if err != nil {
		return bad("%v", err)
	}
	rules, err := b.Lifecycle(ctx)
	var e *objstore.Error
	switch {
	case errors.Is(err, objstore.ErrNotImplemented) && snapshots:
		return bad("the store doesn't implement lifecycle rules, so nothing deletes the snapshots under %s after %s, as the privacy notice says (DESIGN 10.5 item 4)", objstore.SnapshotPrefix, retention.Days(days))
	case errors.Is(err, objstore.ErrNotImplemented):
		return ok("the store doesn't implement lifecycle rules, so none deletes objects here")
	case errors.As(err, &e) && e.Status == http.StatusForbidden:
		return warn("can't tell: the key may not read the bucket's lifecycle rules (%s). Check them where the bucket was made (deploy/runbook.md, The pilot), and then leave this out with -skip lifecycle", e.Code)
	case err != nil:
		return warn("can't tell what the bucket's lifecycle rules are: %v. Check them where the bucket was made (deploy/runbook.md, The pilot), and then leave this out with -skip lifecycle", err)
	}
	name := func(r objstore.LifecycleRule) string {
		if r.ID == "" {
			return "a rule with no ID"
		}
		return fmt.Sprintf("rule %q", r.ID)
	}
	if !snapshots {
		var deleting []string
		for _, r := range rules {
			if r.Expires() {
				deleting = append(deleting, name(r))
			}
		}
		if len(deleting) > 0 {
			return bad("%s deletes objects, and this bucket holds Litestream's replica, the evidence and the key backups, so no lifecycle rule may delete in it; one that only aborts unfinished uploads can stay (DESIGN 10.5 item 4)", strings.Join(deleting, " and "))
		}
		if len(rules) == 0 {
			return ok("no lifecycle rules")
		}
		return ok("none of its lifecycle rules deletes objects")
	}
	for _, r := range rules {
		if r.Expires() && r.ExpireDays > 0 && r.ExpireDays <= days && r.Covers(objstore.SnapshotPrefix) {
			return ok("%s deletes the snapshots under %s after %s", name(r), objstore.SnapshotPrefix, retention.Days(r.ExpireDays))
		}
	}
	return bad("no enabled rule deletes every snapshot under %s within %s, so old snapshots, with whatever they hold of deleted accounts, outlive the %s the privacy notice gives them (docs/retention.json, server.db_snapshots; DESIGN 10.5 item 4)", objstore.SnapshotPrefix, retention.Days(days), retention.Days(days))
}

// snapshotDays is how long the retention schedule says the nightly
// snapshots are kept, the period the snapshot bucket's rule must keep to.
func snapshotDays() (int, error) {
	p, err := retention.Default()
	if err != nil {
		return 0, err
	}
	e, found := p.Find("server.db_snapshots")
	if !found || e.Days <= 0 {
		return 0, errors.New("docs/retention.json gives server.db_snapshots no period in days")
	}
	return e.Days, nil
}

func (d *doctor) checkWriteOnce(ctx context.Context, b *objstore.Bucket) result {
	var r [8]byte
	rand.Read(r[:])
	key := doctorProbePrefix + hex.EncodeToString(r[:])
	if _, err := b.Put(ctx, key, strings.NewReader("first"), objstore.PutOptions{IfNoneMatch: true}); err != nil {
		return bad("a write-once put of %s failed: %v", key, err)
	}
	select {
	case <-time.After(d.writeGap):
	case <-ctx.Done():
	}
	_, err := b.Put(ctx, key, strings.NewReader("second"), objstore.PutOptions{IfNoneMatch: true})
	// The probe goes whatever the second put did, on a clock of its own,
	// so a check that ran out of time still cleans up.
	dctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 15*time.Second)
	derr := b.Delete(dctx, key)
	cancel()
	var res result
	switch {
	case errors.Is(err, objstore.ErrExists):
		res = ok("refused a second write-once put to %s", key)
	case err == nil:
		res = bad("the store took a second If-None-Match: * put to %s, so evidence and snapshots can be overwritten. Use a store with conditional writes", key)
	default:
		res = bad("the second write-once put to %s failed oddly: %v", key, err)
	}
	if derr != nil {
		return bad("%s, and deleting the probe failed, so delete %s by hand: %v", res.detail, key, derr)
	}
	res.detail += ", and deleted the probe"
	return res
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
	d.snapshotFresh = true
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
	secret := d.cfg.Secrets.Get(config.HCAClientSecret)
	if secret == "" || d.cfg.Secrets.Get(config.EmailKey) == "" {
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
		Issuer                string   `json:"issuer"`
		AuthorizationEndpoint string   `json:"authorization_endpoint"`
		TokenEndpoint         string   `json:"token_endpoint"`
		JWKSURI               string   `json:"jwks_uri"`
		GrantTypes            []string `json:"grant_types_supported"`
		AuthMethods           []string `json:"token_endpoint_auth_methods_supported"`
	}
	if err := json.Unmarshal(body, &doc); err != nil {
		return bad("%s isn't a discovery document: %v", u, err)
	}
	if doc.Issuer != d.cfg.HCA.Issuer || doc.AuthorizationEndpoint == "" || doc.TokenEndpoint == "" || doc.JWKSURI == "" {
		return bad("%s names issuer %q and lacks an endpoint, or doesn't name hca.issuer %q", u, doc.Issuer, d.cfg.HCA.Issuer)
	}
	found := fmt.Sprintf("discovery names issuer %s; redirect URI %s/auth/hca/callback", doc.Issuer, d.cfg.BaseURL)
	if !slices.Contains(doc.GrantTypes, "client_credentials") {
		return warn("%s, but lists no client_credentials grant, so doctor can't check the client secret; an operator's first sign-in will", found)
	}
	// One client_credentials grant proves the id and the secret without a
	// person signing in. The token it returns is never kept or shown.
	if err := secretURL(doc.TokenEndpoint); err != nil {
		return bad("the token endpoint %s: %v", doc.TokenEndpoint, err)
	}
	form := url.Values{"grant_type": {"client_credentials"}}
	post := slices.Contains(doc.AuthMethods, "client_secret_post")
	if post {
		form.Set("client_id", d.cfg.HCA.ClientID)
		form.Set("client_secret", secret)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, doc.TokenEndpoint, strings.NewReader(form.Encode()))
	if err != nil {
		return bad("%v", err)
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("User-Agent", d.agent)
	if !post {
		req.SetBasicAuth(url.QueryEscape(d.cfg.HCA.ClientID), url.QueryEscape(secret))
	}
	resp, err := d.client.Do(req)
	if err != nil {
		var uerr *url.Error
		if errors.As(err, &uerr) {
			err = uerr.Err
		}
		return bad("%s; the token endpoint didn't answer: %v", found, err)
	}
	defer resp.Body.Close()
	var answer struct {
		Error string `json:"error"`
	}
	json.NewDecoder(io.LimitReader(resp.Body, 64<<10)).Decode(&answer)
	switch {
	case resp.StatusCode == http.StatusOK:
		return ok("%s; the token endpoint took the client id and secret (one client_credentials grant)", found)
	case answer.Error == "unauthorized_client":
		// Authenticated, and not allowed this grant: the secret is right.
		return ok("%s; the token endpoint took the client id and secret, and this client may not use client_credentials, which sign-in doesn't need", found)
	case answer.Error == "invalid_client":
		return bad("%s; HCA refused the client id or secret (invalid_client): check hca.client_id and %s", found, config.HCAClientSecret)
	default:
		return bad("%s; the token endpoint answered %d %s", found, resp.StatusCode, token(answer.Error))
	}
}

// secretURL refuses a URL that a secret mustn't be sent to: anything but
// https, except http on the loopback host, where tests run a fake HCA.
func secretURL(raw string) error {
	u, err := url.Parse(raw)
	if err != nil || u.Host == "" {
		return errors.New("it isn't a URL")
	}
	switch h := u.Hostname(); {
	case u.Scheme == "https":
		return nil
	case u.Scheme == "http" && (h == "localhost" || h == "127.0.0.1" || h == "::1"):
		return nil
	default:
		return errors.New("it isn't https, so doctor won't send the client secret there")
	}
}

// token keeps v when it is a short run of printable ASCII without spaces,
// as OAuth error codes are, for quoting an answer safely.
func token(v string) string {
	if len(v) > 64 || strings.ContainsFunc(v, func(r rune) bool { return r <= ' ' || r > '~' }) {
		return ""
	}
	return v
}

// witnessRepo matches owner/name.
var witnessRepo = regexp.MustCompile(`^[A-Za-z0-9-]{1,39}/[A-Za-z0-9._-]{1,100}$`)

// checkWitness checks that the witness sidecar publishes what pairtimed
// writes: the deploy key is where compose mounts it from, and the newest
// day root in the witness directory is older than the repository's latest
// commit, which GitHub's public API reports without a credential.
func (d *doctor) checkWitness(ctx context.Context) result {
	repo := d.cfg.Secrets.Get(envWitnessRepo)
	if repo == "" {
		return skip("%s isn't set, so there is no witness repository; compose.yml sets it", envWitnessRepo)
	}
	if !witnessRepo.MatchString(repo) {
		return bad("%s must be owner/name", envWitnessRepo)
	}
	var problems []string
	keyPath := filepath.Join(filepath.Dir(d.cfg.Path), witnessKey)
	if fi, err := os.Lstat(keyPath); err != nil {
		problems = append(problems, fmt.Sprintf("there is no deploy key at config/%s, so the sidecar can't push (deploy/runbook.md, step 5)", witnessKey))
	} else if fi.Mode().Perm()&0o077 != 0 {
		problems = append(problems, fmt.Sprintf("config/%s has mode %04o; ssh refuses a key others can read: chmod 600 it", witnessKey, fi.Mode().Perm()))
	}

	newest, newestAt, err := newestFile(d.witnessDir)
	if err != nil {
		return bad("%v", err)
	}
	u := d.github + "/repos/" + repo + "/commits?per_page=1"
	status, _, body, err := d.get(ctx, u, func(r *http.Request) { r.Header.Set("Accept", "application/vnd.github+json") })
	var committed time.Time
	switch {
	case err != nil:
		return bad("asking GitHub about %s: %v", repo, err)
	case status == http.StatusConflict:
		// GitHub's answer for a repository with no commits.
	case status == http.StatusNotFound:
		return bad("GitHub has no public repository %s", repo)
	case status != http.StatusOK:
		return bad("GitHub answered %d for %s's commits; unauthenticated calls get 60 an hour", status, repo)
	default:
		var commits []struct {
			SHA    string `json:"sha"`
			Commit struct {
				Committer struct {
					Date time.Time `json:"date"`
				} `json:"committer"`
			} `json:"commit"`
		}
		if err := json.Unmarshal(body, &commits); err != nil || len(commits) == 0 {
			return bad("GitHub's list of %s's commits doesn't read: %v", repo, err)
		}
		committed = commits[0].Commit.Committer.Date
	}
	latest := "no commits yet"
	if !committed.IsZero() {
		latest = "latest commit " + committed.UTC().Format(time.RFC3339)
	}
	switch {
	case len(problems) > 0:
		return bad("%s; %s has %s", strings.Join(problems, "; "), repo, latest)
	case newest == "":
		return skip("pairtimed writes no day roots to %s yet; WP-SV17's publisher does. The sidecar and the deploy key are in place, and %s has %s", d.witnessDir, repo, latest)
	case !committed.Before(newestAt):
		return ok("%s, the newest root, is older than %s's %s", newest, repo, latest)
	case time.Since(newestAt) < witnessPushLag:
		return ok("%s is %s old and the sidecar pushes every 5 minutes; %s has %s", newest, time.Since(newestAt).Round(time.Second), repo, latest)
	default:
		return bad("%s, written %s ago, isn't in %s, which has %s: the witness sidecar isn't pushing (docker compose logs witness)", newest, time.Since(newestAt).Round(time.Minute), repo, latest)
	}
}

// newestFile returns the newest regular file under dir, by modification
// time, and when it was modified; "" when there is none or dir is
// missing. Like the witness sidecar, it passes over a file whose name, or
// a directory's on its path, starts with a dot, such as a root pairtimed
// is still writing (deploy/witness/push.sh).
func newestFile(dir string) (string, time.Time, error) {
	var name string
	var at time.Time
	err := filepath.WalkDir(dir, func(p string, e fs.DirEntry, err error) error {
		if err != nil {
			if errors.Is(err, fs.ErrNotExist) {
				return nil
			}
			return err
		}
		if p != dir && strings.HasPrefix(e.Name(), ".") {
			if e.IsDir() {
				return fs.SkipDir
			}
			return nil
		}
		if !e.Type().IsRegular() {
			return nil
		}
		fi, err := e.Info()
		if err != nil {
			return err
		}
		if fi.ModTime().After(at) {
			name, at = p, fi.ModTime()
		}
		return nil
	})
	return name, at, err
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
	if err := distinctPushURLs(d.cfg.Secrets.Get); err != nil {
		return bad("%v", err)
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

func (d *doctor) checkPushSnapshot(ctx context.Context) result {
	return d.checkPush(ctx, envSnapshotHeartbeatURL, "the nightly snapshot", d.snapshotFresh, "the snapshot check didn't pass")
}

func (d *doctor) checkPushLitestream(ctx context.Context) result {
	why := "the replication check didn't pass"
	if d.readiness != nil && !d.hasReadyCheck("replication") {
		why = "/readyz has no replication check to show that Litestream replicates"
	}
	return d.checkPush(ctx, envLitestreamHeartbeatURL, "Litestream", d.readyCheck("replication"), why)
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
