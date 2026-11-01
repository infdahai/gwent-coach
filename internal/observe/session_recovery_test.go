package observe

import (
	"context"
	"encoding/json"
	"path/filepath"
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
