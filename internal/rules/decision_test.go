package rules

import (
	"errors"
	"gwentcoachmvp/internal/cards"
	"runtime"
	"testing"
	"time"
)

func decisionFixture(t testing.TB) (*DecisionService, State, DecisionInput) {
	t.Helper()
	c, err := cards.Load("../../data/cards/14.10.0/en/cards.json")
	if err != nil {
		t.Fatal(err)
	}
	e, err := New(c)
	if err != nil {
		t.Fatal(err)
	}
	s, err := e.Initial([2][]int{{152405, 113310}, {152405}}, [2][]int{{}, {113301}}, 0)
	if err != nil {
		t.Fatal(err)
	}
	v, _ := s.View(0)
	in := DecisionInput{MatchID: "match", Phase: "turn", Player: 0, View: v, HandOrder: []string{s.Cards[0].ID, s.Cards[1].ID}, Deadline: time.Now().Add(time.Second), Candidates: []Action{{Kind: "play", Card: s.Cards[0].ID}, {Kind: "pass"}}}
	return NewDecisionService(e), s, in
}
func TestDecisionFastAndPublicBoundary(t *testing.T) {
	d, s, in := decisionFixture(t)
	defer d.Close()
	a, err := d.Fast(in)
	if err != nil || a.Kind != "play" {
		t.Fatalf("%+v %v", a, err)
	}
	in.Candidates = []Action{{Kind: "play", Card: s.Cards[0].ID}}
	a, err = d.Fast(in)
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err = d.engine.Apply(s, a); err != nil {
		t.Fatal(err)
	}
	in.Candidates = []Action{{Kind: "play", Card: s.Cards[2].ID, Player: 1}}
	if _, err = d.Fast(in); err == nil {
		t.Fatal("accepted opponent card")
	}
	in.View.Visible = append(in.View.Visible, s.Cards[2])
	if _, err = d.Fast(in); err == nil {
		t.Fatal("accepted hidden hand")
	}
	in.Deadline = time.Now().Add(-time.Second)
	if _, err = d.Fast(in); !errors.Is(err, ErrNoDecision) {
		t.Fatal(err)
	}
}
func TestDecisionLeftmost(t *testing.T) {
	d, s, in := decisionFixture(t)
	defer d.Close()
	s.Cards[1].CardID = s.Cards[0].CardID
	s.Cards[1].Power = s.Cards[0].Power
	s.Cards[1].Armor = s.Cards[0].Armor
	in.View, _ = s.View(0)
	in.HandOrder = []string{s.Cards[1].ID, s.Cards[0].ID}
	in.Candidates = []Action{{Kind: "play", Card: s.Cards[0].ID}, {Kind: "play", Card: s.Cards[1].ID}, {Kind: "pass"}}
	fast, err := d.Fast(in)
	if err != nil || fast.Card != s.Cards[1].ID {
		t.Fatalf("fast leftmost: %+v %v", fast, err)
	}
	if err := d.Start(in); err != nil {
		t.Fatal(err)
	}
	completed := false
	for time.Now().Before(in.Deadline) {
		if action, err := d.Poll(in); err == nil {
			if action != fast {
				t.Fatalf("worker action %+v differs from fast %+v", action, fast)
			}
			completed = true
			break
		}
		runtime.Gosched()
	}
	if !completed {
		t.Fatal("worker did not produce a decision")
	}
	in.Candidates = []Action{{Kind: "play", Card: s.Cards[0].ID}, {Kind: "pass"}}
	if action, err := d.Fast(in); err != nil || action.Card != s.Cards[0].ID {
		t.Fatalf("expected next legal card: %+v %v", action, err)
	}
	in.HandOrder = []string{s.Cards[1].ID, s.Cards[1].ID}
	if _, err := d.Fast(in); err == nil {
		t.Fatal("accepted incomplete hand order")
	}
	for i := range s.Cards {
		if s.Cards[i].Owner == 0 && s.Cards[i].Zone == Hand {
			s.Cards[i].Zone = Graveyard
		}
	}
	in.View, _ = s.View(0)
	in.HandOrder = nil
	in.Candidates = []Action{{Kind: "pass"}}
	if action, err := d.Fast(in); err != nil || action.Kind != "pass" {
		t.Fatalf("empty hand: %+v %v", action, err)
	}
}

func TestPassWithCardsRequiresOptInAndNoOpponentPass(t *testing.T) {
	d, _, input := decisionFixture(t)
	defer d.Close()
	input.Candidates = []Action{{Kind: "pass"}}
	if _, err := d.Fast(input); err == nil {
		t.Fatal("pass was not opt-in")
	}
	input.AllowPassWithCards = true
	if action, err := d.Fast(input); err != nil || action.Kind != "pass" {
		t.Fatalf("pass=%+v err=%v", action, err)
	}
	input.View.Visible = append(input.View.Visible, rulesOpaqueEnemy("opaque", 2, 0))
	input.View.Score[1] = 2
	input.Candidates = []Action{{Kind: "pass"}}
	if action, err := d.Fast(input); err != nil || action.Kind != "pass" {
		t.Fatalf("opaque pass=%+v err=%v", action, err)
	}
	input.View.Passed[1] = true
	if _, err := d.Fast(input); err == nil {
		t.Fatal("accepted round-ending pass with opaque board")
	}
}

func TestDecisionWithTacticalAdvantageOnBoard(t *testing.T) {
	d, _, in := decisionFixture(t)
	defer d.Close()
	in.View.Visible = append(in.View.Visible, Instance{ID: "stratagem", CardID: 202140, Owner: 0, Zone: Board, Row: 1})
	if action, err := d.Fast(in); err != nil || action.Kind != "play" {
		t.Fatalf("decision=%+v err=%v", action, err)
	}
	in.View.Visible[len(in.View.Visible)-1].Power = 1
	in.View.Score[0] = 1
	if _, err := d.Fast(in); err == nil {
		t.Fatal("accepted modified stratagem")
	}
}

func TestDecisionSlow(t *testing.T) {
	d, _, in := decisionFixture(t)
	defer d.Close()
	if err := d.Start(in); err != nil {
		t.Fatal(err)
	}
	var a Action
	var err error
	for time.Now().Before(in.Deadline) {
		a, err = d.Poll(in)
		if err == nil {
			break
		}
		runtime.Gosched()
	}
	if err != nil || a.Kind != "play" {
		t.Fatalf("%+v %v", a, err)
	}
	if err = d.Start(in); err != nil {
		t.Fatal(err)
	}
	stale := in
	stale.MatchID = "another"
	if _, err = d.Poll(stale); err == nil {
		t.Fatal("stale accepted")
	}
	d.Close()
	if _, err = d.Poll(in); err == nil {
		t.Fatal("canceled accepted")
	}
}
func TestDecisionStaleAndLate(t *testing.T) {
	d, _, in := decisionFixture(t)
	defer d.Close()
	if err := d.Start(in); err != nil {
		t.Fatal(err)
	}
	for {
		d.mu.Lock()
		ready := d.result != nil
		d.mu.Unlock()
		if ready {
			break
		}
		if time.Now().After(in.Deadline) {
			t.Fatal("analysis timed out")
		}
		runtime.Gosched()
	}
	for _, mutate := range []func(*DecisionInput){
		func(x *DecisionInput) { x.MatchID = "different" },
		func(x *DecisionInput) { x.View.Revision++ },
		func(x *DecisionInput) { x.View.Score[1]++ },
		func(x *DecisionInput) { x.Phase = "other" },
		func(x *DecisionInput) { x.Candidates = nil },
	} {
		x := copyInput(in)
		mutate(&x)
		if _, err := d.Poll(x); err == nil {
			t.Fatal("accepted stale proposal")
		}
	}
	d.mu.Lock()
	d.result.Deadline = time.Now().Add(-time.Second)
	d.mu.Unlock()
	if _, err := d.Poll(in); err == nil {
		t.Fatal("accepted late proposal")
	}
}

func TestDecisionNoLegalAndInvalidProposal(t *testing.T) {
	d, _, in := decisionFixture(t)
	defer d.Close()
	in.Candidates = []Action{{Kind: "play", Card: "unknown"}}
	if _, err := d.Fast(in); err == nil {
		t.Fatal("accepted illegal candidate")
	}
	in.Candidates = []Action{{Kind: "pass"}}
	d.mu.Lock()
	d.result = &Proposal{MatchID: in.MatchID, Phase: in.Phase, Revision: in.View.Revision, Fingerprint: fingerprint(in), Deadline: in.Deadline, Action: Action{Kind: "play", Card: "unknown"}}
	d.mu.Unlock()
	if _, err := d.Poll(in); err == nil {
		t.Fatal("accepted invalid proposal")
	}
	in.Deadline = time.Now().Add(-time.Second)
	if err := d.Start(in); err == nil {
		t.Fatal("accepted expired analysis")
	}
}

func TestDecisionTimeoutFallbackAndHiddenEquivalence(t *testing.T) {
	d, s, in := decisionFixture(t)
	defer d.Close()
	// No completed analysis is needed to return a legal fast fallback.
	if _, err := d.Poll(in); err == nil {
		t.Fatal("unexpected proposal")
	}
	a, err := d.Fast(in)
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err = d.engine.Apply(s, a); err != nil {
		t.Fatal(err)
	}

	// Altering hidden opponent and deck identities cannot alter the view or choice.
	other := clone(s)
	other.Cards[2].CardID = 202462
	other.Cards[2].Power = 3
	other.Cards[2].Armor = 1
	v, err := other.View(0)
	if err != nil {
		t.Fatal(err)
	}
	in2 := copyInput(in)
	in2.View = v
	if fingerprint(in) != fingerprint(in2) {
		t.Fatal("hidden identity entered fingerprint")
	}
	b, err := d.Fast(in2)
	if err != nil || a != b {
		t.Fatalf("hidden information affected choice: %+v %v", b, err)
	}

	// An already expired deadline rejects both work and the fallback.
	in.Deadline = time.Now().Add(-time.Nanosecond)
	if err := d.Start(in); err == nil {
		t.Fatal("expired work accepted")
	}
	if _, err := d.Fast(in); err == nil {
		t.Fatal("expired fallback accepted")
	}
}

func TestDecisionRestartCancellation(t *testing.T) {
	d, _, in := decisionFixture(t)
	if err := d.Start(in); err != nil {
		t.Fatal(err)
	}
	latest := copyInput(in)
	latest.MatchID = "latest"
	latest.Candidates = []Action{{Kind: "play", Card: latest.HandOrder[0]}}
	if err := d.Start(latest); err != nil {
		t.Fatal(err)
	}
	for time.Now().Before(latest.Deadline) {
		if _, err := d.Poll(in); err == nil {
			t.Fatal("superseded result accepted")
		}
		if action, err := d.Poll(latest); err == nil {
			if action.Kind != "play" || action.Card != latest.HandOrder[0] {
				t.Fatalf("unexpected action: %+v", action)
			}
			d.Close()
			if err := d.Start(in); err == nil {
				t.Fatal("closed service restarted")
			}
			return
		}
		runtime.Gosched()
	}
	d.Close()
	t.Fatal("latest analysis did not complete")
}

func TestDecisionSessionDeduplicatesObservations(t *testing.T) {
	d, _, in := decisionFixture(t)
	defer d.Close()
	done := d.done
	if err := d.Start(in); err != nil {
		t.Fatal(err)
	}
	completed := false
	for time.Now().Before(in.Deadline) {
		if _, err := d.Poll(in); err == nil {
			completed = true
			break
		}
		runtime.Gosched()
	}
	if !completed {
		t.Fatal("analysis did not complete")
	}
	d.mu.Lock()
	generation := d.generation
	d.mu.Unlock()
	if err := d.Start(in); err != nil {
		t.Fatal(err)
	}
	d.mu.Lock()
	if d.generation != generation || d.pending != nil || d.done != done {
		d.mu.Unlock()
		t.Fatal("identical observation restarted analysis or worker")
	}
	d.mu.Unlock()
	if _, err := d.Poll(in); err == nil {
		t.Fatal("consumed action returned again")
	}
	changed := copyInput(in)
	changed.Deadline = in.Deadline.Add(time.Second)
	if err := d.Start(changed); err == nil {
		t.Fatal("same observation accepted with a different deadline")
	}
	select {
	case <-done:
		t.Fatal("worker exited between observations")
	default:
	}
}

func TestDecisionInvalidate(t *testing.T) {
	d, _, in := decisionFixture(t)
	defer d.Close()
	if err := d.Start(in); err != nil {
		t.Fatal(err)
	}
	for {
		d.mu.Lock()
		ready := d.result != nil
		d.mu.Unlock()
		if ready {
			break
		}
		if !time.Now().Before(in.Deadline) {
			t.Fatal("analysis did not complete")
		}
		runtime.Gosched()
	}
	d.Invalidate()
	if _, err := d.Poll(in); err == nil {
		t.Fatal("invalidated result accepted")
	}
	if err := d.Start(in); err != nil {
		t.Fatal(err)
	}
	for time.Now().Before(in.Deadline) {
		if _, err := d.Poll(in); err == nil {
			return
		}
		runtime.Gosched()
	}
	t.Fatal("worker did not resume after invalidation")
}

func TestDecisionInvalidUpdateCancelsPrevious(t *testing.T) {
	d, _, in := decisionFixture(t)
	defer d.Close()
	if err := d.Start(in); err != nil {
		t.Fatal(err)
	}
	invalid := copyInput(in)
	invalid.View.Finished = true
	if err := d.Start(invalid); err == nil {
		t.Fatal("invalid update accepted")
	}
	if _, err := d.Poll(in); err == nil {
		t.Fatal("result from superseded observation accepted")
	}
}

func TestDecisionCloseWaitsForWorker(t *testing.T) {
	d, _, in := decisionFixture(t)
	in.Candidates = make([]Action, 10000)
	for i := range in.Candidates {
		in.Candidates[i] = Action{Kind: "pass"}
	}
	if err := d.Start(in); err != nil {
		t.Fatal(err)
	}
	d.Close()
	d.mu.Lock()
	done := d.done
	d.mu.Unlock()
	select {
	case <-done:
	default:
		t.Fatal("worker did not exit")
	}
	if _, err := d.Poll(in); err == nil {
		t.Fatal("closed service returned a result")
	}
}

func TestDecisionAvoidsConcedingWhenPlayAvailable(t *testing.T) {
	d, s, in := decisionFixture(t)
	defer d.Close()
	s.Cards[2].Zone = Board
	s.Cards[2].Row = 0
	s.Cards[2].Power = 10
	s.Passed[1] = true
	v, err := s.View(0)
	if err != nil {
		t.Fatal(err)
	}
	in.View = v
	in.Candidates = []Action{{Kind: "pass"}, {Kind: "play", Card: s.Cards[0].ID}}
	if err := d.Start(in); err != nil {
		t.Fatal(err)
	}
	for time.Now().Before(in.Deadline) {
		a, err := d.Poll(in)
		if err == nil {
			if a.Kind != "play" {
				t.Fatalf("conceded: %+v", a)
			}
			changed := copyInput(in)
			changed.MatchID = "next"
			if err := d.Start(changed); err != nil {
				t.Fatalf("restart after result: %v", err)
			}
			return
		}
		runtime.Gosched()
	}
	t.Fatal("no result")
}

func BenchmarkDecisionFast(b *testing.B) {
	d, _, in := decisionFixture(b)
	defer d.Close()
	in.Deadline = time.Now().Add(time.Hour)
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if _, err := d.Fast(in); err != nil {
			b.Fatal(err)
		}
	}
}
