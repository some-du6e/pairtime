// SPDX-License-Identifier: AGPL-3.0-or-later

package stream

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"
	"testing/synctest"
	"time"

	"github.com/some-du6e/pairtime/internal/server/dashboard"
)

// credited reads the credited events of a stream's text.
func credited(t *testing.T, text string) []Credited {
	t.Helper()
	var out []Credited
	for _, e := range events(text) {
		if e.event != "credited" {
			t.Errorf("an event %q on the owner's stream: %s", e.event, e.data)
			continue
		}
		var c Credited
		if err := json.Unmarshal([]byte(e.data), &c); err != nil {
			t.Fatalf("credited data %s: %v", e.data, err)
		}
		out = append(out, c)
	}
	return out
}

func TestOwnerTopic(t *testing.T) {
	tests := []struct {
		name string
		run  func(t *testing.T, h *Hub, src *viewSource)
	}{
		{name: "a change goes out at once as the View's numbers: the day, today's and the week's clocks, and each project's by id", run: func(t *testing.T, h *Hub, src *viewSource) {
			c := open(t, h, meTopic, as("a1"))
			c.read()
			src.you.Store(90)
			src.agent.Store(30)
			h.Changed("a1")
			synctest.Wait()
			got := credited(t, c.read())
			clocks := Clocks{You: dashboard.Time{FinalS: 90}, Agent: dashboard.Time{FinalS: 30}}
			want := []Credited{{
				Day: "2000-01-01", TodayS: 90, WeekS: 90, Today: clocks, Week: clocks,
				Projects: []ProjectClocks{{Project: "p1", Today: clocks, Week: clocks}},
			}}
			if !reflect.DeepEqual(got, want) {
				t.Errorf("credited events %+v, want %+v", got, want)
			}
		}},
		{name: "an event names projects by id alone, never by the owner's label, the code's name or a device key", run: func(t *testing.T, h *Hub, src *viewSource) {
			c := open(t, h, meTopic, as("a1"))
			c.read()
			src.you.Store(60)
			h.Changed("a1")
			synctest.Wait()
			text := c.read()
			for _, leak := range []string{"secret-name", "Silver Fox", "k_device_key", `"name"`, `"keys"`} {
				if strings.Contains(text, leak) {
					t.Errorf("the stream carries %s:\n%s", leak, text)
				}
			}
			if len(events(text)) != 1 {
				t.Errorf("got %d events, want 1", len(events(text)))
			}
		}},
		{name: "changes within Gap of a read make one more event, Gap after that read began, with the change since as delta_s", run: func(t *testing.T, h *Hub, src *viewSource) {
			c := open(t, h, meTopic, as("a1"))
			c.read()
			src.you.Store(60)
			h.Changed("a1")
			synctest.Wait()
			if got := credited(t, c.read()); len(got) != 1 || got[0].DeltaS != 0 {
				t.Fatalf("the first change made %+v, want one event with delta_s 0", got)
			}
			src.you.Store(120)
			for range 5 {
				h.Changed("a1")
			}
			time.Sleep(DefaultGap - time.Millisecond)
			synctest.Wait()
			if got := c.read(); len(events(got)) != 0 {
				t.Fatalf("an event before Gap passed:\n%s", got)
			}
			time.Sleep(time.Millisecond)
			synctest.Wait()
			got := credited(t, c.read())
			if len(got) != 1 || got[0].TodayS != 120 || got[0].DeltaS != 60 {
				t.Errorf("at Gap the stream got %+v, want one event of 120 s with delta_s 60", got)
			}
			if n := src.calls.Load(); n != 2 {
				t.Errorf("the hub read %d Views, want 2", n)
			}
		}},
		{name: "a new day's first event counts all of today's time as its delta", run: func(t *testing.T, h *Hub, src *viewSource) {
			c := open(t, h, meTopic, as("a1"))
			c.read()
			src.you.Store(600)
			h.Changed("a1")
			synctest.Wait()
			c.read()
			// A day of keepalives, read an hour at a time.
			for range 24 {
				time.Sleep(time.Hour)
				synctest.Wait()
				c.read()
			}
			src.you.Store(30)
			h.Changed("a1")
			synctest.Wait()
			got := credited(t, c.read())
			if len(got) != 1 || got[0].Day != "2000-01-02" || got[0].DeltaS != 30 {
				t.Errorf("the next day's event %+v, want 2000-01-02 with delta_s 30", got)
			}
		}},
		{name: "an account with no stream costs no read, and a stream carries only its own account's events", run: func(t *testing.T, h *Hub, src *viewSource) {
			a1 := open(t, h, meTopic, as("a1"))
			a2 := open(t, h, meTopic, as("a2"))
			a1.read()
			a2.read()
			h.Changed("a3")
			synctest.Wait()
			if n := src.calls.Load(); n != 0 {
				t.Errorf("a change of an account with no stream made %d reads", n)
			}
			h.Changed("a2")
			synctest.Wait()
			if got := events(a1.read()); len(got) != 0 {
				t.Errorf("a2's change reached a1's stream: %+v", got)
			}
			if got := events(a2.read()); len(got) != 1 {
				t.Errorf("a2's stream got %d events, want 1", len(got))
			}
		}},
		{name: "a read that fails publishes nothing, and the next change publishes", run: func(t *testing.T, h *Hub, src *viewSource) {
			c := open(t, h, meTopic, as("a1"))
			c.read()
			src.fail.Store(true)
			h.Changed("a1")
			synctest.Wait()
			if got := c.read(); len(events(got)) != 0 {
				t.Errorf("a failed read published:\n%s", got)
			}
			src.fail.Store(false)
			src.you.Store(45)
			time.Sleep(DefaultGap)
			h.Changed("a1")
			synctest.Wait()
			if got := credited(t, c.read()); len(got) != 1 || got[0].TodayS != 45 {
				t.Errorf("after the failure the stream got %+v, want one event of 45 s", got)
			}
		}},
		{name: "with no stream on it for the window, a user topic stops reading Views and goes, though its account keeps changing", run: func(t *testing.T, h *Hub, src *viewSource) {
			c := open(t, h, meTopic, as("a1"))
			c.read()
			c.close()
			// A change a minute for ten minutes, as from a computer that
			// uploads while no dashboard is open.
			for range 10 {
				h.Changed("a1")
				synctest.Wait()
				time.Sleep(time.Minute)
				synctest.Wait()
			}
			// One read a change while a reconnect could still replay it,
			// in the window's first five minutes, and none after.
			if n := src.calls.Load(); n != 5 {
				t.Errorf("the hub read %d Views, want 5", n)
			}
			h.mu.Lock()
			_, kept := h.topics[userKey+"a1"]
			h.mu.Unlock()
			if kept {
				t.Error("the topic no stream was on for the window is still there")
			}
		}},
		{name: "a change while every stream is away still publishes, so a reconnect within the window replays it", run: func(t *testing.T, h *Hub, src *viewSource) {
			c := open(t, h, meTopic, as("a1"))
			last := lastID(c.read())
			c.close()
			src.you.Store(75)
			h.Changed("a1")
			synctest.Wait()
			time.Sleep(5 * time.Second)
			header := as("a1")
			header.Set("Last-Event-ID", last)
			c = open(t, h, meTopic, header)
			if got := credited(t, c.read()); len(got) != 1 || got[0].TodayS != 75 {
				t.Errorf("the reconnect got %+v, want the change it missed", got)
			}
		}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				src := &viewSource{}
				h := New(Options{Account: testAccount, Source: src})
				defer h.Close()
				tt.run(t, h, src)
			})
		})
	}
}

func TestCreditedOf(t *testing.T) {
	day := func(you, agent float64) []dashboard.Row {
		return []dashboard.Row{
			{Day: "2026-11-24", Project: "p1", Kind: "reviewing", Level: "B", Seconds: you},
			{Day: "2026-11-24", Project: "p1", Kind: "reviewing", Level: "B", Pending: true, Seconds: 15},
			{Day: "2026-11-24", Project: "p2", Agent: true, Seconds: agent},
			{Day: "2026-11-20", Project: "p3", Kind: "testing", Level: "A", Seconds: 300},
			{Day: "2026-11-01", Project: "p4", Kind: "testing", Level: "A", Seconds: 900},
		}
	}
	view := func(t *testing.T, rows []dashboard.Row) dashboard.View {
		t.Helper()
		v, err := dashboard.Build(dashboard.Meta{Today: "2026-11-24", TZ: "UTC"}, rows)
		if err != nil {
			t.Fatal(err)
		}
		return v
	}
	tests := []struct {
		name string
		rows []dashboard.Row
		prev *Credited
		want func(c Credited) bool
	}{
		{name: "today's and the week's totals are the View's, final and pending together", rows: day(60, 40),
			want: func(c Credited) bool {
				return c.TodayS == 75 && c.WeekS == 375 && c.Today.You == (dashboard.Time{FinalS: 60, PendingS: 15}) && c.Today.Agent.FinalS == 40
			}},
		{name: "projects with time today or in the week are listed in the View's order, and one only in the month isn't", rows: day(60, 40),
			want: func(c Credited) bool {
				ids := []string{}
				for _, p := range c.Projects {
					ids = append(ids, p.Project)
				}
				return reflect.DeepEqual(ids, []string{"p3", "p1", "p2"})
			}},
		{name: "the topic's first event has delta_s 0", rows: day(60, 0),
			want: func(c Credited) bool { return c.DeltaS == 0 }},
		{name: "delta_s is today's growth since the last event of the same day", rows: day(60, 0), prev: &Credited{Day: "2026-11-24", TodayS: 30},
			want: func(c Credited) bool { return c.DeltaS == 45 }},
		{name: "a fall of today's time, when a revision lowers a minute, is a negative delta", rows: day(0, 0), prev: &Credited{Day: "2026-11-24", TodayS: 30},
			want: func(c Credited) bool { return c.DeltaS == -15 }},
		{name: "after an event of another day delta_s is all of today", rows: day(60, 0), prev: &Credited{Day: "2026-11-23", TodayS: 500},
			want: func(c Credited) bool { return c.DeltaS == 75 }},
		{name: "a View with no time lists no project, as [] rather than null", rows: nil,
			want: func(c Credited) bool { return c.Projects != nil && len(c.Projects) == 0 && c.TodayS == 0 }},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			c := creditedOf(view(t, tt.rows), tt.prev)
			if !tt.want(c) {
				data, _ := json.Marshal(c)
				t.Errorf("credited %s", data)
			}
		})
	}
}
