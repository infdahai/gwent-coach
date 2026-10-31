package rules

import (
	"context"
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
	in := DecisionInput{MatchID: "match", Phase: "turn", Player: 0, View: v, Deadline: time.Now().Add(time.Second), Candidates: []Action{{Kind: "play", Card: s.Cards[0].ID}, {Kind: "pass"}}}
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
	defer d.Close()
	// Model a worker that has not yet observed cancellation so restart cannot
	// accumulate unbounded concurrent workers.
	ctx, cancel := context.WithCancel(context.Background())
	d.mu.Lock()
	d.running = true
	d.cancel = cancel
	d.mu.Unlock()
	if err := d.Start(in); err == nil {
		t.Fatal("restart accepted while worker active")
	}
	select {
	case <-ctx.Done():
	default:
		t.Fatal("worker not canceled")
	}
	d.mu.Lock()
	d.running = false
	d.cancel = nil
	d.mu.Unlock()
	if err := d.Start(in); err != nil {
		t.Fatal(err)
	}
	d.Close()
	if _, err := d.Poll(in); err == nil {
		t.Fatal("canceled result accepted")
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
			if err := d.Start(in); err != nil {
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
