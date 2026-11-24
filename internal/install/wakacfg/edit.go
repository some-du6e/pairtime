// SPDX-License-Identifier: AGPL-3.0-or-later

package wakacfg

import (
	"bytes"
	"errors"
	"fmt"
	"slices"
	"strconv"
	"strings"
	"time"
)

// The sections the package reads and writes, as wakatime-cli names them.
const (
	apiURLs  = "api_urls"
	settings = "settings"
	internal = "internal"
)

// ownLine reports whether e is a line of pairtime's: one of Spellings
// under [api_urls] whose value is the agent's URL on a port and a key in
// WakaTime's format, as wakatime-cli splits it, and returns them.
func ownLine(e entry) (port int, key string, ok bool) {
	if e.kind != pair || !isSection(e.section, apiURLs) || !slices.Contains(Spellings, strings.ToLower(e.key)) {
		return 0, "", false
	}
	u, k, found := strings.Cut(e.value, "|")
	if !found {
		return 0, "", false
	}
	k = strings.TrimSpace(k)
	port, ok = agentPort(strings.TrimSpace(u))
	if !ok || !ValidKey(k) {
		return 0, "", false
	}
	return port, k, true
}

// agentPort reports whether u is the agent's endpoint, URL of a port, and
// returns the port.
func agentPort(u string) (int, bool) {
	rest, ok := strings.CutPrefix(u, "http://127.0.0.1:")
	if !ok {
		return 0, false
	}
	p, ok := strings.CutSuffix(rest, "/api/v1")
	if !ok || p == "" || len(p) > 5 || p[0] == '0' || strings.Trim(p, "0123456789") != "" {
		return 0, false
	}
	n, err := strconv.Atoi(p)
	if err != nil || n > 65535 {
		return 0, false
	}
	return n, true
}

// line returns the first line of pairtime's for key.
func (f *file) line(key string) (entry, bool) {
	for _, e := range f.entries {
		if _, k, ok := ownLine(e); ok && k == key {
			return e, true
		}
	}
	return entry{}, false
}

// last returns the index of the entry wakatime-cli reads for key in
// section, the last of them, or -1 when there is none.
func (f *file) last(section, key string) int {
	at := -1
	for i, e := range f.entries {
		if e.kind == pair && isSection(e.section, section) && strings.ToLower(e.key) == key {
			at = i
		}
	}
	return at
}

// madeMark is the comment a config pairtime makes starts with (Make). It
// tells uninstall and a second install that the config is pairtime's when
// no record names its key: a [settings] api_url on 127.0.0.1 alone may be
// the teen's own server, such as Wakapi.
const madeMark = "made by pairtime install; pairtime uninstall takes it out"

// mark returns the index of the comment that is madeMark, with either
// comment character and any space around it, or -1.
func (f *file) mark() int {
	for i, e := range f.entries {
		if e.kind != comment {
			continue
		}
		t := strings.TrimSpace(string(f.data[e.start:e.end]))
		if strings.TrimSpace(t[1:]) == madeMark {
			return i
		}
	}
	return -1
}

// ownConfig returns the entries of a config pairtime made (Make) for key:
// the [settings] api_url that is the agent's endpoint, and the api_key
// that is key, or, when key is "", one in WakaTime's format in a config
// with pairtime's mark.
func (f *file) ownConfig(key string) (url, apiKey int, ok bool) {
	url, apiKey = f.last(settings, "api_url"), f.last(settings, "api_key")
	if url < 0 || apiKey < 0 || (key == "" && f.mark() < 0) {
		return -1, -1, false
	}
	if _, isAgent := agentPort(f.entries[url].value); !isAgent {
		return -1, -1, false
	}
	if k := f.entries[apiKey].value; (key == "" && !ValidKey(k)) || (key != "" && k != key) {
		return -1, -1, false
	}
	return url, apiKey, true
}

// proxy reports whether the config sets a proxy: a [settings] proxy with
// a value, as wakatime-cli v2.28.4 reads it (pkg/params loadProxyURL),
// quotes trimmed. wakatime-cli sends every destination's requests through
// that one proxy, pairtime's at 127.0.0.1 too (pkg/api WithProxy, whose
// http.ProxyURL makes no exception for loopback), so the proxy gets them
// in plain text, and they reach the agent only if the proxy passes them
// on to 127.0.0.1 on its own computer.
func (f *file) proxy() bool {
	i := f.last(settings, "proxy")
	return i >= 0 && strings.Trim(f.entries[i].value, `"'`) != ""
}

// imports reports whether the config imports another, a [settings]
// import_cfg with a value, which wakatime-cli reads over it (pkg/ini
// ImportFilePath).
func (f *file) imports() bool {
	i := f.last(settings, "import_cfg")
	return i >= 0 && strings.TrimSpace(f.entries[i].value) != ""
}

// Mentions reports whether data names the agent's endpoint anywhere, as
// pairtime's line and a config pairtime made do, whether or not the
// package can read the file. A file it can't read that doesn't is one
// with nothing of pairtime's in it.
func Mentions(data []byte) bool { return agentURL().Match(data) }

// LineKey returns the key of the first line of pairtime's under
// [api_urls] in data.
func LineKey(data []byte) (string, bool) {
	f, err := parse(data)
	if err != nil {
		return "", false
	}
	for _, e := range f.entries {
		if _, k, ok := ownLine(e); ok {
			return k, true
		}
	}
	return "", false
}

// Key returns the key of the first line of pairtime's in data, under
// [api_urls] or as the api_key of a config pairtime made, so that an
// install that has lost its record of the key, with pairtime's data
// directory, keeps the line's own rather than adding a second line.
func Key(data []byte) (string, bool) {
	f, err := parse(data)
	if err != nil {
		return "", false
	}
	for _, e := range f.entries {
		if _, k, ok := ownLine(e); ok {
			return k, true
		}
	}
	if _, k, ok := f.ownConfig(""); ok {
		return f.entries[k].value, true
	}
	return "", false
}

// Make returns the config pairtime makes for someone who has none:
// wakatime-cli's api_url is the agent's endpoint, with pairtime's key
// (DESIGN 5.6, "No WakaTime config"), after pairtime's mark (madeMark).
func Make(key string, port int) ([]byte, error) {
	if err := checkArgs(key, port); err != nil {
		return nil, err
	}
	return []byte("; " + madeMark + "\n[settings]\napi_url = " + URL(port) + "\napi_key = " + key + "\n"), nil
}

// Add returns data, a config that is there, with pairtime's line for key
// and port under [api_urls], and nothing else changed (DESIGN 5.6). It
// adds the line after the last key of the first [api_urls] section, with
// the first of Spellings no key there has, or adds the section at the end
// of the file. When the line is there already, or data is a config
// pairtime made, it changes only the port, as SetPort does. It refuses a
// config that sets a proxy (ErrProxy).
func Add(data []byte, key string, port int) ([]byte, error) {
	if err := checkArgs(key, port); err != nil {
		return nil, err
	}
	f, err := parse(data)
	if err != nil {
		return nil, err
	}
	if f.proxy() {
		return nil, ErrProxy
	}
	if f.imports() {
		return nil, ErrImport
	}
	if _, ok := f.line(key); ok {
		return SetPort(data, key, port)
	}
	if _, _, ok := f.ownConfig(key); ok {
		return SetPort(data, key, port)
	}
	if _, _, ok := f.ownConfig(""); ok {
		return nil, ErrNotOwn
	}
	spelling, err := f.spelling()
	if err != nil {
		return nil, err
	}
	out, err := f.put(apiURLs, spelling+" = "+URL(port)+"|"+key)
	if err != nil {
		return nil, err
	}
	if err := checkLine(data, out, key, port); err != nil {
		return nil, err
	}
	return out, nil
}

// spelling returns the first of Spellings that no key under [api_urls]
// has, as wakatime-cli reads keys, in lower case.
func (f *file) spelling() (string, error) {
	used := map[string]bool{}
	for _, e := range f.pairs() {
		if isSection(e.section, apiURLs) {
			used[strings.ToLower(e.key)] = true
		}
	}
	for _, s := range Spellings {
		if !used[s] {
			return s, nil
		}
	}
	return "", errors.New("already has a line under [api_urls] for each of the patterns pairtime's line can take, .+, ^.*$ and (?s).*")
}

// SetPort returns data with pairtime's line for key naming port, and
// nothing else changed: the agent calls it when it listens on another port
// than the line names (DESIGN 5.6, "The port"). In a config pairtime made,
// the line is [settings]'s api_url. It returns data as it is when the line
// names port already, and ErrNoLine when there is no line for key.
func SetPort(data []byte, key string, port int) ([]byte, error) {
	if err := checkArgs(key, port); err != nil {
		return nil, err
	}
	f, err := parse(data)
	if err != nil {
		return nil, err
	}
	if e, ok := f.line(key); ok {
		if p, _, _ := ownLine(e); p == port {
			return data, nil
		}
		out := f.replace(e, e.key+" = "+URL(port)+"|"+key)
		if err := checkLine(data, out, key, port); err != nil {
			return nil, err
		}
		return out, nil
	}
	if u, _, ok := f.ownConfig(key); ok {
		e := f.entries[u]
		if p, _ := agentPort(e.value); p == port {
			return data, nil
		}
		out := f.replace(e, e.key+" = "+URL(port))
		if err := checkOwn(data, out, key, port); err != nil {
			return nil, err
		}
		return out, nil
	}
	return nil, ErrNoLine
}

// Section is what install put in a config around pairtime's line when the
// config had no [api_urls] section, for Remove to take out with the line.
// Add writes the blank line and the line break only where the file needs
// them (Added).
type Section struct {
	// Header is the [api_urls] header, which goes once no key is left
	// under it.
	Header bool
	// Blank is the blank line before the header, which goes with it, and
	// Break the line break that ended the file's last line before them,
	// which goes when what is left ends the file.
	Blank, Break bool
}

// Added returns what Add puts around pairtime's line in base, a config
// with no [api_urls] section: the section, a blank line before it unless
// base ends with one or holds nothing but white space, and a line break
// when base's last line has none.
func Added(base []byte) Section {
	s := Section{Header: true}
	f, err := parse(base)
	if err != nil {
		s.Blank = true
		return s
	}
	body := bytes.TrimPrefix(base, bom)
	s.Blank = len(bytes.TrimSpace(body)) > 0 && f.entries[len(f.entries)-1].kind != blank
	s.Break = len(body) > 0 && body[len(body)-1] != '\n'
	return s
}

// Remove returns data, a config install changed and that changed since,
// without pairtime's lines for key, as uninstall takes them out (DESIGN
// 5.6, "Uninstall"). With key "", it takes out every line it recognizes
// as pairtime's, for an uninstall with no record of the key. made says
// install made the config, so its api_url and api_key are pairtime's too
// (Make), and sec what install put around the line in a config that had
// no [api_urls] section, which goes with it. gone reports that data is a
// config pairtime made, with the agent's api_url and the key's api_key,
// and that nothing else is left of it, so uninstall deletes the file. A
// file that only held pairtime's line stays, empty, since install doesn't
// make one of that shape: the file was there before.
func Remove(data []byte, key string, made bool, sec Section) (out []byte, gone bool, err error) {
	f, err := parse(data)
	if err != nil {
		return nil, false, err
	}
	drop := map[int]bool{}
	for i, e := range f.entries {
		if _, k, ok := ownLine(e); ok && (key == "" || k == key) {
			drop[i] = true
		}
	}
	own := false
	if made {
		if u, k, ok := f.ownConfig(key); ok {
			drop[u], drop[k] = true, true
			if m := f.mark(); m >= 0 {
				drop[m] = true
			}
			own = true
		}
	}
	dropped := false // an [api_urls] header went
	if sec.Header {
		for h, e := range f.entries {
			if e.kind != header || !isSection(e.section, apiURLs) || !f.emptied(h, drop) {
				continue
			}
			drop[h], dropped = true, true
			if sec.Blank && h > 0 && f.entries[h-1].kind == blank {
				drop[h-1] = true
			}
		}
	}
	out = f.without(drop)
	if sec.Break && dropped && f.endsDropped(drop) {
		out = out[:len(out)-len(lineBreak(out))]
	}
	a, err := parse(out)
	if err != nil {
		return nil, false, fmt.Errorf("taking pairtime's line out would leave a file wakatime-cli reads differently (%w)", err)
	}
	var kept []entry
	for i, e := range f.entries {
		if e.kind == pair && !drop[i] {
			kept = append(kept, e)
		}
	}
	if !samePairs(a.pairs(), kept) {
		return nil, false, errors.New("taking pairtime's line out would change how wakatime-cli reads another line")
	}
	if own {
		gone = true
		for _, e := range a.entries {
			if e.kind == pair || e.kind == comment || (e.kind == header && !isSection(e.section, settings)) {
				gone = false
			}
		}
	}
	return out, gone, nil
}

// endsDropped reports whether the entries in drop end the file, after an
// entry that stays: what is left then ends with that entry's line break.
func (f *file) endsDropped(drop map[int]bool) bool {
	n := len(f.entries)
	if n == 0 || !drop[n-1] {
		return false
	}
	for i := n - 1; i >= 0; i-- {
		if !drop[i] {
			return true
		}
	}
	return false
}

// emptied reports whether the section whose header is entry h holds no key
// and no comment once the entries in drop are gone. pairtime writes no
// comment there, so one is the teen's, and keeps the section.
func (f *file) emptied(h int, drop map[int]bool) bool {
	for j := h + 1; j < len(f.entries) && f.entries[j].kind != header; j++ {
		if k := f.entries[j].kind; (k == pair || k == comment) && !drop[j] {
			return false
		}
	}
	return true
}

// without returns the file without the entries in drop. An entry that ends
// the file without a line break takes the line break before it along, so
// that the file still ends without one.
func (f *file) without(drop map[int]bool) []byte {
	var b bytes.Buffer
	pos := 0
	for i, e := range f.entries {
		if !drop[i] {
			continue
		}
		start := e.start
		if e.end == len(f.data) && f.data[len(f.data)-1] != '\n' {
			start -= len(lineBreakBefore(f.data[:start]))
		}
		if start < pos {
			start = pos
		}
		b.Write(f.data[pos:start])
		pos = e.end
	}
	b.Write(f.data[pos:])
	return b.Bytes()
}

// Watermark returns data, the bytes of wakatime-internal.cfg or nil when
// it isn't there, with [internal] ai_logs_last_parsed_at set to now, as
// hackatime-setup seeds it (HC 1.5): wakatime-cli's AI sync, finding no
// watermark, reads every agent transcript since 2025-02-24 and sends what
// it finds as new time. A file that has a watermark stays as it is.
func Watermark(data []byte, now time.Time) ([]byte, error) {
	text := "ai_logs_last_parsed_at = " + now.UTC().Format(time.RFC3339)
	if len(data) == 0 {
		return []byte("[" + internal + "]\n" + text + "\n"), nil
	}
	f, err := parse(data)
	if err != nil {
		return nil, err
	}
	var out []byte
	switch i := f.last(internal, "ai_logs_last_parsed_at"); {
	case i >= 0 && f.entries[i].value != "":
		return data, nil
	case i >= 0:
		out = f.replace(f.entries[i], f.entries[i].key+" = "+now.UTC().Format(time.RFC3339))
	default:
		if out, err = f.put(internal, text); err != nil {
			return nil, err
		}
	}
	a, err := parse(out)
	if err != nil {
		return nil, fmt.Errorf("seeding the watermark would make a file wakatime-cli reads differently (%w)", err)
	}
	var kept []entry
	for _, e := range a.pairs() {
		if !(isSection(e.section, internal) && strings.ToLower(e.key) == "ai_logs_last_parsed_at") {
			kept = append(kept, e)
		}
	}
	var was []entry
	for _, e := range f.pairs() {
		if !(isSection(e.section, internal) && strings.ToLower(e.key) == "ai_logs_last_parsed_at") {
			was = append(was, e)
		}
	}
	if !samePairs(kept, was) {
		return nil, errors.New("seeding the watermark would change how wakatime-cli reads another line")
	}
	return out, nil
}

// put returns the file with text, a key and its value, added as the last
// key of the first section of that name, or in a new section at the end of
// the file when there is none. It refuses a file whose sections of that
// name are spelled in more than one way, which viper would read as one of
// them only, and a section where each place for the line comes before an
// indented line, which would become part of the line's value.
func (f *file) put(section, text string) ([]byte, error) {
	first, spelled := -1, ""
	for i, e := range f.entries {
		if e.kind != header || !isSection(e.section, section) {
			continue
		}
		if first < 0 {
			first, spelled = i, e.section
		} else if e.section != spelled {
			return nil, fmt.Errorf("has a [%s] section spelled both %q and %q, which wakatime-cli reads as only one of them", section, spelled, e.section)
		}
	}
	if first < 0 {
		return f.appendSection(section, text), nil
	}
	at, ok := f.insertAt(first)
	if !ok {
		return nil, fmt.Errorf("has an indented line after each place in [%s] where pairtime's line could go, which wakatime-cli would read as part of pairtime's line", spelled)
	}
	return f.insert(at, text), nil
}

// insertAt returns where a key goes in the section whose header is entry
// h: after its last key, or after its header when it has none, or failing
// that at the section's end, and only where the line that follows isn't
// indented, since an indented line would go on with the key's value.
func (f *file) insertAt(h int) (int, bool) {
	last, end := -1, h
	for j := h + 1; j < len(f.entries) && f.entries[j].kind != header; j++ {
		end = j
		if f.entries[j].kind == pair {
			last = j
		}
	}
	tries := []int{f.entries[h].end, f.entries[end].end}
	if last >= 0 {
		tries[0] = f.entries[last].end
	}
	for _, at := range tries {
		if !indented(f.data[at:]) {
			return at, true
		}
	}
	return 0, false
}

// insert returns the file with text added as a line at at, the start of a
// line. At the end of a file whose last line has no line break, the line
// goes after a new line break and leaves the file without a final one.
func (f *file) insert(at int, text string) []byte {
	var b bytes.Buffer
	b.Write(f.data[:at])
	if at == len(f.data) && at > 0 && f.data[at-1] != '\n' {
		b.WriteString(f.eol + text)
	} else {
		b.WriteString(text + f.eol)
	}
	b.Write(f.data[at:])
	return b.Bytes()
}

// appendSection returns the file with a new section holding text at its
// end, after a blank line unless the file is blank or ends with one.
func (f *file) appendSection(section, text string) []byte {
	var b bytes.Buffer
	b.Write(f.data)
	body := bytes.TrimPrefix(f.data, bom)
	if len(body) > 0 && body[len(body)-1] != '\n' {
		b.WriteString(f.eol)
	}
	if len(bytes.TrimSpace(body)) > 0 && f.entries[len(f.entries)-1].kind != blank {
		b.WriteString(f.eol)
	}
	b.WriteString("[" + section + "]" + f.eol + text + f.eol)
	return b.Bytes()
}

// replace returns the file with entry e, a key on one line, rewritten as
// text with e's own line break.
func (f *file) replace(e entry, text string) []byte {
	var b bytes.Buffer
	b.Write(f.data[:e.start])
	b.WriteString(text + lineBreak(f.data[e.start:e.end]))
	b.Write(f.data[e.end:])
	return b.Bytes()
}

// lineBreak returns the line break text ends with, "" for none.
func lineBreak(text []byte) string {
	switch {
	case bytes.HasSuffix(text, []byte("\r\n")):
		return "\r\n"
	case bytes.HasSuffix(text, []byte("\n")):
		return "\n"
	}
	return ""
}

// lineBreakBefore returns the line break data ends with, "" for none.
func lineBreakBefore(data []byte) string { return lineBreak(data) }

// checkLine checks after, which a change made of before: it reads as
// before does but for pairtime's lines for key, of which it has exactly
// one, written as lineFormat has it and naming port.
func checkLine(before, after []byte, key string, port int) error {
	b, err := parse(before)
	if err != nil {
		return err
	}
	a, err := parse(after)
	if err != nil {
		return fmt.Errorf("pairtime's line would make a file wakatime-cli reads differently (%w)", err)
	}
	var ours, kept []entry
	for _, e := range a.pairs() {
		if _, k, ok := ownLine(e); ok && k == key {
			ours = append(ours, e)
		} else {
			kept = append(kept, e)
		}
	}
	if len(ours) != 1 {
		return fmt.Errorf("pairtime's line would be there %d times, not once", len(ours))
	}
	text := strings.TrimRight(string(after[ours[0].start:ours[0].end]), "\r\n")
	if p, _, _ := ownLine(ours[0]); p != port || !lineFormat().MatchString(text) {
		return fmt.Errorf("pairtime's line would read %q, which isn't the line it means", text)
	}
	var was []entry
	for _, e := range b.pairs() {
		if _, k, ok := ownLine(e); !ok || k != key {
			was = append(was, e)
		}
	}
	if !samePairs(kept, was) {
		return errors.New("pairtime's line would change how wakatime-cli reads another line")
	}
	return nil
}

// checkOwn checks after, which SetPort made of before, a config pairtime
// made: its api_url is the agent's on port, and every other key reads as
// it did.
func checkOwn(before, after []byte, key string, port int) error {
	b, err := parse(before)
	if err != nil {
		return err
	}
	a, err := parse(after)
	if err != nil {
		return fmt.Errorf("the new port would make a file wakatime-cli reads differently (%w)", err)
	}
	bu, _, okB := b.ownConfig(key)
	au, _, okA := a.ownConfig(key)
	if !okB || !okA {
		return errors.New("the new port would leave a config that isn't pairtime's")
	}
	if p, _ := agentPort(a.entries[au].value); p != port {
		return fmt.Errorf("the config would name port %d, not %d", p, port)
	}
	var was, kept []entry
	for i, e := range b.entries {
		if e.kind == pair && i != bu {
			was = append(was, e)
		}
	}
	for i, e := range a.entries {
		if e.kind == pair && i != au {
			kept = append(kept, e)
		}
	}
	if !samePairs(kept, was) {
		return errors.New("the new port would change how wakatime-cli reads another line")
	}
	return nil
}

// samePairs reports whether a and b hold the same keys and values, in the
// same sections and order, as wakatime-cli reads them.
func samePairs(a, b []entry) bool {
	return slices.EqualFunc(a, b, func(x, y entry) bool {
		return x.section == y.section && x.key == y.key && x.value == y.value
	})
}

// State is what a config says of wakatime-cli's two destinations, for
// pairtime doctor (Inspect).
type State struct {
	// Line says pairtime's line for the key is there, under [api_urls] or,
	// in a config pairtime made, as its api_url, and Port is the port it
	// names.
	Line bool
	Port int
	// Own says the config is one pairtime made, where the agent is
	// wakatime-cli's only destination (Make).
	Own bool
	// Server is the api_url wakatime-cli sends to besides pairtime:
	// [settings]'s, or WakaTime's own when the config names none (DefaultURL).
	// It is "" in a config pairtime made.
	Server string
	// ServerKey says [settings] has a key for the server, as wakatime-cli's
	// loadAPIKey finds one: the first of api_key and apikey that is there,
	// though an empty one hides the other, then an api_key_vault_cmd.
	// Without one, or WAKATIME_API_KEY, wakatime-cli sends nothing to anyone.
	ServerKey bool
	// BadKey names where the config has a key wakatime-cli refuses for its
	// format, "" when it has none: [settings]'s api_key or apikey, or a
	// key under [api_urls] or [project_api_key]. Any of them fails
	// wakatime-cli's whole run, so it sends nothing to anyone.
	BadKey string
	// Proxy says the config sets a proxy, which wakatime-cli sends
	// pairtime's line through too, so that the line reaches the agent only
	// if the proxy passes it on (Add refuses such a config).
	Proxy bool
	// Import says the config imports another, which wakatime-cli reads
	// over it, so that a line there can take pairtime's place (Add refuses
	// such a config).
	Import bool
}

// Inspect reads data, a config, for pairtime's line for key.
func Inspect(data []byte, key string) (State, error) {
	f, err := parse(data)
	if err != nil {
		return State{}, err
	}
	s := State{Proxy: f.proxy(), Import: f.imports()}
	if u, _, ok := f.ownConfig(key); ok {
		s.Line, s.Own = true, true
		s.Port, _ = agentPort(f.entries[u].value)
		return s, nil
	}
	if e, ok := f.line(key); ok {
		s.Line = true
		s.Port, _, _ = ownLine(e)
	}
	s.Server = DefaultURL
	if i := f.last(settings, "api_url"); i >= 0 && f.entries[i].value != "" {
		s.Server = f.entries[i].value
	}
	for _, k := range []string{"api_key", "apikey"} {
		i := f.last(settings, k)
		if i < 0 {
			continue
		}
		if v := strings.Trim(f.entries[i].value, `"'`); v != "" {
			s.ServerKey = true
			if !ValidKey(v) {
				s.BadKey = "[settings] " + k
			}
		}
		break
	}
	if i := f.last(settings, "api_key_vault_cmd"); !s.ServerKey && i >= 0 && f.entries[i].value != "" {
		s.ServerKey = true
	}
	if s.BadKey == "" {
		s.BadKey = f.badPatternKey()
	}
	return s, nil
}

// badPatternKey returns the section, [api_urls] or [project_api_key], in
// which the config has a key wakatime-cli refuses for its format, and ""
// when it has none. wakatime-cli reads each key's last value, as viper
// keeps them, and an [api_urls] value's key is what follows its first |;
// one without a | has [settings]'s, which Inspect checks
// (loadAPIURLPatterns, loadAPIKeyPatterns).
func (f *file) badPatternKey() string {
	for _, sec := range []string{apiURLs, "project_api_key"} {
		values := map[string]string{}
		var keys []string
		for _, e := range f.pairs() {
			if !isSection(e.section, sec) {
				continue
			}
			k := strings.ToLower(e.key)
			if _, ok := values[k]; !ok {
				keys = append(keys, k)
			}
			values[k] = e.value
		}
		for _, k := range keys {
			v := values[k]
			if sec == apiURLs {
				_, key, found := strings.Cut(v, "|")
				if !found {
					continue
				}
				v = strings.TrimSpace(key)
			}
			if !ValidKey(v) {
				return "[" + sec + "]"
			}
		}
	}
	return ""
}
