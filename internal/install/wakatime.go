// SPDX-License-Identifier: AGPL-3.0-or-later

package install

import (
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/some-du6e/pairtime/internal/install/wakacfg"
)

// ItemWakaTime is the ID of the item that has wakatime-cli, which WakaTime's
// editor plugins run, send every heartbeat to pairtime's agent too, through
// a line in ~/.wakatime.cfg (DESIGN 5.6).
const ItemWakaTime = "wakatime"

// The kinds of the WakaTime item's edits.
const (
	kindWakaTimeLine      = "wakatime_line"
	kindWakaTimeSetup     = "wakatime_setup"
	kindWakaTimeWatermark = "wakatime_watermark"
)

// WakaTimeOptions are what the WakaTime item needs besides Env.
type WakaTimeOptions struct {
	// Data is pairtime's data directory, whose install folder keeps the
	// setup file (wakacfg.SetupFile).
	Data string
	// Port is the port the agent listens on, or will.
	Port int
	// Now is when install runs: the watermark of wakatime-cli's AI sync,
	// in a config pairtime makes.
	Now time.Time
	// Key is the key of pairtime's line. With "", the item keeps the key
	// of a line of pairtime's that is there already, then the setup file's,
	// and makes a new one only when neither has one, so that a second
	// install never adds a second line.
	Key string
}

// WakaTime returns the item that sends wakatime-cli's heartbeats to the
// agent (DESIGN 5.6). In the config wakatime-cli reads it adds pairtime's
// line under [api_urls], beside the teen's own server, such as Hackatime.
// With no config, it makes one whose api_url is the agent, and seeds
// wakatime-cli's watermark, so that wakatime-cli doesn't send the agent
// transcripts of the months before as new time (HC 1.5). First it writes
// the setup file, which tells the agent the line's key and file, so that
// the line is never there without it, and then the watermark, so that
// wakatime-cli never reads a config pairtime made without one. It plans
// the watermark again in a config pairtime made, so that install run again
// after one that stopped seeds it. It refuses a key that isn't in
// WakaTime's format before anything is opened for writing.
func WakaTime(env Env, o WakaTimeOptions) (Item, error) {
	if err := env.check(); err != nil {
		return Item{}, err
	}
	paths, err := wakacfg.Find(env.Home, env.getenv)
	if err != nil {
		return Item{}, err
	}
	cfg, err := readFile(paths.Config)
	if err != nil {
		return Item{}, err
	}
	setup := wakacfg.SetupFile(o.Data)
	key := o.Key
	if key == "" {
		if key, err = wakaTimeKey(cfg, setup); err != nil {
			return Item{}, err
		}
	}
	if !wakacfg.ValidKey(key) {
		return Item{}, wakacfg.ErrKey
	}
	if o.Port < 1 || o.Port > 65535 {
		return Item{}, wakacfg.ErrPort
	}
	edits := []Edit{wakaTimeSetup{path: setup, setup: wakacfg.Setup{Key: key, Config: paths.Config}}}
	if !cfg.Exists || ownConfig(cfg, key) {
		edits = append(edits, wakaTimeWatermark{path: paths.Internal, now: o.Now})
	}
	edits = append(edits, wakaTimeLine{path: paths.Config, key: key, port: o.Port, proxy: wakacfg.ProxyVar(env.getenv)})
	return Item{ID: ItemWakaTime, Summary: "Sends WakaTime's heartbeats to pairtime's agent too", Edits: edits}, nil
}

// ownConfig reports whether cfg is a config pairtime made for key
// (wakacfg.Make), which gets the watermark as a config pairtime makes does.
func ownConfig(cfg Content, key string) bool {
	if !cfg.Exists {
		return false
	}
	s, err := wakacfg.Inspect(cfg.Data, key)
	return err == nil && s.Own
}

// WakaTimeKey returns the key the WakaTime item gives pairtime's line when
// its options name none: that of a line of pairtime's in the config
// wakatime-cli reads for env, then the setup file's in data, pairtime's
// data directory, or a new one. Install plans the item with it, so that
// planning it again on the agent's port gives the same line.
func WakaTimeKey(env Env, data string) (string, error) {
	if err := env.check(); err != nil {
		return "", err
	}
	paths, err := wakacfg.Find(env.Home, env.getenv)
	if err != nil {
		return "", err
	}
	cfg, err := readFile(paths.Config)
	if err != nil {
		return "", err
	}
	return wakaTimeKey(cfg, wakacfg.SetupFile(data))
}

// wakaTimeKey returns the key for pairtime's line: that of a line of
// pairtime's under [api_urls] in cfg, then the setup file's at setup, then
// the api_key of a config pairtime made, by its mark, or a new one. The
// setup file's comes before a config pairtime made, whose api_key the
// teen may have replaced with their own, as WakaTime's extension does
// when they give it one.
func wakaTimeKey(cfg Content, setup string) (string, error) {
	if cfg.Exists {
		if k, ok := wakacfg.LineKey(cfg.Data); ok {
			return k, nil
		}
	}
	if s, err := wakacfg.ReadSetup(setup); err == nil {
		return s.Key, nil
	}
	if cfg.Exists {
		if k, ok := wakacfg.Key(cfg.Data); ok {
			return k, nil
		}
	}
	return wakacfg.NewKey()
}

// wakaKeyOf returns the key recs record for pairtime's line, "" when they
// hold no line of pairtime's, for Diff's mask of the config (Change.shown).
func wakaKeyOf(recs []editRecord) string {
	for _, r := range recs {
		if r.Kind == kindWakaTimeLine {
			return recordedKey(r.Data)
		}
	}
	return ""
}

// WakaTimeConfig returns the config wakatime-cli reads for env, and
// whether it is there, for the consent screen, which says which of the
// two changes install makes (WakaTime).
func WakaTimeConfig(env Env) (path string, there bool, err error) {
	paths, err := wakacfg.Find(env.Home, env.getenv)
	if err != nil {
		return "", false, err
	}
	c, err := readFile(paths.Config)
	if err != nil {
		return "", false, err
	}
	return paths.Config, c.Exists, nil
}

// keyRecord is what the WakaTime item's edits keep in the manifest: the
// key of pairtime's line.
type keyRecord struct {
	Key string `json:"key"`
}

// recordKey returns key as a keyRecord's JSON.
func recordKey(key string) json.RawMessage {
	b, err := json.Marshal(keyRecord{Key: key})
	if err != nil {
		panic(err) // a struct of one string always encodes
	}
	return b
}

// recordedKey returns the key in data, a keyRecord, and "" when there is
// none, for which uninstall takes out what it recognizes as pairtime's.
func recordedKey(data json.RawMessage) string {
	var r keyRecord
	if json.Unmarshal(data, &r) != nil || !wakacfg.ValidKey(r.Key) {
		return ""
	}
	return r.Key
}

// wakaTimeLine is pairtime's line in the config wakatime-cli reads
// (wakacfg.Add), or the config pairtime makes when there is none
// (wakacfg.Make). It records the key, by which uninstall takes the line
// out, and which the agent keeps when it rewrites the line's port. A
// config the editor refuses is left alone, and the refusal says what to
// change and to run install again, never a line to add by hand: no record
// would keep that line's key, so the agent would refuse what wakatime-cli
// sends to it, and those refusals hold back what it sends the teen's own
// server too.
type wakaTimeLine struct {
	path string
	key  string
	port int
	// proxy is the variable of install's environment that sets a proxy
	// (wakacfg.ProxyVar), "" for none. While one is set the line goes in
	// no config of the teen's: install's environment is most often the one
	// the editors start in, and wakatime-cli would send the line through
	// that proxy too. A config pairtime makes still goes in, since its only
	// api_url is the agent on 127.0.0.1, which Go's environment proxies
	// never reach.
	proxy string
}

func (e wakaTimeLine) Target() Target          { return File(e.path) }
func (e wakaTimeLine) Kind() string            { return kindWakaTimeLine }
func (e wakaTimeLine) Record() json.RawMessage { return recordKey(e.key) }

func (e wakaTimeLine) Apply(cur Content) (Content, error) {
	var (
		data []byte
		err  error
	)
	if cur.Exists && e.proxy != "" && !ownConfig(cur, e.key) {
		return Content{}, fmt.Errorf("would get pairtime's line, but %[1]s is set here, and wakatime-cli started with it would send the line through that proxy too: the proxy would get your project and file names unencrypted, and while it doesn't pass them on to pairtime's agent, each send that fails holds back what wakatime-cli sends your own server, so pairtime leaves it alone; run pairtime install again without %[1]s to add pairtime's line", e.proxy)
	}
	if cur.Exists {
		data, err = wakacfg.Add(cur.Data, e.key, e.port)
	} else {
		data, err = wakacfg.Make(e.key, e.port)
	}
	switch {
	case errors.Is(err, wakacfg.ErrNotOwn):
		return Content{}, fmt.Errorf("%w, so pairtime leaves it alone; to send your editor time to pairtime again, delete it and run pairtime install again", err)
	case err != nil:
		return Content{}, fmt.Errorf("%w, so pairtime leaves it alone; change that, then run pairtime install again to add pairtime's line", err)
	}
	next := cur
	next.Exists, next.Data = true, data
	return next, nil
}

// undoWakaTimeLine takes pairtime's lines out of cur, a config that
// changed since pairtime wrote it, as when the agent moved to another
// port: the line with the recorded key, or every line it recognizes as
// pairtime's when the manifest has no key (wakacfg.Remove). A config
// pairtime made goes once nothing else is left in it, and an [api_urls]
// section that base, what the file held before, didn't have goes when no
// key is left in it, with the blank line and the line break install put
// before it, and only those (wakacfg.Added).
//
// When base is cur itself, no record says what the file held before
// install: planFound's check and planInstall, for a target the manifest
// doesn't know, pass it so, as after pairtime's data directory was
// deleted. What is pairtime's is then what Remove recognizes by its
// marker, as adopt counts it: pairtime's lines, a config pairtime made, by
// its api_url and api_key, and, in a file that holds a line of pairtime's,
// an [api_urls] section no key is left in. A file with nothing of
// pairtime's keeps every byte, an empty [api_urls] section of the teen's
// included, since uninstall plans the item in every home. So does a file
// the editor can't read that doesn't name the agent's endpoint anywhere
// (wakacfg.Mentions), as one install left alone for that, and uninstall
// has no problem with it.
func undoWakaTimeLine(cur, base Content, data json.RawMessage) (Content, error) {
	if !cur.Exists {
		return cur, nil
	}
	made := !base.Exists
	var sec wakacfg.Section
	switch {
	case base.Same(cur):
		if _, err := wakacfg.Inspect(cur.Data, ""); err != nil && !wakacfg.Mentions(cur.Data) {
			return cur, nil // a file the package can't read, and nothing of pairtime's in it
		}
		_, holds := wakacfg.Key(cur.Data)
		made, sec = true, wakacfg.Section{Header: holds, Blank: holds}
	case !base.Exists:
		sec = wakacfg.Section{Header: true, Blank: true}
	case !wakacfg.HasAPIURLs(base.Data):
		sec = wakacfg.Added(base.Data)
	}
	out, gone, err := wakacfg.Remove(cur.Data, recordedKey(data), made, sec)
	if err != nil {
		return Content{}, err
	}
	if gone {
		return Content{}, nil
	}
	next := cur
	next.Data = out
	return next, nil
}

// wakaTimeSetup is the setup file, which tells the agent the key of
// pairtime's line and which config it is in (wakacfg.Setup).
type wakaTimeSetup struct {
	path  string
	setup wakacfg.Setup
}

func (e wakaTimeSetup) Target() Target          { return File(e.path) }
func (e wakaTimeSetup) Kind() string            { return kindWakaTimeSetup }
func (e wakaTimeSetup) Record() json.RawMessage { return recordKey(e.setup.Key) }

func (e wakaTimeSetup) Apply(cur Content) (Content, error) {
	next := cur
	next.Exists, next.Data = true, e.setup.Encode()
	return next, nil
}

// undoWakaTimeSetup puts back base, what was there before, when cur is a
// setup file with the recorded key, and leaves anything else as it is.
func undoWakaTimeSetup(cur, base Content, data json.RawMessage) (Content, error) {
	if !cur.Exists {
		return cur, nil
	}
	s, err := wakacfg.DecodeSetup(cur.Data)
	if key := recordedKey(data); err != nil || (key != "" && s.Key != key) {
		return cur, nil
	}
	return base, nil
}

// wakaTimeWatermark seeds wakatime-cli's watermark for its AI sync in
// wakatime-internal.cfg, in a home where pairtime makes the config
// (wakacfg.Watermark).
type wakaTimeWatermark struct {
	path string
	now  time.Time
}

func (e wakaTimeWatermark) Target() Target { return File(e.path) }
func (e wakaTimeWatermark) Kind() string   { return kindWakaTimeWatermark }

func (e wakaTimeWatermark) Apply(cur Content) (Content, error) {
	var data []byte
	if cur.Exists {
		data = cur.Data
	}
	out, err := wakacfg.Watermark(data, e.now)
	if err != nil {
		return Content{}, err
	}
	next := cur
	next.Exists, next.Data = true, out
	return next, nil
}

// undoWakaTimeWatermark leaves wakatime-internal.cfg as it is once
// wakatime-cli has changed it. The file is wakatime-cli's own state, and
// without the watermark the next config that points wakatime-cli at a
// server would have it send every agent transcript since 2025-02-24 as
// new time. A file that still holds only what pairtime wrote goes, as
// uninstall puts back every such target.
func undoWakaTimeWatermark(cur, _ Content, _ json.RawMessage) (Content, error) { return cur, nil }
