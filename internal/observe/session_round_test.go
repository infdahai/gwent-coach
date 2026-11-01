package observe

import (
	"context"
	"testing"
	"time"

	"gwentcoachmvp/internal/cards"
	"gwentcoachmvp/internal/rules"
)

func TestLocalLoopRetainsVersionAfterSessionRoundNavigation(t *testing.T) {
	catalog, err := cards.Load("../../data/cards/14.10.0/en/cards.json")
	if err != nil {
		t.Fatal(err)
	}
	engine, err := rules.New(catalog)
	if err != nil {
		t.Fatal(err)
	}
	state, err := engine.Initial([2][]int{}, [2][]int{}, 0)
	if err != nil {
		t.Fatal(err)
	}
	view, _ := state.View(0)
	sink := &receiptSink{}
	journal := NewActionJournal(sink)
	passed, roundDone := make(chan struct{}), make(chan struct{})
	seq := 0
	var navigationVersion uint64
	loop := LocalLoop{Decisions: rules.NewEvaluator(engine), Journal: journal, DecisionBudget: time.Second, ConfirmationBudget: time.Second,
		Observe: func(ctx context.Context) (SemanticObservation, error) {
			seq++
			if seq == 1 {
				return SemanticObservation{Source: "before", MatchID: "m", Phase: "turn", View: view, Eligible: true, CapturedAt: time.Now()}, nil
			}
			if seq == 2 {
				select {
				case <-passed:
				case <-ctx.Done():
					return SemanticObservation{}, ctx.Err()
				}
				time.Sleep(time.Millisecond)
				v := view
				v.Round = 2
				return withTestEvidence(SemanticObservation{Source: "mulligan", MatchID: "m", Phase: "mulligan", View: v, CapturedAt: time.Now()}), nil
			}
			select {
			case <-roundDone:
			case <-ctx.Done():
				return SemanticObservation{}, ctx.Err()
			}
			time.Sleep(time.Millisecond)
			v := view
			v.Round = 2
			v.Finished = true
			v.Winner = 1
			return SemanticObservation{Source: "result", MatchID: "m", Phase: "result", View: v, Result: true, CapturedAt: time.Now()}, nil
		}, Execute: func(_ context.Context, _ Snapshot, _ rules.Action, _ time.Time) error {
			err := journal.ExecuteStep(Action{Kind: "hold"}, func(a Action) error {
				return a.onReceipt(InputReceipt{Outcome: "delivered", EventsSent: 2, DeliveredAt: time.Now()})
			})
			close(passed)
			return err
		}, Confirm: func(_ rules.Action, o SemanticObservation) bool { return o.Phase == "mulligan" },
		OnRoundStart: func(_ context.Context, o SemanticObservation) error {
			beforeState, _ := semanticState(o)
			version := journal.original.Version + 1
			if err := journal.Begin("round-navigation", Snapshot{Version: version, Source: o.Source, Phase: o.Phase, State: beforeState}, NavigationAction{Input: Action{Kind: "click"}, To: "turn"}); err != nil {
				return err
			}
			if err := journal.ExecuteStep(Action{Kind: "click"}, func(a Action) error {
				return a.onReceipt(InputReceipt{Outcome: "delivered", EventsSent: 2, DeliveredAt: time.Now()})
			}); err != nil {
				return err
			}
			if err := journal.MarkDelivered("round-navigation"); err != nil {
				return err
			}
			o.Source = "round-turn"
			o.Phase = "turn"
			o.CapturedAt = time.Now()
			o = withTestEvidence(o)
			afterState, _ := semanticState(o)
			navigationVersion = version + 1
			if err := journal.Confirm("round-navigation", Snapshot{Version: navigationVersion, Source: o.Source, Phase: o.Phase, State: afterState}, o); err != nil {
				return err
			}
			close(roundDone)
			return nil
		}}
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if err = loop.Run(ctx); err != nil {
		t.Fatal(err)
	}
	last := sink.events[len(sink.events)-1]
	if last.EventType != "result_observed" || last.StateVersion == nil || *last.StateVersion <= navigationVersion {
		t.Fatalf("version regressed: %+v after %d", last, navigationVersion)
	}
}
