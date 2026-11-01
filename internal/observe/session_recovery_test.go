package observe

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"testing"
	"time"

	"gwentcoachmvp/internal/matchlog"
	"gwentcoachmvp/internal/uiphase"
)

func TestSessionNavigationRecoveryUsesRecordedTargetWithoutInput(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "match")
	writer, err := matchlog.Create(dir, "s/1")
	if err != nil {
		t.Fatal(err)
	}
	j := NewActionJournal(writer)
	state, _ := json.Marshal(map[string]string{"MatchID": "s/1", "Stage": "training"})
	original := NavigationAction{Input: Action{Kind: "click", X: 700, Y: 500, Reference: "before", ExpectedPhase: "training"}, To: "mulligan"}
	if err = j.Begin("s/1/entry/0", Snapshot{Version: 1, Source: "before", Phase: "training", State: state}, original); err != nil {
		t.Fatal(err)
	}
	at := time.Now().Add(-time.Second)
	if err = j.ExecuteStep(original.Input, func(a Action) error {
		return a.onReceipt(InputReceipt{Outcome: "delivered", EventsSent: 2, DeliveredAt: at})
	}); err != nil {
		t.Fatal(err)
	}
	if err = j.MarkDelivered("s/1/entry/0"); err != nil {
		t.Fatal(err)
	}
	if err = writer.Abort(); err != nil {
		t.Fatal(err)
	}
	writer, events, err := matchlog.Resume(dir, "s/1")
	if err != nil {
		t.Fatal(err)
	}
	defer writer.Abort()
	j, err = RestoreActionJournal(writer, events)
	if err != nil {
		t.Fatal(err)
	}
	s := TrainingSession{TransitionBudget: time.Second, ObserveStage: func(context.Context, string) (uiphase.Event, error) {
		first := uiphase.FrameRef{ID: "a", StreamID: "s/1", CapturedAt: time.Now()}
		second := first
		second.ID = "b"
		second.CapturedAt = first.CapturedAt.Add(time.Nanosecond)
		return uiphase.Event{Phase: "mulligan", Confirmed: true, Sources: [2]uiphase.FrameRef{first, second}, Evidence: uiphase.Evidence{Frame: second}}, nil
	}, ExecuteInput: func(context.Context, Action) error { t.Fatal("replayed delivered navigation"); return nil }}
	// A changed plan cannot replace the persisted successor or original input.
	if err = s.navigate(context.Background(), "s/1", "s/1/entry/0", SessionTransition{From: "other", To: "wrong", Input: Action{Kind: "click", X: 1, Y: 1}}, j); err != nil {
		t.Fatal(err)
	}
	if j.Pending() != nil {
		t.Fatal("navigation remained unresolved")
	}
}

func TestInspectNavigationRecovery(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "match")
	w, err := matchlog.Create(dir, "live-046-000001")
	if err != nil {
		t.Fatal(err)
	}
	j := NewActionJournal(w)
	state, _ := json.Marshal(map[string]string{"MatchID": "live-046-000001", "Stage": "mulligan-confirm"})
	plan := NavigationAction{Input: Action{Kind: "click", X: 892, Y: 635, Reference: "before", ExpectedPhase: "mulligan-confirm"}, To: "turn"}
	if err = j.Begin("live-046-000001/entry/2", Snapshot{Version: 1, Source: "before", Phase: "mulligan-confirm", State: state}, plan); err != nil {
		t.Fatal(err)
	}
	at := time.Now().Add(-time.Minute).UTC()
	if err = j.ExecuteStep(plan.Input, func(a Action) error {
		return a.onReceipt(InputReceipt{Outcome: "delivered", EventsSent: 2, DeliveredAt: at})
	}); err != nil {
		t.Fatal(err)
	}
	if err = j.MarkDelivered("live-046-000001/entry/2"); err != nil {
		t.Fatal(err)
	}
	first := uiphase.FrameRef{ID: "frame-1", StreamID: "live-046-000001", CapturedAt: at.Add(time.Second)}
	second := uiphase.FrameRef{ID: "frame-2", StreamID: first.StreamID, CapturedAt: at.Add(2 * time.Second)}
	phase, source := "turn", second.ID
	obs := uiphase.Event{Phase: phase, Confirmed: true, Sources: [2]uiphase.FrameRef{first, second}, Evidence: uiphase.Evidence{Frame: second, Phase: phase}}
	if err = w.AppendDurable(matchlog.Event{EventType: "ui_stage_observed", Phase: &phase, FramePath: &source, VisibleState: obs}); err != nil {
		t.Fatal(err)
	}
	// A reused source is not independent evidence.
	obs.Sources[0] = obs.Sources[1]
	if err = w.AppendDurable(matchlog.Event{EventType: "ui_stage_observed", Phase: &phase, FramePath: &source, VisibleState: obs}); err != nil {
		t.Fatal(err)
	}
	if err = w.Abort(); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "events.jsonl")
	claim := filepath.Join(dir, "navigation.claim")
	if err = os.WriteFile(claim, []byte("held"), 0600); err != nil {
		t.Fatal(err)
	}
	before, _ := os.ReadFile(path)
	report, err := InspectNavigationRecovery(path, claim)
	if err != nil {
		t.Fatal(err)
	}
	if report.Status != "delivered, recorded evidence requires review" || report.Claim != "present" || report.Observations != 2 || !reflect.DeepEqual(report.CandidateSequences, []uint64{6}) {
		t.Fatalf("unexpected report: %+v", report)
	}
	after, _ := os.ReadFile(path)
	if !reflect.DeepEqual(before, after) {
		t.Fatal("inspection modified ledger")
	}
	if b, err := os.ReadFile(claim); err != nil || string(b) != "held" {
		t.Fatal("inspection modified claim")
	}
}
