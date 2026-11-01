package observe

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	"gwentcoachmvp/internal/rules"
)

type boundaryEvaluator struct{}

func (boundaryEvaluator) Candidates(rules.DecisionInput) ([]rules.Action, error) {
	return []rules.Action{{Kind: "pass"}}, nil
}
func (boundaryEvaluator) ValidateAction(rules.DecisionInput, rules.Action) error {
	return fmt.Errorf("state changed")
}

func TestLocalLoopRejectsExpandedFilterAndRevalidatesBeforeIntent(t *testing.T) {
	for _, filter := range []bool{true, false} {
		t.Run(fmt.Sprint(filter), func(t *testing.T) {
			sink := &receiptSink{}
			journal := NewActionJournal(sink)
			first := true
			loop := LocalLoop{Decisions: boundaryEvaluator{}, Policy: &loopTestPolicy{}, Journal: journal, DecisionBudget: time.Second, ConfirmationBudget: time.Second,
				Observe: func(ctx context.Context) (SemanticObservation, error) {
					if first {
						first = false
						return SemanticObservation{MatchID: "m", Source: "frame", Phase: "turn", Eligible: true, CapturedAt: time.Now()}, nil
					}
					<-ctx.Done()
					return SemanticObservation{}, ctx.Err()
				},
				Execute: func(context.Context, Snapshot, rules.Action, time.Time) error { t.Fatal("unsafe input"); return nil }, Confirm: func(rules.Action, SemanticObservation) bool { return true }}
			if filter {
				loop.FilterCandidates = func(_ SemanticObservation, a []rules.Action) []rules.Action {
					return append(a, rules.Action{Kind: "play", Card: "invented"})
				}
			}
			err := loop.Run(context.Background())
			want := "final rules validation"
			if filter {
				want = "rules-candidate subset"
			}
			if err == nil || !strings.Contains(err.Error(), want) {
				t.Fatalf("err=%v", err)
			}
			if len(sink.events) != 0 {
				t.Fatalf("intent began before validation: %+v", sink.events)
			}
		})
	}
}
