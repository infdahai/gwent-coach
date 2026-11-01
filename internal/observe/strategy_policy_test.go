package observe

import (
	"context"
	"errors"
	"testing"
	"time"

	"gwentcoachmvp/internal/cards"
	"gwentcoachmvp/internal/rules"
	"gwentcoachmvp/internal/strategy"
)

type choosePassPolicy struct{ release <-chan struct{} }

func (p choosePassPolicy) Choose(ctx context.Context, o strategy.Observation) (string, error) {
	select {
	case <-p.release:
	case <-ctx.Done():
		return "", ctx.Err()
	}
	for _, id := range o.Candidates {
		if id == `{"Kind":"pass","Player":0,"Card":"","Target":"","Row":0,"Draws":[0,0]}` {
			return id, nil
		}
	}
	return "", strategy.ErrNoDecision
}

func strategyFixture(t *testing.T) (*rules.DecisionService, rules.DecisionInput) {
	t.Helper()
	catalog, err := cards.Load("../../data/cards/14.10.0/en/cards.json")
	if err != nil {
		t.Fatal(err)
	}
	engine, err := rules.New(catalog)
	if err != nil {
		t.Fatal(err)
	}
	state, err := engine.Initial([2][]int{{132403}, {}}, [2][]int{}, 0)
	if err != nil {
		t.Fatal(err)
	}
	view, err := state.View(0)
	if err != nil {
		t.Fatal(err)
	}
	in := rules.DecisionInput{MatchID: "m", Phase: "turn", Player: 0, View: view, HandOrder: []string{state.Cards[0].ID}, Deadline: time.Now().Add(2 * time.Second), AllowPassWithCards: true}
	service := rules.NewDecisionService(engine)
	in.Candidates, err = service.Candidates(in)
	if err != nil {
		t.Fatal(err)
	}
	return service, in
}

func TestLocalLoopUsesStrategyAdapterAndConfirmsOnce(t *testing.T) {
	d, in := strategyFixture(t)
	defer d.Close()
	policy, err := NewStrategyPolicy(d, nil, time.Second)
	if err != nil {
		t.Fatal(err)
	}
	defer policy.Close()
	delivered := make(chan struct{})
	seq, sends := 0, 0
	sink := &receiptSink{}
	journal := NewActionJournal(sink)
	loop := LocalLoop{Decisions: d, Policy: policy, Journal: journal, DecisionBudget: time.Second, ConfirmationBudget: time.Second,
		Observe: func(ctx context.Context) (SemanticObservation, error) {
			seq++
			if seq == 1 {
				return SemanticObservation{Source: "before", MatchID: in.MatchID, Phase: "turn", View: in.View, HandOrder: in.HandOrder, Eligible: true, CapturedAt: time.Now()}, nil
			}
			select {
			case <-delivered:
			case <-ctx.Done():
				return SemanticObservation{}, ctx.Err()
			}
			time.Sleep(10 * time.Millisecond)
			return withTestEvidence(SemanticObservation{Source: "after", MatchID: in.MatchID, Phase: "result", View: in.View, Result: true, CapturedAt: time.Now()}), nil
		}, Execute: func(_ context.Context, _ Snapshot, a rules.Action, _ time.Time) error {
			if a.Kind != "play" {
				t.Errorf("action=%+v", a)
			}
			sends++
			return journal.ExecuteStep(Action{Kind: "click", Reference: "before"}, func(input Action) error {
				err := input.onReceipt(InputReceipt{Outcome: "delivered", EventsSent: 2, DeliveredAt: time.Now()})
				close(delivered)
				return err
			})
		}, Confirm: func(_ rules.Action, o SemanticObservation) bool { return o.Result }}
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if err := loop.Run(ctx); err != nil || sends != 1 {
		t.Fatalf("err=%v sends=%d", err, sends)
	}
	if len(sink.events) != 6 || sink.events[4].EventType != "action_confirmed" || sink.events[5].EventType != "result_observed" {
		t.Fatalf("events=%+v", sink.events)
	}
}

func TestStrategyPolicyFallbackPrefersPlayableAndConsumesOnce(t *testing.T) {
	d, in := strategyFixture(t)
	defer d.Close()
	p, err := NewStrategyPolicy(d, nil, time.Second)
	if err != nil {
		t.Fatal(err)
	}
	defer p.Close()
	if err = p.Start(in, 1); err != nil {
		t.Fatal(err)
	}
	a, err := p.Poll(in)
	if err != nil || a.Kind != "play" || a.Card != in.HandOrder[0] {
		t.Fatalf("action=%+v err=%v", a, err)
	}
	if err = p.Start(in, 1); err != nil {
		t.Fatalf("duplicate start: %v", err)
	}
	if _, err = p.Poll(in); !errors.Is(err, rules.ErrNoDecision) {
		t.Fatalf("duplicate result: %v", err)
	}
	in.MatchID = "new-match"
	in.Deadline = time.Now().Add(time.Second)
	if err = p.Start(in, 1); err != nil {
		t.Fatalf("new match: %v", err)
	}
}

func TestStrategyPolicyWaitsForProposalThenFallsBack(t *testing.T) {
	d, in := strategyFixture(t)
	defer d.Close()
	release := make(chan struct{})
	p, err := NewStrategyPolicy(d, choosePassPolicy{release}, 500*time.Millisecond)
	if err != nil {
		t.Fatal(err)
	}
	defer p.Close()
	if err = p.Start(in, 1); err != nil {
		t.Fatal(err)
	}
	if _, err = p.Poll(in); !errors.Is(err, rules.ErrNoDecision) {
		t.Fatalf("early fallback: %v", err)
	}
	close(release)
	until := time.Now().Add(time.Second)
	for time.Now().Before(until) {
		a, e := p.Poll(in)
		if e == nil {
			if a.Kind != "pass" {
				t.Fatalf("proposal=%+v", a)
			}
			return
		}
		if !errors.Is(e, rules.ErrNoDecision) {
			t.Fatal(e)
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatal("proposal not delivered")
}

func TestStrategyPolicyBindsVersionAndDeadline(t *testing.T) {
	d, in := strategyFixture(t)
	defer d.Close()
	p, err := NewStrategyPolicy(d, nil, time.Second)
	if err != nil {
		t.Fatal(err)
	}
	defer p.Close()
	if err = p.Start(in, 7); err != nil {
		t.Fatal(err)
	}
	changed := in
	changed.Deadline = in.Deadline.Add(time.Second)
	if err = p.Start(changed, 7); !errors.Is(err, strategy.ErrObservation) {
		t.Fatalf("deadline changed under same version: %v", err)
	}
	if _, err = p.Poll(in); !errors.Is(err, rules.ErrNoDecision) {
		t.Fatalf("conflicting version remained active: %v", err)
	}
	if err = p.Start(in, 8); err != nil {
		t.Fatal(err)
	}
	if p.observation.Revision != 8 || p.observation.Deadline != in.Deadline {
		t.Fatal("snapshot version/deadline not preserved")
	}
	if _, err = p.Poll(in); err != nil {
		t.Fatal(err)
	}
}

type latePolicy struct {
	release           <-chan struct{}
	started, returned chan<- struct{}
}

func (p latePolicy) Choose(_ context.Context, o strategy.Observation) (string, error) {
	close(p.started)
	<-p.release
	defer close(p.returned)
	return o.Candidates[len(o.Candidates)-1], nil
}

func TestStrategyFallbackBoundaryAndLateProposalConsumption(t *testing.T) {
	d, in := strategyFixture(t)
	defer d.Close()
	release, started, returned := make(chan struct{}), make(chan struct{}), make(chan struct{})
	p, err := NewStrategyPolicy(d, latePolicy{release, started, returned}, time.Second)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		select {
		case <-release:
		default:
			close(release)
		}
		p.Close()
	})
	if err = p.Start(in, 1); err != nil {
		t.Fatal(err)
	}
	select {
	case <-started:
	case <-time.After(time.Second):
		t.Fatal("policy did not start")
	}
	if p.fallbackAt != in.Deadline.Add(-time.Second) {
		t.Fatal("execution reserve not retained")
	}
	if _, err = p.Poll(in); !errors.Is(err, rules.ErrNoDecision) {
		t.Fatalf("consumed before fallback: %v", err)
	}
	p.fallbackAt = time.Now()
	if a, e := p.Poll(in); e != nil || a.Kind != "play" {
		t.Fatalf("fallback=%+v err=%v", a, e)
	}
	close(release)
	select {
	case <-returned:
	case <-time.After(time.Second):
		t.Fatal("policy did not return")
	}
	// A late proposal must leave the consumed revision unavailable.
	for until := time.Now().Add(30 * time.Millisecond); time.Now().Before(until); {
		if _, err = p.Poll(in); !errors.Is(err, rules.ErrNoDecision) {
			t.Fatalf("late duplicate: %v", err)
		}
		time.Sleep(time.Millisecond)
	}
}

func TestStrategyPolicyFallsBackWithExecutionReserve(t *testing.T) {
	d, in := strategyFixture(t)
	defer d.Close()
	release := make(chan struct{})
	p, err := NewStrategyPolicy(d, choosePassPolicy{release}, 3*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	defer p.Close()
	if err = p.Start(in, 1); err != nil {
		t.Fatal(err)
	}
	a, err := p.Poll(in)
	if err != nil || a.Kind != "play" {
		t.Fatalf("fallback=%+v err=%v", a, err)
	}
}
