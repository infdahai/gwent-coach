package observe

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"gwentcoachmvp/internal/cards"
	"gwentcoachmvp/internal/rules"
)

func TestLocalLoopPendingAndConfirmation(t *testing.T) {
	catalog, err := cards.Load("../../data/cards/14.10.0/en/cards.json")
	if err != nil {
		t.Fatal(err)
	}
	engine, err := rules.New(catalog)
	if err != nil {
		t.Fatal(err)
	}
	state, err := engine.Initial([2][]int{{152405}, {}}, [2][]int{}, 0)
	if err != nil {
		t.Fatal(err)
	}
	view, err := state.View(0)
	if err != nil {
		t.Fatal(err)
	}
	base := SemanticObservation{Source: "one", MatchID: "match", Phase: "turn", Player: 0, View: view, HandOrder: []string{state.Cards[0].ID}, Eligible: true}
	input := make(chan SemanticObservation)
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	sends := 0
	delivered := make(chan struct{})
	loop := LocalLoop{Decisions: rules.NewDecisionService(engine), DecisionBudget: time.Second, ConfirmationBudget: time.Second,
		Observe: func(ctx context.Context) (SemanticObservation, error) {
			select {
			case o := <-input:
				o.CapturedAt = time.Now()
				return o, nil
			case <-ctx.Done():
				return SemanticObservation{}, ctx.Err()
			}
		},
		Execute: func(_ context.Context, s Snapshot, a rules.Action, _ time.Time) error {
			sends++
			close(delivered)
			if s.Version != 1 || a.Kind != "play" {
				t.Errorf("unexpected action %v %+v", s.Version, a)
			}
			return nil
		},
		Confirm: func(a rules.Action, o SemanticObservation) bool { return o.Result },
	}
	go func() {
		input <- base
		<-delivered
		time.Sleep(10 * time.Millisecond)
		same := base
		same.Source = "two"
		input <- same
		done := base
		done.Source = "three"
		done.Eligible = false
		done.Result = true
		input <- done
	}()
	if err := loop.Run(ctx); err != nil {
		t.Fatal(err)
	}
	if sends != 1 {
		t.Fatalf("sent %d", sends)
	}
}

type loopTestPolicy struct{ invalidations int }

func (p *loopTestPolicy) Start(rules.DecisionInput, uint64) error { return nil }
func (p *loopTestPolicy) Poll(in rules.DecisionInput) (rules.Action, error) {
	return in.Candidates[0], nil
}
func (p *loopTestPolicy) Invalidate() { p.invalidations++ }

func TestLocalLoopInvalidatesPolicyOnExit(t *testing.T) {
	policy := &loopTestPolicy{}
	loop := LocalLoop{Decisions: rules.NewDecisionService(nil), DecisionBudget: time.Second, ConfirmationBudget: time.Second, Policy: policy,
		Observe: func(context.Context) (SemanticObservation, error) {
			return SemanticObservation{}, errors.New("observation failed")
		},
		Execute: func(context.Context, Snapshot, rules.Action, time.Time) error { return nil },
		Confirm: func(rules.Action, SemanticObservation) bool { return false },
	}
	if err := loop.Run(context.Background()); err == nil || err.Error() != "observation failed" {
		t.Fatalf("unexpected error: %v", err)
	}
	if policy.invalidations != 1 {
		t.Fatalf("exit invalidations: %d", policy.invalidations)
	}
}

func TestLocalLoopRejectsMatchChangeWithPendingAction(t *testing.T) {
	catalog, err := cards.Load("../../data/cards/14.10.0/en/cards.json")
	if err != nil {
		t.Fatal(err)
	}
	engine, err := rules.New(catalog)
	if err != nil {
		t.Fatal(err)
	}
	state, err := engine.Initial([2][]int{{152405}, {}}, [2][]int{}, 0)
	if err != nil {
		t.Fatal(err)
	}
	view, err := state.View(0)
	if err != nil {
		t.Fatal(err)
	}
	observations := []SemanticObservation{
		{Source: "one", MatchID: "first", Phase: "turn", Player: 0, View: view, HandOrder: []string{state.Cards[0].ID}, Eligible: true},
		{Source: "two", MatchID: "second", Phase: "turn", Player: 0, View: view, HandOrder: []string{state.Cards[0].ID}, Eligible: true},
	}
	i, sends, confirmations := 0, 0, 0
	delivered := make(chan struct{})
	loop := LocalLoop{Decisions: rules.NewDecisionService(engine), DecisionBudget: time.Second, ConfirmationBudget: time.Second,
		Observe: func(ctx context.Context) (SemanticObservation, error) {
			if i == len(observations) {
				<-ctx.Done()
				return SemanticObservation{}, ctx.Err()
			}
			if i == 1 {
				select {
				case <-delivered:
					time.Sleep(10 * time.Millisecond)
				case <-ctx.Done():
					return SemanticObservation{}, ctx.Err()
				}
			}
			o := observations[i]
			i++
			o.CapturedAt = time.Now()
			return o, nil
		},
		Execute: func(context.Context, Snapshot, rules.Action, time.Time) error { sends++; close(delivered); return nil },
		Confirm: func(rules.Action, SemanticObservation) bool { confirmations++; return true },
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if err := loop.Run(ctx); err == nil || !strings.Contains(err.Error(), "match changed with an unresolved action") {
		t.Fatalf("unexpected error: %v", err)
	}
	if sends != 1 || confirmations != 0 {
		t.Fatalf("sends=%d confirmations=%d", sends, confirmations)
	}
}

type waitingPolicy struct{ loopTestPolicy }

func (p *waitingPolicy) Poll(rules.DecisionInput) (rules.Action, error) {
	return rules.Action{}, rules.ErrNoDecision
}

func TestLocalLoopDeadlineWhileObservationBlocks(t *testing.T) {
	catalog, err := cards.Load("../../data/cards/14.10.0/en/cards.json")
	if err != nil {
		t.Fatal(err)
	}
	engine, err := rules.New(catalog)
	if err != nil {
		t.Fatal(err)
	}
	state, err := engine.Initial([2][]int{{152405}, {}}, [2][]int{}, 0)
	if err != nil {
		t.Fatal(err)
	}
	view, err := state.View(0)
	if err != nil {
		t.Fatal(err)
	}
	first := true
	policy := &waitingPolicy{}
	loop := LocalLoop{Decisions: rules.NewDecisionService(engine), DecisionBudget: 30 * time.Millisecond, ConfirmationBudget: time.Second, Policy: policy,
		Observe: func(ctx context.Context) (SemanticObservation, error) {
			if first {
				first = false
				return SemanticObservation{Source: "one", MatchID: "m", Phase: "turn", Player: 0, View: view, HandOrder: []string{state.Cards[0].ID}, Eligible: true, CapturedAt: time.Now()}, nil
			}
			<-ctx.Done()
			return SemanticObservation{}, ctx.Err()
		},
		Execute: func(context.Context, Snapshot, rules.Action, time.Time) error { t.Error("unexpected send"); return nil },
		Confirm: func(rules.Action, SemanticObservation) bool { return false },
	}
	defer loop.Decisions.(*rules.DecisionService).Close()
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if err := loop.Run(ctx); err == nil || !strings.Contains(err.Error(), "decision deadline expired") {
		t.Fatalf("unexpected error: %v", err)
	}
	if policy.invalidations < 1 {
		t.Fatal("policy was not invalidated")
	}
}

type delayedPolicy struct{ polls int }

func (p *delayedPolicy) Start(rules.DecisionInput, uint64) error { return nil }
func (p *delayedPolicy) Poll(in rules.DecisionInput) (rules.Action, error) {
	p.polls++
	if p.polls < 2 {
		return rules.Action{}, rules.ErrNoDecision
	}
	return in.Candidates[0], nil
}
func (p *delayedPolicy) Invalidate() {}

func TestLocalLoopPolicyReadyWithoutNewFrameAndConfirmationTimeout(t *testing.T) {
	for _, continuous := range []bool{false, true} {
		t.Run(fmt.Sprintf("continuous=%v", continuous), func(t *testing.T) {
			catalog, err := cards.Load("../../data/cards/14.10.0/en/cards.json")
			if err != nil {
				t.Fatal(err)
			}
			engine, err := rules.New(catalog)
			if err != nil {
				t.Fatal(err)
			}
			state, err := engine.Initial([2][]int{{152405}, {}}, [2][]int{}, 0)
			if err != nil {
				t.Fatal(err)
			}
			view, err := state.View(0)
			if err != nil {
				t.Fatal(err)
			}
			first, sends := true, 0
			delivered := make(chan struct{})
			policy := &delayedPolicy{}
			loop := LocalLoop{Decisions: rules.NewDecisionService(engine), DecisionBudget: time.Second, ConfirmationBudget: 50 * time.Millisecond, Policy: policy,
				Observe: func(ctx context.Context) (SemanticObservation, error) {
					if !first {
						if !continuous {
							<-ctx.Done()
							return SemanticObservation{}, ctx.Err()
						}
						select {
						case <-delivered:
						case <-ctx.Done():
							return SemanticObservation{}, ctx.Err()
						}
					}
					first = false
					return SemanticObservation{Source: "one", MatchID: "m", Phase: "turn", Player: 0, View: view, HandOrder: []string{state.Cards[0].ID}, Eligible: true, CapturedAt: time.Now()}, nil
				},
				Execute: func(context.Context, Snapshot, rules.Action, time.Time) error { sends++; close(delivered); return nil },
				Confirm: func(rules.Action, SemanticObservation) bool {
					t.Error("unchanged observation cannot confirm")
					return true
				},
			}
			defer loop.Decisions.(*rules.DecisionService).Close()
			ctx, cancel := context.WithTimeout(context.Background(), time.Second)
			defer cancel()
			if err := loop.Run(ctx); err == nil || !strings.Contains(err.Error(), "confirmation timed out; outcome unresolved, do not replay") {
				t.Fatalf("unexpected error: %v", err)
			}
			if sends != 1 || policy.polls < 2 {
				t.Fatalf("sends=%d polls=%d", sends, policy.polls)
			}
		})
	}
}

func TestLocalLoopContinuesThroughMulliganAfterConfirmedPass(t *testing.T) {
	catalog, err := cards.Load("../../data/cards/14.10.0/en/cards.json")
	if err != nil {
		t.Fatal(err)
	}
	engine, err := rules.New(catalog)
	if err != nil {
		t.Fatal(err)
	}
	state, err := engine.Initial([2][]int{{}, {}}, [2][]int{}, 0)
	if err != nil {
		t.Fatal(err)
	}
	view, _ := state.View(0)
	delivered, roundDone := make(chan struct{}), make(chan struct{})
	i, sends, rounds := 0, 0, 0
	loop := LocalLoop{Decisions: rules.NewDecisionService(engine), DecisionBudget: time.Second, ConfirmationBudget: time.Second,
		Observe: func(ctx context.Context) (SemanticObservation, error) {
			i++
			if i == 1 {
				return SemanticObservation{Source: "before", MatchID: "m", Phase: "turn", View: view, Eligible: true, CapturedAt: time.Now()}, nil
			}
			select {
			case <-delivered:
			case <-ctx.Done():
				return SemanticObservation{}, ctx.Err()
			}
			if i == 2 {
				time.Sleep(10 * time.Millisecond)
				v := view
				v.Round = 2
				return SemanticObservation{Source: "mulligan", MatchID: "m", Phase: "mulligan", View: v, CapturedAt: time.Now()}, nil
			}
			select {
			case <-roundDone:
			case <-ctx.Done():
				return SemanticObservation{}, ctx.Err()
			}
			if i == 3 {
				return SemanticObservation{}, ErrUnstableVisual
			}
			time.Sleep(10 * time.Millisecond)
			return SemanticObservation{Source: "result", MatchID: "m", Phase: "result", View: view, Result: true, CapturedAt: time.Now()}, nil
		}, Execute: func(_ context.Context, _ Snapshot, a rules.Action, _ time.Time) error {
			if a.Kind != "pass" {
				t.Errorf("action=%+v", a)
			}
			sends++
			close(delivered)
			return nil
		}, Confirm: func(_ rules.Action, o SemanticObservation) bool { return o.Phase == "mulligan" }, OnRoundStart: func(_ context.Context, o SemanticObservation) error { rounds++; close(roundDone); return nil }}
	defer loop.Decisions.(*rules.DecisionService).Close()
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if err := loop.Run(ctx); err != nil || sends != 1 || rounds != 1 {
		t.Fatalf("err=%v sends=%d rounds=%d", err, sends, rounds)
	}
}

func TestLocalLoopWaitsThroughUnstablePostDeliveryFrames(t *testing.T) {
	catalog, err := cards.Load("../../data/cards/14.10.0/en/cards.json")
	if err != nil {
		t.Fatal(err)
	}
	engine, err := rules.New(catalog)
	if err != nil {
		t.Fatal(err)
	}
	state, err := engine.Initial([2][]int{{152405}, {}}, [2][]int{}, 0)
	if err != nil {
		t.Fatal(err)
	}
	view, _ := state.View(0)
	delivered := make(chan struct{})
	i, sends := 0, 0
	loop := LocalLoop{Decisions: rules.NewDecisionService(engine), DecisionBudget: time.Second, ConfirmationBudget: time.Second,
		Observe: func(ctx context.Context) (SemanticObservation, error) {
			i++
			if i == 1 {
				return SemanticObservation{Source: "before", MatchID: "m", Phase: "turn", View: view, HandOrder: []string{state.Cards[0].ID}, Eligible: true, CapturedAt: time.Now()}, nil
			}
			select {
			case <-delivered:
			case <-ctx.Done():
				return SemanticObservation{}, ctx.Err()
			}
			if i == 2 {
				return SemanticObservation{}, ErrUnstableVisual
			}
			time.Sleep(10 * time.Millisecond)
			return SemanticObservation{Source: "after", MatchID: "m", Phase: "done", View: view, Result: true, CapturedAt: time.Now()}, nil
		}, Execute: func(context.Context, Snapshot, rules.Action, time.Time) error { sends++; close(delivered); return nil }, Confirm: func(_ rules.Action, o SemanticObservation) bool { return o.Result }}
	defer loop.Decisions.(*rules.DecisionService).Close()
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if err := loop.Run(ctx); err != nil || sends != 1 {
		t.Fatalf("err=%v sends=%d", err, sends)
	}
}

func TestLocalLoopDiscardsObservationCapturedBeforeDelivery(t *testing.T) {
	catalog, err := cards.Load("../../data/cards/14.10.0/en/cards.json")
	if err != nil {
		t.Fatal(err)
	}
	engine, err := rules.New(catalog)
	if err != nil {
		t.Fatal(err)
	}
	state, err := engine.Initial([2][]int{{152405}, {}}, [2][]int{}, 0)
	if err != nil {
		t.Fatal(err)
	}
	view, err := state.View(0)
	if err != nil {
		t.Fatal(err)
	}
	base := SemanticObservation{Source: "initial", MatchID: "m", Phase: "turn", Player: 0, View: view, HandOrder: []string{state.Cards[0].ID}, Eligible: true}
	prefetched := make(chan struct{})
	completed := make(chan struct{})
	index := 0
	loop := LocalLoop{Decisions: rules.NewDecisionService(engine), DecisionBudget: time.Second, ConfirmationBudget: time.Second,
		Observe: func(ctx context.Context) (SemanticObservation, error) {
			index++
			o := base
			switch index {
			case 1:
				o.CapturedAt = time.Now()
			case 2:
				o.Source, o.Result, o.Eligible = "prefetched", true, false
				// A long delivery can make an already captured frame stale.
				o.CapturedAt = time.Now().Add(-3 * time.Second)
				close(prefetched)
			case 3:
				select {
				case <-completed:
				case <-ctx.Done():
					return SemanticObservation{}, ctx.Err()
				}
				time.Sleep(10 * time.Millisecond)
				o.Source, o.Result, o.Eligible = "fresh", true, false
				o.CapturedAt = time.Now()
			default:
				<-ctx.Done()
				return SemanticObservation{}, ctx.Err()
			}
			return o, nil
		},
		Execute: func(context.Context, Snapshot, rules.Action, time.Time) error {
			<-prefetched
			close(completed)
			return nil
		},
		Confirm: func(_ rules.Action, o SemanticObservation) bool {
			if o.Source != "fresh" {
				t.Errorf("confirmed from %s", o.Source)
			}
			return o.Result
		},
	}
	defer loop.Decisions.(*rules.DecisionService).Close()
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if err := loop.Run(ctx); err != nil {
		t.Fatal(err)
	}
}

func TestGuardedExecutionObservationDeadline(t *testing.T) {
	guard := GuardedExecution(func(ctx context.Context) (SemanticObservation, error) {
		<-ctx.Done()
		return SemanticObservation{}, ctx.Err()
	}, func(context.Context, SemanticObservation, rules.Action) error { t.Error("unexpected send"); return nil })
	if err := guard(context.Background(), Snapshot{}, rules.Action{}, time.Now().Add(30*time.Millisecond)); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestGuardedExecution(t *testing.T) {
	original := SemanticObservation{Source: "first", MatchID: "m", Phase: "turn", Eligible: true, CapturedAt: time.Now()}
	state, err := semanticState(original)
	if err != nil {
		t.Fatal(err)
	}
	expected := Snapshot{Version: 1, Source: "first", Phase: "turn", State: state}
	current := original
	current.Source = "fresh"
	sent := 0
	guard := GuardedExecution(func(context.Context) (SemanticObservation, error) { return current, nil }, func(context.Context, SemanticObservation, rules.Action) error { sent++; return nil })
	if err := guard(context.Background(), expected, rules.Action{Kind: "pass"}, time.Now().Add(time.Second)); err != nil || sent != 1 {
		t.Fatalf("%v %d", err, sent)
	}
	current.View.Revision++
	if err := guard(context.Background(), expected, rules.Action{}, time.Now().Add(time.Second)); err == nil {
		t.Fatal("accepted changed state")
	}
	current = original
	if err := guard(context.Background(), expected, rules.Action{}, time.Now().Add(time.Second)); err == nil {
		t.Fatal("accepted reused source")
	}
	current.Source = "fresh"
	current.Eligible = false
	if err := guard(context.Background(), expected, rules.Action{}, time.Now().Add(time.Second)); err == nil {
		t.Fatal("accepted ineligible state")
	}
	current = original
	current.Source = "fresh"
	if err := guard(context.Background(), expected, rules.Action{}, time.Now().Add(-time.Second)); err == nil {
		t.Fatal("accepted expired deadline")
	}
	if sent != 1 {
		t.Fatalf("unexpected sends %d", sent)
	}
}

func TestLocalLoopRejectsStaleObservation(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	loop := LocalLoop{Decisions: rules.NewDecisionService(nil), DecisionBudget: time.Second, ConfirmationBudget: time.Second,
		Observe: func(context.Context) (SemanticObservation, error) {
			return SemanticObservation{Source: "old", MatchID: "m", Phase: "turn", CapturedAt: time.Now().Add(-time.Minute)}, nil
		},
		Execute: func(context.Context, Snapshot, rules.Action, time.Time) error { return errors.New("must not send") },
		Confirm: func(rules.Action, SemanticObservation) bool { return false },
	}
	if err := loop.Run(ctx); err == nil || !strings.Contains(err.Error(), "stale") {
		t.Fatal(err)
	}
}
