package observe

import (
	"context"
	"fmt"
	"image"
	"testing"
	"time"

	"gwentcoachmvp/internal/rules"
)

type planEvaluator struct{}

func (planEvaluator) Candidates(rules.DecisionInput) ([]rules.Action, error) {
	return []rules.Action{{Kind: "pass"}}, nil
}
func (planEvaluator) ValidateAction(rules.DecisionInput, rules.Action) error { return nil }

func TestLocalLoopDrivesPlannedInput(t *testing.T) {
	sink := &receiptSink{}
	journal := NewActionJournal(sink)
	delivered := make(chan struct{})
	calls := 0
	loop := LocalLoop{Decisions: planEvaluator{}, Journal: journal, DecisionBudget: time.Second, ConfirmationBudget: time.Second,
		Observe: func(ctx context.Context) (SemanticObservation, error) {
			calls++
			if calls == 1 {
				return SemanticObservation{MatchID: "m", Source: "before", Phase: "turn", Eligible: true, CapturedAt: time.Now()}, nil
			}
			select {
			case <-delivered:
				return withTestEvidence(SemanticObservation{MatchID: "m", Source: "after", Phase: "result", Result: true, CapturedAt: time.Now()}), nil
			case <-ctx.Done():
				return SemanticObservation{}, ctx.Err()
			}
		},
		Plan: func(_ SemanticObservation, a rules.Action, deadline time.Time) (ActionPlan, error) {
			return ActionPlan{Action: a, Deadline: deadline, Inputs: []PlannedInput{{Kind: "click", TargetKind: "control", TargetID: "pass", LayoutID: "turn", Point: image.Pt(5, 5)}}}, nil
		},
		ObserveTarget: func(context.Context, PlannedInput, *Snapshot) (TargetEvidence, error) {
			now := time.Now()
			return TargetEvidence{MatchID: "m", Kind: "control", ID: "pass", LayoutID: "turn", Bounds: image.Rect(0, 0, 10, 10), Sources: [2]string{"first", "second"}, Captured: [2]time.Time{now, now.Add(time.Nanosecond)}}, nil
		},
		ExecutePlanned: func(_ context.Context, _ PlannedInput, _ TargetEvidence, _ time.Time) error {
			err := journal.ExecuteStep(Action{Kind: "click", Reference: "second"}, func(a Action) error {
				return a.onReceipt(InputReceipt{Outcome: "delivered", EventsSent: 2, DeliveredAt: time.Now()})
			})
			close(delivered)
			return err
		},
		Confirm: func(_ rules.Action, o SemanticObservation) bool { return o.Result },
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if err := loop.Run(ctx); err != nil {
		t.Fatal(fmt.Errorf("planned loop: %w", err))
	}
	if journal.Pending() != nil {
		t.Fatal("pending action not confirmed")
	}
}
