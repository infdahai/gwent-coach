package observe

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"gwentcoachmvp/internal/matchlog"
	"gwentcoachmvp/internal/rules"
)

func TestDurableRecoveryConfirmsOriginalActionWithoutReselection(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "match")
	w, err := matchlog.Create(dir, "m")
	if err != nil {
		t.Fatal(err)
	}
	j := NewActionJournal(w)
	before := SemanticObservation{MatchID: "m", Source: "before", Phase: "turn", Eligible: true, View: rules.View{Round: 1}}
	state, _ := semanticState(before)
	snapshot := Snapshot{Version: 9, Source: before.Source, Phase: before.Phase, State: state}
	original := rules.Action{Kind: "play", Card: "saved-card", Target: "saved-target", Row: 1}
	if err = j.Begin("m/9", snapshot, original); err != nil {
		t.Fatal(err)
	}
	at := time.Now().Add(-time.Second)
	for _, point := range []int{400, 900} {
		if err = j.ExecuteStep(Action{Kind: "click", X: point, Y: 500, Reference: "before"}, func(a Action) error {
			return a.onReceipt(InputReceipt{Outcome: "delivered", EventsSent: 2, DeliveredAt: at})
		}); err != nil {
			t.Fatal(err)
		}
	}
	if err = j.MarkDelivered("m/9"); err != nil {
		t.Fatal(err)
	}
	if err = w.Abort(); err != nil {
		t.Fatal(err)
	}
	resumed, events, err := matchlog.Resume(dir, "m")
	if err != nil {
		t.Fatal(err)
	}
	defer resumed.Abort()
	journal, err := RestoreActionJournal(resumed, events)
	if samples, e := matchlog.ConfirmedTrainingEvents(events); e != nil || len(samples) != 0 {
		t.Fatalf("unconfirmed delivery became training: %d %v", len(samples), e)
	}
	if err != nil {
		t.Fatal(err)
	}
	pending := journal.Pending()
	var saved rules.Action
	if pending == nil || len(pending.Steps) != 2 || pending.Steps[1].Input.X != 900 {
		t.Fatalf("pending=%+v", pending)
	}
	if err = json.Unmarshal(pending.Action, &saved); err != nil || saved != original {
		t.Fatalf("saved=%+v err=%v", saved, err)
	}
	loop := LocalLoop{Decisions: rules.NewEvaluator(nil), Journal: journal, DecisionBudget: time.Second, ConfirmationBudget: time.Second,
		Observe: func(context.Context) (SemanticObservation, error) {
			return withTestEvidence(SemanticObservation{MatchID: "m", Source: "after", Phase: "result", Result: true, CapturedAt: time.Now()}), nil
		},
		Execute: func(context.Context, Snapshot, rules.Action, time.Time) error {
			t.Fatal("replayed delivered input")
			return nil
		},
		Confirm: func(a rules.Action, o SemanticObservation) bool {
			if a != original {
				t.Errorf("reselected action: %+v", a)
			}
			return o.Result
		}}
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if err = loop.Run(ctx); err != nil {
		t.Fatal(err)
	}
	logged, err := matchlog.Read(filepath.Join(dir, "events.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	intents, confirmed := 0, 0
	for _, e := range logged {
		if e.EventType == "input_intent" {
			intents++
		}
		if e.EventType == "action_confirmed" {
			confirmed++
			if *e.StateVersion <= 9 {
				t.Fatal("semantic version regressed")
			}
		}
	}
	if intents != 2 || confirmed != 1 {
		t.Fatalf("intents=%d confirmed=%d", intents, confirmed)
	}
}

func TestDurableRecoveryMissingReceiptDoesNotSend(t *testing.T) {
	sink := &receiptSink{}
	j := NewActionJournal(sink)
	state, _ := semanticState(SemanticObservation{MatchID: "m"})
	if err := j.Begin("m/1", Snapshot{Version: 1, Source: "before", Phase: "turn", State: state}, rules.Action{Kind: "pass"}); err != nil {
		t.Fatal(err)
	}
	_ = j.ExecuteStep(Action{Kind: "hold"}, func(Action) error { return context.Canceled })
	restored, err := RestoreActionJournal(sink, sink.events)
	if err != nil {
		t.Fatal(err)
	}
	loop := LocalLoop{Decisions: rules.NewEvaluator(nil), Journal: restored, DecisionBudget: time.Second, ConfirmationBudget: time.Second,
		Observe: func(ctx context.Context) (SemanticObservation, error) {
			<-ctx.Done()
			return SemanticObservation{}, ctx.Err()
		},
		Execute: func(context.Context, Snapshot, rules.Action, time.Time) error {
			t.Fatal("sent unresolved step")
			return nil
		}, Confirm: func(rules.Action, SemanticObservation) bool { return true }}
	if err = loop.Run(context.Background()); err == nil || !strings.Contains(err.Error(), "unresolved steps") {
		t.Fatalf("err=%v", err)
	}
}

func TestRestoredConfirmedEligibleSnapshotStartsDecision(t *testing.T) {
	d, in := strategyFixture(t)
	defer d.Close()
	before := SemanticObservation{MatchID: "m", Source: "before", Phase: "turn", View: in.View, HandOrder: in.HandOrder, Eligible: true}
	before.View.Visible = append(before.View.Visible, rules.Instance{ID: "ally", CardID: 152405, Owner: 0, Power: 5, Row: 0, Zone: rules.Board}, rules.Instance{ID: "tactic", CardID: 202140, Owner: 0, Row: 0, Zone: rules.Board})
	before.View.Score[0] = 5
	after := before
	after.Source = "confirmed"
	after.View.Visible = append([]rules.Instance(nil), before.View.Visible[:2]...)
	after.View.Visible[1].Power = 10
	after.View.Score[0] = 10
	dir := filepath.Join(t.TempDir(), "m")
	w, err := matchlog.Create(dir, "m")
	if err != nil {
		t.Fatal(err)
	}
	j := NewActionJournal(w)
	beforeState, _ := semanticState(before)
	if err = j.Begin("m/6", Snapshot{Version: 6, Source: before.Source, Phase: before.Phase, State: beforeState}, rules.Action{Kind: "order", Card: "tactic", Target: "ally"}); err != nil {
		t.Fatal(err)
	}
	at := time.Now().Add(-time.Second)
	if err = j.ExecuteStep(Action{Kind: "click"}, func(a Action) error {
		return a.onReceipt(InputReceipt{Outcome: "delivered", EventsSent: 2, DeliveredAt: at})
	}); err != nil {
		t.Fatal(err)
	}
	if err = j.MarkDelivered("m/6"); err != nil {
		t.Fatal(err)
	}
	after.CapturedAt = time.Now()
	after = withTestEvidence(after)
	afterState, _ := semanticState(after)
	if err = j.Confirm("m/6", Snapshot{Version: 7, Source: after.Source, Phase: after.Phase, State: afterState}, after); err != nil {
		t.Fatal(err)
	}
	if err = w.Abort(); err != nil {
		t.Fatal(err)
	}
	w, events, err := matchlog.Resume(dir, "m")
	if err != nil {
		t.Fatal(err)
	}
	defer w.Abort()
	j, err = RestoreActionJournal(w, events)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(j.original.State, afterState) {
		t.Fatal("semantic snapshot changed during recovery")
	}
	delivered := make(chan struct{})
	sends := 0
	loop := LocalLoop{Decisions: d, Journal: j, DecisionBudget: time.Second, ConfirmationBudget: time.Second,
		Observe: func(context.Context) (SemanticObservation, error) {
			o := after
			o.Source = "current"
			select {
			case <-delivered:
				o.Source = "result"
				o.Phase = "result"
				o.Result = true
				o.Eligible = false
			default:
			}
			o.CapturedAt = time.Now()
			return withTestEvidence(o), nil
		},
		Execute: func(_ context.Context, s Snapshot, a rules.Action, _ time.Time) error {
			if s.Version != 7 || a.Kind != "play" {
				t.Errorf("snapshot=%d action=%+v", s.Version, a)
			}
			sends++
			err := j.ExecuteStep(Action{Kind: "click"}, func(input Action) error {
				return input.onReceipt(InputReceipt{Outcome: "delivered", EventsSent: 2, DeliveredAt: time.Now()})
			})
			close(delivered)
			return err
		}, Confirm: func(_ rules.Action, o SemanticObservation) bool { return o.Result }}
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if err = loop.Run(ctx); err != nil || sends != 1 {
		t.Fatalf("err=%v sends=%d", err, sends)
	}
}

func TestRecoveryRejectsTornEventStream(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "events.jsonl"), []byte(`{"sequence":1`), 0600); err != nil {
		t.Fatal(err)
	}
	if _, _, err := matchlog.Resume(dir, "m"); err == nil {
		t.Fatal("accepted torn receipt log")
	}
}
