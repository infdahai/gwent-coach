package observe

import (
	"context"
	"fmt"
	"testing"
	"time"

	"gwentcoachmvp/internal/rules"
	"gwentcoachmvp/internal/uiphase"
)

func TestResumeFirstRoutesPendingRoundNavigationToSession(t *testing.T) {
	phase := "training"
	resume := false
	inputs, calls, seq := 0, 0, 0
	var session TrainingSession
	session = TrainingSession{ID: "s", LogRoot: t.TempDir(), StartMatch: 1, Matches: 1, TransitionBudget: time.Second,
		Entry: []SessionTransition{{From: "training", To: "mulligan", Input: Action{Kind: "click"}}, {From: "mulligan", To: "confirm", Input: Action{Kind: "click"}}, {From: "confirm", To: "turn", Input: Action{Kind: "click"}}}, Exit: []SessionTransition{{From: "result", To: "training", Input: Action{Kind: "click"}}},
		ObserveStage: func(_ context.Context, id string) (uiphase.Event, error) {
			if calls == 1 && !resume && phase == "confirm" {
				return uiphase.Event{}, fmt.Errorf("simulated process interruption")
			}
			seq++
			a := uiphase.FrameRef{ID: fmt.Sprintf("%d-a", seq), StreamID: id, CapturedAt: time.Now()}
			b := a
			b.ID = fmt.Sprintf("%d-b", seq)
			b.CapturedAt = a.CapturedAt.Add(time.Nanosecond)
			return uiphase.Event{Phase: phase, Confirmed: true, Sources: [2]uiphase.FrameRef{a, b}, Evidence: uiphase.Evidence{Frame: b}}, nil
		}, ExecuteInput: func(_ context.Context, a Action) error {
			inputs++
			if err := a.onReceipt(InputReceipt{Outcome: "delivered", EventsSent: 2, DeliveredAt: time.Now()}); err != nil {
				return err
			}
			switch a.ExpectedPhase {
			case "training":
				phase = "mulligan"
			case "mulligan":
				phase = "confirm"
			case "confirm":
				phase = "turn"
			case "result":
				phase = "training"
			}
			return nil
		}, RunMatch: func(ctx context.Context, id string, j *ActionJournal) (SemanticObservation, error) {
			calls++
			if !resume {
				phase = "mulligan"
				return SemanticObservation{}, session.FinishMulligan(ctx, id, 2, j)
			}
			if j.Pending() != nil || phase != "turn" {
				t.Fatal("round pending was passed into game loop")
			}
			phase = "result"
			o := withTestEvidence(SemanticObservation{Source: "result", MatchID: id, Phase: "result", Result: true, CapturedAt: time.Now(), View: rules.View{Finished: true, Winner: 1}})
			state, _ := semanticState(o)
			err := j.Result(Snapshot{Version: j.original.Version + 1, Phase: o.Phase, Source: o.Source, State: state}, o)
			return o, err
		}}
	if err := session.Run(context.Background()); err == nil {
		t.Fatal("expected interruption")
	}
	if inputs != 4 {
		t.Fatalf("inputs before resume=%d", inputs)
	}
	resume = true
	session.ResumeFirst = true
	if err := session.Run(context.Background()); err != nil {
		t.Fatal(err)
	}
	if inputs != 6 || calls != 2 {
		t.Fatalf("replayed round input: inputs=%d match calls=%d", inputs, calls)
	}
}
