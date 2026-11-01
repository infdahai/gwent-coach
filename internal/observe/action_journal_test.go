package observe

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"gwentcoachmvp/internal/matchlog"
)

type receiptSink struct {
	events []matchlog.Event
	fail   string
}

func (s *receiptSink) AppendDurable(e matchlog.Event) error {
	if e.EventType == s.fail {
		return errors.New("injected storage failure")
	}
	s.events = append(s.events, e)
	return nil
}

func withTestEvidence(o SemanticObservation) SemanticObservation {
	o.Frames = []matchlog.FrameEvidence{{Source: o.Source + "/first", MatchID: o.MatchID, CapturedAt: o.CapturedAt.Add(-time.Nanosecond)}, {Source: o.Source, MatchID: o.MatchID, CapturedAt: o.CapturedAt}}
	return o
}

func TestJournalPersistsIntentDeliveryAndConfirmation(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "match")
	w, err := matchlog.Create(dir, "m")
	if err != nil {
		t.Fatal(err)
	}
	defer w.Abort()
	j := NewActionJournal(w)
	snapshot := Snapshot{Version: 7, Source: "before", Phase: "turn"}
	if err = j.Begin("m/7", snapshot, map[string]string{"kind": "pass"}); err != nil {
		t.Fatal(err)
	}
	at := time.Now()
	if err = j.ExecuteStep(Action{Kind: "hold", Reference: "before"}, func(a Action) error {
		data, e := os.ReadFile(filepath.Join(dir, "events.jsonl"))
		if e != nil {
			t.Fatal(e)
		}
		var rows []matchlog.Event
		for _, line := range splitJSONLines(data) {
			var row matchlog.Event
			if e = json.Unmarshal(line, &row); e != nil {
				t.Fatal(e)
			}
			rows = append(rows, row)
		}
		if len(rows) != 2 || rows[1].EventType != "input_intent" {
			t.Fatal("input began without persisted intent")
		}
		return a.onReceipt(InputReceipt{Outcome: "delivered", EventsSent: 2, DeliveredAt: at})
	}); err != nil {
		t.Fatal(err)
	}
	got, err := j.DeliveredAt("m/7")
	if err != nil || got != at {
		t.Fatalf("timestamp=%v err=%v", got, err)
	}
	if err = j.MarkDelivered("m/7"); err != nil {
		t.Fatal(err)
	}
	after := withTestEvidence(SemanticObservation{Source: "after", MatchID: "m", Phase: "result", Result: true, CapturedAt: at.Add(time.Millisecond)})
	if err = j.Confirm("m/7", Snapshot{Version: 8, Source: after.Source, Phase: after.Phase}, after); err != nil {
		t.Fatal(err)
	}
	if err = j.Result(Snapshot{Version: 8}, after); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(filepath.Join(dir, "events.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"action_intent", "input_intent", "input_receipt", "input_completed", "action_delivered", "action_confirmed", "result_observed"}
	lines := splitJSONLines(data)
	if len(lines) != len(want) {
		t.Fatalf("events=%s", data)
	}
	for i, line := range lines {
		var e matchlog.Event
		if err = json.Unmarshal(line, &e); err != nil {
			t.Fatal(err)
		}
		if e.EventType != want[i] || e.Sequence != uint64(i+1) {
			t.Fatalf("event=%+v", e)
		}
		if i == 2 && (e.DeliveredAt == nil || *e.DeliveredAt != at.UTC().Format(time.RFC3339Nano) || *e.StateVersion != 7) {
			t.Fatalf("receipt=%+v", e)
		}
	}
}

func splitJSONLines(data []byte) [][]byte {
	var lines [][]byte
	start := 0
	for i, b := range data {
		if b == '\n' {
			if i > start {
				lines = append(lines, data[start:i])
			}
			start = i + 1
		}
	}
	return lines
}

func TestJournalStopsOnPersistenceFailureOrPartialDelivery(t *testing.T) {
	for _, failure := range []string{"input_intent", "input_receipt", "input_completed", "partial"} {
		t.Run(failure, func(t *testing.T) {
			sink := &receiptSink{fail: failure}
			j := NewActionJournal(sink)
			if err := j.Begin("a", Snapshot{Version: 1, State: []byte(`{"MatchID":"m"}`)}, "play"); err != nil {
				t.Fatal(err)
			}
			sent := false
			at := time.Now()
			err := j.ExecuteStep(Action{Kind: "click"}, func(a Action) error {
				sent = true
				outcome := "delivered"
				if failure == "partial" {
					outcome = "partial"
				}
				sentCount := uint32(2)
				if failure == "partial" {
					sentCount = 1
				}
				return a.onReceipt(InputReceipt{Outcome: outcome, EventsSent: sentCount, DeliveredAt: at})
			})
			if failure == "input_intent" && sent {
				t.Fatal("sent after failed intent")
			}
			if failure != "partial" && err == nil {
				t.Fatal("lost persistence error")
			}
			if failure == "partial" {
				if e := j.Confirm("a", Snapshot{Version: 2}, SemanticObservation{CapturedAt: at.Add(time.Second)}); e == nil {
					t.Fatal("partial input confirmed")
				}
			}
			for _, e := range sink.events {
				if e.EventType == "action_confirmed" {
					t.Fatal("unexpected confirmation")
				}
			}
		})
	}
}

func TestJournalNaturalResultHasNoReceipt(t *testing.T) {
	sink := &receiptSink{}
	j := NewActionJournal(sink)
	if err := j.Result(Snapshot{Version: 1}, SemanticObservation{Source: "result", Result: true}); err != nil {
		t.Fatal(err)
	}
	if len(sink.events) != 1 || sink.events[0].EventType != "result_observed" {
		t.Fatalf("events=%+v", sink.events)
	}
}
