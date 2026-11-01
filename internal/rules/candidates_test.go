package rules

import (
	"testing"
	"time"

	"gwentcoachmvp/internal/cards"
)

func TestUnknownZeroPowerEnemyObjectAllowsOnlyPass(t *testing.T) {
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
	view.Visible = append(view.Visible, Instance{ID: "object", Owner: 1, Zone: Board, Row: 0})
	service := NewDecisionService(engine)
	defer service.Close()
	in := DecisionInput{MatchID: "test", Phase: "turn", View: view, HandOrder: []string{state.Cards[0].ID}, Deadline: time.Now().Add(time.Second), AllowPassWithCards: true}
	got, err := service.Candidates(in)
	if err != nil || len(got) != 1 || got[0].Kind != "pass" {
		t.Fatalf("candidates=%+v err=%v", got, err)
	}
}

func TestCandidatesEnumeratesLegalTargets(t *testing.T) {
	catalog := &cards.Catalog{Version: "14.10.0", Language: "en", Cards: map[int]cards.Card{}}
	for _, definition := range definitions {
		var card cards.Card
		card.Ability = definition.Text
		card.Attributes.Type = "Special"
		if definition.Kind == "unit" {
			card.Attributes.Type, card.Attributes.Power = "Unit", 5
		}
		if definition.Kind == "plunderer" {
			card.Attributes.Type, card.Attributes.Power, card.Attributes.Armor = "Unit", 3, 1
		}
		if definition.Kind == "wolf_pack" {
			card.Attributes.Type, card.Attributes.Power = "Unit", 1
		}
		if definition.Kind == "knight_errant" || definition.Kind == "keeper_empty_board" {
			card.Attributes.Type, card.Attributes.Power = "Unit", 3
		}
		if definition.Kind == "oxenfurt_scholar" {
			card.Attributes.Type, card.Attributes.Power = "Unit", 2
		}
		if definition.Kind == "isengrim_passive" {
			card.Attributes.Type, card.Attributes.Power = "Unit", 5
		}
		if definition.Kind == "mage_assassin_deploy" {
			card.Attributes.Type, card.Attributes.Power = "Unit", 1
		}
		if definition.Kind == "swordmaster_passive" {
			card.Attributes.Type, card.Attributes.Power = "Unit", 4
		}
		if definition.Kind == "tactical_advantage" {
			card.Attributes.Type = "Stratagem"
		}
		catalog.Cards[definition.ID] = card
	}
	engine, err := New(catalog)
	if err != nil {
		t.Fatal(err)
	}
	state, err := engine.Initial([2][]int{{152405, 202411, 202462}, {152405}}, [2][]int{}, 0)
	if err != nil {
		t.Fatal(err)
	}
	state.Cards[3].Zone, state.Cards[3].Row = Board, 0
	view, err := state.View(0)
	if err != nil {
		t.Fatal(err)
	}
	service := NewDecisionService(engine)
	defer service.Close()
	in := DecisionInput{MatchID: "test", Phase: "turn", Player: 0, View: view, HandOrder: []string{state.Cards[0].ID, state.Cards[1].ID, state.Cards[2].ID}, Deadline: time.Now().Add(time.Second)}
	got, err := service.Candidates(in)
	if err != nil {
		t.Fatal(err)
	}
	want := map[Action]bool{
		{Kind: "pass"}:                                                             true,
		{Kind: "play", Card: state.Cards[0].ID}:                                    true,
		{Kind: "play", Card: state.Cards[0].ID, Row: 1}:                            true,
		{Kind: "play", Card: state.Cards[1].ID, Target: state.Cards[3].ID}:         true,
		{Kind: "play", Card: state.Cards[2].ID, Target: state.Cards[2].ID}:         true,
		{Kind: "play", Card: state.Cards[2].ID, Target: state.Cards[2].ID, Row: 1}: true,
		{Kind: "play", Card: state.Cards[2].ID, Target: state.Cards[3].ID}:         true,
		{Kind: "play", Card: state.Cards[2].ID, Target: state.Cards[3].ID, Row: 1}: true,
	}
	for _, action := range got {
		if !want[action] {
			t.Errorf("unexpected or duplicate action: %+v", action)
		}
		delete(want, action)
	}
	if len(want) != 0 {
		t.Errorf("missing actions: %+v", want)
	}
	deckState, err := engine.Initial([2][]int{{132403, 202243, 152405}, {152405}}, [2][]int{}, 0)
	if err != nil {
		t.Fatal(err)
	}
	deckState.Cards[2].Zone, deckState.Cards[2].Row = Board, 0
	deckState.Cards[3].Zone, deckState.Cards[3].Row = Board, 1
	deckView, err := deckState.View(0)
	if err != nil {
		t.Fatal(err)
	}
	in.View = deckView
	in.HandOrder = []string{deckState.Cards[0].ID, deckState.Cards[1].ID}
	got, err = service.Candidates(in)
	if err != nil {
		t.Fatal(err)
	}
	deckWant := map[Action]bool{
		{Kind: "pass"}: true,
		{Kind: "play", Card: deckState.Cards[0].ID}:                                        true,
		{Kind: "play", Card: deckState.Cards[0].ID, Row: 1}:                                true,
		{Kind: "play", Card: deckState.Cards[1].ID, Target: deckState.Cards[3].ID}:         true,
		{Kind: "play", Card: deckState.Cards[1].ID, Target: deckState.Cards[3].ID, Row: 1}: true,
	}
	for _, action := range got {
		if !deckWant[action] {
			t.Errorf("unexpected deck action: %+v", action)
		}
		delete(deckWant, action)
	}
	if len(deckWant) != 0 {
		t.Errorf("missing deck actions: %+v", deckWant)
	}
	in.Deadline = time.Now().Add(-time.Second)
	if _, err := service.Candidates(in); err != ErrNoDecision {
		t.Fatalf("expired observation: %v", err)
	}
}
