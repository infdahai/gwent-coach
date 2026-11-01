package rules

import (
	"encoding/json"
	"gwentcoachmvp/internal/cards"
	"reflect"
	"testing"
	"time"
)

func engine(t *testing.T) *Engine {
	t.Helper()
	c, err := cards.Load("../../data/cards/14.10.0/en/cards.json")
	if err != nil {
		t.Fatal(err)
	}
	e, err := New(c)
	if err != nil {
		t.Fatal(err)
	}
	return e
}
func initial(t *testing.T, e *Engine, h [2][]int) State {
	t.Helper()
	s, err := e.Initial(h, [2][]int{}, 0)
	if err != nil {
		t.Fatal(err)
	}
	return s
}
func act(t *testing.T, e *Engine, s State, a Action) State {
	t.Helper()
	n, _, err := e.Apply(s, a)
	if err != nil {
		t.Fatal(err)
	}
	return n
}
func TestCoverage(t *testing.T) {
	e := engine(t)
	rows := e.Coverage()
	if len(rows) != 1381 {
		t.Fatal(len(rows))
	}
	n := 0
	for _, r := range rows {
		if r.Supported {
			n++
		}
		if r.Reason == "" {
			t.Fatal(r)
		}
	}
	if n != 10 {
		t.Fatal(n)
	}
	if _, err := e.Initial([2][]int{{112101}, {}}, [2][]int{}, 0); err == nil {
		t.Fatal("unsupported accepted")
	}
	c, _ := cards.Load("../../data/cards/14.10.0/en/cards.json")
	x := c.Cards[113301]
	x.Ability = "Damage a unit by 6.\n"
	c.Cards[113301] = x
	if _, err := New(c); err == nil {
		t.Fatal("changed text accepted")
	}
}
func TestTacticalAdvantageOrder(t *testing.T) {
	e := engine(t)
	s := initial(t, e, [2][]int{{132403, 202140}, {152405}})
	s.Cards[0].Zone, s.Cards[0].Row = Board, 0
	s.Cards[1].Zone, s.Cards[1].Row = Board, 0
	a := Action{Kind: "order", Card: s.Cards[1].ID, Target: s.Cards[0].ID}
	n := act(t, e, s, a)
	if n.Cards[0].Power != 6 || index(n, a.Card) >= 0 || n.Turn != 0 {
		t.Fatalf("bad order result: %+v", n)
	}
	if _, _, err := e.Apply(n, a); err == nil {
		t.Fatal("order reused")
	}
	invalid := a
	invalid.Target = s.Cards[1].ID
	if _, _, err := e.Apply(s, invalid); err == nil {
		t.Fatal("targeted a stratagem")
	}
	v, err := s.View(0)
	if err != nil {
		t.Fatal(err)
	}
	service := NewDecisionService(e)
	defer service.Close()
	in := DecisionInput{MatchID: "test", Phase: "turn", View: v, Deadline: time.Now().Add(time.Second)}
	in.Candidates, err = service.Candidates(in)
	if err != nil {
		t.Fatal(err)
	}
	if got, err := service.Fast(in); err != nil || got != a {
		t.Fatalf("order=%+v err=%v", got, err)
	}
	in.View.Visible = append(in.View.Visible, Instance{ID: "unknown-hand", Owner: 0, Zone: Hand, Row: -1}, Instance{ID: "enemy-unknown", Owner: 1, Zone: Board, Row: 0, Power: 5})
	in.View.HandCount[0] = 1
	in.View.Score[1] = 5
	in.HandOrder = []string{"unknown-hand"}
	in.Candidates, err = service.Candidates(in)
	if err != nil {
		t.Fatal(err)
	}
	if got, err := service.Fast(in); err != nil || got != a {
		t.Fatalf("opaque order=%+v err=%v", got, err)
	}
}

func TestMageDeployAgainstObservedSwordmaster(t *testing.T) {
	e := engine(t)
	s := initial(t, e, [2][]int{{202908, 132403}, {200535}})
	s.Cards[1].Zone, s.Cards[1].Row, s.Cards[1].Power = Board, 0, 6
	s.Cards[2].Zone, s.Cards[2].Row, s.Cards[2].Power = Board, 1, 5
	v, err := s.View(0)
	if err != nil {
		t.Fatal(err)
	}
	v.Visible = append(v.Visible, Instance{ID: "unknown", Owner: 0, Zone: Hand, Row: -1})
	v.HandCount[0]++
	service := NewDecisionService(e)
	defer service.Close()
	in := DecisionInput{MatchID: "m", Phase: "turn", View: v, HandOrder: []string{"unknown", s.Cards[0].ID}, Deadline: time.Now().Add(time.Second)}
	in.Candidates, err = service.Candidates(in)
	if err != nil {
		t.Fatal(err)
	}
	a, err := service.Fast(in)
	if err != nil || a.Card != s.Cards[0].ID || a.Target != s.Cards[2].ID {
		t.Fatalf("action=%+v err=%v", a, err)
	}
	n, _, err := e.Apply(s, a)
	if err != nil || n.Cards[2].Power != 3 || n.Cards[0].Zone != Board || n.Turn != 1 {
		t.Fatalf("state=%+v err=%v", n, err)
	}
	if _, _, err = e.Apply(n, Action{Kind: "pass", Player: 1}); err == nil {
		t.Fatal("accepted unmodeled swordmaster active turn")
	}
	v.Visible = append(v.Visible, Instance{ID: "unknown-board", Owner: 1, Zone: Board, Row: 0, Power: 2})
	v.Score[1] += 2
	in.View = v
	in.Candidates, err = service.Candidates(in)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = service.Fast(in); err == nil {
		t.Fatal("accepted mage with unrelated unknown board")
	}
}

func TestScholarUsesKnownStartingDeckAndRejectsUnknown(t *testing.T) {
	e := engine(t)
	s := initial(t, e, [2][]int{{202218, 132403, 202908}, {200535, 142102}})
	s.Cards[1].Zone, s.Cards[1].Row, s.Cards[1].Power = Board, 0, 5
	s.Cards[2].Zone, s.Cards[2].Row = Board, 0
	s.Cards[3].Zone, s.Cards[3].Row, s.Cards[3].Power = Board, 1, 4
	s.Cards[4].Zone, s.Cards[4].Row, s.Cards[4].Power = Board, 0, 6
	s.StartingHighProvision[0] = 2
	v, err := s.View(0)
	if err != nil {
		t.Fatal(err)
	}
	v.Visible = append(v.Visible, Instance{ID: "unknown", Owner: 0, Zone: Hand, Row: -1})
	v.HandCount[0]++
	service := NewDecisionService(e)
	defer service.Close()
	in := DecisionInput{MatchID: "m", Phase: "turn", View: v, HandOrder: []string{"unknown", s.Cards[0].ID}, Deadline: time.Now().Add(time.Second)}
	in.Candidates, err = service.Candidates(in)
	if err != nil {
		t.Fatal(err)
	}
	a, err := service.Fast(in)
	if err != nil || a.Card != s.Cards[0].ID || a.Target != "" {
		t.Fatalf("action=%+v err=%v", a, err)
	}
	n, _, err := e.Apply(s, a)
	if err != nil || n.Cards[0].Power != 4 {
		t.Fatalf("state=%+v err=%v", n, err)
	}
	v.StartingDeckKnown = false
	v.StartingHighProvision = 0
	in.View = v
	in.Candidates, err = service.Candidates(in)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = service.Fast(in); err == nil {
		t.Fatal("accepted unknown starting deck")
	}
}

func TestEffects(t *testing.T) {
	for _, tc := range []struct {
		name                                     string
		card, armor, power, wantPower, wantArmor int
		dead                                     bool
	}{
		{"armor absorbs", 113301, 6, 5, 5, 1, false},
		{"armor overflow kills", 113301, 1, 3, 0, 0, true},
		{"boost", 113310, 2, 3, 9, 2, false},
		{"stunning armored", 202465, 3, 5, 1, 0, false},
		{"stunning unarmored", 202465, 0, 6, 1, 0, false},
		{"piercing", 202411, 8, 5, 1, 8, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			e := engine(t)
			s := initial(t, e, [2][]int{{tc.card}, {152405}})
			s.Cards[1].Zone = Board
			s.Cards[1].Row = 0
			s.Cards[1].Power = tc.power
			s.Cards[1].Armor = tc.armor
			s = act(t, e, s, Action{Kind: "play", Player: 0, Card: s.Cards[0].ID, Target: s.Cards[1].ID})
			c := s.Cards[1]
			if c.Power != tc.wantPower || c.Armor != tc.wantArmor || (c.Zone == Graveyard) != tc.dead {
				t.Fatal(c)
			}
		})
	}
}
func TestTrainingDeckEffects(t *testing.T) {
	e := engine(t)
	for _, tc := range []struct {
		power, armor, wantPower, wantArmor int
	}{{5, 0, 3, 0}, {6, 0, 2, 0}, {6, 3, 5, 0}} {
		s := initial(t, e, [2][]int{{202243}, {152405}})
		s.Cards[1].Zone, s.Cards[1].Row, s.Cards[1].Power, s.Cards[1].Armor = Board, 0, tc.power, tc.armor
		n := act(t, e, s, Action{Kind: "play", Card: s.Cards[0].ID, Target: s.Cards[1].ID})
		if n.Cards[1].Power != tc.wantPower || n.Cards[1].Armor != tc.wantArmor {
			t.Fatalf("damage case %+v: %+v", tc, n.Cards[1])
		}
	}
	s := initial(t, e, [2][]int{{132403, 152405, 152405}, {152405}})
	s.Cards[1].Zone, s.Cards[1].Row = Board, 0
	s.Cards[2].Zone, s.Cards[2].Row = Board, 1
	s.Cards[3].Zone, s.Cards[3].Row = Board, 0
	n := act(t, e, s, Action{Kind: "play", Card: s.Cards[0].ID})
	if n.Cards[0].Power != 2 {
		t.Fatalf("wolf pack should count other allied units on its row: %+v", n.Cards[0])
	}
	knight := initial(t, e, [2][]int{{202243, 152405}, {}})
	knight.Cards[1].Zone, knight.Cards[1].Row = Board, 0
	if _, _, err := e.Apply(knight, Action{Kind: "play", Card: knight.Cards[0].ID, Target: knight.Cards[1].ID}); err == nil {
		t.Fatal("knight accepted allied target")
	}
}

func TestKeeperEmptyEnemyBoardDecision(t *testing.T) {
	e := engine(t)
	s := initial(t, e, [2][]int{{202245}, {152405}})
	view, err := s.View(0)
	if err != nil {
		t.Fatal(err)
	}
	service := NewDecisionService(e)
	defer service.Close()
	input := DecisionInput{MatchID: "m", Phase: "turn", Player: 0, View: view, HandOrder: []string{s.Cards[0].ID}, Deadline: time.Now().Add(time.Second)}
	input.Candidates, err = service.Candidates(input)
	if err != nil {
		t.Fatal(err)
	}
	action, err := service.Fast(input)
	if err != nil || action.Kind != "play" || action.Card != s.Cards[0].ID || action.Target != "" {
		t.Fatalf("action=%+v err=%v", action, err)
	}
	next, _, err := e.Apply(s, action)
	if err != nil || next.Score(0) != 3 || next.Cards[0].Zone != Board {
		t.Fatalf("state=%+v err=%v", next, err)
	}
	s.Cards[1].Zone, s.Cards[1].Row = Board, 0
	view, err = s.View(0)
	if err != nil {
		t.Fatal(err)
	}
	input.View = view
	input.Candidates, err = service.Candidates(input)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := service.Fast(input); err == nil {
		t.Fatal("accepted unsupported enemy target")
	}
	if _, supported := e.Effect(202245); supported {
		t.Fatal("partial ability reported as fully supported")
	}
}

func TestWolfDecisionWithOpaqueEnemyUnits(t *testing.T) {
	e := engine(t)
	s := initial(t, e, [2][]int{{202245, 132403}, {}})
	view, err := s.View(0)
	if err != nil {
		t.Fatal(err)
	}
	view.Visible = append(view.Visible, rulesOpaqueEnemy("enemy-a", 7, 0), rulesOpaqueEnemy("enemy-b", 2, 1), Instance{ID: "unknown-hand", CardID: 0, Owner: 0, Row: -1, Zone: Hand}, Instance{ID: "unknown-grave", CardID: 0, Owner: 1, Row: -1, Zone: Graveyard})
	view.HandCount[0]++
	view.Score[1] = 9
	in := DecisionInput{MatchID: "m", Phase: "turn", Player: 0, View: view, HandOrder: []string{s.Cards[0].ID, "unknown-hand", s.Cards[1].ID}, Deadline: time.Now().Add(time.Second)}
	d := NewDecisionService(e)
	defer d.Close()
	in.Candidates, err = d.Candidates(in)
	if err != nil {
		t.Fatal(err)
	}
	for _, a := range in.Candidates {
		if a.Kind != "play" || a.Card != s.Cards[1].ID {
			t.Fatalf("unsafe candidate: %+v", a)
		}
	}
	a, err := d.Fast(in)
	if err != nil || a.Card != s.Cards[1].ID || a.Target != "" {
		t.Fatalf("decision=%+v err=%v", a, err)
	}
	view.Visible[len(view.Visible)-3].Owner = 0
	in.View = view
	if _, err := d.Candidates(in); err == nil {
		t.Fatal("accepted unknown allied board")
	}
}
func rulesOpaqueEnemy(id string, power, row int) Instance {
	return Instance{ID: id, CardID: 0, Owner: 1, Power: power, Row: row, Zone: Board}
}

func TestPlundererAndAtomicity(t *testing.T) {
	e := engine(t)
	s := initial(t, e, [2][]int{{152405, 202462}, {152405}})
	s = act(t, e, s, Action{Kind: "play", Card: s.Cards[0].ID})
	s = act(t, e, s, Action{Kind: "pass", Player: 1})
	s = act(t, e, s, Action{Kind: "play", Card: s.Cards[1].ID, Target: s.Cards[0].ID, Row: 1})
	if s.Cards[1].Power != 7 || s.Cards[1].Armor != 1 || s.Cards[0].Power != 3 || s.Turn != 0 {
		t.Fatal(s)
	}
	for _, a := range []Action{{Kind: "play", Card: s.Cards[1].ID}, {Kind: "play", Player: 1, Card: s.Cards[2].ID}, {Kind: "pass", Target: "bad"}, {Kind: "start_round"}, {Kind: "unknown"}} {
		before, _ := json.Marshal(s)
		next, event, err := e.Apply(s, a)
		after, _ := json.Marshal(s)
		if err == nil || string(before) != string(after) || !reflect.DeepEqual(next, s) || event.Sequence != 0 {
			t.Fatalf("not atomic: %+v", a)
		}
	}
}
func TestMatchAndResources(t *testing.T) {
	e := engine(t)
	s, err := e.Initial([2][]int{{152405, 152405}, {152405}}, [2][]int{{113310}, {}}, 0)
	if err != nil {
		t.Fatal(err)
	}
	s = act(t, e, s, Action{Kind: "play", Card: s.Cards[0].ID})
	s = act(t, e, s, Action{Kind: "pass", Player: 1})
	s = act(t, e, s, Action{Kind: "pass"})
	if !s.BetweenRounds || s.Round != 2 || s.Wins != [2]int{1, 0} || s.Cards[1].Zone != Hand || s.Cards[0].Zone != Graveyard {
		t.Fatal(s)
	}
	before := clone(s)
	_, _, err = e.Apply(s, Action{Kind: "start_round", Draws: [2]int{1, 1}})
	if err == nil || !reflect.DeepEqual(before, s) {
		t.Fatal("partial draws")
	}
	s = act(t, e, s, Action{Kind: "start_round", Draws: [2]int{1, 0}})
	if s.Cards[2].Zone != Hand {
		t.Fatal("draw missing")
	}
	s = act(t, e, s, Action{Kind: "play", Card: s.Cards[1].ID})
	s = act(t, e, s, Action{Kind: "pass", Player: 1})
	s = act(t, e, s, Action{Kind: "pass"})
	if !s.Finished || s.Winner != 0 || s.Round != 2 {
		t.Fatal("2-0 must end", s)
	}
	if _, _, err = e.Apply(s, Action{Kind: "pass"}); err == nil {
		t.Fatal("finished match accepted action")
	}
}
func TestTiesAndThirdRound(t *testing.T) {
	e := engine(t)
	for _, tie := range []bool{true, false} {
		s := initial(t, e, [2][]int{{152405}, {152405}})
		for round := 1; round <= 3 && !s.Finished; round++ {
			if round > 1 {
				s = act(t, e, s, Action{Kind: "start_round"})
			}
			if !tie && round < 3 {
				p := round - 1
				if p == 1 {
					s.Turn = 1
				}
				idx := p
				s = act(t, e, s, Action{Kind: "play", Player: p, Card: s.Cards[idx].ID})
			}
			s = act(t, e, s, Action{Kind: "pass", Player: s.Turn})
			s = act(t, e, s, Action{Kind: "pass", Player: s.Turn})
		}
		if !s.Finished || s.Winner != -1 || s.Wins != [2]int{2, 2} {
			t.Fatal(s)
		}
		if tie && s.Round != 2 || !tie && s.Round != 3 {
			t.Fatal(s.Round)
		}
	}
}
func TestImportBoundaries(t *testing.T) {
	e := engine(t)
	base := initial(t, e, [2][]int{{152405, 113310}, {152405}})
	for _, mutate := range []func(*State){
		func(s *State) { s.Wins = [2]int{2, 0} },
		func(s *State) { s.Cards[0].Power = 0 },
		func(s *State) { s.Cards[0].Owner = 2 },
		func(s *State) { s.Cards[0].Zone = Board; s.Cards[0].Row = 0; s.Cards[0].Power = int(^uint(0) >> 1) },
	} {
		s := clone(base)
		mutate(&s)
		if _, err := e.Replay(s, nil); err == nil {
			t.Fatal("invalid import accepted", s)
		}
		if _, _, err := e.Apply(s, Action{Kind: "play", Card: s.Cards[0].ID}); err == nil {
			t.Fatal("invalid action state accepted")
		}
	}
	bad := clone(base)
	bad.Cards[0].Owner = 2
	if _, err := bad.View(0); err == nil {
		t.Fatal("invalid view accepted")
	}
	limit := clone(base)
	limit.Cards[0].Zone = Board
	limit.Cards[0].Row = 0
	limit.Cards[0].Power = 1000000
	if next, _, err := e.Apply(limit, Action{Kind: "play", Card: limit.Cards[1].ID, Target: limit.Cards[0].ID}); err == nil || !reflect.DeepEqual(next, limit) {
		t.Fatal("postcondition failure not atomic")
	}
	c, _ := cards.Load("../../data/cards/14.10.0/en/cards.json")
	card := c.Cards[152405]
	card.Attributes.Type = "Special"
	c.Cards[152405] = card
	if _, err := New(c); err == nil {
		t.Fatal("changed attributes accepted")
	}
	self := initial(t, e, [2][]int{{202462}, {}})
	self = act(t, e, self, Action{Kind: "play", Card: self.Cards[0].ID, Target: self.Cards[0].ID})
	if self.Cards[0].Power != 6 || self.Cards[0].Armor != 0 {
		t.Fatal(self)
	}
}

func TestReplayAndTracker(t *testing.T) {
	e := engine(t)
	s := initial(t, e, [2][]int{{152405}, {152405}})
	tracker := NewTracker(e, s)
	view, _ := tracker.ConfirmedView(0)
	if len(view.Visible) != 1 || view.HandCount != [2]int{1, 1} {
		t.Fatal(view)
	}
	event, err := tracker.Predict(Action{Kind: "play", Card: s.Cards[0].ID})
	if err != nil {
		t.Fatal(err)
	}
	unchanged, _ := tracker.ConfirmedView(0)
	if !reflect.DeepEqual(view, unchanged) {
		t.Fatal("prediction leaked")
	}
	if _, err = tracker.Predict(Action{Kind: "pass"}); err == nil {
		t.Fatal("retry accepted")
	}
	observed, _ := event.After.View(0)
	if err = tracker.Confirm(0, observed, "frame1"); err == nil {
		t.Fatal("undelivered confirmed")
	}
	if err = tracker.Delivered(event.Sequence); err != nil {
		t.Fatal(err)
	}
	bad := observed
	bad.Score[0]++
	if err = tracker.Confirm(0, bad, "frame1"); err == nil {
		t.Fatal("mismatch accepted")
	}
	if err = tracker.Confirm(0, observed, "frame2"); err != nil {
		t.Fatal(err)
	}
	data, _ := json.Marshal([]Event{event})
	var events []Event
	if err = json.Unmarshal(data, &events); err != nil {
		t.Fatal(err)
	}
	replay, err := e.Replay(s, events)
	if err != nil || !reflect.DeepEqual(replay, event.After) {
		t.Fatal(err)
	}
	events[0].After.Wins[0]++
	if _, err = e.Replay(s, events); err == nil {
		t.Fatal("tampered receipt accepted")
	}
	_, opponent, err := e.Apply(event.After, Action{Kind: "pass", Player: 1})
	if err != nil {
		t.Fatal(err)
	}
	if err = tracker.Reconcile([]Event{opponent}, "frame3"); err != nil {
		t.Fatal(err)
	}
}
