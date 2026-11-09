package telemetry

import (
	"path/filepath"
	"testing"
	"time"

	"github.com/romero927/lookout/internal/transcript"
)

var t0 = time.Date(2026, 1, 10, 10, 0, 0, 0, time.UTC)

func asst(id, sess, model string, at time.Time, in, out int64, sub bool) transcript.Record {
	return transcript.Record{
		Kind: transcript.KindAssistant, MessageID: id, SessionID: sess, Model: model,
		Timestamp: at, HasUsage: true, Sidechain: sub,
		Usage: transcript.Usage{Input: in, Output: out},
	}
}

func TestAggregatorDedupeAndWindows(t *testing.T) {
	now := t0.Add(6 * time.Hour) // 16:00
	tests := []struct {
		name  string
		recs  []transcript.Record
		check func(t *testing.T, s Snapshot)
	}{
		{
			name: "split content blocks count once",
			recs: []transcript.Record{
				asst("m1", "s", "opus", now.Add(-time.Minute), 10, 100, false),
				asst("m1", "s", "opus", now.Add(-time.Minute), 10, 100, false),
				asst("m1", "s", "opus", now.Add(-time.Minute), 10, 100, false),
			},
			check: func(t *testing.T, s Snapshot) {
				if s.Session.Usage.Output != 100 || s.Session.Messages != 1 {
					t.Fatalf("session = %+v", s.Session)
				}
			},
		},
		{
			name: "duplicate keeps field-wise max",
			recs: []transcript.Record{
				asst("m1", "s", "opus", now.Add(-time.Minute), 10, 5, false),
				asst("m1", "s", "opus", now.Add(-time.Minute), 10, 80, false),
			},
			check: func(t *testing.T, s Snapshot) {
				if s.Session.Usage.Output != 80 {
					t.Fatalf("output = %d", s.Session.Usage.Output)
				}
			},
		},
		{
			name: "synthetic and usage-less records ignored",
			recs: []transcript.Record{
				asst("m1", "s", transcript.SyntheticModel, now.Add(-time.Minute), 0, 0, false),
				{Kind: transcript.KindAssistant, MessageID: "m2", SessionID: "s", Model: "opus", Timestamp: now},
			},
			check: func(t *testing.T, s Snapshot) {
				if s.Session.Messages != 0 {
					t.Fatalf("messages = %d", s.Session.Messages)
				}
			},
		},
		{
			name: "rolling window, today and session",
			recs: []transcript.Record{
				asst("old", "s", "opus", now.Add(-30*time.Hour), 1, 1, false),  // yesterday
				asst("mid", "s", "opus", now.Add(-6*time.Hour), 1, 10, false),  // today, outside 5h
				asst("new", "s", "opus", now.Add(-1*time.Hour), 1, 100, false), // inside 5h
				asst("oth", "x", "sonnet", now.Add(-time.Minute), 1, 1000, false),
			},
			check: func(t *testing.T, s Snapshot) {
				if s.Session.Usage.Output != 111 {
					t.Fatalf("session out = %d", s.Session.Usage.Output)
				}
				if s.Today.Usage.Output != 1110 {
					t.Fatalf("today out = %d", s.Today.Usage.Output)
				}
				if s.Rolling.Usage.Output != 1100 {
					t.Fatalf("rolling out = %d", s.Rolling.Usage.Output)
				}
				if s.Today.ByModel["sonnet"].Output != 1000 {
					t.Fatalf("by model = %+v", s.Today.ByModel)
				}
				if got := s.Days[len(s.Days)-1].Usage.Output; got != 1110 {
					t.Fatalf("today bucket = %d", got)
				}
				if got := s.Days[len(s.Days)-2].Usage.Output; got != 1 {
					t.Fatalf("yesterday bucket = %d", got)
				}
			},
		},
		{
			name: "subagent usage counts in session and is broken out",
			recs: []transcript.Record{
				asst("m1", "s", "opus", now.Add(-time.Minute), 0, 100, false),
				asst("m2", "s", "haiku", now.Add(-time.Minute), 0, 30, true),
			},
			check: func(t *testing.T, s Snapshot) {
				if s.Session.Usage.Output != 130 || s.SessionSub.Output != 30 {
					t.Fatalf("session=%d sub=%d", s.Session.Usage.Output, s.SessionSub.Output)
				}
			},
		},
		{
			name: "burn rate over window",
			recs: []transcript.Record{
				asst("a", "s", "opus", now.Add(-2*time.Minute), 500, 500, false),
				asst("b", "s", "opus", now.Add(-20*time.Minute), 9999, 9999, false),
			},
			check: func(t *testing.T, s Snapshot) {
				if s.BurnPerMin != 100 || s.OutputPerMin != 50 || s.FreshPerMin != 100 {
					t.Fatalf("burn = %v out = %v", s.BurnPerMin, s.OutputPerMin)
				}
			},
		},
		{
			name: "blocks split on gaps and expire",
			recs: []transcript.Record{
				asst("a", "s", "opus", t0.Add(-10*time.Hour), 0, 1, false),
				asst("b", "s", "opus", t0.Add(4*time.Hour+30*time.Minute), 0, 2, false), // 14:30 -> block 14:00-19:00
				asst("c", "s", "opus", t0.Add(5*time.Hour+50*time.Minute), 0, 4, false), // 15:50
			},
			check: func(t *testing.T, s Snapshot) {
				b := s.Block
				if b == nil || !b.Active {
					t.Fatalf("block = %+v", b)
				}
				if !b.Start.Equal(t0.Add(4*time.Hour)) || !b.End.Equal(t0.Add(9*time.Hour)) {
					t.Fatalf("block span %v - %v", b.Start, b.End)
				}
				if b.Usage.Output != 6 {
					t.Fatalf("block out = %d", b.Usage.Output)
				}
				if s.ProjectedBlock < b.Usage.Total() {
					t.Fatalf("projection %d < total", s.ProjectedBlock)
				}
			},
		},
		{
			name: "out-of-order arrival is sorted",
			recs: []transcript.Record{
				asst("late", "s", "opus", now.Add(-time.Minute), 0, 1, false),
				asst("early", "s", "opus", now.Add(-3*time.Hour), 0, 2, false),
				asst("late", "s", "opus", now.Add(-time.Minute), 0, 7, false), // dup after re-sort
			},
			check: func(t *testing.T, s Snapshot) {
				if s.Session.Usage.Output != 9 || s.Session.Messages != 2 {
					t.Fatalf("session = %+v", s.Session)
				}
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			a := New()
			for i, r := range tt.recs {
				a.Add(r, transcript.Source{})
				if i == 1 {
					a.Snapshot(now, Options{}) // force an intermediate sort
				}
			}
			tt.check(t, a.Snapshot(now, Options{SessionID: "s"}))
		})
	}
}

func TestCurrentSession(t *testing.T) {
	a := New()
	r1 := asst("m1", "s1", "opus", t0, 1, 1, false)
	r1.Cwd = "/work/a"
	r2 := asst("m2", "s2", "sonnet", t0.Add(time.Minute), 1, 1, false)
	r2.Cwd = "/work/b"
	sub := asst("m3", "s1", "haiku", t0.Add(time.Hour), 1, 1, true) // subagent activity doesn't move model
	a.Add(r1, transcript.Source{})
	a.Add(r2, transcript.Source{})
	a.Add(sub, transcript.Source{IsSubagent: true})

	if s, _ := a.Current(""); s.ID != "s2" {
		t.Fatalf("current = %s", s.ID)
	}
	s, ok := a.Current("/work/a/")
	if !ok || s.ID != "s1" || s.Model != "opus" {
		t.Fatalf("current(/work/a) = %+v", s)
	}
}

func TestFixtureTotals(t *testing.T) {
	a := New()
	root := "testroot"
	add := func(p string, src transcript.Source) {
		if err := transcript.ReadAll(p, func(r transcript.Record) { a.Add(r, src) }); err != nil {
			t.Fatal(err)
		}
	}
	td := filepath.Join("..", "transcript", "testdata")
	add(filepath.Join(td, "session_basic.jsonl"), transcript.Source{SessionID: "sess-0001"})
	add(filepath.Join(td, "subagent.jsonl"), transcript.Source{SessionID: "sess-0001", IsSubagent: true})
	_ = root
	s := a.Snapshot(time.Date(2026, 1, 10, 12, 0, 0, 0, time.UTC), Options{SessionID: "sess-0001"})
	// msg_1 (10/200/1000/5000) + msg_2 (5/40/100/6000) + msg_3 (7/30/0/9000) + sub (3/20/0/400)
	want := transcript.Usage{Input: 25, Output: 290, CacheCreate: 1100, CacheRead: 20400, Thinking: 50}
	if s.Session.Usage != want {
		t.Fatalf("session usage = %+v, want %+v", s.Session.Usage, want)
	}
	if s.Session.Messages != 4 || s.SessionSub.Output != 20 {
		t.Fatalf("messages=%d sub=%+v", s.Session.Messages, s.SessionSub)
	}
	cur, _ := a.Current("")
	if cur.Model != "claude-sonnet-test" || cur.Compactions != 1 || cur.Context != 9007 {
		t.Fatalf("current = %+v", cur)
	}
}
