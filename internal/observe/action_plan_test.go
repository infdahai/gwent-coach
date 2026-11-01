package observe

import (
	"context"
	"errors"
	"image"
	"testing"
	"time"

	"gwentcoachmvp/internal/rules"
)

func TestPlannerAutomaticOnlyAndExpiredDeadline(t *testing.T) {
	now := time.Now()
	action := rules.Action{Kind: "play", Card: "wolf", Row: 0}
	o := SemanticObservation{HandOrder: []string{"wolf"}, View: rules.View{Visible: []rules.Instance{{ID: "wolf", CardID: 132403, Zone: rules.Hand, Owner: 0}}}}
	state := LoopState{CardPoints: []image.Point{{X: 1, Y: 1}}, SelectedPhases: map[string]string{"wolf": "selected"}, AutoEndedPhases: map[string]string{"wolf": "ended"}}
	planner := ActionPlanner{Config: NativeLoopConfig{RowPoints: [2]image.Point{{X: 2, Y: 2}}}}
	plan, err := planner.Build(o, state, action, now.Add(time.Second))
	if err != nil || !plan.AutoEnd || plan.Inputs[len(plan.Inputs)-1].Kind != "" {
		t.Fatalf("automatic-only plan=%+v err=%v", plan, err)
	}
	pending := PendingPlan{Plan: plan, MatchID: "m"}
	pending.Plan.Deadline = now.Add(-time.Millisecond)
	if _, err = pending.NextInput(now, TargetEvidence{}); err == nil {
		t.Fatal("expired deadline accepted")
	}
}

func TestDrivePlanAutoEndAndRecovery(t *testing.T) {
	now := time.Now()
	sink := &receiptSink{}
	journal := NewActionJournal(sink)
	original := SemanticObservation{MatchID: "m", Phase: "turn", Source: "base", CapturedAt: now}
	state, err := semanticState(original)
	if err != nil {
		t.Fatal(err)
	}
	snapshot := Snapshot{Version: 1, Source: "base", Phase: "turn", State: state}
	plan := ActionPlan{Action: rules.Action{Kind: "play"}, Deadline: now.Add(time.Second), AutoEnd: true, Inputs: []PlannedInput{{Kind: "click", Point: image.Pt(1, 1)}, {TargetKind: "control", TargetID: "end-turn"}}}
	if err = journal.Begin("a", snapshot, plan); err != nil {
		t.Fatal(err)
	}
	if err = journal.ExecuteStep(Action{Kind: "click", X: 1, Y: 1, Reference: "x"}, func(a Action) error {
		return a.onReceipt(InputReceipt{Outcome: "delivered", EventsSent: 2, DeliveredAt: time.Now()})
	}); err != nil {
		t.Fatal(err)
	}
	last, err := journal.DeliveredAt("a")
	if err != nil {
		t.Fatal(err)
	}
	pending := PendingPlan{Plan: plan, MatchID: "m", Next: 1, After: last}
	attempt := 0
	loop := LocalLoop{Journal: journal, ObserveAutoEnd: func(context.Context, rules.Action, time.Time) (SemanticObservation, bool, error) {
		attempt++
		if attempt == 1 {
			return SemanticObservation{}, false, ErrUnstableVisual
		}
		first := time.Now()
		second := first.Add(time.Nanosecond)
		return SemanticObservation{MatchID: "m", Source: "second", CapturedAt: second, Frames: withTestEvidence(SemanticObservation{MatchID: "m", Source: "second", CapturedAt: second}).Frames}, true, nil
	}, ExecutePlanned: func(context.Context, PlannedInput, TargetEvidence, time.Time) error {
		return errors.New("must not send end-turn")
	}}
	if err = loop.drivePlan(context.Background(), &pending, snapshot, "a"); err != nil {
		t.Fatal(err)
	}
	if !journal.Pending().AutoEnded || attempt != 2 {
		t.Fatal("automatic end not recorded after unstable frame")
	}
}

func TestPendingPlanRejectsOldEvidenceAndUncertainReceipt(t *testing.T) {
	now := time.Now()
	plan := PendingPlan{MatchID: "m", After: now.Add(-time.Second), Plan: ActionPlan{Deadline: now.Add(time.Second), Inputs: []PlannedInput{{Kind: "click", TargetKind: "card", TargetID: "a", LayoutID: "hand", Point: image.Pt(10, 10)}}}}
	e := TargetEvidence{MatchID: "m", Kind: "card", ID: "a", LayoutID: "hand", Bounds: image.Rect(0, 0, 20, 20), Sources: [2]string{"one", "two"}, Captured: [2]time.Time{now.Add(-500 * time.Millisecond), now.Add(-time.Millisecond)}}
	if _, err := plan.NextInput(now, e); err != nil {
		t.Fatal(err)
	}
	old := e
	old.Captured[0] = plan.After
	if _, err := plan.NextInput(now, old); err == nil {
		t.Fatal("accepted pre-delivery frame")
	}
	if err := plan.RecordReceipt(InputReceipt{Outcome: "partial", EventsSent: 1, DeliveredAt: now}); err == nil {
		t.Fatal("accepted partial receipt")
	}
	if _, err := plan.NextInput(now, e); err == nil {
		t.Fatal("replayed uncertain input")
	}
}
