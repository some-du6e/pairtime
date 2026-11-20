// SPDX-License-Identifier: AGPL-3.0-or-later

package ledger

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/some-du6e/pairtime/pkg/event"
	"github.com/some-du6e/pairtime/pkg/ledger/features"
)

// contentMembers returns the "f" and "c" members, each with its leading
// comma, that the body of ev's record carries after "e" with content c:
// f on the four kinds of spec 10.7, c on the three of spec 10.1.
func contentMembers(t *testing.T, k *Keys, ev event.Event, c Content) string {
	t.Helper()
	fk := k.Features()
	var f *features.F
	switch ev.Kind() {
	case event.KindPromptSubmit, event.KindCLITrack:
		p, err := fk.Prompt(string(c.Text), c.Class)
		if c.NoText {
			p, err = features.Textless(c.Class)
		}
		if err != nil {
			t.Fatal(err)
		}
		f = &p
	case event.KindAgentTool:
		tf, err := fk.Tool(c.Edits, c.Command)
		if err != nil {
			t.Fatal(err)
		}
		f = &tf
	case event.KindShellCommand:
		sf := fk.Shell(c.Command)
		f = &sf
	}
	var out strings.Builder
	if f != nil {
		js, err := json.Marshal(f)
		if err != nil {
			t.Fatal(err)
		}
		out.WriteString(`,"f":` + string(js))
	}
	text, commits := c.Text, ev.Kind() == event.KindPromptSubmit || ev.Kind() == event.KindCLITrack
	if p, ok := ev.Data.(*event.AgentOutput); ok && p.IsFinal {
		text, commits = CommittedMessage(c.Text), true
	}
	commits = commits && !c.NoText
	if commits {
		cm, err := k.Commit(ev.Seq, text)
		if err != nil {
			t.Fatal(err)
		}
		fmt.Fprintf(&out, `,"c":{"n":%d,"r":"%s"}`, cm.N, cm.R)
	}
	return out.String()
}

func TestEventRecordContent(t *testing.T) {
	k := testKeys(t)
	cases := []struct {
		name string
		kind event.Kind
		edit func(*event.Event, *Content)
		// wantF and wantC say whether the body has "f" and "c".
		wantF, wantC bool
	}{
		{"a prompt carries f and commits its text", event.KindPromptSubmit, nil, true, true},
		{"an empty prompt carries f and commits the empty text", event.KindPromptSubmit, func(e *event.Event, c *Content) {
			p := e.Data.(*event.PromptSubmit)
			p.LenChars, p.PastedChars, p.TypedLenEst = 0, 0, 0
			c.Text = nil
		}, true, true},
		{"a track label carries the prompt features and commits its text", event.KindCLITrack, nil, true, true},
		{"a final agent message commits its text and carries no f", event.KindAgentOutput, nil, false, true},
		{"an agent message that isn't final commits nothing", event.KindAgentOutput, func(e *event.Event, c *Content) {
			e.Data.(*event.AgentOutput).IsFinal = false
			c.Text = nil
		}, false, false},
		{"an agent.tool record carries f and commits nothing", event.KindAgentTool, nil, true, false},
		{"a read carries an f that holds only v", event.KindAgentTool, func(_ *event.Event, c *Content) { *c = Content{} }, true, false},
		{"a shell.command record carries f", event.KindShellCommand, nil, true, false},
		{"a shell.command with no command line carries an f that holds only v", event.KindShellCommand, func(_ *event.Event, c *Content) { *c = Content{} }, true, false},
		{"a session.start record carries neither", event.KindSessionStart, nil, false, false},
		{"an os.presence record carries neither", event.KindOSPresence, nil, false, false},
		{"a bridge prompt, whose text the device never had, carries an f of v and pc and no c (spec 10.1)", event.KindPromptSubmit, func(e *event.Event, c *Content) {
			e.Via = event.ViaBridge
			*c = Content{NoText: true, Class: features.Substantive}
		}, true, false},
		{"a plugin's prompt that came without text carries an f of v and pc and no c", event.KindPromptSubmit, func(e *event.Event, c *Content) {
			e.Via = event.ViaPlugin
			*c = Content{NoText: true, Class: features.Answer}
		}, true, false},
		{"a plugin's final agent message that came without text commits nothing", event.KindAgentOutput, func(e *event.Event, c *Content) {
			e.Via = event.ViaPlugin
			*c = Content{NoText: true}
		}, false, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			ev, err := sampleEvent(c.kind)
			if err != nil {
				t.Fatal(err)
			}
			ev.Seq = 41
			content := sampleContent(c.kind)
			if c.edit != nil {
				c.edit(&ev, &content)
			}
			rec, err := EventRecord(k, ev, content)
			if err != nil {
				t.Fatal(err)
			}
			b, err := ParseBody(rec.Body)
			if err != nil {
				t.Fatal(err)
			}
			if (b.F != nil) != c.wantF || (b.C != nil) != c.wantC {
				t.Errorf("body %s has f %v and c %v, want %v and %v", rec.Body, b.F != nil, b.C != nil, c.wantF, c.wantC)
			}
			shareable, err := ev.Shareable().MarshalJSON()
			if err != nil {
				t.Fatal(err)
			}
			if want := `{"n":"` + k.Nonce(41).String() + `","e":` + string(shareable) + contentMembers(t, k, ev, content) + `}`; string(rec.Body) != want {
				t.Errorf("body is\n%s\nwant\n%s", rec.Body, want)
			}
			if again, err := b.Encode(); err != nil || !bytes.Equal(again, rec.Body) {
				t.Errorf("decoding and encoding the body gives %s, %v", again, err)
			}
			if err := CheckBody(ev.DeviceID, rec.Header, rec.Body); err != nil {
				t.Errorf("the body disagrees with its own header: %v", err)
			}
			if f, present, err := b.Features(rec.Header.RType()); present != c.wantF || err != nil {
				t.Errorf("Features gives present %v, %v", present, err)
			} else if present && f.V != features.Version {
				t.Errorf("f.v is %d", f.V)
			} else if present && (ev.Kind() == event.KindPromptSubmit || ev.Kind() == event.KindCLITrack) && f.HasText() != c.wantC {
				t.Errorf("f has the features of a text %v, and the record commits one %v", f.HasText(), c.wantC)
			}
		})
	}
}

func TestEventRecordCommitsTheFirst16KiB(t *testing.T) {
	k := testKeys(t)
	ev, err := sampleEvent(event.KindAgentOutput)
	if err != nil {
		t.Fatal(err)
	}
	ev.Seq = 5
	// 16,382 bytes, then a three-byte character that the cut can't split.
	raw := []byte(strings.Repeat("a", MessageMax-2) + "\U00004e2d" + "tail")
	rec, err := EventRecord(k, ev, Content{Text: raw})
	if err != nil {
		t.Fatal(err)
	}
	b, err := ParseBody(rec.Body)
	if err != nil {
		t.Fatal(err)
	}
	want, err := k.Commit(5, raw[:MessageMax-2])
	if err != nil {
		t.Fatal(err)
	}
	cases := []struct {
		name string
		ok   bool
	}{
		{"the cut stops before a character it would split", len(CommittedMessage(raw)) == MessageMax-2},
		{"the record commits the cut, not the whole message", b.C != nil && *b.C == want},
		{"a message of exactly 16,384 bytes commits in full", len(CommittedMessage(raw[:MessageMax])) == MessageMax},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if !c.ok {
				t.Error("doesn't hold")
			}
		})
	}
}

func TestEventRecordRefusesContent(t *testing.T) {
	k := testKeys(t)
	write := func(kind event.Kind, edit func(*event.Event, *Content)) error {
		ev, err := sampleEvent(kind)
		if err != nil {
			return err
		}
		ev.Seq = 3
		c := sampleContent(kind)
		edit(&ev, &c)
		_, err = EventRecord(k, ev, c)
		return err
	}
	cases := []struct {
		name string
		err  error
	}{
		{"a prompt with no class is refused", write(event.KindPromptSubmit, func(_ *event.Event, c *Content) { c.Class = "" })},
		{"a prompt with a class SM 2.2 doesn't have is refused", write(event.KindPromptSubmit, func(_ *event.Event, c *Content) { c.Class = "bogus" })},
		{"a prompt whose text isn't valid UTF-8 is refused", write(event.KindPromptSubmit, func(_ *event.Event, c *Content) { c.Text = []byte("fix \xff it") })},
		{"a prompt whose text is a UTF-8-encoded surrogate is refused", write(event.KindPromptSubmit, func(_ *event.Event, c *Content) { c.Text = []byte("a\xed\xa0\x80b") })},
		{"a prompt with no text, whose len_chars says it has one, is refused", write(event.KindPromptSubmit, func(_ *event.Event, c *Content) { c.Text = nil })},
		{"a prompt with edit hunks is refused", write(event.KindPromptSubmit, func(_ *event.Event, c *Content) { c.Edits = sampleContent(event.KindAgentTool).Edits })},
		{"a prompt with a command is refused", write(event.KindPromptSubmit, func(_ *event.Event, c *Content) { c.Command = "ls" })},
		{"a track label with no class is refused", write(event.KindCLITrack, func(_ *event.Event, c *Content) { c.Class = "" })},
		{"an agent message that isn't final, with text, is refused", write(event.KindAgentOutput, func(e *event.Event, _ *Content) { e.Data.(*event.AgentOutput).IsFinal = false })},
		{"a final agent message with no text, whose words say it has one, is refused", write(event.KindAgentOutput, func(_ *event.Event, c *Content) { c.Text = nil })},
		{"an agent message with a class is refused", write(event.KindAgentOutput, func(_ *event.Event, c *Content) { c.Class = features.Substantive })},
		{"an agent.tool record with text is refused", write(event.KindAgentTool, func(_ *event.Event, c *Content) { c.Text = []byte("x") })},
		{"an agent.tool hunk with a backslash path is refused", write(event.KindAgentTool, func(_ *event.Event, c *Content) { c.Edits[0].Path = `src\nav.js` })},
		{"an agent.tool hunk with a '..' component is refused", write(event.KindAgentTool, func(_ *event.Event, c *Content) { c.Edits[0].Path = "src/../nav.js" })},
		{"an agent.tool hunk whose range has lines but starts at 0 is refused", write(event.KindAgentTool, func(_ *event.Event, c *Content) { c.Edits[0].NewStart = 0 })},
		{"an agent.tool hunk number of 2^31 is refused", write(event.KindAgentTool, func(_ *event.Event, c *Content) { c.Edits[0].OldLines = 1 << 31 })},
		{"a shell.command record with edit hunks is refused", write(event.KindShellCommand, func(_ *event.Event, c *Content) { c.Edits = sampleContent(event.KindAgentTool).Edits })},
		{"a session.start record with a command is refused", write(event.KindSessionStart, func(_ *event.Event, c *Content) { c.Command = "claude" })},
		{"an os.focus record with text is refused", write(event.KindOSFocus, func(_ *event.Event, c *Content) { c.Text = []byte("window title") })},
		{"a hook prompt marked NoText is refused; only bridge and plugin records may lack their text", write(event.KindPromptSubmit, func(_ *event.Event, c *Content) { *c = Content{NoText: true, Class: features.Substantive} })},
		{"a track label marked NoText is refused; a label always has its text", write(event.KindCLITrack, func(_ *event.Event, c *Content) { *c = Content{NoText: true, Class: features.Substantive} })},
		{"a bridge prompt marked NoText that has text is refused", write(event.KindPromptSubmit, func(e *event.Event, c *Content) {
			e.Via = event.ViaBridge
			c.NoText = true
		})},
		{"a bridge prompt marked NoText with no class is refused", write(event.KindPromptSubmit, func(e *event.Event, c *Content) {
			e.Via = event.ViaBridge
			*c = Content{NoText: true}
		})},
		{"an agent.tool record marked NoText is refused", write(event.KindAgentTool, func(_ *event.Event, c *Content) { c.NoText = true })},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if c.err == nil {
				t.Fatal("written")
			}
			// The errors name the field, never the content.
			for _, secret := range []string{"window title", "fix \xff it", "nav.js", "navbar"} {
				if strings.Contains(c.err.Error(), secret) {
					t.Errorf("the error quotes content: %v", c.err)
				}
			}
		})
	}
}

func TestCheckBodyContent(t *testing.T) {
	k := testKeys(t)
	nonce := k.Nonce(7).String()
	// record returns the header of record 7 holding the sample event of
	// kind, and the event as a body's "e" holds it.
	record := func(kind event.Kind, edit func(*event.Event)) (Header, string) {
		ev, err := sampleEvent(kind)
		if err != nil {
			t.Fatal(err)
		}
		ev.Seq = 7
		if edit != nil {
			edit(&ev)
		}
		r, _ := RTypeOf(kind)
		h, err := NewHeader(7, r, ev.ProjectKey, ev.SessionKey)
		if err != nil {
			t.Fatal(err)
		}
		e, err := ev.Shareable().MarshalJSON()
		if err != nil {
			t.Fatal(err)
		}
		return h, string(e)
	}
	body := func(e, members string) []byte { return []byte(`{"n":"` + nonce + `","e":` + e + members + `}`) }
	prompt, promptE := record(event.KindPromptSubmit, nil)
	bridge, bridgeE := record(event.KindPromptSubmit, func(e *event.Event) { e.Via = event.ViaBridge })
	pluginPrompt, pluginPromptE := record(event.KindPromptSubmit, func(e *event.Event) { e.Via = event.ViaPlugin })
	track, trackE := record(event.KindCLITrack, nil)
	tool, toolE := record(event.KindAgentTool, nil)
	shell, shellE := record(event.KindShellCommand, nil)
	output, outputE := record(event.KindAgentOutput, nil)
	pluginOutput, pluginOutputE := record(event.KindAgentOutput, func(e *event.Event) { e.Via = event.ViaPlugin })
	notFinal, notFinalE := record(event.KindAgentOutput, func(e *event.Event) { e.Data.(*event.AgentOutput).IsFinal = false })
	start, startE := record(event.KindSessionStart, nil)
	checkpoint, err := NewHeader(7, RTypeCheckpoint, "p_x", event.NoSession)
	if err != nil {
		t.Fatal(err)
	}
	f, err := k.Features().Prompt("fix the navbar", features.Substantive)
	if err != nil {
		t.Fatal(err)
	}
	fJSON, err := json.Marshal(f)
	if err != nil {
		t.Fatal(err)
	}
	pf := string(fJSON)
	mh := f.MH.String()
	c := `{"n":3,"r":"` + emptyRoot.String() + `"}`
	textless := `{"v":1,"pc":"substantive"}`
	hunk := `{"p":"00112233445566778899aabbccddeeff","os":1,"ol":1,"ns":1,"nl":1}`
	cases := []struct {
		name   string
		header Header
		body   []byte
		want   error
	}{
		{"a prompt's body with f and c agrees with its header", prompt, body(promptE, `,"f":`+pf+`,"c":`+c), nil},
		{"a prompt's body without f and c disagrees: no body predates features (spec 4.5)", prompt, body(promptE, ""), ErrBodyHeader},
		{"a hook prompt's body without c disagrees, so a client can't keep it out of the draw", prompt, body(promptE, `,"f":`+pf), ErrBodyHeader},
		{"a hook prompt's body without f disagrees, so a client can't keep it out of the re-score", prompt, body(promptE, `,"c":`+c), ErrBodyHeader},
		{"a hook prompt with c and an f of only v and pc disagrees", prompt, body(promptE, `,"f":`+textless+`,"c":`+c), ErrBodyHeader},
		{"a bridge prompt's body with an f of v and pc and no c agrees (spec 10.1)", bridge, body(bridgeE, `,"f":`+textless), nil},
		{"a plugin prompt's body that came without text agrees with an f of v and pc", pluginPrompt, body(pluginPromptE, `,"f":`+textless), nil},
		{"a plugin prompt's body that came with its text agrees with f and c", pluginPrompt, body(pluginPromptE, `,"f":`+pf+`,"c":`+c), nil},
		{"a bridge prompt without c whose f has a sketch disagrees", bridge, body(bridgeE, `,"f":`+pf), ErrBodyHeader},
		{"a bridge prompt without f disagrees", bridge, body(bridgeE, ""), ErrBodyHeader},
		{"a track label's body without c disagrees; a label always has its text", track, body(trackE, `,"f":`+pf), ErrBodyHeader},
		{"a track label's body with an f of v and pc disagrees", track, body(trackE, `,"f":`+textless+`,"c":`+c), ErrBodyHeader},
		{"a final agent message's body with c agrees", output, body(outputE, `,"c":`+c), nil},
		{"a final agent message's body without c disagrees", output, body(outputE, ""), ErrBodyHeader},
		{"a plugin's final agent message that came without text agrees without c", pluginOutput, body(pluginOutputE, ""), nil},
		{"an agent.tool body whose f holds only v agrees", tool, body(toolE, `,"f":{"v":1}`), nil},
		{"an agent.tool body without f disagrees", tool, body(toolE, ""), ErrBodyHeader},
		{"a shell.command body without f disagrees", shell, body(shellE, ""), ErrBodyHeader},
		{"an f of version 2 isn't accepted: its features are unknown", prompt, body(promptE, `,"f":{"v":2,"zz":[1]},"c":`+c), features.ErrUnknown},
		{"an f of version 0 isn't a body: versions start at 1", tool, body(toolE, `,"f":{"v":0}`), ErrBody},
		{"an f whose v is null isn't a body", tool, body(toolE, `,"f":{"v":null}`), ErrBody},
		{"a version 1 f with a member the table doesn't list isn't a body", prompt, body(promptE, `,"f":{"v":1,"mh":"`+mh+`","new":true},"c":`+c), ErrBody},
		{"an f with an unlisted member and a null one isn't a body, whatever order a map gives them", tool, body(toolE, `,"f":{"v":1,"ch":null,"new":1}`), ErrBody},
		{"c on an agent message that isn't final disagrees", notFinal, body(notFinalE, `,"c":`+c), ErrBodyHeader},
		{"c on an agent.tool record disagrees", tool, body(toolE, `,"f":{"v":1},"c":`+c), ErrBodyHeader},
		{"f on an agent message disagrees", output, body(outputE, `,"f":{"v":1},"c":`+c), ErrBodyHeader},
		{"f on a session.start record disagrees", start, body(startE, `,"f":{"v":1}`), ErrBodyHeader},
		{"c on a session.start record disagrees", start, body(startE, `,"c":`+c), ErrBodyHeader},
		{"f on a checkpoint disagrees", checkpoint, body(`{"project_key":"p_x","t":1795089600000,"state":{}}`, `,"f":{"v":1}`), ErrBodyHeader},
		{"c on a checkpoint disagrees", checkpoint, body(`{"project_key":"p_x","t":1795089600000,"state":{}}`, `,"c":`+c), ErrBodyHeader},
		{"a prompt's f with hunks disagrees with its kind", prompt, body(promptE, `,"f":`+strings.TrimSuffix(pf, "}")+`,"h":[`+hunk+`]},"c":`+c), ErrBodyHeader},
		{"a prompt's f with a sketch and no template disagrees with its kind", prompt, body(promptE, `,"f":{"v":1,"mh":"`+mh+`","pc":"answer"},"c":`+c), ErrBodyHeader},
		{"an agent.tool f with a sketch disagrees with its kind", tool, body(toolE, `,"f":{"v":1,"mh":"`+mh+`"}`), ErrBodyHeader},
		{"a prompt's f with a sketch that isn't 512 hex digits isn't a body", prompt, body(promptE, `,"f":{"v":1,"mh":"00","th":"`+f.TH.String()+`","pc":"answer"},"c":`+c), ErrBody},
		{"a prompt's f with a class SM 2.2 doesn't have isn't a body", prompt, body(promptE, `,"f":{"v":1,"mh":"`+mh+`","th":"`+f.TH.String()+`","pc":"great"},"c":`+c), ErrBody},
		{"an agent.tool f with an empty list of hunks isn't a body", tool, body(toolE, `,"f":{"v":1,"h":[]}`), ErrBody},
		{"an agent.tool hunk starting at 0 with lines isn't a body", tool, body(toolE, `,"f":{"v":1,"h":[{"p":"00112233445566778899aabbccddeeff","os":0,"ol":2,"ns":1,"nl":2}]}`), ErrBody},
		{"a null hunk isn't a body", tool, body(toolE, `,"f":{"v":1,"h":[null]}`), ErrBody},
		{"an empty hunk object isn't a body", tool, body(toolE, `,"f":{"v":1,"h":[{}]}`), ErrBody},
		{"a hunk without its path isn't a body", tool, body(toolE, `,"f":{"v":1,"h":[{"os":1,"ol":1,"ns":1,"nl":1}]}`), ErrBody},
		{"a hunk with a member besides p, os, ol, ns and nl isn't a body", tool, body(toolE, `,"f":{"v":1,"h":[{"p":"00112233445566778899aabbccddeeff","os":1,"ol":1,"ns":1,"nl":1,"x":0}]}`), ErrBody},
		{"an f with no v isn't a body", tool, body(toolE, `,"f":{}`), ErrBody},
		{"an f that isn't an object isn't a body", tool, body(toolE, `,"f":[1]`), ErrBody},
		{"a null f isn't a body", tool, body(toolE, `,"f":null`), ErrBody},
		{"a null c isn't a body", prompt, body(promptE, `,"f":`+pf+`,"c":null`), ErrBody},
		{"a c without its root isn't a body", prompt, body(promptE, `,"f":`+pf+`,"c":{"n":3}`), ErrBody},
		{"a c with a member besides n and r isn't a body", prompt, body(promptE, `,"f":`+pf+`,"c":{"n":3,"r":"`+emptyRoot.String()+`","x":1}`), ErrBody},
		{"a c whose root is uppercase hex isn't a body", prompt, body(promptE, `,"f":`+pf+`,"c":{"n":3,"r":"`+strings.ToUpper(emptyRoot.String())+`"}`), ErrBody},
		{"a c with a negative count isn't a body", prompt, body(promptE, `,"f":`+pf+`,"c":{"n":-1,"r":"`+emptyRoot.String()+`"}`), ErrBody},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			err := CheckBody("d_7f3a9c", c.header, c.body)
			switch {
			case c.want == nil && err != nil:
				t.Errorf("CheckBody: %v", err)
			case c.want != nil && !errors.Is(err, c.want):
				t.Errorf("CheckBody gives %v, want an error wrapping %v", err, c.want)
			}
		})
	}
}

func TestBodyFeaturesVersions(t *testing.T) {
	cases := []struct {
		name string
		f    string
		want error
	}{
		{"an f of version 2 is unknown", `{"v":2}`, features.ErrUnknown},
		{"an f of version 2 with members version 1 doesn't list is unknown", `{"v":2,"zz":1}`, features.ErrUnknown},
		{"an f of version 0 is malformed: versions start at 1", `{"v":0}`, features.ErrMalformed},
		{"an f of a negative version is malformed", `{"v":-1}`, features.ErrMalformed},
		{"an f whose v is null is malformed, not version 0", `{"v":null}`, features.ErrMalformed},
		{"a v that isn't an integer is malformed", `{"v":"1"}`, features.ErrMalformed},
		{"a version 1 f with a member the table doesn't list is malformed", `{"v":1,"x":1}`, features.ErrMalformed},
		{"a member named in another case is malformed, not read as its namesake", `{"v":1,"CH":"00112233445566778899aabbccddeeff"}`, features.ErrMalformed},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			b := Body{E: json.RawMessage(`{}`), F: json.RawMessage(c.f)}
			_, present, err := b.Features(0x0c)
			if !present || !errors.Is(err, c.want) {
				t.Errorf("Features gives present %v, %v; want an error wrapping %v", present, err, c.want)
			}
		})
	}
}
