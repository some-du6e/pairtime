// SPDX-License-Identifier: AGPL-3.0-or-later

package ledger

import (
	"bytes"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"testing"

	"github.com/some-du6e/pairtime/pkg/event"
)

func TestRTypeTable(t *testing.T) {
	// Spec 4.2's table, written out: the codes are frozen, so this list
	// must never follow a change to pkg/event's kindTable.
	spec42 := []string{
		0x01: "session.start", 0x02: "session.end", 0x03: "prompt.submit", 0x04: "agent.output",
		0x05: "agent.tool", 0x06: "agent.permission", 0x07: "agent.interrupt", 0x08: "agent.queue",
		0x09: "agent.stop", 0x0a: "agent.subagent", 0x0b: "editor.activity", 0x0c: "shell.command",
		0x0d: "git.change", 0x0e: "os.presence", 0x0f: "os.focus", 0x10: "os.power",
		0x11: "otel.active_time", 0x12: "cli.track",
	}
	cases := []struct {
		name  string
		check func(t *testing.T)
	}{
		{"0x01 to 0x12 hold the 18 kinds in spec 4.2's order", func(t *testing.T) {
			for code := 0x01; code <= 0x12; code++ {
				k, ok := RType(code).EventKind()
				if !ok || string(k) != spec42[code] {
					t.Errorf("rtype 0x%02x holds %q, spec 4.2 says %q", code, k, spec42[code])
				}
				if r, ok := RTypeOf(event.Kind(spec42[code])); !ok || r != RType(code) {
					t.Errorf("RTypeOf(%s) = 0x%02x, %v; want 0x%02x", spec42[code], uint8(r), ok, code)
				}
			}
		}},
		{"every kind pkg/event defines has an rtype, so none can reach the ledger without one", func(t *testing.T) {
			for _, k := range event.Kinds() {
				if _, ok := RTypeOf(k); !ok {
					t.Errorf("kind %s has no rtype; give it the next free code from 0x13", k)
				}
			}
		}},
		{"0x40 is a checkpoint and 0x41 an upload-failure note", func(t *testing.T) {
			if RTypeCheckpoint != 0x40 || RTypeUploadFailure != 0x41 {
				t.Errorf("checkpoint is 0x%02x and upload failure 0x%02x", uint8(RTypeCheckpoint), uint8(RTypeUploadFailure))
			}
			if RTypeCheckpoint.String() != "checkpoint" || RTypeUploadFailure.String() != "upload_failure" {
				t.Errorf("their names are %q and %q", RTypeCheckpoint, RTypeUploadFailure)
			}
		}},
		{"0x00, 0x13 to 0x3f and 0x42 to 0xff are reserved", func(t *testing.T) {
			for code := 0; code < 256; code++ {
				want := code >= 0x01 && code <= 0x12 || code == 0x40 || code == 0x41
				if RType(code).Known() != want {
					t.Errorf("rtype 0x%02x: Known() = %v, want %v", code, !want, want)
				}
			}
		}},
	}
	for _, c := range cases {
		t.Run(c.name, c.check)
	}
}

// localFieldsSet returns the JSON names of p's local fields that aren't
// zero.
func localFieldsSet(p event.Payload) []string {
	var set []string
	v := reflect.ValueOf(p).Elem()
	for i := 0; i < v.NumField(); i++ {
		f := v.Type().Field(i)
		if f.Tag.Get("pairtime") == "local" && !v.Field(i).IsZero() {
			set = append(set, strings.Split(f.Tag.Get("json"), ",")[0])
		}
	}
	return set
}

func TestEventRecordBodies(t *testing.T) {
	k := testKeys(t)
	for i, kind := range event.Kinds() {
		t.Run(string(kind)+": the body is the nonce, the shareable event, then f and c where the kind has them, and decoding then encoding it gives the same bytes", func(t *testing.T) {
			ev, err := sampleEvent(kind)
			if err != nil {
				t.Fatal(err)
			}
			ev.Seq = uint64(100 + i)
			if len(localFieldsSet(ev.Data)) == 0 && hasLocalFields(ev.Data) {
				t.Fatal("the sample sets none of its local fields, so it can't show they are cleared")
			}
			rec, err := EventRecord(k, ev, sampleContent(kind))
			if err != nil {
				t.Fatal(err)
			}
			n := k.Nonce(ev.Seq)
			if prefix := `{"n":"` + hex.EncodeToString(n[:]) + `","e":`; !bytes.HasPrefix(rec.Body, []byte(prefix)) {
				t.Errorf("body doesn't start with %s: %s", prefix, rec.Body)
			}
			shareable, err := ev.Shareable().MarshalJSON()
			if err != nil {
				t.Fatal(err)
			}
			if want := `{"n":"` + n.String() + `","e":` + string(shareable) + contentMembers(t, k, ev, sampleContent(kind)) + `}`; string(rec.Body) != want {
				t.Errorf("body is\n%s\nwant\n%s", rec.Body, want)
			}

			// Decode, then encode: the body type gives the stored bytes back.
			b, err := ParseBody(rec.Body)
			if err != nil {
				t.Fatal(err)
			}
			again, err := b.Encode()
			if err != nil {
				t.Fatal(err)
			}
			if !bytes.Equal(again, rec.Body) {
				t.Errorf("decoding and encoding the body gives\n%s\nnot\n%s", again, rec.Body)
			}
			// And through the event: decode it, write its record again.
			got, err := b.Event()
			if err != nil {
				t.Fatal(err)
			}
			if set := localFieldsSet(got.Data); len(set) != 0 {
				t.Errorf("the stored event keeps local fields %v", set)
			}
			rec2, err := EventRecord(k, got, sampleContent(kind))
			if err != nil {
				t.Fatal(err)
			}
			if !bytes.Equal(rec2.Body, rec.Body) || rec2.Header != rec.Header {
				t.Errorf("writing the decoded event again gives\n%s\nnot\n%s", rec2.Body, rec.Body)
			}

			h := rec.Header
			project, _ := ProjectDigest(ev.ProjectKey)
			session, _ := SessionDigest(ev.SessionKey)
			if r, _ := RTypeOf(kind); h.Seq() != ev.Seq || h.RType() != r || h.Project() != project || h.Session() != session {
				t.Errorf("header %x isn't s %d, rtype 0x%02x and the event's digests", h, ev.Seq, uint8(r))
			}
			if err := CheckBody(ev.DeviceID, rec.Header, rec.Body); err != nil {
				t.Errorf("the body disagrees with its own header: %v", err)
			}
			if err := k.CheckNonce(ev.Seq, rec.Body); err != nil {
				t.Error(err)
			}
		})
	}
}

func hasLocalFields(p event.Payload) bool {
	tp := reflect.TypeOf(p).Elem()
	for i := 0; i < tp.NumField(); i++ {
		if tp.Field(i).Tag.Get("pairtime") == "local" {
			return true
		}
	}
	return false
}

func TestOtherRecordBodies(t *testing.T) {
	k := testKeys(t)
	cases := []struct {
		name     string
		write    func() (Record, error)
		wantBody string
	}{
		{
			"a checkpoint stores its project, minute and state compacted, under the project's digest and no session",
			func() (Record, error) {
				return CheckpointRecord(k, 3, "p_example", 1795089600000, json.RawMessage("{ \"buckets\": [1, 2],\n  \"since\": 0 }"))
			},
			`{"project_key":"p_example","t":1795089600000,"state":{"buckets":[1,2],"since":0}}`,
		},
		{
			"an upload-failure note stores its time and reason, under no project and no session",
			func() (Record, error) {
				return UploadFailureRecord(k, 4, 1795089612345, "tls")
			},
			`{"t":1795089612345,"reason":"tls"}`,
		},
		{
			"a record's text is escaped the way encoding/json escapes it",
			func() (Record, error) {
				return CheckpointRecord(k, 5, "p_example", 1795089600000, json.RawMessage(`{"label":"<&>"}`))
			},
			// encoding/json writes <, & and > as six-character unicode
			// escapes; x5c is the backslash that starts each one.
			"{\"project_key\":\"p_example\",\"t\":1795089600000,\"state\":{\"label\":\"\x5cu003c\x5cu0026\x5cu003e\"}}",
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			rec, err := c.write()
			if err != nil {
				t.Fatal(err)
			}
			n := k.Nonce(rec.Header.Seq())
			if want := `{"n":"` + n.String() + `","e":` + c.wantBody + `}`; string(rec.Body) != want {
				t.Errorf("body is %s, want %s", rec.Body, want)
			}
			b, err := ParseBody(rec.Body)
			if err != nil {
				t.Fatal(err)
			}
			if again, err := b.Encode(); err != nil || !bytes.Equal(again, rec.Body) {
				t.Errorf("decoding and encoding gives %s, %v", again, err)
			}
			if err := rec.Header.Check(); err != nil {
				t.Error(err)
			}
			if err := CheckBody("d_example", rec.Header, rec.Body); err != nil {
				t.Error(err)
			}
		})
	}
}

func TestWritersRefuse(t *testing.T) {
	k := testKeys(t)
	event1 := func(kind event.Kind, edit func(*event.Event)) func() error {
		return func() error {
			ev, err := sampleEvent(kind)
			if err != nil {
				return err
			}
			ev.Seq = 1
			edit(&ev)
			_, err = EventRecord(k, ev, sampleContent(kind))
			return err
		}
	}
	cases := []struct {
		name  string
		write func() error
	}{
		{"an os.presence event with a project_key is refused (spec 4.3)", event1(event.KindOSPresence, func(e *event.Event) { e.ProjectKey = "p_9f2c" })},
		{"an os.focus event with a project_key is refused (spec 4.3)", event1(event.KindOSFocus, func(e *event.Event) { e.ProjectKey = "p_9f2c" })},
		{"an os.power event with a project_key is refused (spec 4.3)", event1(event.KindOSPower, func(e *event.Event) { e.ProjectKey = "p_9f2c" })},
		{"an event with seq 0 is refused; sequence numbers start at 1", event1(event.KindAgentOutput, func(e *event.Event) { e.Seq = 0 })},
		{"an event that fails Validate is refused", event1(event.KindPromptSubmit, func(e *event.Event) { e.Data.(*event.PromptSubmit).TypedLenEst = 7 })},
		{"an event whose project_key is a path is refused", event1(event.KindAgentTool, func(e *event.Event) { e.ProjectKey = "src/main.go" })},
		{"an event with no payload is refused", event1(event.KindAgentOutput, func(e *event.Event) { e.Data = nil })},
		{"a checkpoint with no project is refused", func() error {
			_, err := CheckpointRecord(k, 1, "", 1795089600000, json.RawMessage(`{}`))
			return err
		}},
		{"a checkpoint whose state isn't JSON is refused", func() error {
			_, err := CheckpointRecord(k, 1, "p_x", 1795089600000, json.RawMessage(`{"a":`))
			return err
		}},
		{"a checkpoint with a null state is refused", func() error {
			_, err := CheckpointRecord(k, 1, "p_x", 1795089600000, json.RawMessage(`null`))
			return err
		}},
		{"a checkpoint at a minute not divisible by 15 is refused (spec 4.7)", func() error {
			_, err := CheckpointRecord(k, 1, "p_x", 1795089660000, json.RawMessage(`{}`))
			return err
		}},
		{"an upload-failure note with a reason DESIGN 3.6 doesn't list is refused", func() error {
			_, err := UploadFailureRecord(k, 1, 1795089612345, "the wifi was down")
			return err
		}},
		{"a header of rtype 0x13, kept for a kind added later, is refused", func() error {
			_, err := NewHeader(1, 0x13, "", event.NoSession)
			return err
		}},
		{"a header of rtype 0x42, which version 1 never assigns, is refused", func() error {
			_, err := NewHeader(1, 0x42, "", event.NoSession)
			return err
		}},
		{"a header of rtype 0x00 is refused", func() error {
			_, err := NewHeader(1, 0x00, "", event.NoSession)
			return err
		}},
		{"a checkpoint header with a session is refused", func() error {
			_, err := NewHeader(1, RTypeCheckpoint, "p_x", "s_x")
			return err
		}},
		{"an upload-failure header with a project is refused", func() error {
			_, err := NewHeader(1, RTypeUploadFailure, "p_x", event.NoSession)
			return err
		}},
		{"a header with a session key that isn't an opaque key is refused", func() error {
			_, err := NewHeader(1, 0x03, "p_x", "a b")
			return err
		}},
		{"a header whose session key is empty is refused; no session is \"none\"", func() error {
			_, err := NewHeader(1, 0x03, "p_x", "")
			return err
		}},
		{"a header whose project key is 129 characters is refused", func() error {
			_, err := NewHeader(1, 0x03, strings.Repeat("p", 129), "s_x")
			return err
		}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if err := c.write(); err == nil {
				t.Error("written")
			}
		})
	}
}

func TestCheckBody(t *testing.T) {
	k := testKeys(t)
	ev, err := sampleEvent(event.KindPromptSubmit)
	if err != nil {
		t.Fatal(err)
	}
	ev.Seq = 7
	rec, err := EventRecord(k, ev, sampleContent(event.KindPromptSubmit))
	if err != nil {
		t.Fatal(err)
	}
	orig, err := ParseBody(rec.Body)
	if err != nil {
		t.Fatal(err)
	}
	// body returns rec's body with its event edited, keeping its f and c,
	// so each case below breaks one rule only.
	body := func(edit func(*event.Event)) []byte {
		e := ev
		payload := *ev.Data.(*event.PromptSubmit)
		e.Data = &payload
		edit(&e)
		raw, err := e.Shareable().MarshalJSON()
		if err != nil {
			t.Fatal(err)
		}
		out, err := Body{N: k.Nonce(7), E: raw, F: orig.F, C: orig.C}.Encode()
		if err != nil {
			t.Fatal(err)
		}
		return out
	}
	header := func(s uint64, r RType, project, session string) Header {
		var h Header
		binary.BigEndian.PutUint64(h[:8], s)
		h[8] = byte(r)
		p, _ := ProjectDigest(project)
		ss, _ := SessionDigest(session)
		copy(h[9:25], p[:])
		copy(h[25:], ss[:])
		return h
	}
	nonceHex := k.Nonce(7).String()
	n := `{"n":"` + nonceHex + `"`
	shareable, err := ev.Shareable().MarshalJSON()
	if err != nil {
		t.Fatal(err)
	}
	e := string(shareable)
	f, c := string(orig.F), `{"n":`+fmt.Sprint(orig.C.N)+`,"r":"`+orig.C.R.String()+`"}`
	checkpoint := header(7, RTypeCheckpoint, "p_x", event.NoSession)
	note := header(7, RTypeUploadFailure, "", event.NoSession)
	cases := []struct {
		name   string
		header Header
		body   []byte
		want   error
	}{
		{"a body that holds its header's event agrees with it", rec.Header, rec.Body, nil},
		{"a body whose event is another kind than its header's rtype disagrees", header(7, 0x04, "p_9f2c", "s_4be1f0"), rec.Body, ErrBodyHeader},
		{"a body whose seq isn't its header's s disagrees", rec.Header, body(func(e *event.Event) { e.Seq = 8 }), ErrBodyHeader},
		{"a body whose device_id isn't the chain's device disagrees (spec 4.5)", rec.Header, body(func(e *event.Event) { e.DeviceID = "d_other" }), ErrBodyHeader},
		{"a body whose project_key doesn't have its header's project digest disagrees", rec.Header, body(func(e *event.Event) { e.ProjectKey = "p_other" }), ErrBodyHeader},
		{"a body with no project_key under a header with a project disagrees", rec.Header, body(func(e *event.Event) { e.ProjectKey = "" }), ErrBodyHeader},
		{"a body whose session_key doesn't have its header's session digest disagrees", rec.Header, body(func(e *event.Event) { e.SessionKey = "s_other" }), ErrBodyHeader},
		{"a body whose record isn't an event, under an event rtype, disagrees", rec.Header, []byte(n + `,"e":{"stand_in":"prompt.submit","seq":7}}`), ErrBodyHeader},
		{"a body that doesn't start with the n member isn't a body", rec.Header, []byte(`{"e":` + e + `,"n":"` + nonceHex + `"}`), ErrBody},
		{"a body with a member part 1 doesn't define isn't a body", rec.Header, []byte(n + `,"e":` + e + `,"x":1}`), ErrBody},
		{"a body with data after the object isn't a body", rec.Header, append(bytes.Clone(rec.Body), []byte(`{}`)...), ErrBody},
		{"a body with a null record isn't a body", rec.Header, []byte(n + `,"e":null}`), ErrBody},
		{"a body whose nonce is uppercase hex isn't a body", rec.Header, []byte(`{"n":"` + strings.ToUpper(nonceHex) + `","e":{}}`), ErrBody},
		{"a body whose nonce runs past 64 hex digits isn't a body", rec.Header, []byte(`{"n":"` + nonceHex + `0","e":{}}`), ErrBody},
		{"a body with the event under E instead of e isn't a body; names match exactly", rec.Header, []byte(n + `,"E":` + e + `,"f":` + f + `,"c":` + c + `}`), ErrBody},
		{"a body with e twice isn't a body, whichever one a reader keeps", rec.Header, []byte(n + `,"e":{},"e":` + e + `,"f":` + f + `,"c":` + c + `}`), ErrBody},
		{"a body with c and C isn't a body", rec.Header, []byte(n + `,"e":` + e + `,"f":` + f + `,"c":` + c + `,"C":` + c + `}`), ErrBody},
		{"a body whose event holds a member name with a capital isn't a body", rec.Header, []byte(n + `,"e":` + strings.Replace(e, `"seq"`, `"SEQ"`, 1) + `,"f":` + f + `,"c":` + c + `}`), ErrBody},
		{"a body whose event repeats a member isn't a body", rec.Header, []byte(n + `,"e":` + strings.Replace(e, `"seq":7`, `"seq":7,"seq":8`, 1) + `,"f":` + f + `,"c":` + c + `}`), ErrBody},
		{"a body with f after c isn't a body; the members come in the table's order", rec.Header, []byte(n + `,"e":` + e + `,"c":` + c + `,"f":` + f + `}`), ErrBody},
		{"a body that isn't valid UTF-8 isn't a body", rec.Header, []byte(n + `,"e":` + strings.Replace(e, `"p_9f2c"`, "\"p_9f2c\xff\"", 1) + `,"f":` + f + `,"c":` + c + `}`), ErrBody},
		{"a body under rtype 0x13, a kind added later, is reported as unknown", header(7, 0x13, "", event.NoSession), []byte(n + `,"e":{}}`), ErrUnknownRType},
		{"a body under rtype 0x42, which version 1 never assigns, is a header error", header(7, 0x42, "", event.NoSession), []byte(n + `,"e":{}}`), ErrHeader},
		{"a checkpoint body that names its header's project agrees (spec 4.7)", checkpoint, []byte(n + `,"e":{"project_key":"p_x","t":1795089600000,"state":{"any":"state"}}}`), nil},
		{"a checkpoint body that names another project disagrees", checkpoint, []byte(n + `,"e":{"project_key":"p_y","t":1795089600000,"state":{}}}`), ErrBodyHeader},
		{"a checkpoint body with only a state isn't a body", checkpoint, []byte(n + `,"e":{"any":"state"}}`), ErrBody},
		{"a checkpoint body with a member besides project_key, t and state isn't a body", checkpoint, []byte(n + `,"e":{"project_key":"p_x","t":1795089600000,"state":{},"x":1}}`), ErrBody},
		{"a checkpoint body whose members are out of order isn't a body", checkpoint, []byte(n + `,"e":{"t":1795089600000,"project_key":"p_x","state":{}}}`), ErrBody},
		{"a checkpoint at a minute not divisible by 15 isn't a body", checkpoint, []byte(n + `,"e":{"project_key":"p_x","t":1795089660000,"state":{}}}`), ErrBody},
		{"an upload-failure note with its time and a listed reason agrees", note, []byte(n + `,"e":{"t":1795089612345,"reason":"http_429"}}`), nil},
		{"an upload-failure note with a reason DESIGN 3.6 doesn't list isn't a body", note, []byte(n + `,"e":{"t":1795089612345,"reason":"wifi"}}`), ErrBody},
		{"an upload-failure note with free text besides its reason isn't a body", note, []byte(n + `,"e":{"t":1795089612345,"reason":"dns","detail":"x"}}`), ErrBody},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			err := CheckBody(ev.DeviceID, c.header, c.body)
			switch {
			case c.want == nil && err != nil:
				t.Errorf("CheckBody: %v", err)
			case c.want != nil && !errors.Is(err, c.want):
				t.Errorf("CheckBody gives %v, want an error wrapping %v", err, c.want)
			}
		})
	}
}

func TestCheckNonce(t *testing.T) {
	k := testKeys(t)
	rec, err := UploadFailureRecord(k, 9, testT, "dns")
	if err != nil {
		t.Fatal(err)
	}
	other, err := DeriveKeys(bytes.Repeat([]byte{0xff}, KeySize))
	if err != nil {
		t.Fatal(err)
	}
	cases := []struct {
		name string
		keys *Keys
		s    uint64
		body []byte
		ok   bool
	}{
		{"a body carries its own record's nonce", k, 9, rec.Body, true},
		{"a body doesn't carry another record's nonce", k, 10, rec.Body, false},
		{"a body doesn't carry the nonce another device's K_leaf gives", other, 9, rec.Body, false},
		{"a body cut short inside its nonce carries none", k, 9, rec.Body[:20], false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			err := c.keys.CheckNonce(c.s, c.body)
			if c.ok != (err == nil) {
				t.Errorf("CheckNonce = %v", err)
			}
			if err != nil && !errors.Is(err, ErrNonce) {
				t.Errorf("%v doesn't wrap ErrNonce", err)
			}
		})
	}
}

func TestKeysAreNeverPrinted(t *testing.T) {
	k := testKeys(t)
	leaf, tok, feat, vault := k.LeafKey(), k.TokenKey(), k.FeatKey(), k.VaultKey()
	secrets := [][]byte{leaf[:], tok[:], feat[:], vault[:]}
	var cases []struct {
		name string
		out  string
	}
	for _, verb := range []string{"%v", "%+v", "%#v", "%s", "%x", "%X", "%d", "%q"} {
		cases = append(cases,
			struct{ name, out string }{"Keys formats as a placeholder under " + verb, fmt.Sprintf(verb, *k)},
			struct{ name, out string }{"*Keys formats as a placeholder under " + verb, fmt.Sprintf(verb, k)},
		)
	}
	js, err := json.Marshal(k)
	if err != nil {
		t.Fatal(err)
	}
	cases = append(cases, struct{ name, out string }{"encoding/json writes Keys as {}", string(js)})
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if c.out != "ledger.Keys{redacted}" && c.out != "{}" {
				t.Errorf("printed %q", c.out)
			}
			for _, s := range secrets {
				if strings.Contains(strings.ToLower(c.out), hex.EncodeToString(s)) || strings.Contains(c.out, fmt.Sprint(s[:4])) {
					t.Errorf("output %q shows key bytes", c.out)
				}
			}
		})
	}
}
