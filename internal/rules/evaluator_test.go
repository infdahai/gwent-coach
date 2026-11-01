package rules

import (
	"testing"
	"time"

	"gwentcoachmvp/internal/cards"
)

func TestEvaluatorIndependentOfPolicyAndRetainsOpaqueBoundary(t *testing.T) {
	catalog, err := cards.Load("../../data/cards/14.10.0/en/cards.json")
	if err != nil {
		t.Fatal(err)
	}
	engine, err := New(catalog)
	if err != nil {
		t.Fatal(err)
	}
	state, err := engine.Initial([2][]int{{132403}, {}}, [2][]int{}, 0)
	if err != nil {
		t.Fatal(err)
	}
	view, _ := state.View(0)
	in := DecisionInput{MatchID: "m", Phase: "turn", View: view, HandOrder: []string{state.Cards[0].ID}, Deadline: time.Now().Add(-time.Second)}
	evaluator := NewEvaluator(engine)
	play := Action{Kind: "play", Card: state.Cards[0].ID}
	if err = evaluator.ValidateAction(in, play); err != nil {
		t.Fatalf("candidate-free validation: %v", err)
	}
	if _, err = evaluator.Candidates(in); err == nil {
		t.Fatal("candidate enumeration ignored deadline")
	}
	in.View.Visible = append(in.View.Visible, Instance{ID: "unknown-object", Owner: 1, Zone: Board, Row: 0})
	if err = evaluator.ValidateAction(in, play); err == nil {
		t.Fatal("unknown object allowed play")
	}
	in.AllowPassWithCards = true
	if err = evaluator.ValidateAction(in, Action{Kind: "pass"}); err != nil {
		t.Fatal(err)
	}
	in.View.Passed[1] = true
	if err = evaluator.ValidateAction(in, Action{Kind: "pass"}); err == nil {
		t.Fatal("opaque terminal round simulation accepted")
	}
}

func TestOrderCandidatesPreservesOrderAbilityAndHandPriority(t *testing.T) {
	in := DecisionInput{HandOrder: []string{"second", "first"}, Candidates: []Action{{Kind: "pass"}, {Kind: "play", Card: "first"}, {Kind: "play", Card: "second"}, {Kind: "order", Card: "tactic"}}}
	got := OrderCandidates(in)
	if got[0].Kind != "order" || got[1].Card != "second" || got[2].Card != "first" || got[3].Kind != "pass" {
		t.Fatalf("order=%+v", got)
	}
	if in.Candidates[0].Kind != "pass" {
		t.Fatal("mutated caller candidates")
	}
}
